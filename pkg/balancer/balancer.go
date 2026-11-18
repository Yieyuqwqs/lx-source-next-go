// Package balancer 账号池熔断器。
//
// 原先是靠加大账号数量硬扛风控（连续失败就换号，但换到哪、要不要冷却全凭感觉）。
// 这里给每个节点加一个状态机：连续失败到达阈值即熔断，冷却期内不再选中，
// 冷却结束后放行一次试探，成功则恢复，失败则继续冷却。
package balancer

import (
	"sync"
	"time"
)

// State 是节点的熔断状态。
type State int

const (
	// Closed 正常可用。
	Closed State = iota
	// Open 已熔断，冷却中。
	Open
	// HalfOpen 冷却结束，允许一次试探。
	HalfOpen
)

// String 便于日志输出。
func (s State) String() string {
	switch s {
	case Closed:
		return "closed"
	case Open:
		return "open"
	case HalfOpen:
		return "half_open"
	}
	return "unknown"
}

// Config 是熔断器参数。
type Config struct {
	// Threshold 连续失败多少次后熔断；<=0 时取 3。
	Threshold int
	// Cooldown 熔断冷却时长；<=0 时取 60 秒。
	Cooldown time.Duration
	// Now 注入时钟，便于测试；为 nil 时用 time.Now。
	Now func() time.Time
}

func (c Config) threshold() int {
	if c.Threshold <= 0 {
		return 3
	}
	return c.Threshold
}

func (c Config) cooldown() time.Duration {
	if c.Cooldown <= 0 {
		return time.Minute
	}
	return c.Cooldown
}

type node struct {
	fails     int
	openUntil time.Time
	probing   bool
}

// Breaker 是并发安全的节点熔断器。
type Breaker struct {
	mu    sync.Mutex
	cfg   Config
	now   func() time.Time
	nodes map[string]*node
}

// New 创建熔断器。
func New(cfg Config) *Breaker {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &Breaker{
		cfg:   cfg,
		now:   now,
		nodes: make(map[string]*node),
	}
}

func (b *Breaker) get(key string) *node {
	n, ok := b.nodes[key]
	if !ok {
		n = &node{}
		b.nodes[key] = n
	}
	return n
}

// Allow 判断该节点当前是否可用于发起请求。
func (b *Breaker) Allow(key string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.get(key)
	if n.fails < b.cfg.threshold() {
		return true
	}
	if b.now().Before(n.openUntil) {
		return false
	}
	if n.probing {
		// 已有试探在进行中，避免并发放行多个
		return false
	}
	n.probing = true
	return true
}

// Success 上报一次成功，重置失败计数并恢复。
func (b *Breaker) Success(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.get(key)
	n.fails = 0
	n.openUntil = time.Time{}
	n.probing = false
}

// Failure 上报一次失败，达到阈值后进入熔断。
func (b *Breaker) Failure(key string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.get(key)
	n.probing = false
	n.fails++
	if n.fails >= b.cfg.threshold() {
		n.openUntil = b.now().Add(b.cfg.cooldown())
	}
}

// State 返回节点当前状态。
func (b *Breaker) State(key string) State {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := b.get(key)
	if n.fails < b.cfg.threshold() {
		return Closed
	}
	if b.now().Before(n.openUntil) {
		return Open
	}
	return HalfOpen
}

// Pick 按给定顺序返回第一个可用的节点。
func (b *Breaker) Pick(keys []string) (string, bool) {
	for _, k := range keys {
		if b.Allow(k) {
			return k, true
		}
	}
	return "", false
}

// Snapshot 返回各节点的失败次数，便于排查。
func (b *Breaker) Snapshot() map[string]int {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make(map[string]int, len(b.nodes))
	for k, n := range b.nodes {
		out[k] = n.fails
	}
	return out
}

// Reset 清空全部节点状态。
func (b *Breaker) Reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nodes = make(map[string]*node)
}
