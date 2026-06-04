// Package config 提供 Pandora 服务的通用配置结构。
//
// 设计:
//   - 基础字段(Server/Node/Redis/Kafka/Snowflake/Locker/Registry/Timeouts)集中放 Base
//   - 各服务的 internal/conf/conf.go 嵌入 Base 并加业务字段
//   - 配置加载用 Kratos config(W2 file source,W3+ 接 etcd)
//
// 跟之前 go-zero 版本的区别(2026-06-04 重写):
//   - 删 zrpc.RpcServerConf 嵌入,改 Pandora 自定义 Server 结构
//   - go-zero LogConf 改 zap(详见 pkg/log/log.go)
//   - 字段保留 mmorpg 拷过来的语义,但风格按 Kratos 惯例(yaml + protobuf 都能映射)
//
// 文件加载示例(各服务 main.go):
//
//	c := kconfig.New(kconfig.WithSource(file.NewSource("./etc/login-dev.yaml")))
//	if err := c.Load(); err != nil { panic(err) }
//	var cfg config.Base
//	if err := c.Scan(&cfg); err != nil { panic(err) }
package config

import (
	"fmt"
	"time"

	"github.com/IBM/sarama"
)

// Base 是所有 Pandora 服务的通用配置基类。
//
// 各业务服务 internal/conf/conf.go 模板:
//
//	type Config struct {
//	    config.Base `yaml:",inline"`             // 公共
//	    BusinessKnob int    `yaml:"business_knob"` // 业务私有
//	}
type Base struct {
	// Server 监听配置(gRPC + HTTP)
	Server Server `yaml:"server"`

	// Node 节点级配置(redis 客户端、session 超时等)
	Node NodeConfig `yaml:"node"`

	// Snowflake 全局 ID 生成参数
	Snowflake SnowflakeConf `yaml:"snowflake,omitempty"`

	// Locker 分布式锁默认 TTL
	Locker LockerConf `yaml:"locker,omitempty"`

	// Registry 服务注册发现(W2 用 file 配置,W3+ 接 etcd)
	Registry RegistryConf `yaml:"registry,omitempty"`

	// Timeouts 各种通用超时
	Timeouts TimeoutConf `yaml:"timeouts,omitempty"`

	// Kafka 生产者/消费者通用配置
	Kafka KafkaConfig `yaml:"kafka,omitempty"`
}

// Server Kratos 风格的 server 监听配置(替代 go-zero zrpc.RpcServerConf)。
type Server struct {
	Grpc Grpc `yaml:"grpc"`
	Http Http `yaml:"http,omitempty"` // 可选,W2 大部分服务只暴露 gRPC
}

// Grpc gRPC server 监听。
type Grpc struct {
	Network string        `yaml:"network,omitempty"` // 默认 "tcp"
	Addr    string        `yaml:"addr"`              // 例 ":50001"
	Timeout time.Duration `yaml:"timeout,omitempty"` // 默认 1s
}

// Http HTTP server 监听(给 protoc-gen-go-http 生成的 handler 用)。
type Http struct {
	Network string        `yaml:"network,omitempty"`
	Addr    string        `yaml:"addr"`              // 例 ":51001"
	Timeout time.Duration `yaml:"timeout,omitempty"`
}

// NodeConfig 节点级配置。
type NodeConfig struct {
	// ZoneId 是分服 ID。Pandora 单服模式默认填 1。
	ZoneId           uint32        `yaml:"zone_id"`
	SessionExpireMin uint32        `yaml:"session_expire_min,omitempty"` // 默认 1440 (24h)
	RedisClient      RedisConf     `yaml:"redis_client"`
	LeaseTTL         int64         `yaml:"lease_ttl,omitempty"`          // 秒,默认 10
	MaxLoginDuration time.Duration `yaml:"max_login_duration,omitempty"` // 默认 24h
	LogoutGraceTime  time.Duration `yaml:"logout_grace_time,omitempty"`  // 默认 5m
}

// RedisConf Redis 客户端配置。
type RedisConf struct {
	Host         string        `yaml:"host"`
	Password     string        `yaml:"password,omitempty"`
	DB           uint32        `yaml:"db,omitempty"`
	DefaultTTL   time.Duration `yaml:"default_ttl,omitempty"`
	DialTimeout  time.Duration `yaml:"dial_timeout,omitempty"`
	ReadTimeout  time.Duration `yaml:"read_timeout,omitempty"`
	WriteTimeout time.Duration `yaml:"write_timeout,omitempty"`
}

