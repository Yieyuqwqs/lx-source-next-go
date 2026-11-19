package cache_test

import (
	"context"
	"testing"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/cache"
	"github.com/lxsrcs/lx-source-next/pkg/cache/policy"
)

// mapPolicy 是最小策略：固定 TTL、可选不可缓存。
type mapPolicy struct {
	ttl   time.Duration
	allow bool
}

// Decide 实现 cache.Policy。
func (p mapPolicy) Decide(cache.Meta) (time.Duration, bool) { return p.ttl, p.allow }

// TestEmptyQualityIsCacheable 空音质（歌词这类）应可缓存。
func TestEmptyQualityIsCacheable(t *testing.T) {
	pol := policy.NewDynamic(func() policy.Config {
		return policy.Config{Mode: policy.ModeSafe, TTL: time.Minute,
			Quals: []string{"128k"}} // 只允许 128k，正常音质会被挡
	})
	if _, ok := pol.Decide(cache.Meta{Source: "wy", Quality: "lrc"}); ok {
		t.Error("未知音质不该缓存")
	}
	if _, ok := pol.Decide(cache.Meta{Source: "wy"}); !ok {
		t.Error("空音质（歌词）应该可缓存")
	}
	if _, ok := pol.Decide(cache.Meta{Source: "wy", Quality: "128k"}); !ok {
		t.Error("128k 应该可缓存")
	}
}

// TestReaderInvalidate 作废后要重新回源。
func TestReaderInvalidate(t *testing.T) {
	store := cache.NewMemory(0)
	r := cache.NewReader(store, mapPolicy{ttl: time.Minute, allow: true}, cache.ReaderOptions{})

	loads := 0
	load := func(context.Context) ([]byte, error) {
		loads++
		return []byte("payload"), nil
	}
	ctx := context.Background()
	if _, err := r.Get(ctx, "k", cache.Meta{Source: "wy"}, load); err != nil {
		t.Fatalf("首次读取: %v", err)
	}
	if _, err := r.Get(ctx, "k", cache.Meta{Source: "wy"}, load); err != nil {
		t.Fatalf("二次读取: %v", err)
	}
	if loads != 1 {
		t.Fatalf("命中缓存不该回源，loads = %d", loads)
	}
	if err := r.Invalidate(ctx, "k"); err != nil {
		t.Fatalf("Invalidate: %v", err)
	}
	if _, err := r.Get(ctx, "k", cache.Meta{Source: "wy"}, load); err != nil {
		t.Fatalf("作废后读取: %v", err)
	}
	if loads != 2 {
		t.Errorf("作废后必须回源，loads = %d", loads)
	}
}
