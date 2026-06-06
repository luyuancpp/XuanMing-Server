// Package conf 是 matchmaker 服务的私有配置结构。
package conf

import (
	"time"

	"github.com/luyuancpp/pandora/pkg/config"
)

// Config 是 matchmaker 服务的完整配置。
type Config struct {
	config.Base `yaml:",inline" mapstructure:",squash"`

	Match MatchConf `yaml:"match" json:"match"`
}

// MatchConf 是 matchmaker 服务私有配置。
type MatchConf struct {
	// TeamAddr 是 team 服务 gRPC 直连地址(StartMatch 时拉取队伍快照校验 READY)。
	// 留空则 StartMatch 跳过 team 校验(本机不起 team 也能跑撮合骨架)。
	TeamAddr string `yaml:"team_addr,omitempty" json:"team_addr,omitempty"`

	// ConfirmTimeout 确认期时长,凑齐 10 人后等待全员确认的窗口(默认 15s)。
	ConfirmTimeout config.Duration `yaml:"confirm_timeout,omitempty" json:"confirm_timeout,omitempty"`

	// MatchInterval 后台撮合循环的扫描间隔(默认 2s)。
	MatchInterval config.Duration `yaml:"match_interval,omitempty" json:"match_interval,omitempty"`

	// TicketTTL 排队票据 Redis key 的 TTL(默认 30min,防僵尸票据)。
	TicketTTL config.Duration `yaml:"ticket_ttl,omitempty" json:"ticket_ttl,omitempty"`

	// MatchTTL 已撮合 match Redis key 的 TTL(默认 30min)。
	MatchTTL config.Duration `yaml:"match_ttl,omitempty" json:"match_ttl,omitempty"`

	// TeamSize 一方人数(MOBA 5v5,一方 5 人)。
	TeamSize int `yaml:"team_size,omitempty" json:"team_size,omitempty"`

	// MmrBaseWindow 初始 MMR 撮合窗口半宽(默认 200);两张票 avg_mmr 差 ≤ 窗口才可同场。
	MmrBaseWindow int `yaml:"mmr_base_window,omitempty" json:"mmr_base_window,omitempty"`

	// MmrWidenPerSec 每等待 1 秒窗口放宽的 MMR(默认 20),等待越久越容易撮合。
	MmrWidenPerSec int `yaml:"mmr_widen_per_sec,omitempty" json:"mmr_widen_per_sec,omitempty"`

	// MmrMaxWindow MMR 窗口放宽上限(默认 2000),超过即不再放宽。
	MmrMaxWindow int `yaml:"mmr_max_window,omitempty" json:"mmr_max_window,omitempty"`

	// OptimisticRetry WATCH/MULTI/EXEC 乐观锁冲突时最大重试次数。
	// 耗尽后返回 ErrMatchConcurrent(4006)。
	OptimisticRetry int `yaml:"optimistic_retry,omitempty" json:"optimistic_retry,omitempty"`
}

// Defaults 填默认值,防止 yaml 缺字段时零值引发 panic。
func (c *Config) Defaults() {
	if c.Match.ConfirmTimeout == 0 {
		c.Match.ConfirmTimeout = config.Duration(15 * time.Second)
	}
	if c.Match.MatchInterval == 0 {
		c.Match.MatchInterval = config.Duration(2 * time.Second)
	}
	if c.Match.TicketTTL == 0 {
		c.Match.TicketTTL = config.Duration(30 * time.Minute)
	}
	if c.Match.MatchTTL == 0 {
		c.Match.MatchTTL = config.Duration(30 * time.Minute)
	}
	if c.Match.TeamSize == 0 {
		c.Match.TeamSize = 5
	}
	if c.Match.MmrBaseWindow == 0 {
		c.Match.MmrBaseWindow = 200
	}
	if c.Match.MmrWidenPerSec == 0 {
		c.Match.MmrWidenPerSec = 20
	}
	if c.Match.MmrMaxWindow == 0 {
		c.Match.MmrMaxWindow = 2000
	}
	if c.Match.OptimisticRetry == 0 {
		c.Match.OptimisticRetry = 3
	}
	if c.Server.Grpc.Addr == "" {
		c.Server.Grpc.Addr = ":50011"
	}
	if c.Server.Http.Addr == "" {
		c.Server.Http.Addr = ":51011"
	}
}
