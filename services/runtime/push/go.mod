module github.com/luyuancpp/pandora/services/runtime/push

go 1.24.0

toolchain go1.24.5

// W2 ⑤ push 服务(Pandora 第二个 Kratos 业务服,server stream)。
//
// 依赖来源:
//   - pkg/ (公共框架,go.work use + replace)
//   - proto/gen/go/pandora/push/v1 (PushService 协议生成)
//   - proto/gen/go/pandora/common/v1 (错误码)
//   - Kratos v2.9.2 / google.golang.org/grpc(server stream API)
//
// W2 mock 范围:
//   - Subscribe 收到请求后,启动 ticker(默认 5s),周期性 Send PushFrame
//     (topic=pandora.system.notify, payload="hello", ts_ms=now)
//   - 不接 kafka(W3 接 sarama consumer + 多 topic 路由)
//   - 不接 redis ZSET 离线补推(W3)
//   - 不解 JWT,player_id 从 metadata x-player-id 取(联调便利,W3 走 Envoy jwt_authn)

require (
	github.com/go-kratos/kratos/v2 v2.9.2
	github.com/luyuancpp/pandora/pkg v0.0.0-00010101000000-000000000000
	github.com/luyuancpp/pandora/proto v0.0.0-00010101000000-000000000000
	google.golang.org/grpc v1.79.3
)

require (
	dario.cat/mergo v1.0.0 // indirect
	github.com/IBM/sarama v1.43.1 // indirect
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/davecgh/go-spew v1.1.1 // indirect
	github.com/eapache/go-resiliency v1.6.0 // indirect
	github.com/eapache/go-xerial-snappy v0.0.0-20230731223053-c322873962e3 // indirect
	github.com/eapache/queue v1.1.0 // indirect
	github.com/fsnotify/fsnotify v1.6.0 // indirect
	github.com/go-kratos/aegis v0.2.0 // indirect
	github.com/go-playground/form/v4 v4.2.0 // indirect
	github.com/golang/snappy v0.0.4 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/gorilla/mux v1.8.1 // indirect
	github.com/hashicorp/errwrap v1.0.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	github.com/hashicorp/go-uuid v1.0.3 // indirect
	github.com/jcmturner/aescts/v2 v2.0.0 // indirect
	github.com/jcmturner/dnsutils/v2 v2.0.0 // indirect
	github.com/jcmturner/gofork v1.7.6 // indirect
	github.com/jcmturner/gokrb5/v8 v8.4.4 // indirect
	github.com/jcmturner/rpc/v2 v2.0.3 // indirect
	github.com/klauspost/compress v1.17.11 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/pierrec/lz4/v4 v4.1.21 // indirect
	github.com/prometheus/client_golang v1.21.1 // indirect
	github.com/prometheus/client_model v0.6.1 // indirect
	github.com/prometheus/common v0.62.0 // indirect
	github.com/prometheus/procfs v0.15.1 // indirect
	github.com/rcrowley/go-metrics v0.0.0-20201227073835-cf1acfcdf475 // indirect
	go.uber.org/multierr v1.10.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	golang.org/x/crypto v0.46.0 // indirect
	golang.org/x/net v0.48.0 // indirect
	golang.org/x/sync v0.19.0 // indirect
	golang.org/x/sys v0.39.0 // indirect
	golang.org/x/text v0.32.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20251202230838-ff82c1b0f217 // indirect
	google.golang.org/protobuf v1.36.11 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

// 本地 workspace 内的模块通过 replace 指向源码目录。
// 这样 `go mod tidy` 不会去 vcs 抓远端版本(远端目前没 tag),
// 同时 `go build`(在 go.work 下)也能正常解析。
replace (
	github.com/luyuancpp/pandora/pkg => ../../../pkg
	github.com/luyuancpp/pandora/proto => ../../../proto
)
