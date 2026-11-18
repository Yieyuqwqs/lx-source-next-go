package cache

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type stubPolicy struct {
	ttl time.Duration
	ok  bool
}

func (s stubPolicy) Decide(Meta) (time.Duration, bool) { return s.ttl, s.ok }

func TestReaderHitPath(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(8)
	_ = c.Set(ctx, "k", []byte("cached"), 0)
	r := NewReader(c, stubPolicy{ttl: time.Minute, ok: true}, ReaderOptions{})

	var loads atomic.Int64
	data, err := r.Get(ctx, "k", Meta{}, func(context.Context) ([]byte, error) {
		loads.Add(1)
		return []byte("upstream"), nil
	})
	if err != nil || string(data) != "cached" {
		t.Fatalf("应命中缓存，得到 %q, %v", data, err)
	}
	if loads.Load() != 0 {
		t.Error("命中时不应访问上游")
	}
	st := r.Stats()
	if st.Hits != 1 || st.Misses != 0 {
		t.Errorf("统计 = %+v", st)
	}
}

func TestReaderMissFillsCache(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(8)
	r := NewReader(c, stubPolicy{ttl: time.Minute, ok: true}, ReaderOptions{})

	var loads atomic.Int64
	load := func(context.Context) ([]byte, error) {
		loads.Add(1)
		return []byte("fresh"), nil
	}
	if _, err := r.Get(ctx, "k", Meta{}, load); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, "k", Meta{}, load); err != nil {
		t.Fatal(err)
	}
	if loads.Load() != 1 {
		t.Errorf("第二次应命中缓存，上游调用 %d 次", loads.Load())
	}
	st := r.Stats()
	if st.Hits != 1 || st.Misses != 1 || st.Loads != 1 {
		t.Errorf("统计 = %+v", st)
	}
	if _, ok, _ := c.Get(ctx, "k"); !ok {
		t.Error("未命中后应回填缓存")
	}
}

func TestReaderNonCacheablePassthrough(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(8)
	r := NewReader(c, stubPolicy{ok: false}, ReaderOptions{})

	var loads atomic.Int64
	load := func(context.Context) ([]byte, error) {
		loads.Add(1)
		return []byte("v"), nil
	}
	for i := 0; i < 3; i++ {
		if _, err := r.Get(ctx, "k", Meta{}, load); err != nil {
			t.Fatal(err)
		}
	}
	if loads.Load() != 3 {
		t.Errorf("不可缓存时应每次都取上游，实际 %d", loads.Load())
	}
	if _, ok, _ := c.Get(ctx, "k"); ok {
		t.Error("不可缓存时不应写入缓存")
	}
}

func TestReaderCoalescesConcurrentMisses(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(8)
	r := NewReader(c, stubPolicy{ttl: time.Minute, ok: true}, ReaderOptions{})

	var loads atomic.Int64
	release := make(chan struct{})
	load := func(context.Context) ([]byte, error) {
		loads.Add(1)
		<-release
		return []byte("once"), nil
	}

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			data, err := r.Get(ctx, "hot", Meta{}, load)
			if err != nil || string(data) != "once" {
				t.Errorf("结果 = %q, %v", data, err)
			}
		}()
	}
	time.Sleep(30 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := loads.Load(); got != 1 {
		t.Errorf("%d 个并发请求应只打到上游 1 次，实际 %d", n, got)
	}
}

func TestReaderDoesNotCacheEmpty(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(8)
	r := NewReader(c, stubPolicy{ttl: time.Minute, ok: true}, ReaderOptions{})

	data, err := r.Get(ctx, "k", Meta{}, func(context.Context) ([]byte, error) {
		return nil, nil
	})
	if err != nil || len(data) != 0 {
		t.Fatalf("data=%q err=%v", data, err)
	}
	if _, ok, _ := c.Get(ctx, "k"); ok {
		t.Error("空结果不应写进缓存")
	}
}

func TestReaderPropagatesLoadError(t *testing.T) {
	ctx := context.Background()
	r := NewReader(NewMemory(8), stubPolicy{ttl: time.Minute, ok: true}, ReaderOptions{})
	want := errors.New("上游失败")
	_, err := r.Get(ctx, "k", Meta{}, func(context.Context) ([]byte, error) { return nil, want })
	if !errors.Is(err, want) {
		t.Errorf("err = %v", err)
	}
	if r.Stats().Errors != 1 {
		t.Error("错误计数应 +1")
	}
	if _, ok, _ := r.c.Get(ctx, "k"); ok {
		t.Error("失败不应写缓存")
	}
}

// failingCache 模拟缓存后端故障。
type failingCache struct{}

func (failingCache) Get(context.Context, string) ([]byte, bool, error) {
	return nil, false, errors.New("磁盘坏了")
}
func (failingCache) Set(context.Context, string, []byte, time.Duration) error { return nil }
func (failingCache) Del(context.Context, string) error                        { return nil }

func TestReaderDegradesOnCacheError(t *testing.T) {
	ctx := context.Background()
	r := NewReader(failingCache{}, stubPolicy{ttl: time.Minute, ok: true}, ReaderOptions{})
	data, err := r.Get(ctx, "k", Meta{}, func(context.Context) ([]byte, error) {
		return []byte("upstream"), nil
	})
	if err != nil || string(data) != "upstream" {
		t.Errorf("缓存故障时应降级直读上游，得到 %q, %v", data, err)
	}
}
