package policy

import (
	"sync"
	"testing"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/cache"
)

func TestModeNone(t *testing.T) {
	p := NewDynamic(func() Config { return Config{Mode: ModeNone} })
	if _, ok := p.Decide(cache.Meta{Source: "wy", Quality: "320k"}); ok {
		t.Error("Mode=0 应不可缓存")
	}
}

func TestDefaultTTL(t *testing.T) {
	p := NewDynamic(func() Config { return Config{Mode: ModeSafe} })
	ttl, ok := p.Decide(cache.Meta{Source: "wy", Quality: "320k"})
	if !ok {
		t.Fatal("应可缓存")
	}
	if ttl != DefaultTTL {
		t.Errorf("未配置 TTL 时应回落默认值，得到 %v", ttl)
	}
}

func TestIncludeSemantics(t *testing.T) {
	p := NewDynamic(func() Config {
		return Config{Mode: ModeSafe, TTL: time.Minute, Plats: []string{"wy", "kg"}}
	})
	if _, ok := p.Decide(cache.Meta{Source: "wy", Quality: "320k"}); !ok {
		t.Error("列表内平台应可缓存")
	}
	if _, ok := p.Decide(cache.Meta{Source: "kw", Quality: "320k"}); ok {
		t.Error("列表外平台不应缓存")
	}
}

func TestExcludeSemantics(t *testing.T) {
	p := NewDynamic(func() Config {
		return Config{Mode: ModeSafe, TTL: time.Minute, Plats: []string{"wy"}, ExcludePlat: true}
	})
	if _, ok := p.Decide(cache.Meta{Source: "wy", Quality: "320k"}); ok {
		t.Error("排除模式下列表内平台不应缓存")
	}
	if _, ok := p.Decide(cache.Meta{Source: "kg", Quality: "320k"}); !ok {
		t.Error("排除模式下其他平台应可缓存")
	}
}

func TestEmptyListMeansUnlimited(t *testing.T) {
	p := NewDynamic(func() Config { return Config{Mode: ModeSafe, TTL: time.Minute} })
	if _, ok := p.Decide(cache.Meta{Source: "anything", Quality: "flac"}); !ok {
		t.Error("列表为空应视为不限制")
	}
}

func TestQualityAliasNormalized(t *testing.T) {
	p := NewDynamic(func() Config {
		return Config{Mode: ModeSafe, TTL: time.Minute, Quals: []string{"flac24bit"}}
	})
	// 配置写规范名，请求用别名，必须能匹配上
	if _, ok := p.Decide(cache.Meta{Source: "wy", Quality: "hires"}); !ok {
		t.Error("别名 hires 应归一化为 flac24bit 并命中")
	}
	if _, ok := p.Decide(cache.Meta{Source: "wy", Quality: "fl24"}); !ok {
		t.Error("别名 fl24 应归一化并命中")
	}
	if _, ok := p.Decide(cache.Meta{Source: "wy", Quality: "320k"}); ok {
		t.Error("未在列表内的音质不应缓存")
	}
}

func TestSpecialQuality(t *testing.T) {
	base := Config{Mode: ModeSafe, TTL: time.Minute}
	if _, ok := NewDynamic(func() Config { return base }).Decide(cache.Meta{Source: "wy", Quality: "temp"}); ok {
		t.Error("特殊音质默认不应参与缓存")
	}
	withSpecial := base
	withSpecial.AllowSpecial = true
	if _, ok := NewDynamic(func() Config { return withSpecial }).Decide(cache.Meta{Source: "wy", Quality: "temp"}); !ok {
		t.Error("显式允许后特殊音质应可缓存")
	}
}

func TestUnknownQualityRejected(t *testing.T) {
	p := NewDynamic(func() Config { return Config{Mode: ModeSafe, TTL: time.Minute} })
	if _, ok := p.Decide(cache.Meta{Source: "wy", Quality: "384k"}); ok {
		t.Error("未知音质不应缓存")
	}
}

func TestDynamicRebuildsOnChange(t *testing.T) {
	var mu sync.Mutex
	cfg := Config{Mode: ModeSafe, TTL: time.Minute, Plats: []string{"wy"}}
	p := NewDynamic(func() Config {
		mu.Lock()
		defer mu.Unlock()
		return cfg
	})

	if _, ok := p.Decide(cache.Meta{Source: "kg", Quality: "320k"}); ok {
		t.Fatal("初始不应允许 kg")
	}
	mu.Lock()
	cfg = Config{Mode: ModeSafe, TTL: time.Minute, Plats: []string{"kg"}}
	mu.Unlock()

	if _, ok := p.Decide(cache.Meta{Source: "kg", Quality: "320k"}); !ok {
		t.Error("配置变化后应重新编译并生效")
	}
}
