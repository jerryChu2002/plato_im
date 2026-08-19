package consumer

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/hardcore-os/plato/common/cache"
	"github.com/hardcore-os/plato/common/config"
	"github.com/hardcore-os/plato/common/convid"
	"github.com/hardcore-os/plato/common/gwclient"
	messagedto "github.com/hardcore-os/plato/common/idl/domain/message"
	"github.com/hardcore-os/plato/common/mq"
	"github.com/hardcore-os/plato/common/prpc"
	"github.com/hardcore-os/plato/common/router"
	"github.com/hardcore-os/plato/common/stateroute"
	groupservice "github.com/hardcore-os/plato/domain/group/rpc/service"
	"github.com/hardcore-os/plato/domain/message/storage"
	userservice "github.com/hardcore-os/plato/domain/user/rpc/service"
	"google.golang.org/protobuf/proto"
)

var (
	groupClient     groupservice.GroupClient
	groupClientOnce sync.Once
	userClient      userservice.UserClient
	userClientOnce  sync.Once
)

// getGroupClient 惰性建立到group领域服务的rpc client。这是"任意一台健康实例都行"的
// 无状态读请求，走common/prpc的标准服务发现+负载均衡路径，与gwclient(精确路由到
// 具体一台持有连接状态的实例)是两种不同的调用场景。
func getGroupClient() groupservice.GroupClient {
	groupClientOnce.Do(func() {
		pCli, err := prpc.NewPClient(config.GetDomainGroupServerName())
		if err != nil {
			panic(err)
		}
		groupClient = groupservice.NewGroupClient(pCli.Conn())
	})
	return groupClient
}

func getUserClient() userservice.UserClient {
	userClientOnce.Do(func() {
		pCli, err := prpc.NewPClient(config.GetDomainUserServerName())
		if err != nil {
			panic(err)
		}
		userClient = userservice.NewUserClient(pCli.Conn())
	})
	return userClient
}

func listUserDeviceDids(ctx context.Context, userID uint64) ([]uint64, error) {
	resp, err := getUserClient().ListUserDevices(ctx, &userservice.ListUserDevicesRequest{UserID: userID})
	if err != nil {
		return nil, err
	}
	if resp.GetBase().GetCode() != 0 {
		return nil, fmt.Errorf("list user devices: %s", resp.GetBase().GetMsg())
	}
	return resp.GetDeviceIDs(), nil
}

func listGroupMemberUserIDs(ctx context.Context, groupID uint64) ([]uint64, error) {
	resp, err := getGroupClient().ListMembers(ctx, &groupservice.ListMembersReq{GroupID: groupID})
	if err != nil {
		return nil, err
	}
	userIDs := make([]uint64, 0, len(resp.GetMembers()))
	for _, m := range resp.GetMembers() {
		userIDs = append(userIDs, m.GetMemberUserID())
	}
	return userIDs, nil
}

// Start 阻塞式启动Kafka consumer group：落库(幂等) + 决定接收者 + 委托目标state分片推送。
// 调用方应该在独立goroutine里调用本函数。
func Start(sm *storage.StorageManager) {
	stateroute.Init()
	err := mq.StartConsumerGroup(
		config.GetKafkaBrokers(),
		config.GetKafkaConsumerGroup(),
		[]string{config.GetKafkaChatTopic()},
		func(ctx context.Context, key, value []byte) error {
			return handleChatMsg(ctx, sm, value)
		},
	)
	if err != nil {
		panic(err)
	}
}

