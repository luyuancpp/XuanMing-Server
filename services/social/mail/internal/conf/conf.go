// Package conf 是 mail 服务的私有配置结构(2026-06-29)。
package conf

import (
	"github.com/luyuancpp/pandora/pkg/config"
)

// Config 是 mail 服务的完整配置。
type Config struct {
	config.Base `yaml:",inline" mapstructure:",squash"`

	Mail MailConf `yaml:"mail" json:"mail"`
}

// MailConf 是 mail 服务私有配置。
type MailConf struct {
	// DefaultSysTtlDays 系统/公会邮件默认有效期天数(end_ms 为 0 时补,默认 7)。
	DefaultSysTtlDays int `yaml:"default_sys_ttl_days,omitempty" json:"default_sys_ttl_days,omitempty"`

	// MaxTitleLen 邮件标题最大长度(utf8 rune,默认 64)。
	MaxTitleLen int `yaml:"max_title_len,omitempty" json:"max_title_len,omitempty"`

	// MaxBodyLen 邮件正文最大长度(utf8 rune,默认 2048)。
	MaxBodyLen int `yaml:"max_body_len,omitempty" json:"max_body_len,omitempty"`

	// MaxAttachments 单封邮件附件上限(默认 16)。
	MaxAttachments int `yaml:"max_attachments,omitempty" json:"max_attachments,omitempty"`

	// InventoryAddr inventory 服务 gRPC 地址(host:port),领取附件入库用。
	InventoryAddr string `yaml:"inventory_addr,omitempty" json:"inventory_addr,omitempty"`

	// AllowNoopGrant inventory 不可用时允许空领(只标记 claim,不真发),仅测试环境。
	AllowNoopGrant bool `yaml:"allow_noop_grant,omitempty" json:"allow_noop_grant,omitempty"`
}

// Defaults 填默认值,防止 yaml 缺字段时零值引发非预期行为。
func (c *Config) Defaults() {
	if c.Mail.DefaultSysTtlDays <= 0 {
		c.Mail.DefaultSysTtlDays = 7
	}
	if c.Mail.MaxTitleLen <= 0 {
		c.Mail.MaxTitleLen = 64
	}
	if c.Mail.MaxBodyLen <= 0 {
		c.Mail.MaxBodyLen = 2048
	}
	if c.Mail.MaxAttachments <= 0 {
		c.Mail.MaxAttachments = 16
	}
	if c.Server.Grpc.Addr == "" {
		c.Server.Grpc.Addr = ":50009"
	}
	if c.Server.Http.Addr == "" {
		c.Server.Http.Addr = ":51009"
	}
}
