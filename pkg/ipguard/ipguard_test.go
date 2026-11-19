package ipguard

import (
	"net/netip"
	"testing"
)

func TestParsePrefix(t *testing.T) {
	for _, s := range []string{"192.168.10.22", "192.168.0.0/16", "::1", "2001:db8::/32", "  10.0.0.5  "} {
		if _, err := ParsePrefix(s); err != nil {
			t.Errorf("ParsePrefix(%q) 失败: %v", s, err)
		}
	}
	for _, s := range []string{"", "abc", "999.1.1.1", "10.0.0.0/33"} {
		if _, err := ParsePrefix(s); err == nil {
			t.Errorf("ParsePrefix(%q) 应当失败", s)
		}
	}
}

func newGuard(t *testing.T, cfg Config) *Guard {
	t.Helper()
	g, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return g
}

func TestWhitelistTrusted(t *testing.T) {
	g := newGuard(t, Config{
		Allow: []string{"127.0.0.1", "192.168.0.0/16"},
		Deny:  []string{"10.0.0.0/8"},
	})
	d := g.Check(netip.MustParseAddr("192.168.10.22"))
	if !d.Allow || !d.Trusted {
		t.Errorf("白名单命中 = %+v", d)
	}
}

func TestNonStrict(t *testing.T) {
	g := newGuard(t, Config{Deny: []string{"10.0.0.0/8"}})
	if d := g.Check(netip.MustParseAddr("10.1.2.3")); d.Allow {
		t.Errorf("普通模式下黑名单应被拒绝: %+v", d)
	}
	if d := g.Check(netip.MustParseAddr("8.8.8.8")); !d.Allow || d.Trusted {
		t.Errorf("普通模式下普通 IP 应放行: %+v", d)
	}
}

func TestStrict(t *testing.T) {
	// 严格模式：黑名单当普通用户放行，未过白的一律拒绝
	g := newGuard(t, Config{
		Allow:  []string{"127.0.0.1"},
		Deny:   []string{"10.0.0.0/8"},
		Strict: true,
	})
	if d := g.Check(netip.MustParseAddr("10.1.2.3")); !d.Allow || d.Trusted {
		t.Errorf("严格模式黑名单 = %+v, want allow 且非 trusted", d)
	}
	if d := g.Check(netip.MustParseAddr("8.8.8.8")); d.Allow {
		t.Errorf("严格模式未过白的 IP 应拒绝: %+v", d)
	}
	if d := g.Check(netip.MustParseAddr("127.0.0.1")); !d.Allow || !d.Trusted {
		t.Errorf("严格模式白名单 = %+v", d)
	}
}

func TestIPv4Mapped(t *testing.T) {
	g := newGuard(t, Config{Deny: []string{"10.0.0.0/8"}})
	// ::ffff:10.0.0.1 应被当成 10.0.0.1 处理
	d := g.Check(netip.MustParseAddr("::ffff:10.0.0.1"))
	if d.Allow {
		t.Errorf("IPv4-mapped 地址未被正确解映射: %+v", d)
	}
}

func TestLenAndNilGuard(t *testing.T) {
	g := newGuard(t, Config{Allow: []string{"127.0.0.1", "192.168.0.0/16"}})
	a, b := g.Len()
	if a != 2 || b != 0 {
		t.Errorf("Len = %d,%d", a, b)
	}
	var nilGuard *Guard
	if d := nilGuard.Check(netip.MustParseAddr("1.1.1.1")); !d.Allow {
		t.Error("nil Guard 应放行")
	}
}
