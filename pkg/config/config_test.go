package config

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const sampleTOML = `
[Auth.BanList]
Enable = true
Strict = false
White = ['127.0.0.1', '192.168.0.0/16']
Black = []

[Auth.RateLimit]
Enable = true
Block = 30
Globle = 60
Single = 15

[Auth.ApiPass]
Enable = true
Value = 'test-key'

[Apis]
WebLog = true
ApiOnly = false

[Srcs.Wy]
Enable = true

[Data]
Mode = 1
Type = 0

[Errs]
Expi = 120
`

func TestParseValid(t *testing.T) {
	d, err := Parse([]byte(sampleTOML))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !d.Auth.RateLimit.Enable || d.Auth.RateLimit.Block != 30 {
		t.Errorf("RateLimit 解析错误: %+v", d.Auth.RateLimit)
	}
	if d.Auth.BanList.Strict {
		t.Error("Strict 应为 false")
	}
	if got := len(d.Auth.BanList.White); got != 2 {
		t.Errorf("白名单条目 = %d, want 2", got)
	}
	if d.Auth.ApiPass.Value != "test-key" {
		t.Errorf("ApiPass.Value = %q", d.Auth.ApiPass.Value)
	}
}

func TestParseRejectsUnknownField(t *testing.T) {
	_, err := Parse([]byte("[Auth.RateLimit]\nEnable = true\nTypo = 1\n"))
	if err == nil {
		t.Fatal("未知字段应报错，而不是被静默忽略")
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		mut  func(*Dynamic)
		ok   bool
	}{
		{"默认配置", func(d *Dynamic) {}, true},
		{"负数分区", func(d *Dynamic) { d.Auth.RateLimit.Block = -1 }, false},
		{"分区过大", func(d *Dynamic) { d.Auth.RateLimit.Block = 99999 }, false},
		{"单键为负", func(d *Dynamic) { d.Auth.RateLimit.Single = -5 }, false},
		{"密钥为空", func(d *Dynamic) { d.Auth.ApiPass.Enable = true; d.Auth.ApiPass.Value = "" }, false},
		{"缓存模式非法", func(d *Dynamic) { d.Data.Mode = 5 }, false},
		{"错误音频时效为负", func(d *Dynamic) { d.Errs.Expi = -1 }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Default()
			c.mut(d)
			err := d.Validate()
			if c.ok && err != nil {
				t.Errorf("应当通过，却报错: %v", err)
			}
			if !c.ok && err == nil {
				t.Error("应当报错，却通过了")
			}
		})
	}
}

func TestHolderSwapIsAtomic(t *testing.T) {
	h := NewHolder(nil)
	if h.Current() == nil || h.Dynamic() == nil {
		t.Fatal("初始快照不应为空")
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	// 读者：始终应拿到完整对象
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				s := h.Current()
				if s == nil || s.Dyn == nil {
					t.Error("读到了空快照")
					return
				}
				if s.Dyn.Data.Mode < 0 || s.Dyn.Data.Mode > 2 {
					t.Error("读到了半成品配置")
					return
				}
			}
		}()
	}
	// 写者：不断换快照
	for i := 0; i < 200; i++ {
		d := Default()
		d.Data.Mode = i % 3
		h.Swap(d, [32]byte{}, time.Now())
	}
	close(stop)
	wg.Wait()

	if v := h.Version(); v != 201 {
		t.Errorf("版本号 = %d, want 201", v)
	}
}

func TestReloaderOnce(t *testing.T) {
	h := NewHolder(nil)
	var payload atomic.Value
	payload.Store([]byte(sampleTOML))
	loader := &dynamicLoader{get: func() ([]byte, error) { return payload.Load().([]byte), nil }}
	r := NewReloader(h, loader, nil)

	// 首次：内容与初始快照不同 → 发布
	out, err := r.Once()
	if err != nil {
		t.Fatalf("首次重载: %v", err)
	}
	if !out.Changed {
		t.Error("首次应当发布")
	}
	v1 := h.Version()

	// 第二次：内容未变 → 跳过，版本不变
	out, err = r.Once()
	if err != nil {
		t.Fatalf("第二次重载: %v", err)
	}
	if !out.Skipped || out.Changed {
		t.Errorf("内容未变应当跳过，实际 %+v", out)
	}
	if h.Version() != v1 {
		t.Error("跳过时不应改变版本号")
	}

	// 修改内容 → 发布新版本
	// 注意：不能重复声明已有的 TOML 表（[Errs] 已存在会解析失败），
	// 所以要追加一个原本不存在的表。
	payload.Store([]byte(sampleTOML + "\n[Srcs.Mg]\nEnable = true\n"))
	out, err = r.Once()
	if err != nil {
		t.Fatalf("第三次重载: %v", err)
	}
	if !out.Changed || h.Version() != v1+1 {
		t.Errorf("内容变化应当发布，实际 %+v version=%d", out, h.Version())
	}
	if !h.Dynamic().Srcs.Mg.Enable {
		t.Error("新配置未生效：Srcs.Mg.Enable 应为 true")
	}
}

func TestReloaderKeepsOldOnError(t *testing.T) {
	h := NewHolder(nil)
	bad := false
	loader := &dynamicLoader{get: func() ([]byte, error) {
		if bad {
			return []byte("[Auth.RateLimit]\nBlock = -1\n"), nil
		}
		return []byte(sampleTOML), nil
	}}
	r := NewReloader(h, loader, nil)
	if _, err := r.Once(); err != nil {
		t.Fatalf("首次重载: %v", err)
	}
	before := h.Version()
	beforeExp := h.Dynamic().Errs.Expi

	bad = true
	if _, err := r.Once(); err == nil {
		t.Fatal("非法配置应返回错误")
	}
	if h.Version() != before {
		t.Error("非法配置不应改变版本号")
	}
	if h.Dynamic().Errs.Expi != beforeExp {
		t.Error("非法配置不应影响旧快照")
	}
}

func TestReloaderReadError(t *testing.T) {
	h := NewHolder(nil)
	r := NewReloader(h, BytesLoader{Err: errors.New("磁盘挂了")}, nil)
	if _, err := r.Once(); err == nil {
		t.Fatal("读取失败应返回错误")
	}
	if h.Version() != 1 {
		t.Error("读取失败不应改变快照")
	}
}

func TestReloaderRunStops(t *testing.T) {
	h := NewHolder(nil)
	r := NewReloader(h, BytesLoader{Data: []byte(sampleTOML)}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx, 10*time.Millisecond) }()

	time.Sleep(30 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("Run 返回 %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run 未能及时退出")
	}
}

// dynamicLoader 让测试可以在运行中替换载荷。
type dynamicLoader struct {
	get func() ([]byte, error)
}

func (d *dynamicLoader) Read() ([]byte, error) { return d.get() }
