// Package kw 对接酷我上游协议。
//
// 分层：
//   - cipher.go  纯函数：DES 加解密与 q 参数构造
//   - quality.go 纯函数：音质 ↔ 酷我 br 参数
//   - parse.go   纯函数：响应报文 → 结构体（容错，覆盖多种已知形态）
//   - client.go  唯一做网络 IO 的地方，HTTP 执行器可注入
//
// 协议里尚未确认的部分（密钥、mobi.s 的字段名、响应确切形状）
// 全部收敛成配置项或容错解析，不写死在代码里。
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

// maxBody 是响应体读取上限，防止异常上游打爆内存。
const maxBody = 8 << 20

// Doer 抽象 HTTP 执行器，测试可注入假实现，无需真实网络。
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Config 是上游接入参数。
//
// 需要确认的协议细节都在这里，而不是散落在代码中：
//   - BaseURL：mobi 接口域名，如 https://mobi.kuwo.cn
//   - Key：q 参数的 DES 密钥（8 字节）
//   - PathConvert：取链路径，默认 /mobi.s
//   - PathLyric：歌词路径，待抓包确认
type Config struct {
	// Src 是平台二字码，默认 kw。
	Src string
	// BaseURL 是上游根地址，为空时所有请求直接返回「未配置」。
	BaseURL string
	// Key 是 DES 密钥，必须 8 字节；为空同样视为未配置。
	Key []byte
	// PathConvert 取链路径。
	PathConvert string
	// PathLyric 歌词路径。
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

// New 创建客户端。doer 为 nil 时使用 http.DefaultClient。
func New(cfg Config, doer Doer) *Client {
	if cfg.Src == "" {
		cfg.Src = "kw"
	}
	if cfg.PathConvert == "" {
		cfg.PathConvert = "/mobi.s"
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

// Src 实现 source.Source。
func (c *Client) Src() string { return c.cfg.Src }

// ready 检查必要配置是否齐全。
//
// 未配置时返回明确错误，而不是发起一个注定失败的请求。
func (c *Client) ready() error {
	if strings.TrimSpace(c.cfg.BaseURL) == "" {
		return apierr.New(apierr.CodeUnavailable, "kw: 未配置上游地址（Config.BaseURL）")
	}
	if len(c.cfg.Key) != 8 {
		return apierr.Newf(apierr.CodeUnavailable, "kw: 未配置 8 字节 DES 密钥（当前 %d 字节）", len(c.cfg.Key))
	}
	return nil
}

// URL 实现 source.Source：取播放直链。
//
// 流程：音质回落 → 参数组装 → DES 加密 → 请求 → 容错解析。
//
// TODO(协议确认)：params 的字段名与取值来自旧版公开实现，
// 需要一次真实抓包核对（尤其 type / sig / p2p / corp / user）。
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
// TODO(协议确认)：歌词接口的路径与参数名。
func (c *Client) Lyric(ctx context.Context, id string) (source.LyricInfo, error) {
	if err := c.ready(); err != nil {
		return source.LyricInfo{}, err
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

// 以下能力尚未实现：upstream 路径与参数都要抓包确认。
var errNotImplemented = errors.New("kw: 该能力尚未接入，等待抓包确认")

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
	u := strings.TrimRight(c.cfg.BaseURL, "/") + path
	if len(params) > 0 {
		u += "?" + params.Encode()
	}
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

// buildURL 由 base 与 path 拼接，供测试断言。
func buildURL(base, path string, params url.Values) (string, error) {
	u, err := url.Parse(strings.TrimRight(base, "/") + path)
	if err != nil {
		return "", fmt.Errorf("kw: 非法上游地址: %w", err)
	}
	if len(params) > 0 {
		u.RawQuery = params.Encode()
	}
	return u.String(), nil
}
