package cache

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoryRoundTrip(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(8)

	if _, ok, err := c.Get(ctx, "k"); err != nil || ok {
		t.Fatalf("空缓存应未命中，ok=%v err=%v", ok, err)
	}
	if err := c.Set(ctx, "k", []byte("v"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	data, ok, err := c.Get(ctx, "k")
	if err != nil || !ok || string(data) != "v" {
		t.Fatalf("Get = %q,%v,%v", data, ok, err)
	}
	if err := c.Del(ctx, "k"); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if _, ok, _ := c.Get(ctx, "k"); ok {
		t.Error("删除后应未命中")
	}
}

func TestMemoryCopiesData(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(8)
	src := []byte("abc")
	_ = c.Set(ctx, "k", src, 0)
	src[0] = 'z' // 修改调用方缓冲，不应影响缓存

	data, _, _ := c.Get(ctx, "k")
	if string(data) != "abc" {
		t.Errorf("缓存被外部修改污染: %q", data)
	}
}

func TestMemoryTTL(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	c := NewMemory(8).WithClock(func() time.Time { return now })

	_ = c.Set(ctx, "k", []byte("v"), time.Minute)
	if _, ok, _ := c.Get(ctx, "k"); !ok {
		t.Fatal("未过期应命中")
	}
	now = now.Add(2 * time.Minute)
	if _, ok, _ := c.Get(ctx, "k"); ok {
		t.Error("过期后应未命中")
	}
	if c.Len() != 0 {
		t.Errorf("过期项应被惰性清除，Len=%d", c.Len())
	}
}

func TestMemoryBounded(t *testing.T) {
	ctx := context.Background()
	c := NewMemory(4)
	for i := 0; i < 50; i++ {
		_ = c.Set(ctx, string(rune('a'+i%26))+string(rune('0'+i/26)), []byte("v"), time.Minute)
	}
	if c.Len() > 4 {
		t.Errorf("内存缓存超出上限: %d", c.Len())
	}
}

func TestFileRoundTrip(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	c, err := NewFile(dir)
	if err != nil {
		t.Fatalf("NewFile: %v", err)
	}

	key := "wy/1999253939/320k.mp3"
	if err := c.Set(ctx, key, []byte("audio-bytes"), 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	data, ok, err := c.Get(ctx, key)
	if err != nil || !ok || string(data) != "audio-bytes" {
		t.Fatalf("Get = %q,%v,%v", data, ok, err)
	}

	// 数据文件必须字节原样，便于后续直接提供 Range 下载
	p, err := c.Path(key)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	raw, err := os.ReadFile(p)
	if err != nil || string(raw) != "audio-bytes" {
		t.Fatalf("磁盘内容 = %q, %v", raw, err)
	}

	if err := c.Del(ctx, key); err != nil {
		t.Fatalf("Del: %v", err)
	}
	if _, ok, _ := c.Get(ctx, key); ok {
		t.Error("删除后应未命中")
	}
	if _, err := os.Stat(p + metaSuffix); !os.IsNotExist(err) {
		t.Error("元信息文件应一并删除")
	}
}

func TestFileTTL(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	c, err := NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	c.WithClock(func() time.Time { return now })

	key := "kw/42/flac.flac"
	if err := c.Set(ctx, key, []byte("x"), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := c.Get(ctx, key); !ok {
		t.Fatal("未过期应命中")
	}
	now = now.Add(time.Hour)
	if _, ok, _ := c.Get(ctx, key); ok {
		t.Error("过期后应未命中")
	}
	// 过期即清理
	p, _ := c.Path(key)
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("过期文件应被清理")
	}
}

func TestFileRejectsTraversal(t *testing.T) {
	c, err := NewFile(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bad := []string{
		"", "../etc/passwd", "wy/../../x", "wy//x", "wy/./x",
		"/abs/path", "wy/.hidden/x", "wy/a b/x", "wy/x\\y",
	}
	for _, k := range bad {
		if _, err := c.Path(k); err == nil {
			t.Errorf("键 %q 应被拒绝", k)
		}
	}
}

func TestFileOverwriteIsAtomic(t *testing.T) {
	ctx := context.Background()
	c, _ := NewFile(t.TempDir())
	key := "kg/1/128k.mp3"

	_ = c.Set(ctx, key, []byte("v1"), 0)
	_ = c.Set(ctx, key, []byte("v2"), 0)

	data, ok, _ := c.Get(ctx, key)
	if !ok || string(data) != "v2" {
		t.Errorf("覆盖后应读到新值，得到 %q", data)
	}
	// 不应残留临时文件
	p, _ := c.Path(key)
	entries, _ := os.ReadDir(filepath.Dir(p))
	for _, e := range entries {
		if len(e.Name()) > 5 && e.Name()[:5] == ".tmp-" {
			t.Errorf("残留临时文件: %s", e.Name())
		}
	}
}

func TestFileClearsStaleMetaOnNoTTL(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	c, _ := NewFile(t.TempDir())
	c.WithClock(func() time.Time { return now })
	key := "wy/9/flac.flac"

	_ = c.Set(ctx, key, []byte("v"), time.Second) // 带 TTL
	_ = c.Set(ctx, key, []byte("v"), 0)           // 改成不过期

	now = now.Add(time.Hour)
	if _, ok, _ := c.Get(ctx, key); !ok {
		t.Error("改为不过期后，旧过期时间不应再生效")
	}
}

func TestGroupCoalesces(t *testing.T) {
	g := NewGroup()
	var calls atomic.Int64
	release := make(chan struct{})

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			data, err := g.Do("k", func() ([]byte, error) {
				calls.Add(1)
				<-release
				return []byte("shared"), nil
			})
			if err != nil || string(data) != "shared" {
				t.Errorf("结果 = %q, %v", data, err)
			}
		}()
	}
	time.Sleep(20 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := calls.Load(); got != 1 {
		t.Errorf("加载次数 = %d, want 1（应被合并）", got)
	}
	if g.InFlight() != 0 {
		t.Errorf("结束后应清空进行中表，实际 %d", g.InFlight())
	}
}

func TestGroupDistinctKeys(t *testing.T) {
	g := NewGroup()
	var calls atomic.Int64
	load := func() ([]byte, error) { calls.Add(1); return []byte("x"), nil }
	g.Do("a", load)
	g.Do("b", load)
	if calls.Load() != 2 {
		t.Errorf("不同键应各自加载，实际 %d", calls.Load())
	}
}

func TestGroupPropagatesError(t *testing.T) {
	g := NewGroup()
	want := errors.New("上游炸了")
	_, err := g.Do("k", func() ([]byte, error) { return nil, want })
	if !errors.Is(err, want) {
		t.Errorf("err = %v", err)
	}
	if g.InFlight() != 0 {
		t.Error("失败后应清理进行中表")
	}
}
