// Package cache 提供缓存存储抽象与读穿（read-through）能力。
//
// 分层意图：
//   - Cache 是纯存储，只有 Get/Set/Del，不认识业务、不认识配置；
//   - Policy 决定「能不能缓存、缓存多久」，与存储实现无关；
//   - Reader 把两者串成读穿语义，并用请求合并挡住击穿。
//
// 换存储（本地文件 → 内存 → Redis）只替换 Cache 实现，
// Reader、Policy、调用方一行都不用改。
package cache

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound 表示键不存在或已过期。
//
// 注意：多数情况下调用方不需要区分「未命中」和「过期」，
// 因此 Cache.Get 用 (ok bool) 表达，本错误供需要显式判断的场景使用。
var ErrNotFound = errors.New("cache: 未命中")

// Meta 描述一个待缓存资源。字段由调用方填充，供 Policy 判定。
//
// 刻意只放与策略相关的维度：缓存实现本身不需要它。
type Meta struct {
	// Source 平台二字码，如 wy / kg。
	Source string
	// Quality 音质名，可能是别名写法，由策略层归一化。
	//
	// 留空表示该资源没有音质概念（例如歌词），策略层按「可缓存」处理。
	Quality string
}

// Cache 是最小存储抽象。
//
// ttl <= 0 表示不过期。实现可以自行决定过期判定时机，
// 但 Get 必须保证过期的条目被视为未命中。
type Cache interface {
	Get(ctx context.Context, key string) (data []byte, ok bool, err error)
	Set(ctx context.Context, key string, data []byte, ttl time.Duration) error
	Del(ctx context.Context, key string) error
}

// Policy 决定资源是否可缓存以及缓存多久。
//
// 返回 cacheable=false 时，Reader 会直接透传到上游，不写缓存、也不做请求合并
// （不可缓存意味着每次都必须拿到最新数据）。
type Policy interface {
	Decide(m Meta) (ttl time.Duration, cacheable bool)
}

// StatsSnapshot 是命中统计快照。
type StatsSnapshot struct {
	Hits   uint64
	Misses uint64
	Loads  uint64
	Errors uint64
}

// HitRate 返回命中率，无请求时返回 0。
func (s StatsSnapshot) HitRate() float64 {
	total := s.Hits + s.Misses
	if total == 0 {
		return 0
	}
	return float64(s.Hits) / float64(total)
}
