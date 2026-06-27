// Package client 管理压测客户端到各后端服务的 gRPC 连接,
// 并提供注入 metadata(x-pandora-player-id / x-pandora-trace-id)的出站 context。
//
// 设计要点:
//   - 每个服务一条共享 *grpc.ClientConn,靠 HTTP/2 多路复用承载几十万 VU 的并发流,
//     不给每个 VU 建连(否则句柄 / 内存爆炸)。
//   - 直连后端 gRPC 端口,用 insecure 凭证;Envoy 对照样本走单独的 TLS 连接。
//   - metadata key 与 pkg/middleware 约定保持一致(此处本地常量化,避免引入 pkg 依赖树)。
package client

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"strconv"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"

	"github.com/luyuancpp/pandora/robot/stress/internal/scenario"

	auctionv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/auction/v1"
	battlev1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/battle/v1"
	chatv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/chat/v1"
	friendv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/friend/v1"
	locatorv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/locator/v1"
	loginv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/login/v1"
	matchv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/match/v1"
	playerv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/player/v1"
	pushv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/push/v1"
	teamv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/team/v1"
)

const (
	// 与 pkg/middleware 约定一致(login / 各服务从 metadata 取 player_id / trace_id)。
	metadataKeyPlayerID = "x-pandora-player-id"
	metadataKeyTraceID  = "x-pandora-trace-id"
)

// Pool 持有到各后端服务的共享连接与对应 gRPC client。
type Pool struct {
	conns []*grpc.ClientConn

	Login        loginv1.LoginServiceClient
	Player       playerv1.PlayerServiceClient
	Friend       friendv1.FriendServiceClient
	Chat         chatv1.ChatServiceClient
	Locator      locatorv1.PlayerLocatorServiceClient
	Team         teamv1.TeamServiceClient
	Matchmaker   matchv1.MatchServiceClient
	Auction      auctionv1.AuctionServiceClient
	BattleResult battlev1.BattleResultServiceClient
	Push         pushv1.PushServiceClient

	// LoginViaEnvoy 是经 Envoy(TLS)的对照入口,仅小比例 VU 用。
	LoginViaEnvoy loginv1.LoginServiceClient
}

// New 按配置建立所有共享连接。任何一条连不上则回收已建连接并报错。
func New(t scenario.Targets) (*Pool, error) {
	p := &Pool{}

	dialInsecure := func(addr string) (*grpc.ClientConn, error) {
		cc, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, fmt.Errorf("连接 %q 失败: %w", addr, err)
		}
		p.conns = append(p.conns, cc)
		return cc, nil
	}

	type bind struct {
		addr   string
		assign func(*grpc.ClientConn)
	}
	binds := []bind{
		{t.Login, func(cc *grpc.ClientConn) { p.Login = loginv1.NewLoginServiceClient(cc) }},
		{t.Player, func(cc *grpc.ClientConn) { p.Player = playerv1.NewPlayerServiceClient(cc) }},
		{t.Friend, func(cc *grpc.ClientConn) { p.Friend = friendv1.NewFriendServiceClient(cc) }},
		{t.Chat, func(cc *grpc.ClientConn) { p.Chat = chatv1.NewChatServiceClient(cc) }},
		{t.Locator, func(cc *grpc.ClientConn) { p.Locator = locatorv1.NewPlayerLocatorServiceClient(cc) }},
		{t.Team, func(cc *grpc.ClientConn) { p.Team = teamv1.NewTeamServiceClient(cc) }},
		{t.Matchmaker, func(cc *grpc.ClientConn) { p.Matchmaker = matchv1.NewMatchServiceClient(cc) }},
		{t.Auction, func(cc *grpc.ClientConn) { p.Auction = auctionv1.NewAuctionServiceClient(cc) }},
		{t.BattleResult, func(cc *grpc.ClientConn) { p.BattleResult = battlev1.NewBattleResultServiceClient(cc) }},
		{t.Push, func(cc *grpc.ClientConn) { p.Push = pushv1.NewPushServiceClient(cc) }},
	}
	for _, b := range binds {
		if b.addr == "" {
			continue
		}
		cc, err := dialInsecure(b.addr)
		if err != nil {
			p.Close()
			return nil, err
		}
		b.assign(cc)
	}

	// Envoy 对照入口(TLS,dev-ca 自签;压测对照不校验证书)。
	if t.EnvoyAddr != "" {
		creds := credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}) // #nosec G402 — 压测对照样本,dev 自签证书
		cc, err := grpc.NewClient(t.EnvoyAddr, grpc.WithTransportCredentials(creds))
		if err != nil {
			p.Close()
			return nil, fmt.Errorf("连接 Envoy %q 失败: %w", t.EnvoyAddr, err)
		}
		p.conns = append(p.conns, cc)
		p.LoginViaEnvoy = loginv1.NewLoginServiceClient(cc)
	}

	return p, nil
}

// Close 关闭所有共享连接。
func (p *Pool) Close() {
	for _, cc := range p.conns {
		_ = cc.Close()
	}
	p.conns = nil
}

// OutgoingContext 返回注入了 player_id / trace_id metadata 的出站 context,
// 模拟 Envoy 鉴权后下发给后端的请求头,直连后端时绕过 Envoy。
func OutgoingContext(ctx context.Context, playerID uint64) context.Context {
	md := metadata.New(map[string]string{
		metadataKeyPlayerID: strconv.FormatUint(playerID, 10),
		metadataKeyTraceID:  NewTraceID(),
	})
	return metadata.NewOutgoingContext(ctx, md)
}

// NewTraceID 生成 16 字节十六进制 trace_id(不引入 uuid 依赖)。
func NewTraceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		// rand.Read 在 Windows/Linux 上几乎不会失败;失败时退化为全零,不阻断压测。
		return "00000000000000000000000000000000"
	}
	return hex.EncodeToString(b[:])
}
