package config

import (
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// Bootstrap 是进程级配置，启动后不再变化。
type Bootstrap struct {
	// Listen 监听地址，如 ":1101"。
	Listen string `toml:"Listen"`
	// LogDir 日志目录。
	LogDir string `toml:"LogDir"`
	// Console 是否同时输出到控制台。
	Console bool `toml:"Console"`
	// Debug 调试模式。
	Debug bool `toml:"Debug"`
	// Proxy 是否存在前置代理（决定是否信任转发头）。
	Proxy bool `toml:"Proxy"`
	// RealIP 自定义真实 IP 请求头，以 | 分隔。
	RealIP string `toml:"RealIP"`
	// Cors 是否添加跨域响应头。
	Cors bool `toml:"Cors"`
	// ReloadInterval 动态配置轮询间隔，如 "2s"。
	ReloadInterval string `toml:"ReloadInterval"`
	// CacheDir 缓存目录。属于部署形态，不参与热更。
	CacheDir string `toml:"CacheDir"`
	// DNSServer 自定义 DNS 服务器（形如 223.5.5.5:53）。
	//
	// 留空时自动判断：能读到 /etc/resolv.conf 就用系统默认，
	// 读不到（典型是 Android/Termux 上的静态二进制）就回落到公共 DNS。
	DNSServer string `toml:"DNSServer"`
	// Kw 是酷我来源的上游参数。
	Kw KwConf `toml:"Kw"`
	// Wy 是网易云的取链参数。
	Wy WyConf `toml:"Wy"`
	// Tx 是 QQ 音乐的取链参数。
	Tx TxConf `toml:"Tx"`
}

// DefaultBootstrap 返回默认启动配置。
func DefaultBootstrap() Bootstrap {
	return Bootstrap{
		Listen:         ":1101",
		LogDir:         "data/log",
		Console:        true,
		ReloadInterval: "2s",
		CacheDir:       "data/cache",
		Kw:             KwConf{Strategy: "anti"},
		Wy:             WyConf{Agg: false},
	}
}

// Validate 检查启动配置。
func (b Bootstrap) Validate() error {
	if strings.TrimSpace(b.Listen) == "" {
		return fmt.Errorf("config: Main.Listen 不能为空")
	}
	return nil
}

// RealIPHeaders 把 RealIP 拆成请求头列表。
func (b Bootstrap) RealIPHeaders() []string {
	if strings.TrimSpace(b.RealIP) == "" {
		return nil
	}
	parts := strings.Split(b.RealIP, "|")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// ParseBootstrap 解析启动配置。
func ParseBootstrap(raw []byte) (Bootstrap, error) {
	b := DefaultBootstrap()
	dec := toml.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		return b, fmt.Errorf("config: 解析 bootstrap 失败: %w", err)
	}
	if err := b.Validate(); err != nil {
		return b, err
	}
	return b, nil
}

// LoadBootstrap 从文件读取启动配置。文件不存在时返回默认值。
func LoadBootstrap(path string) (Bootstrap, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return DefaultBootstrap(), nil
		}
		return Bootstrap{}, fmt.Errorf("config: 读取 %s: %w", path, err)
	}
	return ParseBootstrap(raw)
}

// KwConf 是酷我来源的上游参数。
//
// 放在启动配置而不是动态配置：上游地址与密钥属于部署形态，
// 改它们本就该重启，热更它们只会制造"配置变了但仍连旧地址"的困扰。
type KwConf struct {
	// Strategy 上游策略：anti 免密钥（默认），mobi 需要密钥。
	Strategy string `toml:"Strategy"`
	// BaseURL 上游根地址；留空时按策略取内置默认值。
	BaseURL string `toml:"BaseURL"`
	// Key mobi 策略用的 8 字节 DES 密钥。
	Key string `toml:"Key"`
}

// WyConf 是网易云来源的参数。
//
// 官方接口匿名请求只返回 url:null，取直链必须借第三方签名服务；
// 这个开关就是「要不要借」——关掉就只走官方接口，行为与未接入时一致。
//
// 默认关闭的依据（实测）：公共签名服务从部分网络访问时通时不通
// （Cloudflare 握手失败/超时），默认打开等于给每次取链加几秒不确定延迟。
// 自建一个 Meting / 签名服务后，把 AggURL 指过去再打开 Agg 才稳。
type WyConf struct {
	// Agg 是否借第三方签名服务取直链；默认 false。
	Agg bool `toml:"Agg"`
	// AggURL 自定义服务地址；留空用内置默认值。
	AggURL string `toml:"AggURL"`
	// Cookie 是网易云登录态（形如 "MUSIC_U=xxx"），留空为匿名。
	//
	// 匿名取链只能借第三方；带上自己的 Cookie 后官方接口直接可用，最稳。
	Cookie string `toml:"Cookie"`
}

// TxConf 是 QQ 音乐的取链参数。
//
// 匿名请求形状再对也拿不到 purl（服务端只回 104003 / 空串），
// 必须带上登录态 Cookie 才能取到真实地址。
type TxConf struct {
	// Cookie 是 QQ 音乐登录态（含 uin 与 qqmusic_key）。
	Cookie string `toml:"Cookie"`
	// Uin / Ukey 是从 Cookie 里取出的 QQ 号与会话票据；填了才能拿 VIP 音质。
	Uin  string `toml:"Uin"`
	Ukey string `toml:"Ukey"`
}
