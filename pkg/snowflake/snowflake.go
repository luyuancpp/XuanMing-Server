// Package snowflake 提供 64 位全局唯一 ID 生成器。
//
// Layout: [time:32][node:17][step:15]
// Epoch:  2026-03-14 00:00:00 UTC (1773446400)
//
// 直接复用自 mmorpg/go/shared/snowflake/。线程安全(mutex)。
package snowflake

import (
	"fmt"
	"sync"
	"time"
)

const (
	Epoch    uint64 = 1773446400
	NodeBits uint64 = 17
	StepBits uint64 = 15

	timeShift = NodeBits + StepBits
	nodeShift = StepBits
	stepMask  = (1 << StepBits) - 1
	NodeMask  = (1 << NodeBits) - 1
)

// Node 是单一节点的 ID 生成器。线程安全。
type Node struct {
	mu       sync.Mutex
	nodeID   uint64
	lastTime uint64
	step     uint64
}

// NewNode 创建一个 SnowFlake 生成器。nodeID 超过 17 位会 panic。
func NewNode(nodeID uint64) *Node {
	if nodeID > NodeMask {
		panic(fmt.Sprintf("snowflake: node ID %d exceeds max %d", nodeID, NodeMask))
	}
	return &Node{nodeID: nodeID}
}

// Generate 产生一个全局唯一的 uint64 ID。
func (n *Node) Generate() uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()

	now := nowEpoch()

	if now < n.lastTime {
		// 时钟回拨 — 自旋等到追上
		now = n.waitNextTime(n.lastTime)
	}

	if now > n.lastTime {
		n.lastTime = now
		n.step = 0
	} else {
		if n.step >= stepMask {
			now = n.waitNextTime(n.lastTime)
			n.lastTime = now
			n.step = 0
		} else {
			n.step++
		}
	}

	return (n.lastTime << timeShift) |
		(n.nodeID << nodeShift) |
		n.step
}

func (n *Node) waitNextTime(last uint64) uint64 {
	for {
		now := nowEpoch()
		if now > last {
			return now
		}
		time.Sleep(time.Millisecond)
	}
}

func nowEpoch() uint64 {
	return uint64(time.Now().Unix()) - Epoch
}
