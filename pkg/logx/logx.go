// Package logx 把标准库 slog 接到 SSE 日志流，并提供凭据脱敏。
//
// 原先自造的 pkg/zlog（自己实现 Level / Group / 滚动日志）重复了 slog 的能力。
// 这里不再造 Logger：只实现 slog.Handler，日志产生完全走标准库。
package logx

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"
)

// Record 是一条可供 SSE 推送的日志记录。
type Record struct {
	Time  time.Time
	Level slog.Level
	// Group 是当前 WithGroup 累积的分组路径，用 "." 连接。
	Group string
	Msg   string
	Attrs []slog.Attr
}

// IsSensitive 判断属性名是否为凭据类，需要脱敏。
func IsSensitive(key string) bool {
	k := strings.ToLower(key)
	for _, s := range sensitiveKeys {
		if strings.Contains(k, s) {
			return true
		}
	}
	return false
}

var sensitiveKeys = []string{
	"cookie", "token", "secret", "password", "pass", "apikey", "api_key",
	"authorization", "auth", "credential", "music_u", "refresh", "session",
}

// redacted 是脱敏后的占位值。
const redacted = "[REDACTED]"

// Redact 对单个属性脱敏；嵌套分组会递归处理。
func Redact(a slog.Attr) slog.Attr {
	if IsSensitive(a.Key) && a.Value.Kind() != slog.KindGroup {
		return slog.String(a.Key, redacted)
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		out := make([]any, 0, len(group))
		for _, g := range group {
			out = append(out, Redact(g))
		}
		return slog.Group(a.Key, out...)
	}
	return a
}

// Hub 是日志扇出中心：保留最近若干条，并分发给所有订阅者。
type Hub struct {
	mu    sync.RWMutex
	ring  []Record
	size  int
	next  int
	full  bool
	subs  map[int]chan Record
	subID int
}

// NewHub 创建容量为 size 条的环形缓冲；size<=0 时取 256。
func NewHub(size int) *Hub {
	if size <= 0 {
		size = 256
	}
	return &Hub{
		ring: make([]Record, size),
		size: size,
		subs: make(map[int]chan Record),
	}
}

// publish 写入环形缓冲并广播。慢消费者会被丢弃而不是阻塞日志写入。
func (h *Hub) publish(r Record) {
	h.mu.Lock()
	h.ring[h.next] = r
	h.next = (h.next + 1) % h.size
	if h.next == 0 {
		h.full = true
	}
	subs := make([]chan Record, 0, len(h.subs))
	for _, c := range h.subs {
		subs = append(subs, c)
	}
	h.mu.Unlock()

	for _, c := range subs {
		select {
		case c <- r:
		default: // 缓冲满，丢弃该条，避免拖住写日志的 goroutine
		}
	}
}

// Records 返回当前缓冲里的记录，按时间从旧到新。
func (h *Hub) Records() []Record {
	h.mu.RLock()
	defer h.mu.RUnlock()
	n := h.next
	if h.full {
		n = h.size
	}
	out := make([]Record, 0, n)
	if h.full {
		out = append(out, h.ring[h.next:]...)
	}
	out = append(out, h.ring[:h.next]...)
	return out
}

// Subscribe 订阅日志流，返回接收通道和取消函数。通道缓冲满时会丢包。
func (h *Hub) Subscribe(buf int) (<-chan Record, func()) {
	if buf <= 0 {
		buf = 64
	}
	h.mu.Lock()
	if h.subID == 0 {
		h.subID = 1
	}
	id := h.subID
	h.subID++
	c := make(chan Record, buf)
	h.subs[id] = c
	h.mu.Unlock()

	cancel := func() {
		h.mu.Lock()
		if ch, ok := h.subs[id]; ok {
			delete(h.subs, id)
			close(ch)
		}
		h.mu.Unlock()
	}
	return c, cancel
}

// Subscribers 返回当前订阅者数量。
func (h *Hub) Subscribers() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subs)
}

// Handler 包装一个下游 Handler：日志同时写下游，并进入 Hub。
// next 为 nil 时只进 Hub。
func (h *Hub) Handler(next slog.Handler) slog.Handler {
	return &teeHandler{hub: h, next: next}
}

type teeHandler struct {
	hub    *Hub
	next   slog.Handler
	attrs  []slog.Attr
	groups []string
}

func (t *teeHandler) Enabled(ctx context.Context, l slog.Level) bool {
	if t.next != nil {
		return t.next.Enabled(ctx, l)
	}
	return true
}

func (t *teeHandler) Handle(ctx context.Context, r slog.Record) error {
	rec := Record{
		Time:  r.Time,
		Level: r.Level,
		Group: strings.Join(t.groups, "."),
		Msg:   r.Message,
	}
	if len(t.attrs) > 0 {
		rec.Attrs = make([]slog.Attr, 0, len(t.attrs)+r.NumAttrs())
		rec.Attrs = append(rec.Attrs, t.attrs...)
	} else {
		rec.Attrs = make([]slog.Attr, 0, r.NumAttrs())
	}
	r.Attrs(func(a slog.Attr) bool {
		rec.Attrs = append(rec.Attrs, a)
		return true
	})
	for i := range rec.Attrs {
		rec.Attrs[i] = Redact(rec.Attrs[i])
	}
	t.hub.publish(rec)

	if t.next != nil {
		return t.next.Handle(ctx, r)
	}
	return nil
}

func (t *teeHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	if len(attrs) == 0 {
		return t
	}
	merged := make([]slog.Attr, 0, len(t.attrs)+len(attrs))
	merged = append(merged, t.attrs...)
	merged = append(merged, attrs...)
	nh := &teeHandler{hub: t.hub, next: t.next, attrs: merged, groups: t.groups}
	if t.next != nil {
		nh.next = t.next.WithAttrs(attrs)
	}
	return nh
}

func (t *teeHandler) WithGroup(name string) slog.Handler {
	if name == "" {
		return t
	}
	groups := make([]string, 0, len(t.groups)+1)
	groups = append(groups, t.groups...)
	groups = append(groups, name)
	nh := &teeHandler{hub: t.hub, next: t.next, attrs: t.attrs, groups: groups}
	if t.next != nil {
		nh.next = t.next.WithGroup(name)
	}
	return nh
}
