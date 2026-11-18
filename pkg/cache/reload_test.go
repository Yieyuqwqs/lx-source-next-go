package cache_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/cache"
	"github.com/lxsrcs/lx-source-next/pkg/cache/policy"
)

// cfgBox 模拟「配置快照可被热更」。
type cfgBox struct {
	mu  sync.Mutex
	cfg policy.Config
}

func (b *cfgBox) get() policy.Config {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cfg
}

func (b *cfgBox) set(c policy.Config) {
	b.mu.Lock()
	b.cfg = c
	b.mu.Unlock()
}

// TestConfigReloadKeepsCachedData 是本包最重要的一条边界测试：
// 配置变了只应该改变「后续写入的规则与 TTL」，绝不能动已有数据。
func TestConfigReloadKeepsCachedData(t *testing.T) {
	ctx := context.Background()

	now := time.Now()
	fc, err := cache.NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	fc.WithClock(func() time.Time { return now })

	box := &cfgBox{cfg: policy.Config{
		Mode:  policy.ModeSafe,
		TTL:   time.Hour,
		Plats: []string{"wy"},
	}}
	pol := policy.NewDynamic(box.get)
	r := cache.NewReader(fc, pol, cache.ReaderOptions{})

	meta := cache.Meta{Source: "wy", Quality: "320k"}
	var loads atomic.Int64
	load := func(context.Context) ([]byte, error) {
		loads.Add(1)
		return []byte("payload"), nil
	}
	// 首次：未命中 → 回填
	if _, err := r.Get(ctx, "wy/1/320k.mp3", meta, load); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 1 {
		t.Fatalf("首次应访问上游，实际 %d", loads.Load())
	}

	// ---- 热更配置：改 TTL、放开平台、改排除语义 ----
	box.set(policy.Config{
		Mode:        policy.ModeFast,
		TTL:         time.Second,
		Plats:       []string{"wy", "kg"},
		ExcludePlat: false,
	})

	// 已有数据必须还在，且直接命中，不访问上游
	data, err := r.Get(ctx, "wy/1/320k.mp3", meta, load)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "payload" {
		t.Errorf("热更后旧数据丢失: %q", data)
	}
	if loads.Load() != 1 {
		t.Errorf("热更不应导致重新拉取，上游调用 %d 次", loads.Load())
	}

	// 新规则生效：kg 现在可以缓存了
	kgMeta := cache.Meta{Source: "kg", Quality: "flac"}
	if _, err := r.Get(ctx, "kg/2/flac.flac", kgMeta, load); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 2 {
		t.Errorf("新规则应允许 kg 缓存，上游调用 %d 次", loads.Load())
	}

	// 新 TTL 生效：改为 1 秒后，新写入的条目 2 秒后就该过期
	now = now.Add(2 * time.Second)
	if _, ok, _ := fc.Get(ctx, "kg/2/flac.flac"); ok {
		t.Error("新 TTL（1s）应已生效，条目应过期")
	}
	// 而按旧 TTL（1h）写入的条目仍在
	if _, ok, _ := fc.Get(ctx, "wy/1/320k.mp3"); !ok {
		t.Error("旧条目应按旧 TTL 继续存活，不应被热更清掉")
	}
}

// TestRuleTakesEffectImmediately 验证规则判定本身随配置变化。
func TestRuleTakesEffectImmediately(t *testing.T) {
	ctx := context.Background()
	box := &cfgBox{cfg: policy.Config{
		Mode:  policy.ModeSafe,
		TTL:   time.Minute,
		Plats: []string{"wy"},
	}}
	pol := policy.NewDynamic(box.get)
	r := cache.NewReader(cache.NewMemory(64), pol, cache.ReaderOptions{})

	var loads atomic.Int64
	load := func(context.Context) ([]byte, error) {
		loads.Add(1)
		return []byte("v"), nil
	}

	// 当前规则只允许 wy
	if _, err := r.Get(ctx, "kg/1/320k.mp3", cache.Meta{Source: "kg", Quality: "320k"}, load); err != nil {
		t.Fatal(err)
	}
	// 不可缓存 → 每次都直读
	if _, err := r.Get(ctx, "kg/1/320k.mp3", cache.Meta{Source: "kg", Quality: "320k"}, load); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 2 {
		t.Fatalf("规则外平台不应被缓存，上游调用 %d 次", loads.Load())
	}

	// 热更：放开 kg
	box.set(policy.Config{Mode: policy.ModeSafe, TTL: time.Minute, Plats: []string{"wy", "kg"}})
	if _, err := r.Get(ctx, "kg/1/320k.mp3", cache.Meta{Source: "kg", Quality: "320k"}, load); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 3 {
		t.Fatalf("新规则后应重新拉取并缓存，上游调用 %d 次", loads.Load())
	}
	// 这次应该命中了
	if _, err := r.Get(ctx, "kg/1/320k.mp3", cache.Meta{Source: "kg", Quality: "320k"}, load); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 3 {
		t.Errorf("规则生效后应命中缓存，上游调用 %d 次", loads.Load())
	}
}

// TestModeNoneDisablesCaching 验证 Mode=0 时不缓存。
func TestModeNoneDisablesCaching(t *testing.T) {
	ctx := context.Background()
	box := &cfgBox{cfg: policy.Config{Mode: policy.ModeNone}}
	pol := policy.NewDynamic(box.get)
	c := cache.NewMemory(8)
	r := cache.NewReader(c, pol, cache.ReaderOptions{})

	_, _ = r.Get(ctx, "k", cache.Meta{Source: "wy", Quality: "320k"}, func(context.Context) ([]byte, error) {
		return []byte("v"), nil
	})
	if c.Len() != 0 {
		t.Error("Mode=0 时不应写入缓存")
	}
}
