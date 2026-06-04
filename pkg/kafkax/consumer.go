// Package kafkax — Consumer
//
// 来源:抽自 mmorpg/go/db/internal/kafka/key_ordered_consumer.go,**剥业务依赖**
// (db_proto.DBTask / proto_sql / proto2mysql / dynamicpb / scene-specific cache key),
// 保留:
//   - sarama ConsumerGroup + per-partition worker
//   - Handler 接口让业务自己处理消息(不再绑定 DBTask schema)
//
// **W1-D2 阶段**:不实现 redis retry queue + DLQ,留 W2 battle_result 时再补。
package kafkax

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/IBM/sarama"
	klog "github.com/go-kratos/kratos/v2/log"
)

// Handler 是消息处理函数,由业务实现。
//
// 返回 nil → ack。返回非 nil → 当前实现:log + ack(W1-D2 简化版,W2 接入 retry/DLQ 时改)。
type Handler func(ctx context.Context, msg *sarama.ConsumerMessage) error

// KeyOrderedConsumer 是 Pandora 通用 Kafka 消费者。
type KeyOrderedConsumer struct {
	consumer       sarama.ConsumerGroup
	topic          string
	groupID        string
	partitionCount int32
	handler        Handler
	workers        map[int32]*worker
	wg             *sync.WaitGroup
	ctx            context.Context
	cancel         context.CancelFunc
}

// ConsumerConfig 是消费者配置。
type ConsumerConfig struct {
	Brokers        []string
	Topic          string
	GroupID        string
	PartitionCount int32
	Version        sarama.KafkaVersion // 默认 V3_6_0_0
}

// NewKeyOrderedConsumer 创建消费者。
func NewKeyOrderedConsumer(cfg ConsumerConfig, handler Handler) (*KeyOrderedConsumer, error) {
	if handler == nil {
		return nil, errors.New("handler must not be nil")
	}
	if cfg.GroupID == "" {
		return nil, errors.New("groupID must not be empty")
	}

	c := sarama.NewConfig()
	if cfg.Version == (sarama.KafkaVersion{}) {
		c.Version = sarama.V3_6_0_0
	} else {
		c.Version = cfg.Version
	}
	c.Consumer.Offsets.Initial = sarama.OffsetOldest
	c.Consumer.Return.Errors = true

	cg, err := sarama.NewConsumerGroup(cfg.Brokers, cfg.GroupID, c)
	if err != nil {
		return nil, fmt.Errorf("new consumer group: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &KeyOrderedConsumer{
		consumer:       cg,
		topic:          cfg.Topic,
		groupID:        cfg.GroupID,
		partitionCount: cfg.PartitionCount,
		handler:        handler,
		workers:        make(map[int32]*worker),
		wg:             &sync.WaitGroup{},
		ctx:            ctx,
		cancel:         cancel,
	}, nil
}

// Start 启动消费循环。
func (k *KeyOrderedConsumer) Start() {
	k.wg.Add(1)
	go func() {
		defer k.wg.Done()
		for {
			select {
			case <-k.ctx.Done():
				return
			default:
			}
			if err := k.consumer.Consume(k.ctx, []string{k.topic}, k); err != nil {
				if errors.Is(err, sarama.ErrClosedConsumerGroup) {
					return
				}
				klog.Errorf("[kafkax] consume err topic=%s: %v", k.topic, err)
			}
		}
	}()

	k.wg.Add(1)
	go func() {
		defer k.wg.Done()
		for {
			select {
			case <-k.ctx.Done():
				return
			case err, ok := <-k.consumer.Errors():
				if !ok {
					return
				}
				klog.Errorf("[kafkax] consumer error: %v", err)
			}
		}
	}()

	klog.Infof("[kafkax] consumer started: topic=%s group=%s", k.topic, k.groupID)
}

// Close 优雅关闭。
func (k *KeyOrderedConsumer) Close() error {
	k.cancel()
	if err := k.consumer.Close(); err != nil {
		klog.Errorf("[kafkax] close: %v", err)
	}
	k.wg.Wait()
	klog.Infof("[kafkax] consumer closed: topic=%s", k.topic)
	return nil
}

// ==================== sarama.ConsumerGroupHandler 接口实现 ====================

func (k *KeyOrderedConsumer) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (k *KeyOrderedConsumer) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (k *KeyOrderedConsumer) ConsumeClaim(
	sess sarama.ConsumerGroupSession,
	claim sarama.ConsumerGroupClaim,
) error {
	for {
		select {
		case msg, ok := <-claim.Messages():
			if !ok {
				return nil
			}
			if err := k.handler(sess.Context(), msg); err != nil {
				// W1-D2 简化:log + ack。W2 写 battle_result 时改为 retry queue / DLQ。
				klog.Errorf("[kafkax] handler err topic=%s partition=%d offset=%d key=%s: %v",
					msg.Topic, msg.Partition, msg.Offset, string(msg.Key), err)
			}
			sess.MarkMessage(msg, "")
		case <-sess.Context().Done():
			return nil
		}
	}
}

// worker 占位(W2 实现 per-partition worker 队列时启用)。
type worker struct {
	partition int32
}
