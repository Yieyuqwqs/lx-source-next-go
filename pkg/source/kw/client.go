// Package kw 对接酷我上游协议。
//
// 分层：
//   - cipher.go  纯函数：DES 加解密与 q 参数构造
//   - quality.go 纯函数：音质 ↔ 酷我 br 参数
//   - parse.go   纯函数：响应报文 → 结构体（容错多形态）
//   - client.go  唯一做网络 IO 的地方，HTTP 执行器可注入
//
// 两条上游路径：
//   - anti（默认）：antiserver 的 anti.s 接口，匿名、免密钥、纯文本返回；
//   - mobi：mobi.s 接口，需要 8 字节 DES 密钥。
//
// 默认走 anti，是因为它不需要任何凭据即可工作；
// mobi 作为可选路径保留，密钥到手后改配置即可启用。
package kw

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

// 上游策略。
const (
	// StrategyAnti 走 antiserver，匿名、免密钥。
	StrategyAnti = "anti"
	// StrategyMobi 走 mobi.s，需要 DES 密钥。
	StrategyMobi = "mobi"
)

// 公开的默认上游地址。
const (
	// DefaultAntiBaseURL 是 antiserver 地址，社区长期公开使用。
	DefaultAntiBaseURL = "https://antiserver.kuwo.cn"
	// DefaultMobiBaseURL 是 mobi 接口地址。
	DefaultMobiBaseURL = "https://mobi.kuwo.cn"
)

// maxBody 是响应体读取上限，防止异常上游打爆内存。
const maxBody = 8 << 20

// Doer 抽象 HTTP 执行器，测试可注入假实现，无需真实网络。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config 是上游接入参数。
//
// 需要确认的协议细节都收敛在这里，而不是散落在代码中。
type Config struct {
	// Src 是平台二字码，默认 kw。
	Src string
	// Strategy 是上游策略，取 StrategyAnti 或 StrategyMobi。
	// 留空时按 Key 是否就绪自动推断：有 8 字节密钥 → mobi，否则 anti。
	Strategy string
	// BaseURL 是上游根地址，为空时按策略取默认值。
	BaseURL string
	// Key 是 mobi 策略用的 DES 密钥，必须 8 字节。
	Key []byte
	// PathConvert 取链路径，为空时按策略取默认值。
	PathConvert string
	// PathLyric 歌词路径，默认 /mobi.s。
	PathLyric string
	// UserAgent 请求头。
	UserAgent string
	// Expire 直链有效期近似值，用于填充 expi。
	Expire time.Duration
}

// Client 实现 source.Source。
type Client struct {
	cfg  Config
	doer Doer
	now  func() time.Time
}

// 编译期断言：Client 必须满足 source.Source。
var _ source.Source = (*Client)(nil)

// New 创建客户端。doer 为 nil 时使用带超时的 http.Client。
func New(cfg Config, doer Doer) *Client {
	strategy := cfg.Strategy
	if strategy == "" {
		// 没显式指定时：密钥齐了就走 mobi，否则走免密钥的 anti。
		if len(cfg.Key) == 8 {
			strategy = StrategyMobi
		} else {
			strategy = StrategyAnti
		}
	}
	cfg.Strategy = strategy

	switch strategy {
	case StrategyAnti:
		if cfg.BaseURL == "" {
			cfg.BaseURL = DefaultAntiBaseURL
		}
		if cfg.PathConvert == "" {
			cfg.PathConvert = "/anti.s"
		}
	case StrategyMobi:
		if cfg.BaseURL == "" {
			cfg.BaseURL = DefaultMobiBaseURL
		}
		if cfg.PathConvert == "" {
			cfg.PathConvert = "/mobi.s"
		}
	}

	if cfg.Src == "" {
		cfg.Src = "kw"
	}
	if cfg.PathLyric == "" {
		cfg.PathLyric = "/mobi.s"
	}
	if cfg.Expire <= 0 {
		cfg.Expire = 30 * time.Minute
	}
	if doer == nil {
		doer = &http.Client{Timeout: 10 * time.Second}
	}
	return &Client{cfg: cfg, doer: doer, now: time.Now}
}

