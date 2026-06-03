// Package grpcserver 提供 Pandora 服务的 gRPC server 包装。
//
// 设计目标:
//   - 包装 go-zero zrpc.MustNewServer
//   - 默认挂接 panic recover / trace_id 注入 / metrics 直方图 / grpcstats 接入
//   - 业务侧:只需 BuildServer(cfg) + 注册 pb.RegisterXxxServer + Start
package grpcserver

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/zeromicro/go-zero/core/logx"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/luyuancpp/pandora/pkg/errcode"
	"github.com/luyuancpp/pandora/pkg/grpcstats"
	"github.com/luyuancpp/pandora/pkg/log"
	"github.com/luyuancpp/pandora/pkg/metrics"
)

const (
	// MetadataKeyTraceID 是 gRPC metadata 中 trace_id 的 key,大小写不敏感。
	MetadataKeyTraceID = "x-pandora-trace-id"
)

// 全局共享的 grpcstats Collector,环境变量启用,所有服务复用同一份。
var defaultCollector = grpcstats.New(grpcstats.Options{})

// rpcDurationSeconds 跟踪 RPC 处理时间。
var rpcDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "pandora",
	Subsystem: "rpc",
	Name:      "duration_seconds",
	Help:      "gRPC server-side handler duration in seconds.",
	Buckets:   metrics.StandardBuckets,
}, []string{"service", "method", "code"})

// rpcTotal 跟踪 RPC 调用次数。
var rpcTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "pandora",
	Subsystem: "rpc",
	Name:      "total",
	Help:      "gRPC server-side call count.",
}, []string{"service", "method", "code"})

func init() {
	metrics.Register(rpcDurationSeconds)
	metrics.Register(rpcTotal)
}

// MustNewServer 创建并配置一个 zrpc Server。
//
// 业务:
//
//	srv := grpcserver.MustNewServer(cfg.RpcServerConf)
//	pb.RegisterLoginServiceServer(srv.Server(), loginsvc.New(svcCtx))
//	srv.Start()
func MustNewServer(c zrpc.RpcServerConf, customInterceptors ...grpc.UnaryServerInterceptor) *zrpc.RpcServer {
	srv := zrpc.MustNewServer(c, func(s *grpc.Server) {
		// 业务自己 RegisterXxxServer(s, ...)
	})

	// 默认拦截器(顺序很重要,从外到内:recover → trace → metrics → grpcstats → 业务)
	srv.AddUnaryInterceptors(recoverInterceptor)
	srv.AddUnaryInterceptors(traceIDInterceptor)
	srv.AddUnaryInterceptors(metricsInterceptor)
	srv.AddUnaryInterceptors(defaultCollector.UnaryServerInterceptor())

	for _, ic := range customInterceptors {
		srv.AddUnaryInterceptors(ic)
	}

	return srv
}

// Collector 暴露 grpcstats Collector,业务可通过 Enable/Disable 切换流量统计。
func Collector() *grpcstats.Collector {
	return defaultCollector
}

// ==================== 拦截器 ====================

// recoverInterceptor 捕获 handler panic,转成 Internal Error。
func recoverInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (resp interface{}, err error) {
	defer func() {
		if r := recover(); r != nil {
			logx.Errorf("[grpcserver] panic in %s: %v", info.FullMethod, r)
			err = errcode.New(errcode.ErrInternal, "panic: %v", r)
		}
	}()
	return handler(ctx, req)
}

// traceIDInterceptor 从 metadata 提取 trace_id,没有则生成一个,塞进 ctx。
func traceIDInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	traceID := ""
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if values := md.Get(MetadataKeyTraceID); len(values) > 0 {
			traceID = values[0]
		}
	}
	if traceID == "" {
		traceID = uuid.NewString()
	}
	ctx = log.WithTraceID(ctx, traceID)
	return handler(ctx, req)
}

// metricsInterceptor 记录 RPC duration / total。
func metricsInterceptor(
	ctx context.Context,
	req interface{},
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (interface{}, error) {
	start := time.Now()
	service, method := splitFullMethod(info.FullMethod)

	resp, err := handler(ctx, req)

	codeStr := codeLabel(err)
	elapsed := time.Since(start).Seconds()

	rpcDurationSeconds.WithLabelValues(service, method, codeStr).Observe(elapsed)
	rpcTotal.WithLabelValues(service, method, codeStr).Inc()

	return resp, err
}

// splitFullMethod 把 "/login.LoginService/Login" 切成 ("LoginService", "Login")。
func splitFullMethod(fullMethod string) (service, method string) {
	parts := strings.Split(strings.TrimPrefix(fullMethod, "/"), "/")
	if len(parts) != 2 {
		return "unknown", fullMethod
	}
	svc := parts[0]
	if dot := strings.LastIndex(svc, "."); dot >= 0 {
		svc = svc[dot+1:]
	}
	return svc, parts[1]
}

// codeLabel 把 error 转成稳定的 label 值(避免高基数)。
func codeLabel(err error) string {
	if err == nil {
		return "ok"
	}
	c := errcode.As(err)
	switch c {
	case errcode.OK:
		return "ok"
	case errcode.ErrTimeout:
		return "timeout"
	case errcode.ErrInvalidArg:
		return "invalid_arg"
	case errcode.ErrNotFound:
		return "not_found"
	case errcode.ErrUnauthorized:
		return "unauthorized"
	case errcode.ErrInternal, errcode.ErrUnknown:
		return "internal"
	default:
		// 业务错误码:用段位粒度的 label(避免太多 label 值)
		switch {
		case c >= 1000 && c < 2000:
			return "login_err"
		case c >= 2000 && c < 3000:
			return "player_err"
		case c >= 3000 && c < 4000:
			return "team_err"
		case c >= 4000 && c < 5000:
			return "match_err"
		case c >= 5000 && c < 6000:
			return "ds_err"
		case c >= 6000 && c < 7000:
			return "battle_err"
		case c >= 7000 && c < 8000:
			return "trade_err"
		default:
			return "other_err"
		}
	}
}
