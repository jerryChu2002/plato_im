//go:build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hardcore-os/plato/common/convid"
	messagedto "github.com/hardcore-os/plato/common/idl/domain/message"
	"github.com/hardcore-os/plato/common/idl/message"
	"github.com/hardcore-os/plato/common/tcp"
	groupservice "github.com/hardcore-os/plato/domain/group/rpc/service"
	messageservice "github.com/hardcore-os/plato/domain/message/rpc/service"
	userservice "github.com/hardcore-os/plato/domain/user/rpc/service"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/proto"
)

const (
	userEndpoint    = "127.0.0.1:8903"
	messageEndpoint = "127.0.0.1:8904"
	groupEndpoint   = "127.0.0.1:8906"
	primaryGateway  = "127.0.0.1:8900"
	secondGateway   = "127.0.0.1:8910"
	testTimeout     = 8 * time.Second
)

type tcpTestClient struct {
	conn      *net.TCPConn
	writeMu   sync.Mutex
	connID    atomic.Uint64
	clientID  atomic.Uint64
	acks      chan *message.ACKMsg
	pushes    chan *messagedto.ChatMsgDTO
	closed    chan struct{}
	closeOnce sync.Once
}

func newTCPTestClient(t *testing.T, endpoint string, userID, did uint64, token string) *tcpTestClient {
	t.Helper()
	rawConn, err := net.DialTimeout("tcp", endpoint, 3*time.Second)
	if err != nil {
		t.Fatalf("dial gateway %s: %v", endpoint, err)
	}
	conn, ok := rawConn.(*net.TCPConn)
	if !ok {
		rawConn.Close()
		t.Fatalf("dial gateway %s did not return TCP connection", endpoint)
	}
	c := &tcpTestClient{
		conn: conn, acks: make(chan *message.ACKMsg, 8), pushes: make(chan *messagedto.ChatMsgDTO, 8), closed: make(chan struct{}),
	}
	go c.readLoop()
	login := &message.LoginMsg{Head: &message.LoginMsgHead{UserID: userID, DeviceID: did, SessionToken: token}}
	c.sendProto(t, message.CmdType_Login, login)
	ack := receive(t, c.acks)
	if ack.GetCode() != 0 {
		c.close()
		t.Fatalf("TCP login user=%d did=%d: %s", userID, did, ack.GetMsg())
	}
	c.connID.Store(ack.GetConnID())
	go c.heartbeatLoop()
	return c
}

func (c *tcpTestClient) sendChat(t *testing.T, toUserID uint64, clientMsgID, body string) {
	t.Helper()
	up := &message.UPMsg{
		Head:      &message.UPMsgHead{ClientID: c.clientID.Add(1) - 1, ConnID: c.connID.Load(), SessionId: "integration", ToUserID: toUserID, ClientMsgID: clientMsgID},
		UPMsgBody: []byte(body),
	}
	c.sendProto(t, message.CmdType_UP, up)
	ack := receive(t, c.acks)
	if ack.GetCode() != 0 {
		t.Fatalf("send chat: %s", ack.GetMsg())
	}
}

func (c *tcpTestClient) sendGroupChat(t *testing.T, groupID uint64, clientMsgID, body string) {
	t.Helper()
	up := &message.UPMsg{
		Head: &message.UPMsgHead{
			ClientID: c.clientID.Add(1) - 1, ConnID: c.connID.Load(), SessionId: "integration",
			GroupID: groupID, ClientMsgID: clientMsgID,
		},
		UPMsgBody: []byte(body),
	}
	c.sendProto(t, message.CmdType_UP, up)
	ack := receive(t, c.acks)
	if ack.GetCode() != 0 {
		t.Fatalf("send group chat: %s", ack.GetMsg())
	}
}

func (c *tcpTestClient) markRead(t *testing.T, conversationID string, seq uint64) {
	t.Helper()
	c.sendProto(t, message.CmdType_ReadReceipt, &message.ReadReceiptMsg{ConversationID: conversationID, ReadSeq: seq})
}