// WithClock 注入时钟，便于测试 expi。
func (c *Client) WithClock(f func() time.Time) *Client {
	if f != nil {
		c.now = f
	}
	return c
}

// Strategy 返回当前生效的策略，便于排查。
func (c *Client) Strategy() string { return c.cfg.Strategy }

// BaseURL 返回当前生效的上游地址。
func (c *Client) BaseURL() string { return c.cfg.BaseURL }

// Src 实现 source.Source。
func (c *Client) Src() string { return c.cfg.Src }

// ready 检查必要配置是否齐全。
//
// 未配置时返回明确错误，而不是发起一个注定失败的请求。
func (c *Client) ready() error {
	if strings.TrimSpace(c.cfg.BaseURL) == "" {
		return apierr.New(apierr.CodeUnavailable, "kw: 未配置上游地址")
	}
	switch c.cfg.Strategy {
	case StrategyAnti:
		return nil // 免密钥
	case StrategyMobi:
		if len(c.cfg.Key) != 8 {
			return apierr.Newf(apierr.CodeUnavailable, "kw: mobi 策略需要 8 字节 DES 密钥（当前 %d 字节）", len(c.cfg.Key))
		}
		return nil
	}
	return apierr.Newf(apierr.CodeUnavailable, "kw: 未知上游策略 %q", c.cfg.Strategy)
}

// URL 实现 source.Source：取播放直链。
func (c *Client) URL(ctx context.Context, id, q string) (source.LinkInfo, error) {
	if err := c.ready(); err != nil {
		return source.LinkInfo{}, err
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return source.LinkInfo{}, apierr.New(apierr.CodeBadRequest, "缺少音乐 ID")
	}
	br, canonical, ok := ResolveQuality(q)
	if !ok {
		return source.LinkInfo{}, apierr.Newf(apierr.CodeBadRequest, "酷我不支持音质 %q", q)
	}

	switch c.cfg.Strategy {
	case StrategyAnti:
		return c.urlAnti(ctx, id, br, canonical)
	case StrategyMobi:
		return c.urlMobi(ctx, id, br, canonical)
	}
	return source.LinkInfo{}, apierr.Newf(apierr.CodeUnavailable, "kw: 未知上游策略 %q", c.cfg.Strategy)
}

// urlAnti 走 antiserver，全程不需要密钥。
//
// TODO(协议确认)：br 参数在该接口上是否生效尚待核对；
// 若不生效，实际音质以对端返回为准。
func (c *Client) urlAnti(ctx context.Context, id, br, canonical string) (source.LinkInfo, error) {
	params := url.Values{}
	params.Set("type", "convert_url")
	params.Set("format", "mp3")
	params.Set("response", "url")
	params.Set("rid", "MUSIC_"+id)
	params.Set("br", br)

	body, err := c.get(ctx, c.cfg.PathConvert, params)
	if err != nil {
		return source.LinkInfo{}, err
	}
	link, err := ParseAntiURL(body)
	if err != nil {
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable, "kw: 解析取链响应失败", err)
	}
	return source.LinkInfo{
		Qual: canonical,
		Expi: c.now().Add(c.cfg.Expire).Unix(),
		Link: link,
	}, nil
}

// urlMobi 走 mobi.s，参数 DES 加密后放进 q。
//
// TODO(协议确认)：params 的字段名与取值来自公开实现，
// 需要一次真实抓包核对（尤其 type / sig / p2p / corp / user）。
func (c *Client) urlMobi(ctx context.Context, id, br, canonical string) (source.LinkInfo, error) {
	params := url.Values{}
	params.Set("type", "convert_url2")
	params.Set("br", br)
	params.Set("rid", id)
	params.Set("format", "mp3")
	params.Set("sig", "0")
	params.Set("p2p", "1")
	params.Set("corp", "kuwo")
	params.Set("user", "0")

	q64, err := BuildMobiQuery(params, c.cfg.Key)
	if err != nil {
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeInternal, "kw: 构造 q 参数失败", err)
	}
	body, err := c.get(ctx, c.cfg.PathConvert, url.Values{"f": {"kuwo"}, "q": {q64}})
	if err != nil {
		return source.LinkInfo{}, err
	}
	res, err := ParseConvertURL(body)
	if err != nil {
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable, "kw: 解析取链响应失败", err)
	}
	return source.LinkInfo{
		Qual: canonical,
		Expi: c.now().Add(c.cfg.Expire).Unix(),
		Link: res.URL,
	}, nil
}

