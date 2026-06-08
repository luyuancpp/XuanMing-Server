// Package conf 是 ds_allocator 服务的私有配置结构。
package conf

import (
	"time"

	"github.com/luyuancpp/pandora/pkg/config"
)

// Config 是 ds_allocator 服务的完整配置。
type Config struct {
	config.Base `yaml:",inline" mapstructure:",squash"`

	Allocator AllocatorConf `yaml:"allocator" json:"allocator"`
	Agones    AgonesConf    `yaml:"agones" json:"agones"`
}

// AgonesConf 是真 Agones GameServerAllocation 后端配置(W4 ⑫)。
//
// Enabled=false(默认)→ 用 MockGameServerAllocator;Enabled=true → 用
// AgonesGameServerAllocator(经 k8s apiserver REST 调 allocation.agones.dev/v1
// GameServerAllocation,provider 无关:ACK / 自建 / minikube 上跑的 Agones 都一致)。
//
// 集群内运行时 token_path / ca_path / api_server / namespace 留空即用 in-cluster 默认;
// 集群外联调可显式指定 api_server + token_path(或经 kubectl proxy 不带 token)。
type AgonesConf struct {
	// Enabled 打开真 Agones 分配(默认 false → Mock)。
	Enabled bool `yaml:"enabled,omitempty" json:"enabled,omitempty"`

	// APIServer k8s apiserver 地址(默认 https://kubernetes.default.svc,in-cluster)。
	APIServer string `yaml:"api_server,omitempty" json:"api_server,omitempty"`

	// Namespace GameServerAllocation / GameServer 所在命名空间(默认 default)。
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`

	// FleetName 选择 GameServer 的 Fleet 名(selector agones.dev/fleet=<FleetName>)。
	// Enabled=true 时必填,否则构造失败。
	FleetName string `yaml:"fleet_name,omitempty" json:"fleet_name,omitempty"`

	// TokenPath ServiceAccount bearer token 文件路径
	// (默认 /var/run/secrets/kubernetes.io/serviceaccount/token;留 "-" 显式禁用 token)。
	TokenPath string `yaml:"token_path,omitempty" json:"token_path,omitempty"`

	// CAPath apiserver CA 证书路径
	// (默认 /var/run/secrets/kubernetes.io/serviceaccount/ca.crt)。
	CAPath string `yaml:"ca_path,omitempty" json:"ca_path,omitempty"`

	// InsecureSkipTLSVerify 跳过 apiserver TLS 校验(仅 dev,生产禁用)。
	InsecureSkipTLSVerify bool `yaml:"insecure_skip_tls_verify,omitempty" json:"insecure_skip_tls_verify,omitempty"`

	// AllocateTimeout 单次 allocate / release REST 调用超时(默认 5s)。
	AllocateTimeout config.Duration `yaml:"allocate_timeout,omitempty" json:"allocate_timeout,omitempty"`
}

// AllocatorConf 是 ds_allocator 服务私有配置。
type AllocatorConf struct {
	// HeartbeatTimeout DS 心跳超时阈值(默认 15s,不变量 §4)。
	// 超过此时长没收到 Heartbeat → 标记 abandoned + 释放(W4 ② 仅释放,补偿留 W4 ③)。
	HeartbeatTimeout config.Duration `yaml:"heartbeat_timeout,omitempty" json:"heartbeat_timeout,omitempty"`

	// SweepInterval 心跳超时扫描间隔(默认 5s)。
	SweepInterval config.Duration `yaml:"sweep_interval,omitempty" json:"sweep_interval,omitempty"`

	// BattleTTL 战斗 DS 镜像 Redis key 的 TTL(默认 2h,防僵尸镜像)。
	BattleTTL config.Duration `yaml:"battle_ttl,omitempty" json:"battle_ttl,omitempty"`

	// MockDSAddrHost W4 ② MockGameServerAllocator 返回的假 DS host(默认 127.0.0.1)。
	// W4 ③ 接 Agones 后此字段废弃,addr 由 GameServerAllocation status 返回。
	MockDSAddrHost string `yaml:"mock_ds_addr_host,omitempty" json:"mock_ds_addr_host,omitempty"`

	// MockDSPortBase W4 ② MockGameServerAllocator 端口基址(默认 30000)。
	// 每场 match 端口 = MockDSPortBase + (match_id % MockDSPortRange)。
	MockDSPortBase int `yaml:"mock_ds_port_base,omitempty" json:"mock_ds_port_base,omitempty"`

	// MockDSPortRange Mock 端口取模范围(默认 1000)。
	MockDSPortRange int `yaml:"mock_ds_port_range,omitempty" json:"mock_ds_port_range,omitempty"`
}

// Defaults 填默认值。
func (c *Config) Defaults() {
	if c.Allocator.HeartbeatTimeout == 0 {
		c.Allocator.HeartbeatTimeout = config.Duration(15 * time.Second)
	}
	if c.Allocator.SweepInterval == 0 {
		c.Allocator.SweepInterval = config.Duration(5 * time.Second)
	}
	if c.Allocator.BattleTTL == 0 {
		c.Allocator.BattleTTL = config.Duration(2 * time.Hour)
	}
	if c.Allocator.MockDSAddrHost == "" {
		c.Allocator.MockDSAddrHost = "127.0.0.1"
	}
	if c.Allocator.MockDSPortBase == 0 {
		c.Allocator.MockDSPortBase = 30000
	}
	if c.Allocator.MockDSPortRange == 0 {
		c.Allocator.MockDSPortRange = 1000
	}
	if c.Agones.APIServer == "" {
		c.Agones.APIServer = "https://kubernetes.default.svc"
	}
	if c.Agones.Namespace == "" {
		c.Agones.Namespace = "default"
	}
	if c.Agones.TokenPath == "" {
		c.Agones.TokenPath = "/var/run/secrets/kubernetes.io/serviceaccount/token"
	}
	if c.Agones.CAPath == "" {
		c.Agones.CAPath = "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt"
	}
	if c.Agones.AllocateTimeout == 0 {
		c.Agones.AllocateTimeout = config.Duration(5 * time.Second)
	}
	if c.Server.Grpc.Addr == "" {
		c.Server.Grpc.Addr = ":50020"
	}
	if c.Server.Http.Addr == "" {
		c.Server.Http.Addr = ":51020"
	}
}