func (c *tcpTestClient) sendProto(t *testing.T, cmdType message.CmdType, value proto.Message) {
	t.Helper()
	payload, err := proto.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	cmd, err := proto.Marshal(&message.MsgCmd{Type: cmdType, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	pkg := (&tcp.DataPgk{Len: uint32(len(cmd)), Data: cmd}).Marshal()
	c.writeMu.Lock()
	_, err = c.conn.Write(pkg)
	c.writeMu.Unlock()
	if err != nil {
		t.Fatalf("write gateway: %v", err)
	}
}

func (c *tcpTestClient) readLoop() {
	defer c.close()
	for {
		data, err := tcp.ReadData(c.conn)
		if err != nil {
			return
		}
		cmd := &message.MsgCmd{}
		if proto.Unmarshal(data, cmd) != nil {
			continue
		}
		switch cmd.GetType() {
		case message.CmdType_ACK:
			ack := &message.ACKMsg{}
			if proto.Unmarshal(cmd.GetPayload(), ack) == nil {
				c.acks <- ack
			}
		case message.CmdType_Push:
			push := &message.PushMsg{}
			dto := &messagedto.ChatMsgDTO{}
			if proto.Unmarshal(cmd.GetPayload(), push) == nil && proto.Unmarshal(push.GetContent(), dto) == nil {
				c.pushes <- dto
				ack := &message.ACKMsg{Type: message.CmdType_UP, ConnID: c.connID.Load(), MsgID: push.GetMsgID(), SessionID: push.GetSessionID()}
				c.sendProtoNoTest(message.CmdType_ACK, ack)
			}
		}
	}
}

func (c *tcpTestClient) heartbeatLoop() {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			c.sendProtoNoTest(message.CmdType_Heartbeat, &message.HeartbeatMsg{Head: &message.HeartbeatMsgHead{}})
		case <-c.closed:
			return
		}
	}
}

func (c *tcpTestClient) sendProtoNoTest(cmdType message.CmdType, value proto.Message) {
	payload, err := proto.Marshal(value)
	if err != nil {
		return
	}
	cmd, err := proto.Marshal(&message.MsgCmd{Type: cmdType, Payload: payload})
	if err != nil {
		return
	}
	c.writeMu.Lock()
	_, _ = c.conn.Write((&tcp.DataPgk{Len: uint32(len(cmd)), Data: cmd}).Marshal())
	c.writeMu.Unlock()
}

func (c *tcpTestClient) close() {
	c.closeOnce.Do(func() {
		close(c.closed)
		_ = c.conn.Close()
	})
}

func TestAccountMultiDeviceAcrossGateways(t *testing.T) {
	userConn := dialGRPC(t, userEndpoint)
	defer userConn.Close()
	messageConn := dialGRPC(t, messageEndpoint)
	defer messageConn.Close()
	userClient := userservice.NewUserClient(userConn)
	messageClient := messageservice.NewMessageClient(messageConn)

	stamp := time.Now().UnixNano()
	accountA, accountB := fmt.Sprintf("integration-a-%d", stamp), fmt.Sprintf("integration-b-%d", stamp)
	baseDid := uint64(stamp)
	didA, didB1, didB2 := baseDid+1, baseDid+2, baseDid+3
	identityA := register(t, userClient, accountA, "secret-a", didA)
	identityB1 := register(t, userClient, accountB, "secret-b", didB1)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	wrong, err := userClient.Login(ctx, &userservice.LoginRequest{Account: accountB, Password: "wrong-password", DeviceID: didB2})
	cancel()
	if err != nil || wrong.GetBase().GetCode() == 0 {
		t.Fatalf("wrong password login = (%v, %v), want rejected", wrong, err)
	}
	identityB2 := login(t, userClient, accountB, "secret-b", didB2)

	a := newTCPTestClient(t, primaryGateway, identityA.GetUserID(), didA, identityA.GetSessionToken())
	defer a.close()
	b1 := newTCPTestClient(t, primaryGateway, identityB1.GetUserID(), didB1, identityB1.GetSessionToken())
	defer b1.close()
	b2 := newTCPTestClient(t, secondGateway, identityB2.GetUserID(), didB2, identityB2.GetSessionToken())
	defer b2.close()

	clientMsgID1 := fmt.Sprintf("integration-%d-1", stamp)
	a.sendChat(t, identityB1.GetUserID(), clientMsgID1, "online fanout")
	pushB1, pushB2 := receive(t, b1.pushes), receive(t, b2.pushes)
	if pushB1.GetClientMsgID() != clientMsgID1 || pushB2.GetClientMsgID() != clientMsgID1 {
		t.Fatalf("fanout clientMsgIDs = %q, %q", pushB1.GetClientMsgID(), pushB2.GetClientMsgID())
	}

	conversationID := convid.Single(identityA.GetUserID(), identityB1.GetUserID())
	waitUnread(t, messageClient, conversationID, didB1, 1)
	waitUnread(t, messageClient, conversationID, didB2, 1)
	b1.markRead(t, conversationID, pushB1.GetSeq())
	waitUnread(t, messageClient, conversationID, didB1, 0)
	waitUnread(t, messageClient, conversationID, didB2, 1)

	b2.close()
	time.Sleep(300 * time.Millisecond)
	clientMsgID2 := fmt.Sprintf("integration-%d-2", stamp)
	a.sendChat(t, identityB1.GetUserID(), clientMsgID2, "offline pull")
	_ = receive(t, b1.pushes)
	waitUnread(t, messageClient, conversationID, didB2, 2)

	b2 = newTCPTestClient(t, secondGateway, identityB2.GetUserID(), didB2, identityB2.GetSessionToken())
	defer b2.close()
	ctx, cancel = context.WithTimeout(context.Background(), testTimeout)
	pull, err := messageClient.PullOfflineMsgs(ctx, &messageservice.PullOfflineMsgsRequest{ConversationID: conversationID, FromSeq: pushB2.GetSeq(), Limit: 10})
	cancel()
	if err != nil || len(pull.GetMsgs()) != 1 || pull.GetMsgs()[0].GetClientMsgID() != clientMsgID2 {
		t.Fatalf("offline pull = (%v, %v)", pull, err)
	}
}

