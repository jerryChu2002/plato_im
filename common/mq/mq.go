// Package mq 对 Kafka(sarama)做了一层薄封装，只暴露"按key同步发送"和"consumer group消费"
// 两个能力，供 state(生产)、domain/message 的 consumer(消费)使用。
package mq

import (
	"context"
	"sync"

	"github.com/IBM/sarama"
)

var (
	producerOnce sync.Once
	syncProducer sarama.SyncProducer
	brokers      []string
)

// InitProducer 初始化一个同步、幂等的生产者：
//   - RequiredAcks=WaitForAll(acks=all)：等待所有ISR副本确认，保证broker端不丢
//   - Idempotent=true：防止客户端网络重试导致broker端出现物理重复(需要MaxOpenRequests=1配合)
func InitProducer(kafkaBrokers []string) {
	producerOnce.Do(func() {
		brokers = kafkaBrokers
		cfg := sarama.NewConfig()
		cfg.Producer.RequiredAcks = sarama.WaitForAll
		cfg.Producer.Idempotent = true
		cfg.Producer.Retry.Max = 5
		cfg.Producer.Return.Successes = true
		cfg.Net.MaxOpenRequests = 1
		var err error
		syncProducer, err = sarama.NewSyncProducer(kafkaBrokers, cfg)
		if err != nil {
			panic(err)
		}
	})
}

// PublishWithKey 同步发送一条消息；key相同的消息会被sarama默认的hash分区器路由到
// 同一个分区，从而保证单分区内"生产顺序==消费顺序"(比如用conversationID做key，
// 保证同一会话的消息严格有序)。
func PublishWithKey(topic, key string, value []byte) (partition int32, offset int64, err error) {
	msg := &sarama.ProducerMessage{
		Topic: topic,
		Key:   sarama.StringEncoder(key),
		Value: sarama.ByteEncoder(value),
	}
	return syncProducer.SendMessage(msg)
}

// Handler 业务消费回调。返回error时该条消息不会被标记为已消费，offset也不会被提交，
// consumer group后续会重新投递这条消息——依赖业务层的幂等落库来保证"重复消费不出错"。
type Handler func(ctx context.Context, key, value []byte) error

type consumerGroupHandler struct {
	handler Handler
}

func (h *consumerGroupHandler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *consumerGroupHandler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *consumerGroupHandler) ConsumeClaim(sess sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		if err := h.handler(sess.Context(), msg.Key, msg.Value); err != nil {
			// 业务处理失败：既不MarkMessage也不提交offset，故意什么都不做，
			// 等下一轮poll/rebalance时这条消息会被重新投递
			continue
		}
		sess.MarkMessage(msg, "")
		sess.Commit()
	}
	return nil
}

// StartConsumerGroup 阻塞式启动一个consumer group。
// AutoCommit被关闭，只有handler处理成功后才MarkMessage+Commit，
// 这是"consumer宕机重启/rebalance后不丢消息(at-least-once)"的关键。
// 调用方应该在独立goroutine里调用本函数。
func StartConsumerGroup(kafkaBrokers []string, groupID string, topics []string, handler Handler) error {
	cfg := sarama.NewConfig()
	cfg.Consumer.Offsets.AutoCommit.Enable = false
	cfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	cg, err := sarama.NewConsumerGroup(kafkaBrokers, groupID, cfg)
	if err != nil {
		return err
	}
	h := &consumerGroupHandler{handler: handler}
	ctx := context.Background()
	for {
		if err := cg.Consume(ctx, topics, h); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}
