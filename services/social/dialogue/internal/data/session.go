package data

import (
	"context"
	"sync"
)

// Session 是一次 NPC 对话的服务端会话状态。
// dialogue_id 是服务端持有的会话 ID(不变量:由 snowflake 生成,客户端不可伪造)。
type Session struct {
	DialogueID uint64
	PlayerID   uint64
	NpcID      uint32
	NodeID     string // 当前所在节点
	CreatedMs  int64
	ExpiresMs  int64 // 绝对过期时间戳(毫秒);超过即视为不存在
}

// SessionStore 是对话会话的存储抽象。
//
// 当前实现 MemorySessionStore 是单实例内存版(对话短时、单玩家、无副作用,
// 符合 go-services.md §2.10 dialogue 依赖「无 redis」)。若后续需要水平扩展
// 或会话跨实例,只需替换实现为 Redis 版,biz / service 不动。
type SessionStore interface {
	// Create 新建会话。dialogue_id 冲突返回 false(几乎不会发生,snowflake 唯一)。
	Create(ctx context.Context, s *Session) (bool, error)
	// Get 取会话;不存在或已过期返回 (nil, false, nil)。
	Get(ctx context.Context, dialogueID uint64, nowMs int64) (*Session, bool, error)
	// Update 覆盖写已存在的会话(推进节点)。
	Update(ctx context.Context, s *Session) error
	// Delete 删除会话(幂等)。
	Delete(ctx context.Context, dialogueID uint64) error
}

// MemorySessionStore 是进程内内存会话存储(带惰性 + 主动过期回收)。
type MemorySessionStore struct {
	mu       sync.Mutex
	sessions map[uint64]*Session
}

// NewMemorySessionStore 构造。
func NewMemorySessionStore() *MemorySessionStore {
	return &MemorySessionStore{sessions: make(map[uint64]*Session)}
}

// Create 实现 SessionStore。
func (m *MemorySessionStore) Create(_ context.Context, s *Session) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.sessions[s.DialogueID]; exists {
		return false, nil
	}
	cp := *s
	m.sessions[s.DialogueID] = &cp
	return true, nil
}

// Get 实现 SessionStore(惰性过期:命中已过期会话则删除并视为不存在)。
func (m *MemorySessionStore) Get(_ context.Context, dialogueID uint64, nowMs int64) (*Session, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[dialogueID]
	if !ok {
		return nil, false, nil
	}
	if s.ExpiresMs > 0 && nowMs >= s.ExpiresMs {
		delete(m.sessions, dialogueID)
		return nil, false, nil
	}
	cp := *s
	return &cp, true, nil
}

// Update 实现 SessionStore。
func (m *MemorySessionStore) Update(_ context.Context, s *Session) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cp := *s
	m.sessions[s.DialogueID] = &cp
	return nil
}

// Delete 实现 SessionStore(幂等)。
func (m *MemorySessionStore) Delete(_ context.Context, dialogueID uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, dialogueID)
	return nil
}

// SweepExpired 主动清理已过期会话,返回清理数量。
// main.go 用一个 ticker goroutine 周期调用,避免被遗弃的会话(创建后不再访问)堆积。
func (m *MemorySessionStore) SweepExpired(nowMs int64) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := 0
	for id, s := range m.sessions {
		if s.ExpiresMs > 0 && nowMs >= s.ExpiresMs {
			delete(m.sessions, id)
			n++
		}
	}
	return n
}
