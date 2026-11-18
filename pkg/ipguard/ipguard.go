// Package ipguard 用标准库 netip 做 IP 黑白名单匹配。
//
// 旧实现是手写掩码比较，网段语义容易出错。这里统一用 netip.Prefix，
// 并把 config 里说反了的「严格模式」语义显式建模成 Decision。
package ipguard

import (
	"fmt"
	"net/netip"
	"strings"
)

// Decision 是一次校验的结论。
type Decision struct {
	// Allow 是否放行。
	Allow bool
	// Trusted 命中白名单，可跳过速率限制（但仍需密钥校验）。
	Trusted bool
}

// Config 是名单配置。元素可以是单个 IP 或 CIDR。
//
// 语义（与配置文件一致）：
//   - 命中 Allow：放行，且不受速率限制；
//   - 命中 Deny：普通模式拒绝；严格模式当作普通用户放行；
//   - 都没命中：普通模式放行；严格模式拒绝。
type Config struct {
	Allow  []string
	Deny   []string
	Strict bool
}

// Guard 是编译好的黑白名单。
type Guard struct {
	allow  []netip.Prefix
	deny   []netip.Prefix
	strict bool
}

// New 编译名单。
func New(cfg Config) (*Guard, error) {
	allow, err := parseAll(cfg.Allow)
	if err != nil {
		return nil, fmt.Errorf("ipguard: 白名单: %w", err)
	}
	deny, err := parseAll(cfg.Deny)
	if err != nil {
		return nil, fmt.Errorf("ipguard: 黑名单: %w", err)
	}
	return &Guard{allow: allow, deny: deny, strict: cfg.Strict}, nil
}

func parseAll(items []string) ([]netip.Prefix, error) {
	out := make([]netip.Prefix, 0, len(items))
	for _, raw := range items {
		p, err := ParsePrefix(raw)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// ParsePrefix 解析单个 IP 或 CIDR。
func ParsePrefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Prefix{}, fmt.Errorf("空地址")
	}
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return netip.Prefix{}, err
		}
		return p.Masked(), nil
	}
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// Check 校验一个 IP。
func (g *Guard) Check(addr netip.Addr) Decision {
	addr = addr.Unmap()
	if g == nil {
		return Decision{Allow: true}
	}
	for _, p := range g.allow {
		if p.Contains(addr) {
			return Decision{Allow: true, Trusted: true}
		}
	}
	denied := false
	for _, p := range g.deny {
		if p.Contains(addr) {
			denied = true
			break
		}
	}
	if denied {
		// 严格模式：黑名单反而当普通用户放行；其余未过白的一律拒绝
		return Decision{Allow: g.strict, Trusted: false}
	}
	return Decision{Allow: !g.strict, Trusted: false}
}

// CheckString 是 Check 的字符串入口，解析失败一律拒绝。
func (g *Guard) CheckString(s string) Decision {
	addr, err := netip.ParseAddr(strings.TrimSpace(s))
	if err != nil {
		return Decision{Allow: false}
	}
	return g.Check(addr)
}

// Len 返回两边的条目数。
func (g *Guard) Len() (allow, deny int) {
	if g == nil {
		return 0, 0
	}
	return len(g.allow), len(g.deny)
}
