// Package stats 负责压测指标的并发计数与每分钟落盘(robot-stats.jsonl)。
//
// 输出格式见 docs/design/stress-single-cell-client.md §8,是 stress_summarize.ps1
// 五段二维表的输入之一(另一来源是各服务 prom 快照)。
package stats

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// Counters 是一组原子计数器,VU 各 goroutine 无锁累加。
type Counters struct {
	VUOnline        atomic.Int64
	LoginOK         atomic.Int64
	LoginFail       atomic.Int64
	SubscribeActive atomic.Int64
	MatchEnqueue    atomic.Int64
	MatchConfirmed  atomic.Int64
	MatchDispatched atomic.Int64
	BattleReported  atomic.Int64
	RPCErrors       atomic.Int64
}

// latencyDigest 收集 RPC 时延样本,按分钟算 p50/p99。
// 用有界蓄水池,避免几十万 VU 把内存撑爆。
type latencyDigest struct {
	mu       sync.Mutex
	samples  []float64
	capacity int
	seen     int64
}

func newLatencyDigest(capacity int) *latencyDigest {
	return &latencyDigest{capacity: capacity, samples: make([]float64, 0, capacity)}
}

// Observe 记录一次 RPC 时延(毫秒)。超过容量后按蓄水池抽样替换。
func (d *latencyDigest) Observe(ms float64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.seen++
	if len(d.samples) < d.capacity {
		d.samples = append(d.samples, ms)
		return
	}
	// 简单蓄水池:用 seen 取模做确定性替换,足够给压测时延一个稳定近似。
	idx := int(d.seen % int64(d.capacity))
	d.samples[idx] = ms
}

// drain 取出当前样本并清空,返回 (p50, p99)。无样本返回 (0,0)。
func (d *latencyDigest) drain() (p50, p99 float64) {
	d.mu.Lock()
	s := d.samples
	d.samples = make([]float64, 0, d.capacity)
	d.seen = 0
	d.mu.Unlock()
	if len(s) == 0 {
		return 0, 0
	}
	sort.Float64s(s)
	return percentile(s, 0.50), percentile(s, 0.99)
}

func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(q * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// Record 是写入 robot-stats.jsonl 的一行(一分钟一行)。
type Record struct {
	TS              string  `json:"ts"`
	Minute          int64   `json:"minute"`
	Machine         string  `json:"machine"`
	VUOnline        int64   `json:"vu_online"`
	LoginOK         int64   `json:"login_ok"`
	LoginFail       int64   `json:"login_fail"`
	SubscribeActive int64   `json:"subscribe_active"`
	MatchEnqueue    int64   `json:"match_enqueue"`
	MatchConfirmed  int64   `json:"match_confirmed"`
	MatchDispatched int64   `json:"match_dispatched"`
	BattleReported  int64   `json:"battle_reported"`
	RPCP50Ms        float64 `json:"rpc_p50_ms"`
	RPCP99Ms        float64 `json:"rpc_p99_ms"`
	Errors          int64   `json:"errors"`
}

// Collector 聚合计数器并按分钟把 Record 追加到 jsonl 文件。
type Collector struct {
	Counters *Counters
	latency  *latencyDigest
	machine  string
	path     string
}

// New 创建 Collector;path 为输出 jsonl 路径,目录会自动创建。
func New(machine, path string) (*Collector, error) {
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return &Collector{
		Counters: &Counters{},
		latency:  newLatencyDigest(4096),
		machine:  machine,
		path:     path,
	}, nil
}

// ObserveRPC 记录一次 RPC 时延(毫秒)。
func (c *Collector) ObserveRPC(ms float64) { c.latency.Observe(ms) }

// snapshot 生成当前分钟的 Record(计数取累计值快照,时延取并清空)。
func (c *Collector) snapshot(now time.Time) Record {
	p50, p99 := c.latency.drain()
	return Record{
		TS:              now.UTC().Format(time.RFC3339),
		Minute:          now.Unix() / 60,
		Machine:         c.machine,
		VUOnline:        c.Counters.VUOnline.Load(),
		LoginOK:         c.Counters.LoginOK.Load(),
		LoginFail:       c.Counters.LoginFail.Load(),
		SubscribeActive: c.Counters.SubscribeActive.Load(),
		MatchEnqueue:    c.Counters.MatchEnqueue.Load(),
		MatchConfirmed:  c.Counters.MatchConfirmed.Load(),
		MatchDispatched: c.Counters.MatchDispatched.Load(),
		BattleReported:  c.Counters.BattleReported.Load(),
		RPCP50Ms:        p50,
		RPCP99Ms:        p99,
		Errors:          c.Counters.RPCErrors.Load(),
	}
}

// writeRecord 追加一行 jsonl。
func (c *Collector) writeRecord(r Record) error {
	f, err := os.OpenFile(c.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	enc := json.NewEncoder(w)
	if err := enc.Encode(r); err != nil {
		return err
	}
	return w.Flush()
}

// Run 每分钟落盘一次,直到 ctx 关闭。返回的 error channel 只在写文件失败时投递。
// 调用方应在单独 goroutine 里 go collector.Run(ctx)。
func (c *Collector) Run(done <-chan struct{}, onErr func(error)) {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			// 退出前补落最后一分钟。
			if err := c.writeRecord(c.snapshot(time.Now())); err != nil && onErr != nil {
				onErr(err)
			}
			return
		case now := <-ticker.C:
			if err := c.writeRecord(c.snapshot(now)); err != nil && onErr != nil {
				onErr(err)
			}
		}
	}
}
