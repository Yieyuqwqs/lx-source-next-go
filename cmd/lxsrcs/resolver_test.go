package main

import (
	"testing"

	"github.com/lxsrcs/lx-source-next/pkg/config"
)

func TestNormalizeDNSServer(t *testing.T) {
	cases := map[string]string{
		"223.5.5.5":            "223.5.5.5:53",
		"223.5.5.5:53":         "223.5.5.5:53",
		"8.8.8.8:5353":         "8.8.8.8:5353",
		"2400:3200::1":         "[2400:3200::1]:53",
		"[2400:3200::1]:53":    "[2400:3200::1]:53",
	}
	for in, want := range cases {
		if got := normalizeDNSServer(in); got != want {
			t.Errorf("normalizeDNSServer(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPickDNSServerExplicit(t *testing.T) {
	// 显式配置优先，无论系统配置是否可用
	boot := config.Bootstrap{DNSServer: " 119.29.29.29 "}
	if got := pickDNSServer(boot); got != "119.29.29.29:53" {
		t.Errorf("显式 DNS 未生效: %q", got)
	}
}

func TestPickDNSServerFallback(t *testing.T) {
	boot := config.Bootstrap{}
	got := pickDNSServer(boot)
	// 结果取决于本机是否有 /etc/resolv.conf，两种都合法，但要各自自洽
	if resolvConfUsable() {
		if got != "" {
			t.Errorf("系统 DNS 可用时不应接管，实际 %q", got)
		}
	} else {
		if got != fallbackDNSServer {
			t.Errorf("系统 DNS 不可用时应回落，实际 %q", got)
		}
	}
}
