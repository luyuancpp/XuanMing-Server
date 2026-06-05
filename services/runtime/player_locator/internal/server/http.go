// Package server — HTTP server 注册(仅 /metrics)。
//
// locator.proto 没有 google.api.http 注解,跟 push.proto 同理:
// 不生成 RESTful handler,本 HTTP server 只挂 /metrics 给 Prometheus 抓。
package server

import (
	khttp "github.com/go-kratos/kratos/v2/transport/http"

	"github.com/luyuancpp/pandora/pkg/metrics"
	phttp "github.com/luyuancpp/pandora/pkg/transport/http"

	"github.com/luyuancpp/pandora/services/runtime/player_locator/internal/conf"
)

// NewHTTPServer 构造 HTTP server,仅注册 /metrics。
func NewHTTPServer(cfg *conf.Config) *khttp.Server {
	srv := phttp.MustNewServer(cfg.Server.Http)
	srv.Handle("/metrics", metrics.MustHandler())
	return srv
}
