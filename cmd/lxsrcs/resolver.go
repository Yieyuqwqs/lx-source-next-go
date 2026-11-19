package main

import (
	"context"
	"log/slog"
	"net"
	"os"
	"strings"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/config"
)

// fallbackDNSServer 是读不到系统 DNS 配置时的回落值。
//
// 阿里公共 DNS，国内可达性好。
const fallbackDNSServer = "223.5.5.5:53"

// resolvConfPath 是 Go 纯解析器读取的系统 DNS 配置。
const resolvConfPath = "/etc/resolv.conf"

// pickDNSServer 决定是否要接管 DNS，返回空串表示沿用系统默认。
//
// 背景：Release 里的二进制是 CGO_ENABLED=0 的静态文件，
// Go 会走纯解析器、依赖 /etc/resolv.conf。
// Android（含 Termux）上这个文件通常不存在，
// 于是所有域名解析都会立刻失败——表现为「请求上游失败」且耗时只有几毫秒。
func pickDNSServer(boot config.Bootstrap) string {
	if v := strings.TrimSpace(boot.DNSServer); v != "" {
		return normalizeDNSServer(v)
	}
	if resolvConfUsable() {
		return ""
	}
	return fallbackDNSServer
}

// normalizeDNSServer 补上默认 53 端口。
func normalizeDNSServer(v string) string {
	if _, _, err := net.SplitHostPort(v); err == nil {
		return v
	}
	return net.JoinHostPort(v, "53")
}

// resolvConfUsable 判断系统 DNS 配置是否可用。
func resolvConfUsable() bool {
	b, err := os.ReadFile(resolvConfPath)
	if err != nil {
		return false
	}
	// 要求至少有一条 nameserver 记录，空文件等同于不可用
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "nameserver") {
			return true
		}
	}
	return false
}

// installResolver 在需要时接管全局 DNS 解析。
func installResolver(boot config.Bootstrap, logger *slog.Logger) {
	server := pickDNSServer(boot)
	if server == "" {
		logger.Debug("沿用系统 DNS 配置")
		return
	}
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			d := net.Dialer{Timeout: 5 * time.Second}
			return d.DialContext(ctx, network, server)
		},
	}
	logger.Info("已接管 DNS 解析", "server", server,
		"reason", "系统 DNS 配置不可用，常见于 Android/Termux 上的静态二进制")
}
