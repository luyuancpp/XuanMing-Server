// Package grpcclient 提供 Pandora 服务调用其它 gRPC 服务的客户端包装。
//
// 设计目标:
//   - 包装 zrpc.MustNewClient
//   - 出站 trace_id 透传(从 log.WithTraceID 的 ctx 提取,放进 metadata)
//   - 客户端 metrics:rpc_client_duration_seconds + total
//   - 默认 5s 超时
package grpcclient

import (
	"context"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/zeromicro/go-zero/zrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"github.com/luyuancpp/pandora/pkg/log"
	"github.com/luyuancpp/pandora/pkg/metrics"
)

const (
	// MetadataKeyTraceID 与 grpcserver 保持一致。
	MetadataKeyTraceID = "x-pandora-trace-id"
	DefaultTimeout     = 5 * time.Second
)

// clientDurationSeconds 跟踪客户端调用耗时。
var clientDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
	Namespace: "pandora",
	Subsystem: "rpc_client",
	Name:      "duration_seconds",
	Help:      "gRPC client-side call duration in seconds.",
	Buckets:   metrics.StandardBuckets,
}, []string{"target", "method"})

var clientTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
	Namespace: "pandora",
	Subsystem: "rpc_client",
	Name:      "total",
	Help:      "gRPC client-side call count.",
}, []string{"target", "method", "outcome"})

func init() {
	metrics.Register(clientDurationSeconds)
	metrics.Register(clientTotal)
}

// MustNewClient 创建一个挂载默认拦截器的 zrpc client。
//
//	client := grpcclient.MustNewClient(cfg.PlayerLocatorRpc)
//	plc := plpb.NewPlayerLocatorClient(client.Conn())
func MustNewClient(c zrpc.RpcClientConf, target string, opts ...zrpc.ClientOption) zrpc.Client {
	allOpts := append([]zrpc.ClientOption{
		zrpc.WithUnaryClientInterceptor(traceForwardInterceptor),
		zrpc.WithUnaryClientInterceptor(makeMetricsInterceptor(target)),
	}, opts...)
	return zrpc.MustNewClient(c, allOpts...)
}

// traceForwardInterceptor 把 ctx 中的 trace_id 写进 outgoing metadata。
func traceForwardInterceptor(
	ctx context.Context,
	method string,
	req, reply interface{},
	cc *grpc.ClientConn,
	invoker grpc.UnaryInvoker,
	opts ...grpc.CallOption,
) error {
	if v := ctx.Value(log.CtxKeyTraceID); v != nil {
		if s, ok := v.(string); ok && s != "" {
			ctx = metadata.AppendToOutgoingContext(ctx, MetadataKeyTraceID, s)
		}
	}
	return invoker(ctx, method, req, reply, cc, opts...)
}

func makeMetricsInterceptor(target string) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply interface{},
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		start := time.Now()
		shortMethod := shortMethod(method)
		err := invoker(ctx, method, req, reply, cc, opts...)
		clientDurationSeconds.WithLabelValues(target, shortMethod).Observe(time.Since(start).Seconds())

		outcome := "ok"
		if err != nil {
			outcome = "err"
		}
		clientTotal.WithLabelValues(target, shortMethod, outcome).Inc()
		return err
	}
}

// shortMethod 把 "/login.LoginService/Login" 截短成 "LoginService/Login"。
func shortMethod(fullMethod string) string {
	parts := strings.SplitN(strings.TrimPrefix(fullMethod, "/"), "/", 2)
	if len(parts) != 2 {
		return fullMethod
	}
	svc := parts[0]
	if dot := strings.LastIndex(svc, "."); dot >= 0 {
		svc = svc[dot+1:]
	}
	return svc + "/" + parts[1]
}

// WithTimeout 是给业务侧用的便捷函数,在 ctx 上设默认超时。
func WithTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	if _, ok := parent.Deadline(); ok {
		// 已经有超时,不覆盖
		return parent, func() {}
	}
	return context.WithTimeout(parent, DefaultTimeout)
}
