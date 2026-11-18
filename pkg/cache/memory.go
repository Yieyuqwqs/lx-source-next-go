package cache

import (
	"context"
	"sync"
	"time"
)

// Memory 是内存缓存，适用于单实例、可承受重启丢失的场景。
//
// 并发安全；过期条目在读取时惰性淘汰，写入超容量时做一次清扫。
type Memory struct {
	mu  sync.RWMutex
	m   map[string]memEntry
	max int
	now func() time.Time
}

type memEntry struct {
	data    []byte
	expires time.Time
}

// NewMemory 创建内存缓存。maxEntries <= 0 时取 4096。
func NewMemory(maxEntries int) *Memory {
	if maxEntries <= 0 {
		maxEntries = 4096
	}
	return &Memory{
		m:   make(map[string]memEntry),
		max: maxEntries,
		now: time.Now,
	}
}

// WithClock 注入时钟，便于测试。
func (c *Memory) WithClock(f func() time.Time) *Memory {
	if f != nil {
		c.now = f
	}
	return c
}

// Get 实现 Cache。
func (c *Memory) Get(_ context.Context, key string) ([]byte, bool, error) {
	now := c.now()

	c.mu.RLock()
	e, ok := c.m[key]
	c.mu.RUnlock()
	if !ok {
		return nil, false, nil
	}
	if !e.expires.IsZero() && now.After(e.expires) {
		c.mu.Lock()
		delete(c.m, key)
		c.mu.Unlock()
		return nil, false, nil
	}
	return e.data, true, nil
}

// Set 实现 Cache。data 会被拷贝，调用方后续修改不影响缓存内容。
func (c *Memory) Set(_ context.Context, key string, data []byte, ttl time.Duration) error {
	cp := make([]byte, len(data))
	copy(cp, data)

	var expires time.Time
	if ttl > 0 {
		expires = c.now().Add(ttl)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= c.max {
		c.sweepLocked(c.now())
	}
	// 清扫后仍然超限，就随机淘汰一条，保证内存有上界
	for len(c.m) >= c.max {
		for k := range c.m {
			delete(c.m, k)
			break
		}
	}
	c.m[key] = memEntry{data: cp, expires: expires}
	return nil
}

// Del 实现 Cache。
func (c *Memory) Del(_ context.Context, key string) error {
	c.mu.Lock()
	delete(c.m, key)
	c.mu.Unlock()
	return nil
}

// Len 返回条目数（含未清理的过期项）。
func (c *Memory) Len() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.m)
}

// sweepLocked 删除已过期条目。调用方需持有写锁。
func (c *Memory) sweepLocked(now time.Time) {
	for k, e := range c.m {
		if !e.expires.IsZero() && now.After(e.expires) {
			delete(c.m, k)
		}
	}
}