func handleChatMsg(ctx context.Context, sm *storage.StorageManager, value []byte) error {
	dto := &messagedto.ChatMsgDTO{}
	if err := proto.Unmarshal(value, dto); err != nil {
		// 反序列化失败通常是格式问题，重试也没用，但为了遵循"处理失败不提交offset"的统一
		// 策略这里仍返回error(消息会被反复重试)。生产环境应该有死信队列/毒消息隔离机制，
		// 当前demo规模下暂不实现，只在此处留下这段说明。
		fmt.Printf("consumer: unmarshal err=%s\n", err.Error())
		return err
	}

	// 1. 幂等落库：Kafka是at-least-once语义，同一条消息可能被投递/消费多次，
	// SaveMsg内部用(conversation_id, client_msg_id)唯一约束做幂等，重复调用不会产生脏数据。
	isNew, err := sm.SaveMsg(ctx, &storage.MessageDAO{
		ConversationID: dto.ConversationID,
		FromDid:        dto.FromDid,
		ToDid:          dto.ToDid,
		FromUserID:     dto.FromUserID,
		ToUserID:       dto.ToUserID,
		GroupID:        dto.GroupID,
		BizType:        int32(dto.BizType),
		Content:        dto.Content,
		Seq:            dto.Seq,
		ClientMsgID:    dto.ClientMsgID,
		CreateTimeMS:   dto.CreateTime,
	})
	if err != nil {
		fmt.Printf("consumer: SaveMsg err=%s\n", err.Error())
		return err
	}
	// 数据库是事实来源；缓存写成功后才允许Kafka提交offset。若进程在两步之间退出，
	// Kafka会重新投递，数据库唯一约束跳过重复插入，下面的ZSET写入则继续幂等修复缓存。
	if err := cache.AddRecentMessage(ctx, dto.ConversationID, cache.ScoredMessage{Seq: dto.Seq, Payload: value}); err != nil {
		fmt.Printf("consumer: AddRecentMessage err=%s\n", err.Error())
		return err
	}
	if !isNew {
		// 这条消息之前已经成功落库过了(Kafka重复投递同一条消息)，说明第一次处理时
		// 已经推送过一次，这里必须跳过，否则用户会重复收到同一条消息。
		return nil
	}

	// 2. 决定接收者并推送。只推给当前在线的用户；离线用户不做任何特殊标记——
	// 消息已经落库，群/单聊成员上线后可以通过历史消息拉取拿到。
	if convid.IsGroup(dto.ConversationID) {
		groupID, ok := convid.ParseGroupID(dto.ConversationID)
		if !ok {
			return nil
		}
		memberUserIDs, err := listGroupMemberUserIDs(ctx, groupID)
		if err != nil {
			fmt.Printf("consumer: listGroupMemberUserIDs err=%s\n", err.Error())
			// 落库已经成功，拉取成员失败不影响"消息不丢"，只是这次没能实时推送
			return nil
		}
		for _, memberUserID := range memberUserIDs {
			if memberUserID == dto.FromUserID {
				continue // 不用推给发送者自己
			}
			memberDids, err := listUserDeviceDids(ctx, memberUserID)
			if err != nil {
				fmt.Printf("consumer: listUserDeviceDids userID=%d err=%s\n", memberUserID, err.Error())
				continue
			}
			for _, memberDid := range memberDids {
				pushToTarget(ctx, memberDid, dto)
			}
		}
		return nil
	}
	targetDids, err := listUserDeviceDids(ctx, dto.ToUserID)
	if err != nil {
		fmt.Printf("consumer: listUserDeviceDids userID=%d err=%s\n", dto.ToUserID, err.Error())
		return nil
	}
	for _, targetDid := range targetDids {
		pushToTarget(ctx, targetDid, dto)
	}
	return nil
}

// pushToTarget 单聊/群聊fan-out共用的推送路径：查路由确认目标did在不在线 ->
// 按一致性哈希找到该did当前由哪个state实例托管 -> 委托该实例的PushToConn代为推送。
// 推送的payload是整个marshal后的ChatMsgDTO(而不仅是dto.Content)，与好友/群通知
// (common/notify.PushSystemMsg)使用的信封格式保持一致，客户端只需按一套逻辑
// (先unmarshal ChatMsgDTO，再按BizType分支处理)解析所有类型的下行推送。
func pushToTarget(ctx context.Context, targetDid uint64, dto *messagedto.ChatMsgDTO) {
	if targetDid == 0 {
		return
	}
	rec, err := router.QueryRecord(ctx, targetDid)
	if err != nil || rec == nil {
		// 查不到路由 = 对方当前不在线，消息已经落库，不算失败
		return
	}
	stateEndpoint, ok := stateroute.Resolve(targetDid)
	if !ok {
		fmt.Printf("consumer: no state instance available for did=%d\n", targetDid)
		return
	}
	payload, err := proto.Marshal(dto)
	if err != nil {
		fmt.Printf("consumer: marshal ChatMsgDTO err=%s\n", err.Error())
		return
	}
	rpcCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := gwclient.PushToConnOnState(rpcCtx, stateEndpoint, rec.ConndID, dto.Seq, payload); err != nil {
		fmt.Printf("consumer: PushToConnOnState err=%s\n", err.Error())
	}
}