// Lyric 实现 source.Source：取歌词。
//
// 目前只支持 mobi 策略（anti 接口不提供歌词）。
// TODO(协议确认)：歌词接口的路径与参数名。
func (c *Client) Lyric(ctx context.Context, id string) (source.LyricInfo, error) {
	if err := c.ready(); err != nil {
		return source.LyricInfo{}, err
	}
	if c.cfg.Strategy != StrategyMobi {
		return source.LyricInfo{}, apierr.New(apierr.CodeUnavailable, "kw: 当前策略不提供歌词（需要 mobi 策略 + 密钥）")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return source.LyricInfo{}, apierr.New(apierr.CodeBadRequest, "缺少音乐 ID")
	}

	params := url.Values{}
	params.Set("type", "lyric")
	params.Set("rid", id)
	params.Set("corp", "kuwo")
	params.Set("user", "0")

	q64, err := BuildMobiQuery(params, c.cfg.Key)
	if err != nil {
		return source.LyricInfo{}, apierr.Wrap(apierr.CodeInternal, "kw: 构造 q 参数失败", err)
	}
	body, err := c.get(ctx, c.cfg.PathLyric, url.Values{"f": {"kuwo"}, "q": {q64}})
	if err != nil {
		return source.LyricInfo{}, err
	}
	res, err := ParseLyric(body)
	if err != nil {
		return source.LyricInfo{}, apierr.Wrap(apierr.CodeUnavailable, "kw: 解析歌词失败", err)
	}
	return source.LyricInfo{Lyric: res.Lyric, TLyric: res.TLyric}, nil
}

// errNotImplemented 表示能力尚未接入。
var errNotImplemented = errors.New("kw: 该能力尚未接入，等待协议确认")

// Movie 实现 source.Source。
func (c *Client) Movie(context.Context, string, string) (source.LinkInfo, error) {
	return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable, "kw", errNotImplemented)
}

// Music 实现 source.Source。
func (c *Client) Music(context.Context, string) (source.MusicInfo, error) {
	return source.MusicInfo{}, apierr.Wrap(apierr.CodeUnavailable, "kw", errNotImplemented)
}

// Video 实现 source.Source。
func (c *Client) Video(context.Context, string) (source.MovieInfo, error) {
	return source.MovieInfo{}, apierr.Wrap(apierr.CodeUnavailable, "kw", errNotImplemented)
}

// Pic 实现 source.Source。
func (c *Client) Pic(context.Context, string, string) (string, error) {
	return "", apierr.Wrap(apierr.CodeUnavailable, "kw", errNotImplemented)
}

// get 是唯一发起 HTTP 请求的地方。
func (c *Client) get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	u := buildURL(c.cfg.BaseURL, path, params)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "kw: 构造请求失败", err)
	}
	if c.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", c.cfg.UserAgent)
	}

	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeUnavailable, "kw: 请求上游失败", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, apierr.Newf(apierr.CodeUnavailable, "kw: 上游返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeUnavailable, "kw: 读取响应失败", err)
	}
	return body, nil
}

// buildURL 由 base 与 path 拼接。
func buildURL(base, path string, params url.Values) string {
	u := strings.TrimRight(base, "/") + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
	return u
}

// Describe 返回一行可读的运行期描述，便于启动日志。
func (c *Client) Describe() string {
	return fmt.Sprintf("kw 源：策略=%s 地址=%s 密钥=%s", c.cfg.Strategy, c.cfg.BaseURL, maskKey(c.cfg.Key))
}

// maskKey 只暴露密钥长度，不打印内容。
func maskKey(k []byte) string {
	if len(k) == 0 {
		return "未配置"
	}
	return fmt.Sprintf("已配置(%d 字节)", len(k))
}