// KafkaConfig Kafka 生产/消费通用配置。
type KafkaConfig struct {
	Brokers          []string      `yaml:"brokers"`
	GroupID          string        `yaml:"group_id,omitempty"`
	PartitionCnt     int32         `yaml:"partition_cnt,omitempty"`     // 默认 4
	InitialPartition int           `yaml:"initial_partition,omitempty"` // 默认 4
	DialTimeout      time.Duration `yaml:"dial_timeout,omitempty"`
	ReadTimeout      time.Duration `yaml:"read_timeout,omitempty"`
	WriteTimeout     time.Duration `yaml:"write_timeout,omitempty"`
	RetryMax         int           `yaml:"retry_max,omitempty"`
	RetryBackoff     time.Duration `yaml:"retry_backoff,omitempty"`
	ChannelBuffer    int           `yaml:"channel_buffer,omitempty"`
	SyncInterval     time.Duration `yaml:"sync_interval,omitempty"`
	StatsInterval    time.Duration `yaml:"stats_interval,omitempty"`
	// CompressionType: "none" | "gzip" | "snappy" | "lz4" | "zstd"(默认 none)
	// 用 string 比 int 更人类可读,内部用 ParseCompression 转换。
	CompressionType string `yaml:"compression_type,omitempty"`
	Idempotent      bool   `yaml:"idempotent,omitempty"`        // 默认 true
	MaxOpenRequests int    `yaml:"max_open_requests,omitempty"` // idempotent=true 时必须为 1
	RetentionMs     int64  `yaml:"retention_ms,omitempty"`      // 默认 7 天
}

// ParseCompression 把 yaml 里的字符串转成 sarama 类型。
// 不识别的值返回 sarama.CompressionNone(不报错,日志由调用方打)。
func (k KafkaConfig) ParseCompression() sarama.CompressionCodec {
	switch k.CompressionType {
	case "gzip":
		return sarama.CompressionGZIP
	case "snappy":
		return sarama.CompressionSnappy
	case "lz4":
		return sarama.CompressionLZ4
	case "zstd":
		return sarama.CompressionZSTD
	case "", "none":
		return sarama.CompressionNone
	default:
		return sarama.CompressionNone
	}
}

// SnowflakeConf 雪花算法参数。
type SnowflakeConf struct {
	Epoch    int64  `yaml:"epoch,omitempty"`     // 默认 1773446400 (2026-03-14 UTC)
	NodeBits uint32 `yaml:"node_bits,omitempty"` // 默认 17
	StepBits uint32 `yaml:"step_bits,omitempty"` // 默认 15
}

// LockerConf 分布式锁默认 TTL。
type LockerConf struct {
	AccountLockTTL uint32 `yaml:"account_lock_ttl,omitempty"` // 秒,默认 10
	PlayerLockTTL  uint32 `yaml:"player_lock_ttl,omitempty"`
}

// RegistryConf 服务注册发现。
type RegistryConf struct {
	Etcd EtcdRegistryConf `yaml:"etcd,omitempty"`
}

// EtcdRegistryConf etcd 注册中心(W3+ 接入)。
type EtcdRegistryConf struct {
	Hosts       []string      `yaml:"hosts"`
	Key         string        `yaml:"key,omitempty"`           // service path,默认按服务名构造
	DialTimeout time.Duration `yaml:"dial_timeout,omitempty"`  // 默认 5s
}

// TimeoutConf 各种公共超时。
type TimeoutConf struct {
	EtcdDialTimeout         time.Duration `yaml:"etcd_dial_timeout,omitempty"`
	ServiceDiscoveryTimeout time.Duration `yaml:"service_discovery_timeout,omitempty"`
	TaskWaitTimeout         time.Duration `yaml:"task_wait_timeout,omitempty"`
	RoleCacheExpire         time.Duration `yaml:"role_cache_expire,omitempty"`
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
	const prefix = "pandora."
	if len(originalTopic) > len(prefix) && originalTopic[:len(prefix)] == prefix {
		return "pandora.dlq." + originalTopic[len(prefix):]
	}
	return "pandora.dlq." + originalTopic
}
