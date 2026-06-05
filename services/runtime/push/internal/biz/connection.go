// Package biz 是 push 服务的业务逻辑层(usecase)。
//
// 本文件实现 ConnectionManager:player_id → stream 的内存索引。
//
// W2 mock 阶段:
//   - Subscribe handler 把自己的 stream 注册进来,断开时反注册
//   - mock ticker 直接闭包持有 stream,不通过 manager 路由
//
// W3 真实化:
//   - kafka consumer 收到事件 → 用 manager.SendTo(player_id, frame) 路由
//   - 系统公告类(pandora.system.notify)走 manager.Broadcast
//
// 设计要点(对齐 docs/design/gateway-decision.md):
//   - 一个 player_id 只允许有一个在线 stream(同账号顶号:旧 stream Close,新 stream 替换)
//   - 不变量 §3.1:玩家在线只能在一个 DS(push 服务并发场景同理:同账号一条 push 长连)
package biz

import (
	"sync"

	grpc "google.golang.org/grpc"

	pushv1 "github.com/luyuancpp/pandora/proto/gen/go/pandora/push/v1"
)

// PushStream 是 push 服务对 gRPC server stream 的别名。
//
// 用 grpc.ServerStreamingServer[PushFrame] 是 gRPC v1.62+ 的泛型形态,
// 跟 push_grpc.pb.go 里 Subscribe 的签名一致。
type PushStream = grpc.ServerStreamingServer[pushv1.PushFrame]

// ConnectionManager 维护 player_id → 在线 stream 的索引。
//
// 并发安全(读写锁)。
//
// W2 mock 阶段 manager 已经可用,但 ticker 直接持 stream;
// W3 kafka consumer 加上来后才真正用 SendTo / Broadcast。
type ConnectionManager struct {
	mu       sync.RWMutex
	bySlot   map[int64]PushStream // key = player_id;value = 该玩家当前的 stream
	closeFns map[int64]func()     // 旧 stream 被顶号时的 close 回调(W3 接 ctx cancel)
}

// NewConnectionManager 构造空索引。
func NewConnectionManager() *ConnectionManager {
	return &ConnectionManager{
		bySlot:   make(map[int64]PushStream),
		closeFns: make(map[int64]func()),
	}
}

// Register 把 (player_id, stream) 加入索引;若已存在则触发旧的 closeFn(顶号语义)。
//
// closeFn 由调用方提供,用于通知旧 Subscribe goroutine 主动退出(W3 接 ctx cancel)。
// 调用方应在 Subscribe 阻塞结束后调 Unregister 反注册。
func (m *ConnectionManager) Register(playerID int64, stream PushStream, closeFn func()) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if oldClose, exists := m.closeFns[playerID]; exists && oldClose != nil {
		// 顶号:先通知旧 stream 退出
		oldClose()
	}
	m.bySlot[playerID] = stream
	m.closeFns[playerID] = closeFn
}

// Unregister 把 player_id 从索引中移除(仅当当前 stream 等于传入的 stream 时才移除,
// 避免顶号场景下新 stream 把自己的位置删掉)。
func (m *ConnectionManager) Unregister(playerID int64, stream PushStream) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if cur, ok := m.bySlot[playerID]; ok && cur == stream {
		delete(m.bySlot, playerID)
		delete(m.closeFns, playerID)
	}
}

// SendTo 给指定 player 发送一帧 PushFrame。
// 玩家不在线返回 false(由调用方决定写离线缓存还是丢弃)。
//
// W3:kafka consumer 收到 key=player_id 的事件后调本方法。
func (m *ConnectionManager) SendTo(playerID int64, frame *pushv1.PushFrame) (bool, error) {
	m.mu.RLock()
	stream, ok := m.bySlot[playerID]
	m.mu.RUnlock()

	if !ok {
		return false, nil
	}
	if err := stream.Send(frame); err != nil {
		return true, err
	}
	return true, nil
}

// Broadcast 给所有在线玩家发送一帧(系统公告类用)。
// 返回成功发送数 + 失败数(失败按 stream 计,本方法不打日志)。
func (m *ConnectionManager) Broadcast(frame *pushv1.PushFrame) (sent int, failed int) {
	m.mu.RLock()
	// 快照一份 slice,避免长时间持锁
	streams := make([]PushStream, 0, len(m.bySlot))
	for _, s := range m.bySlot {
		streams = append(streams, s)
	}
	m.mu.RUnlock()

	for _, s := range streams {
		if err := s.Send(frame); err != nil {
			failed++
		} else {
			sent++
		}
	}
	return
}

// Size 当前在线 stream 数(给 /metrics + 调试用)。
func (m *ConnectionManager) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.bySlot)
}
