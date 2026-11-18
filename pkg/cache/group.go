package cache

import "sync"

// Group 合并同一时刻针对同一个键的重复加载。
//
// 与 golang.org/x/sync/singleflight 的差异：这里的加载上下文会与
// 首个调用方的取消信号脱钩（context.WithoutCancel），
// 因此某个客户端断开不会导致所有等待者一起失败，结果也能正常写回缓存。
// 代价是需要给加载设一个兜底超时，由 Reader 负责。
type Group struct {
	mu sync.Mutex
	m  map[string]*call
}

type call struct {
	done chan struct{}
	data []byte
	err  error
}

// NewGroup 创建合并器。
func NewGroup() *Group {
	return &Group{m: make(map[string]*call)}
}

// Do 执行并合并同键的并发调用。
//
// fn 只会被调用一次；其余调用方等待并共享同一份结果。
func (g *Group) Do(key string, fn func() ([]byte, error)) ([]byte, error) {
	g.mu.Lock()
	if c, ok := g.m[key]; ok {
		g.mu.Unlock()
		<-c.done
		return c.data, c.err
	}
	c := &call{done: make(chan struct{})}
	g.m[key] = c
	g.mu.Unlock()

	// 保证即使 fn panic 也能唤醒等待者并清理表项
	defer func() {
		g.mu.Lock()
		delete(g.m, key)
		g.mu.Unlock()
		close(c.done)
	}()

	c.data, c.err = fn()
	return c.data, c.err
}

// InFlight 返回当前正在加载的键数量，便于观测。
func (g *Group) InFlight() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.m)
}
