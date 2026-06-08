// Package redisx 提供 Pandora 统一 Redis client 构造。
package redisx

import (
	"github.com/redis/go-redis/v9"
	"github.com/redis/go-redis/v9/maintnotifications"

	"github.com/luyuancpp/pandora/pkg/config"
)

// DefaultMaintNotificationsMode 是 Pandora 默认的维护通知模式。
//
// 自建 Redis(本地 / k8s 内 Redis 7.x)不支持 CLIENT MAINT_NOTIFICATIONS,
// go-redis 默认的 auto 模式会在握手失败时打印噪音日志:
//
//	maintnotifications disabled due to handshake error
//
// 默认关闭探测;接 Redis Cloud / Enterprise 时可经
// config.RedisConf.MaintNotifications 显式改为 "auto" / "enabled"。
const DefaultMaintNotificationsMode = maintnotifications.ModeDisabled

// NewClient 按公共 RedisConf 构造 go-redis 客户端。
//
// 维护通知模式由 c.MaintNotifications 配置驱动(留空 = disabled),不再硬编码,
// 既默认消除自建 Redis 的启动噪音,又给云托管 Redis 保留开关。
func NewClient(c config.RedisConf) *redis.Client {
	return redis.NewClient(&redis.Options{
		Addr:         c.Host,
		Password:     c.Password,
		DB:           int(c.DB),
		DialTimeout:  c.DialTimeout.Std(),
		ReadTimeout:  c.ReadTimeout.Std(),
		WriteTimeout: c.WriteTimeout.Std(),
		MaintNotificationsConfig: &maintnotifications.Config{
			Mode: resolveMaintMode(c.MaintNotifications),
		},
	})
}

// resolveMaintMode 把配置字符串映射成 go-redis 维护通知模式。
// 空串或非法值安全回退到 DefaultMaintNotificationsMode(disabled),不 panic。
func resolveMaintMode(s string) maintnotifications.Mode {
	if m := maintnotifications.Mode(s); m.IsValid() {
		return m
	}
	return DefaultMaintNotificationsMode
}
