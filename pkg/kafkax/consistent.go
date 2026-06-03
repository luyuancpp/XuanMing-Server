// Package kafkax 提供 Pandora 通用的 Kafka 生产者 / 消费者封装。
//
// 来源:抽自 mmorpg/go/login/internal/kafka/key_ordered_producer.go +
// db/internal/kafka/key_ordered_consumer.go,**剥业务依赖**(db_proto.DBTask /
// proto_sql / proto2mysql 等),只保留通用框架。
//
// 本文件:一致性哈希,从 mmorpg/go/login/internal/logic/pkg/consistent/ 整体复制,
// 改 package 名为 kafkax。
package kafkax

import (
	"hash/fnv"
	"sort"
	"sync"
)

// Consistent 是一个 key 到 partition 的稳定路由表(一致性哈希)。
// FNV-1a hash,RWMutex 读多写少,默认 20 个虚拟节点 / partition。
type Consistent struct {
	ring         map[uint32]int32
	sortedHashes []uint32
	replicaCount int
	partitionSet map[int32]struct{}
	mu           sync.RWMutex
}

// NewConsistent 创建实例。replicaCount 默认 20。
func NewConsistent(replicaCount ...int) *Consistent {
	defaultReplica := 20
	if len(replicaCount) > 0 && replicaCount[0] > 0 {
		defaultReplica = replicaCount[0]
	}
	return &Consistent{
		ring:         make(map[uint32]int32, defaultReplica*100),
		replicaCount: defaultReplica,
		partitionSet: make(map[int32]struct{}),
	}
}

// AddPartition 加入一个 partition 到哈希环。重复添加是 no-op。
func (c *Consistent) AddPartition(partition int32) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.partitionSet[partition]; exists {
		return
	}

	for i := 0; i < c.replicaCount; i++ {
		replicaKey := genReplicaKey(partition, i)
		hashVal := fnvHash(replicaKey)
		c.ring[hashVal] = partition
		c.sortedHashes = append(c.sortedHashes, hashVal)
	}

	c.partitionSet[partition] = struct{}{}
	sort.Slice(c.sortedHashes, func(i, j int) bool {
		return c.sortedHashes[i] < c.sortedHashes[j]
	})
}

// GetPartition 把 key 路由到 partition。环为空返回 (0, false)。
func (c *Consistent) GetPartition(key string) (int32, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if len(c.ring) == 0 {
		return 0, false
	}

	keyHash := fnvHash([]byte(key))
	idx := sort.Search(len(c.sortedHashes), func(i int) bool {
		return c.sortedHashes[i] >= keyHash
	})

	if idx == len(c.sortedHashes) {
		idx = 0
	}
	return c.ring[c.sortedHashes[idx]], true
}

// GetPartitionCount 返回 partition 数量。
func (c *Consistent) GetPartitionCount() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.partitionSet)
}

// GetPartitions 返回排序后的 partition 列表。
func (c *Consistent) GetPartitions() []int32 {
	c.mu.RLock()
	defer c.mu.RUnlock()

	partitions := make([]int32, 0, len(c.partitionSet))
	for p := range c.partitionSet {
		partitions = append(partitions, p)
	}
	sort.Slice(partitions, func(i, j int) bool {
		return partitions[i] < partitions[j]
	})
	return partitions
}

func genReplicaKey(partition int32, replicaIdx int) []byte {
	return []byte{
		byte(partition >> 24), byte(partition >> 16), byte(partition >> 8), byte(partition),
		byte(replicaIdx >> 24), byte(replicaIdx >> 16), byte(replicaIdx >> 8), byte(replicaIdx),
	}
}

func fnvHash(data []byte) uint32 {
	h := fnv.New32a()
	_, _ = h.Write(data)
	return h.Sum32()
}
