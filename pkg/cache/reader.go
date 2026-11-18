package cache

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"time"
)

// ReadFunc 在缓存未命中时从上游取数据。
type ReadFunc func(ctx context.Context) ([]byte, error)

// Reader 提供读穿语义：先查缓存，未命中再取上游并回填。
//
// 三个关键行为：
//  1. 不可缓存的资源直接透传上游，不写缓存也不合并；
//  2. 同一键的并发未命中会被 Group 合并，只有第一个请求打到上游；
//  3. 回填用策略给的 TTL，而策略来自当前配置快照——
//     配置变化只改变「后续写什么 TTL」，绝不动已有数据。
type Reader struct {
	c       Cache
	pol     Policy
	g       *Group
	logger  *slog.Logger
	timeout time.Duration

	hits   atomic.Uint64
	misses atomic.Uint64
	loads  atomic.Uint64
	errs   atomic.Uint64
}

// ReaderOptions 是 Reader 的构造参数。
type ReaderOptions struct {
	// Timeout 是单次上游加载的兜底超时，<=0 时取 30 秒。
	Timeout time.Duration
	// Logger 可为空。
	Logger *slog.Logger
}

// NewReader 构造读穿器。pol 为 nil 时视为「不可缓存」。
func NewReader(c Cache, pol Policy, opt ReaderOptions) *Reader {
	to := opt.Timeout
	if to <= 0 {
		to = 30 * time.Second
	}
	logger := opt.Logger
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Reader{
		c:       c,
		pol:     pol,
		g:       NewGroup(),
		logger:  logger,
		timeout: to,
	}
}

// Stats 返回统计快照。
func (r *Reader) Stats() StatsSnapshot {
	return StatsSnapshot{
		Hits:   r.hits.Load(),
		Misses: r.misses.Load(),
		Loads:  r.loads.Load(),
		Errors: r.errs.Load(),
	}
}

// InFlight 返回当前合并中的键数量。
func (r *Reader) InFlight() int { return r.g.InFlight() }

// Get 返回 key 对应的数据。
//
// 缓存命中直接返回；未命中时调用 load，并（若策略允许）回填缓存。
func (r *Reader) Get(ctx context.Context, key string, m Meta, load ReadFunc) ([]byte, error) {
	if load == nil {
		return nil, ErrNotFound
	}

	var ttl time.Duration
	cacheable := false
	if r.pol != nil {
		ttl, cacheable = r.pol.Decide(m)
	}
	if !cacheable {
		// 不可缓存：每次都必须是最新数据，不做合并
		r.loads.Add(1)
		data, err := load(ctx)
		if err != nil {
			r.errs.Add(1)
		}
		return data, err
	}

	if data, ok, err := r.c.Get(ctx, key); err != nil {
		// 缓存故障不应影响主流程，降级为直读上游
		r.logger.Warn("缓存读取失败，降级直读上游", "key", key, "err", err)
	} else if ok {
		r.hits.Add(1)
		return data, nil
	}
	r.misses.Add(1)

	data, err := r.g.Do(key, func() ([]byte, error) {
		// 与首个调用方的取消脱钩：某个客户端断开不应让所有等待者一起失败，
		// 结果也要能正常写回缓存。用兜底超时防止泄漏。
		lctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), r.timeout)
		defer cancel()

		r.loads.Add(1)
		out, err := load(lctx)
		if err != nil {
			r.errs.Add(1)
			return nil, err
		}
		if len(out) == 0 {
			// 空结果不回填，避免把上游异常写进缓存
			return out, nil
		}
		if err := r.c.Set(ctx, key, out, ttl); err != nil {
			r.logger.Warn("缓存回填失败", "key", key, "err", err)
		}
		return out, nil
	})
	return data, err
}
