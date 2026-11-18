// Package policy 把缓存配置编译成判定器。
//
// 它是 cache.Policy 的唯一实现，也是唯二认识 pkg/quality 的地方
// （音质别名要先归一化，否则配置里写 fl24、请求里传 hires 就会互相错过）。
//
// 本包不认识存储实现，因此「换 Redis」「换内存」都不需要动它。
package policy

import (
	"fmt"
	"sync"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/cache"
	"github.com/lxsrcs/lx-source-next/pkg/quality"
)

// 缓存模式。
const (
	// ModeNone 不缓存。
	ModeNone = 0
	// ModeSafe 安全模式：等缓存落盘后再返回缓存地址。
	ModeSafe = 1
	// ModeFast 极速模式：首次直接返回上游链接，后台回填。
	ModeFast = 2
)

// DefaultTTL 是未显式配置 TTL 时的兜底值。
const DefaultTTL = time.Hour

// Config 是缓存规则配置。
//
// 列表语义：启用排除模式时列表是「不缓存的集合」；
// 否则列表是「缓存的集合」。列表为空表示不限制。
type Config struct {
	// Mode 缓存模式，ModeNone 表示全关。
	Mode int
	// TTL 缓存有效期；<=0 时用 DefaultTTL。
	TTL time.Duration
	// Plats 平台二字码列表。
	Plats []string
	// ExcludePlat 列表是否为排除语义。
	ExcludePlat bool
	// Quals 音质列表。
	Quals []string
	// ExcludeQual 列表是否为排除语义。
	ExcludeQual bool
	// AllowSpecial 是否允许缓存特殊音质（temp 试听 / orig 原声）。
	AllowSpecial bool
}

// Dynamic 是随配置变化的判定器。
//
// 它只在配置指纹变化时重建内部编译结果，
// 因此热更会让「后续写缓存的 TTL 与规则」立刻更新，
// 而已经存在于缓存里的数据一个字节都不会被动到。
type Dynamic struct {
	get func() Config

	mu  sync.Mutex
	key string
	cur *compiled
}

type compiled struct {
	mode        int
	ttl         time.Duration
	plats       map[string]struct{}
	quals       map[string]struct{}
	excludePlat bool
	excludeQual bool
	allowSpec   bool
}

// NewDynamic 创建判定器。get 会在每次判定时被调用（开销极小）。
func NewDynamic(get func() Config) *Dynamic {
	if get == nil {
		get = func() Config { return Config{Mode: ModeNone} }
	}
	return &Dynamic{get: get}
}

// Decide 实现 cache.Policy。
func (d *Dynamic) Decide(m cache.Meta) (time.Duration, bool) {
	cfg := d.get()
	c := d.compiled(cfg)
	if c.mode == ModeNone {
		return 0, false
	}
	if !allowed(m.Source, c.plats, c.excludePlat) {
		return 0, false
	}

	q, ok := quality.Canonical(m.Quality)
	if !ok {
		// 未知音质不参与缓存，避免把平台新音质名误当普通音质存进去
		return 0, false
	}
	if !quality.IsFallback(q) && !c.allowSpec {
		return 0, false
	}
	if !allowed(q, c.quals, c.excludeQual) {
		return 0, false
	}
	return c.ttl, true
}

func (d *Dynamic) compiled(cfg Config) *compiled {
	key := fingerprint(cfg)

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.cur != nil && d.key == key {
		return d.cur
	}
	c := &compiled{
		mode:        cfg.Mode,
		ttl:         cfg.TTL,
		plats:       toSet(cfg.Plats),
		quals:       toSet(cfg.Quals),
		excludePlat: cfg.ExcludePlat,
		excludeQual: cfg.ExcludeQual,
		allowSpec:   cfg.AllowSpecial,
	}
	if c.ttl <= 0 {
		c.ttl = DefaultTTL
	}
	d.cur = c
	d.key = key
	return c
}

// allowed 按集合语义判定。
//
// 空集合视为「不限制」：配置文件里没写平台列表时，
// 合理预期是「全都缓存」，而不是「一个都不缓存」。
func allowed(v string, set map[string]struct{}, exclude bool) bool {
	if len(set) == 0 {
		return true
	}
	_, in := set[v]
	if exclude {
		return !in
	}
	return in
}

func toSet(list []string) map[string]struct{} {
	if len(list) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(list))
	for _, s := range list {
		if s == "" {
			continue
		}
		out[s] = struct{}{}
	}
	return out
}

func fingerprint(c Config) string {
	return fmt.Sprintf("%d|%d|%v|%v|%v|%v|%v",
		c.Mode, int64(c.TTL), c.Plats, c.Quals, c.ExcludePlat, c.ExcludeQual, c.AllowSpecial)
}
