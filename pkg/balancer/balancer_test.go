package balancer

import (
	"testing"
	"time"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time      { return f.t }
func (f *fakeClock) Add(d time.Duration) { f.t = f.t.Add(d) }

func newTestBreaker(clk *fakeClock) *Breaker {
	return New(Config{Threshold: 3, Cooldown: time.Minute, Now: clk.Now})
}

func TestOpensAfterThreshold(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	b := newTestBreaker(clk)

	for i := 0; i < 2; i++ {
		b.Failure("n1")
	}
	if s := b.State("n1"); s != Closed {
		t.Fatalf("两次失败后状态 = %v, want closed", s)
	}
	if !b.Allow("n1") {
		t.Fatal("未达阈值应放行")
	}

	b.Failure("n1") // 第三次
	if s := b.State("n1"); s != Open {
		t.Fatalf("三次失败后状态 = %v, want open", s)
	}
	if b.Allow("n1") {
		t.Fatal("熔断期内不应放行")
	}
}

func TestHalfOpenProbe(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	b := newTestBreaker(clk)
	for i := 0; i < 3; i++ {
		b.Failure("n1")
	}
	clk.Add(61 * time.Second)

	if s := b.State("n1"); s != HalfOpen {
		t.Fatalf("冷却结束状态 = %v, want half_open", s)
	}
	if !b.Allow("n1") {
		t.Fatal("半开状态应放行一次试探")
	}
	// 试探进行中，再来一个请求不能被放行
	if b.Allow("n1") {
		t.Fatal("试探进行中不应再放行")
	}
	// 试探成功即恢复
	b.Success("n1")
	if s := b.State("n1"); s != Closed {
		t.Fatalf("恢复后状态 = %v, want closed", s)
	}
	if !b.Allow("n1") {
		t.Fatal("恢复后应放行")
	}
}

func TestProbeFailureReopens(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	b := newTestBreaker(clk)
	for i := 0; i < 3; i++ {
		b.Failure("n1")
	}
	clk.Add(61 * time.Second)
	if !b.Allow("n1") {
		t.Fatal("应放行试探")
	}
	b.Failure("n1")
	if s := b.State("n1"); s != Open {
		t.Fatalf("试探失败后状态 = %v, want open", s)
	}
	if b.Allow("n1") {
		t.Fatal("试探失败后应继续冷却")
	}
}

func TestPickSkipsOpenNodes(t *testing.T) {
	clk := &fakeClock{t: time.Unix(0, 0)}
	b := newTestBreaker(clk)
	for i := 0; i < 3; i++ {
		b.Failure("a")
	}
	got, ok := b.Pick([]string{"a", "b", "c"})
	if !ok || got != "b" {
		t.Fatalf("Pick = %q,%v want b", got, ok)
	}
	for i := 0; i < 3; i++ {
		b.Failure("b")
		b.Failure("c")
	}
	if _, ok := b.Pick([]string{"a", "b", "c"}); ok {
		t.Fatal("全部熔断时不应选出节点")
	}
}

func TestDefaults(t *testing.T) {
	b := New(Config{})
	if b.cfg.threshold() != 3 || b.cfg.cooldown() != time.Minute {
		t.Errorf("默认参数 = %d, %v", b.cfg.threshold(), b.cfg.cooldown())
	}
	b.Reset()
	if len(b.Snapshot()) != 0 {
		t.Error("Reset 后快照应为空")
	}
}
