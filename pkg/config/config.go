// Package config 提供 Pandora 服务的通用配置结构。
//
// 设计来源:抽自 mmorpg/go/login/internal/config/config.go,剥掉 MMO 业务
// 字段(LegacyGate / SaToken / SceneManager 等),保留 go-zero zrpc 集成 +
// 公共基础设施配置(Redis / Kafka / Snowflake / Locker / Etcd / Timeouts)。
//
// 用法:各服务的 internal/config/config.go 嵌入 config.Base 并加业务字段。
//
//	type Config struct {
//	    config.Base                           // 公共
//	    PlayerLocatorRpc zrpc.RpcClientConf   // 业务私有
//	    MyBusinessKnob   int
//	}
package config

import (
	"fmt"
	"time"

	"github.com/IBM/sarama"
	"github.com/zeromicro/go-zero/zrpc"
)

// Base 是所有 Pandora 服务的通用配置基��。
// 嵌入 zrpc.RpcServerConf 获得 go-zero 服务发现/注册/Listen 全套。
type Base struct {
	zrpc.RpcServerConf

	// Node 节点级配置(redis 客户端、session 超时等)
	Node NodeConfig `json:"Node"`

	// Snowflake 全局 ID 生成参数
	Snowflake SnowflakeConf `json:"Snowflake"`

	// Locker 分布式锁默认 TTL
	Locker LockerConf `json:"Locker,optional"`

	// Registry 服务注册发现(默认走 zrpc 内置 etcd)
	Registry RegistryConf `json:"Registry,optional"`

	// Timeouts 各种通用超时
	Timeouts TimeoutConf `json:"Timeouts,optional"`

	// Kafka 生产者/消费者通用配置(无 topic 字段,由业务侧 BuildTopic)
	Kafka KafkaConfig `json:"Kafka,optional"`
}

// NodeConfig 节点级配置。
type NodeConfig struct {
	// ZoneId 是分服 ID。Pandora 单服模式默认填 1。
	ZoneId           uint32        `json:"ZoneId,default=1"`
	SessionExpireMin uint32        `json:"SessionExpireMin,default=1440"` // 24h
	RedisClient      RedisConf     `json:"RedisClient"`
	LeaseTTL         int64         `json:"LeaseTTL,default=10"`           // 秒
	MaxLoginDuration time.Duration `json:"MaxLoginDuration,default=24h"`
	LogoutGraceTime  time.Duration `json:"LogoutGraceTime,default=5m"`
}

// RedisConf Redis 客户端配置。
type RedisConf struct {
	Host         string        `json:"Host"`
	Password     string        `json:"Password,optional"`
	DB           uint32        `json:"DB,default=0"`
	DefaultTTL   time.Duration `json:"DefaultTTL,default=24h"`
	DialTimeout  time.Duration `json:"DialTimeout,default=3s"`
	ReadTimeout  time.Duration `json:"ReadTimeout,default=2s"`
	WriteTimeout time.Duration `json:"WriteTimeout,default=2s"`
}

// KafkaConfig Kafka 生产/消费通用配置。
type KafkaConfig struct {
	Brokers          []string                `json:"Brokers"`
	GroupID          string                  `json:"GroupID,optional"`
	PartitionCnt     int32                   `json:"PartitionCnt,default=4"`
	InitialPartition int                     `json:"InitialPartition,default=4"`
	DialTimeout      time.Duration           `json:"DialTimeout,default=10s"`
	ReadTimeout      time.Duration           `json:"ReadTimeout,default=10s"`
	WriteTimeout     time.Duration           `json:"WriteTimeout,default=10s"`
	RetryMax         int                     `json:"RetryMax,default=3"`
	RetryBackoff     time.Duration           `json:"RetryBackoff,default=200ms"`
	ChannelBuffer    int                     `json:"ChannelBuffer,default=256"`
	SyncInterval     time.Duration           `json:"SyncInterval,default=1s"`
	StatsInterval    time.Duration           `json:"StatsInterval,default=30s"`
	CompressionType  sarama.CompressionCodec `json:"CompressionType,default=0"`
	Idempotent       bool                    `json:"Idempotent,default=true"`
	MaxOpenRequests  int                     `json:"MaxOpenRequests,default=1"`
	RetentionMs      int64                   `json:"RetentionMs,default=604800000"` // 7 天
}

// SnowflakeConf 雪花算法参数。Pandora 默认 17 位 NodeID + 15 位 step。
type SnowflakeConf struct {
	Epoch    int64  `json:"Epoch,default=1773446400"` // 2026-03-14 UTC
	NodeBits uint32 `json:"NodeBits,default=17"`
	StepBits uint32 `json:"StepBits,default=15"`
}

// LockerConf 分布式锁默认 TTL。
type LockerConf struct {
	AccountLockTTL uint32 `json:"AccountLockTTL,default=10"` // 秒
	PlayerLockTTL  uint32 `json:"PlayerLockTTL,default=10"`
}

// RegistryConf 服务注册发现。
type RegistryConf struct {
	Etcd EtcdRegistryConf `json:"Etcd"`
}

// EtcdRegistryConf etcd 注册中心。
type EtcdRegistryConf struct {
	Hosts       []string      `json:"Hosts"`
	Key         string        `json:"Key,optional"` // service path,默认按服务名构造
	DialTimeout time.Duration `json:"DialTimeout,default=5s"`
}

// TimeoutConf 各种公共超时。
type TimeoutConf struct {
	EtcdDialTimeout         time.Duration `json:"EtcdDialTimeout,default=5s"`
	ServiceDiscoveryTimeout time.Duration `json:"ServiceDiscoveryTimeout,default=5s"`
	TaskWaitTimeout         time.Duration `json:"TaskWaitTimeout,default=10s"`
	RoleCacheExpire         time.Duration `json:"RoleCacheExpire,default=5m"`
}

// BuildTopic 按 docs/design/infra.md §4 规范构造 kafka topic。
//
//	BuildTopic("battle", "result")          → "pandora.battle.result"
//	BuildTopic("login",  "event")           → "pandora.login.event"
func BuildTopic(domain, event string) string {
	return fmt.Sprintf("pandora.%s.%s", domain, event)
}

// BuildDLQTopic 构造死信队列 topic(infra.md §4.4)。
//
//	BuildDLQTopic("pandora.battle.result") → "pandora.dlq.battle.result"
func BuildDLQTopic(originalTopic string) string {
	// 替换 "pandora." 前缀为 "pandora.dlq."
	const prefix = "pandora."
	if len(originalTopic) > len(prefix) && originalTopic[:len(prefix)] == prefix {
		return "pandora.dlq." + originalTopic[len(prefix):]
	}
	return "pandora.dlq." + originalTopic
}
