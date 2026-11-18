package logx

import (
	"context"
	"log/slog"
	"testing"
	"time"
)

func TestHubReceivesAndRedacts(t *testing.T) {
	h := NewHub(8)
	logger := slog.New(h.Handler(nil))
	logger.Info("请求完成", "cookie", "MUSIC_U=secret", "user", "bob", "token", "abc")

	recs := h.Records()
	if len(recs) != 1 {
		t.Fatalf("记录数 = %d, want 1", len(recs))
	}
	r := recs[0]
	if r.Msg != "请求完成" || r.Level != slog.LevelInfo {
		t.Errorf("记录内容不对: %+v", r)
	}
	got := map[string]string{}
	for _, a := range r.Attrs {
		got[a.Key] = a.Value.String()
	}
	if got["cookie"] != redacted {
		t.Errorf("cookie 未脱敏: %q", got["cookie"])
	}
	if got["token"] != redacted {
		t.Errorf("token 未脱敏: %q", got["token"])
	}
	if got["user"] != "bob" {
		t.Errorf("普通字段被误改: %q", got["user"])
	}
}

func TestRingWraps(t *testing.T) {
	h := NewHub(3)
	logger := slog.New(h.Handler(nil))
	for _, m := range []string{"a", "b", "c", "d", "e"} {
		logger.Info(m)
	}
	recs := h.Records()
	if len(recs) != 3 {
		t.Fatalf("环形缓冲长度 = %d, want 3", len(recs))
	}
	var msgs []string
	for _, r := range recs {
		msgs = append(msgs, r.Msg)
	}
	want := []string{"c", "d", "e"}
	for i := range want {
		if msgs[i] != want[i] {
			t.Fatalf("顺序 = %v, want %v", msgs, want)
		}
	}
}

func TestSubscribe(t *testing.T) {
	h := NewHub(8)
	ch, cancel := h.Subscribe(4)
	defer cancel()
	if h.Subscribers() != 1 {
		t.Fatalf("订阅者数 = %d", h.Subscribers())
	}

	h.publish(Record{Msg: "推送测试"})
	select {
	case r := <-ch:
		if r.Msg != "推送测试" {
			t.Errorf("收到 %q", r.Msg)
		}
	case <-time.After(time.Second):
		t.Fatal("订阅者未收到日志")
	}

	cancel()
	if h.Subscribers() != 0 {
		t.Errorf("取消后订阅者数 = %d", h.Subscribers())
	}
}

func TestSlowSubscriberDoesNotBlock(t *testing.T) {
	h := NewHub(8)
	_, cancel := h.Subscribe(1)
	defer cancel()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			h.publish(Record{Msg: "大量日志"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("慢消费者阻塞了日志写入")
	}
}

func TestWithGroupAndAttrs(t *testing.T) {
	h := NewHub(8)
	logger := slog.New(h.Handler(nil)).With("svc", "api").WithGroup("wy")
	logger.Info("取链失败", "code", -460)

	recs := h.Records()
	if len(recs) != 1 {
		t.Fatalf("记录数 = %d", len(recs))
	}
	if recs[0].Group != "wy" {
		t.Errorf("Group = %q, want wy", recs[0].Group)
	}
	if len(recs[0].Attrs) != 2 {
		t.Errorf("属性数 = %d, want 2", len(recs[0].Attrs))
	}
}

func TestIsSensitive(t *testing.T) {
	yes := []string{"cookie", "Cookie", "refresh_token", "api_key", "Authorization", "MUSIC_U", "password"}
	for _, k := range yes {
		if !IsSensitive(k) {
			t.Errorf("IsSensitive(%q) 应为 true", k)
		}
	}
	no := []string{"user", "songid", "quality", "latency"}
	for _, k := range no {
		if IsSensitive(k) {
			t.Errorf("IsSensitive(%q) 应为 false", k)
		}
	}
}

func TestRedactGroup(t *testing.T) {
	a := slog.Group("auth", slog.String("token", "abc"), slog.String("user", "bob"))
	r := Redact(a)
	if r.Value.Kind() != slog.KindGroup {
		t.Fatal("分组被破坏")
	}
	for _, g := range r.Value.Group() {
		switch g.Key {
		case "token":
			if g.Value.String() != redacted {
				t.Errorf("嵌套 token 未脱敏: %q", g.Value.String())
			}
		case "user":
			if g.Value.String() != "bob" {
				t.Errorf("嵌套 user 被误改")
			}
		}
	}
}

func TestHandlerEnabled(t *testing.T) {
	h := NewHub(1)
	if !h.Handler(nil).Enabled(context.Background(), slog.LevelError) {
		t.Error("无下游时应当启用")
	}
}
