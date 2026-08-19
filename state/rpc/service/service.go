package service

import (
	context "context"
	"fmt"
)

const (
	CancelConnCmd = 1
	SendMsgCmd    = 2
	PushToConnCmd = 3
)

type CmdContext struct {
	Ctx      *context.Context
	Cmd      int32
	Endpoint string
	ConnID   uint64
	Payload  []byte
	Seq      uint64
}

type Service struct {
	CmdChannel chan *CmdContext
}

func (s *Service) CancelConn(ctx context.Context, sr *StateRequest) (*StateResponse, error) {
	c := context.TODO()
	s.CmdChannel <- &CmdContext{
		Ctx:      &c,
		Cmd:      CancelConnCmd,
		ConnID:   sr.ConnID,
		Endpoint: sr.GetEndpoint(),
	}
	return &StateResponse{
		Code: 0,
		Msg:  "success",
	}, nil
}

// SendMsg 由gateway server rpc过来
func (s *Service) SendMsg(ctx context.Context, sr *StateRequest) (*StateResponse, error) {
	c := context.TODO()
	s.CmdChannel <- &CmdContext{
		Ctx:      &c,
		Cmd:      SendMsgCmd,
		ConnID:   sr.ConnID,
		Endpoint: sr.GetEndpoint(),
		Payload:  sr.GetData(),
	}
	fmt.Printf("SendMsg connID=%d, channel=%d\n", sr.ConnID, len(s.CmdChannel))
	return &StateResponse{
		Code: 0,
		Msg:  "success",
	}, nil
}

// mustEmbedUnimplementedStateServer 满足新版protoc-gen-go-grpc生成的StateServer接口
// 要求的前向兼容标记方法(与domain/user/rpc/service/service.go的既有写法保持一致)。
func (s *Service) mustEmbedUnimplementedStateServer() {}

// PushToConn 供跨分片/跨服务的调用方(好友通知、群消息fan-out、Kafka consumer等)使用，
// 委托"持有目标connID内存状态"的这个state实例代为推送
func (s *Service) PushToConn(ctx context.Context, sr *StateRequest) (*StateResponse, error) {
	c := context.TODO()
	s.CmdChannel <- &CmdContext{
		Ctx:     &c,
		Cmd:     PushToConnCmd,
		ConnID:  sr.ConnID,
		Payload: sr.GetData(),
		Seq:     sr.GetSeq(),
	}
	return &StateResponse{
		Code: 0,
		Msg:  "success",
	}, nil
}
