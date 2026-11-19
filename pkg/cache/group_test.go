package cache_test

import (
	"testing"

	"github.com/lxsrcs/lx-source-next/pkg/cache"
)

// TestGroupPanicReturnsError 加载函数 panic 时，调用方要拿到错误而不是 (nil, nil)。
func TestGroupPanicReturnsError(t *testing.T) {
	g := cache.NewGroup()
	data, err := g.Do("k", func() ([]byte, error) {
		panic("boom")
	})
	if err == nil {
		t.Fatal("panic 必须转成错误")
	}
	if data != nil {
		t.Errorf("panic 时不该返回数据: %q", data)
	}
	// 表项要清理干净，下一次还能正常用
	if n := g.InFlight(); n != 0 {
		t.Errorf("InFlight = %d，应为 0", n)
	}
	out, err := g.Do("k", func() ([]byte, error) { return []byte("ok"), nil })
	if err != nil || string(out) != "ok" {
		t.Fatalf("panic 之后应能继续工作: %q %v", out, err)
	}
}
