// Package pushenc 提供"把业务内容包装成客户端可解析的下行推送字节流"的公共封装：
// MsgCmd{Type:Push, Payload: PushMsg{Content, MsgID}}，与gateway/common/tcp的收发协议一致。
// 供不经过state可靠推送状态机(ACK重推)的"低频/非关键路径"直接推送场景使用，
// 比如好友申请通知、群邀请通知——避免每个调用方各自重复这两层marshal的样板代码。
package pushenc

import (
	"github.com/hardcore-os/plato/common/idl/message"
	"google.golang.org/protobuf/proto"
)

// WrapPush 把content包装成MsgCmd{Type:Push, Payload:PushMsg{Content,MsgID}}并序列化，
// 返回的字节流可以直接交给gwclient.PushToGateway下发。
func WrapPush(content []byte, msgID uint64) ([]byte, error) {
	pushMsg := &message.PushMsg{Content: content, MsgID: msgID}
	payload, err := proto.Marshal(pushMsg)
	if err != nil {
		return nil, err
	}
	mc := &message.MsgCmd{Type: message.CmdType_Push, Payload: payload}
	return proto.Marshal(mc)
}