func TestLoginAgainAfterClientLogout(t *testing.T) {
	userConn := dialGRPC(t, userEndpoint)
	defer userConn.Close()
	userClient := userservice.NewUserClient(userConn)

	stamp := time.Now().UnixNano()
	account := fmt.Sprintf("logout-login-%d", stamp)
	did := uint64(stamp)
	firstIdentity := register(t, userClient, account, "secret-login", did)

	firstClient := newTCPTestClient(t, primaryGateway, firstIdentity.GetUserID(), did, firstIdentity.GetSessionToken())
	firstClient.close()
	time.Sleep(time.Second)

	secondIdentity := login(t, userClient, account, "secret-login", did)
	secondClient := newTCPTestClient(t, primaryGateway, secondIdentity.GetUserID(), did, secondIdentity.GetSessionToken())
	secondClient.close()
}

func TestGroupMembersAndMultiDeviceDelivery(t *testing.T) {
	userConn := dialGRPC(t, userEndpoint)
	defer userConn.Close()
	groupConn := dialGRPC(t, groupEndpoint)
	defer groupConn.Close()
	messageConn := dialGRPC(t, messageEndpoint)
	defer messageConn.Close()
	userClient := userservice.NewUserClient(userConn)
	groupClient := groupservice.NewGroupClient(groupConn)
	messageClient := messageservice.NewMessageClient(messageConn)

	stamp := time.Now().UnixNano()
	baseDid := uint64(stamp)
	ownerIdentity := register(t, userClient, fmt.Sprintf("group-owner-%d", stamp), "secret-owner", baseDid+1)
	memberIdentity1 := register(t, userClient, fmt.Sprintf("group-member-%d", stamp), "secret-member", baseDid+2)
	memberIdentity2 := login(t, userClient, fmt.Sprintf("group-member-%d", stamp), "secret-member", baseDid+3)

	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	created, err := groupClient.CreateGroup(ctx, &groupservice.CreateGroupReq{
		OwnerUserID: ownerIdentity.GetUserID(), Name: fmt.Sprintf("integration-group-%d", stamp),
	})
	cancel()
	if err != nil || created.GetBase().GetCode() != 0 {
		t.Fatalf("create group = (%v, %v)", created, err)
	}
	groupID := created.GetGroupID()

	ctx, cancel = context.WithTimeout(context.Background(), testTimeout)
	invited, err := groupClient.InviteMembers(ctx, &groupservice.InviteMembersReq{
		GroupID: groupID, OperatorUserID: ownerIdentity.GetUserID(), MemberUserIDs: []uint64{memberIdentity1.GetUserID()},
	})
	cancel()
	if err != nil || invited.GetBase().GetCode() != 0 {
		t.Fatalf("invite group member = (%v, %v)", invited, err)
	}

	ctx, cancel = context.WithTimeout(context.Background(), testTimeout)
	members, err := groupClient.ListMembers(ctx, &groupservice.ListMembersReq{GroupID: groupID})
	cancel()
	if err != nil || len(members.GetMembers()) != 2 {
		t.Fatalf("list members = (%v, %v), want 2", members, err)
	}
	if members.GetMembers()[0].GetMemberUserID() != ownerIdentity.GetUserID() ||
		members.GetMembers()[1].GetMemberUserID() != memberIdentity1.GetUserID() {
		t.Fatalf("member userIDs = [%d %d]", members.GetMembers()[0].GetMemberUserID(), members.GetMembers()[1].GetMemberUserID())
	}

	owner := newTCPTestClient(t, primaryGateway, ownerIdentity.GetUserID(), baseDid+1, ownerIdentity.GetSessionToken())
	defer owner.close()
	member1 := newTCPTestClient(t, primaryGateway, memberIdentity1.GetUserID(), baseDid+2, memberIdentity1.GetSessionToken())
	defer member1.close()
	member2 := newTCPTestClient(t, secondGateway, memberIdentity2.GetUserID(), baseDid+3, memberIdentity2.GetSessionToken())
	defer member2.close()

	clientMsgID := fmt.Sprintf("group-integration-%d", stamp)
	owner.sendGroupChat(t, groupID, clientMsgID, "group multi-device fanout")
	push1, push2 := receive(t, member1.pushes), receive(t, member2.pushes)
	if push1.GetClientMsgID() != clientMsgID || push2.GetClientMsgID() != clientMsgID ||
		push1.GetGroupID() != groupID || push2.GetGroupID() != groupID {
		t.Fatalf("group pushes = (%v, %v)", push1, push2)
	}

	conversationID := convid.Group(groupID)
	waitUnread(t, messageClient, conversationID, baseDid+2, 1)
	waitUnread(t, messageClient, conversationID, baseDid+3, 1)
	member1.markRead(t, conversationID, push1.GetSeq())
	waitUnread(t, messageClient, conversationID, baseDid+2, 0)
	waitUnread(t, messageClient, conversationID, baseDid+3, 1)
}

