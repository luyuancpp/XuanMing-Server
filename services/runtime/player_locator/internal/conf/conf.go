// Package conf 是 player_locator 服务的私有配置结构。
package conf

import (
	"time"

	"github.com/luyuancpp/pandora/pkg/config"
)

// Config 是 player_locator 服务的完整配置。
type Config struct {
	config.Base `yaml:",inline" mapstructure:",squash"`

	Locator LocatorConf `yaml:"locator" json:"locator"`
}

// LocatorConf 是 player_locator 私有配置。
type LocatorConf struct {
	// LocationTTL Redis hash 的 TTL(W2 ④ 坑:不写 yaml,由 Defaults 提供)。
	// 默认 30s,对齐 infra.md §3.2 表中的 30s heartbeat。
	LocationTTL time.Duration `yaml:"location_ttl,omitempty" json:"location_ttl,omitempty"`
}

// Defaults 填默认值。
func (c *Config) Defaults() {
	if c.Locator.LocationTTL == 0 {
		c.Locator.LocationTTL = 30 * time.Second
	}
	if c.Server.Grpc.Addr == "" {
		c.Server.Grpc.Addr = ":50006"
	}
	if c.Server.Http.Addr == "" {
		c.Server.Http.Addr = ":51006"
	}
}