func dialGRPC(t *testing.T, endpoint string) *grpc.ClientConn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.DialContext(ctx, endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatalf("dial gRPC %s: %v", endpoint, err)
	}
	return conn
}

func register(t *testing.T, client userservice.UserClient, account, password string, did uint64) *userservice.RegisterResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	resp, err := client.Register(ctx, &userservice.RegisterRequest{Account: account, Password: password, Nickname: account, DeviceID: did})
	if err != nil || resp.GetBase().GetCode() != 0 {
		t.Fatalf("register %s = (%v, %v)", account, resp, err)
	}
	return resp
}

func login(t *testing.T, client userservice.UserClient, account, password string, did uint64) *userservice.LoginResponse {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testTimeout)
	defer cancel()
	resp, err := client.Login(ctx, &userservice.LoginRequest{Account: account, Password: password, DeviceID: did})
	if err != nil || resp.GetBase().GetCode() != 0 {
		t.Fatalf("login %s = (%v, %v)", account, resp, err)
	}
	return resp
}

func waitUnread(t *testing.T, client messageservice.MessageClient, conversationID string, did, want uint64) {
	t.Helper()
	deadline := time.Now().Add(testTimeout)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		resp, err := client.GetMaxSeq(ctx, &messageservice.GetMaxSeqRequest{ConversationID: conversationID, Did: did})
		cancel()
		if err == nil && resp.GetBase().GetCode() == 0 && resp.GetUnreadCount() == want {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("unread did=%d did not become %d", did, want)
}

func receive[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(testTimeout):
		var zero T
		t.Fatalf("receive timed out after %s", testTimeout)
		return zero
	}
}
