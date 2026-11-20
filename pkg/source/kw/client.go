// Package kw 对接酷我上游协议。
//
// 分层：
//   - cipher.go  纯函数：DES 加解密与 q 参数构造
//   - quality.go 纯函数：音质 ↔ 酷我 br 参数
//   - parse.go   纯函数：响应报文 → 结构体（容错多形态）
//   - client.go  唯一做网络 IO 的地方，HTTP 执行器可注入
//
// 两条取链路径：
//   - anti（默认）：antiserver 的 anti.s 接口，匿名、免密钥、纯文本返回；
//   - mobi：mobi.s 接口，需要 8 字节 DES 密钥。
//
// 默认走 anti，是因为它不需要任何凭据即可工作；
// mobi 作为可选路径保留，密钥到手后改配置即可启用。
//
// 歌词不走上面两条：它挂在 mlyric.kuwo.cn/mobi.s 上，
// 匿名可用、免密钥（载荷是 zlib + Base64 + 7 字节异或），与策略无关。
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
	"github.com/lxsrcs/lx-source-next/pkg/quality"
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
	// DefaultLyricBaseURL 是歌词接口地址（mlyric，匿名可用）。
	DefaultLyricBaseURL = "https://mlyric.kuwo.cn"
	// lyricPath 是歌词接口路径，拼在 LyricBaseURL 后面。
	lyricPath = "/mobi.s"
)

// maxBody 是响应体读取上限，防止异常上游打爆内存。
const maxBody = 8 << 20

// DefaultAntiQuality 是 anti 策略默认上报的音质（保守取值）。
const DefaultAntiQuality = quality.Q128

// defaultUserAgent 是默认请求标识。
const defaultUserAgent = "Mozilla/5.0 (Linux; Android 13) AppleWebKit/537.36 (KHTML, like Gecko) Mobile Safari/537.36"

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
	// LyricBaseURL 歌词接口根地址，默认 DefaultLyricBaseURL。
	//
	// 歌词与取链是两套宿主：mlyric 匿名可用、免密钥，
	// 也不受 anti/mobi 策略影响。
	LyricBaseURL string
	// SearchHost 搜索接口地址，与取链不同域；留空用内置默认值。
	SearchHost string
	// UserAgent 请求头。
	UserAgent string
	// AntiQuality 是 anti 策略下上报的音质。
	//
	// antiserver 会忽略 br 参数、固定返回同一个文件，
	// 因此这里无法得知真实码率。默认取最保守的 128k：
	// 宁可在界面上低报，也不把 mp3 谎称成无损。
	AntiQuality string
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
	if cfg.LyricBaseURL == "" {
		cfg.LyricBaseURL = DefaultLyricBaseURL
	}
	if cfg.AntiQuality == "" {
		cfg.AntiQuality = DefaultAntiQuality
	}
	if c, ok := quality.Canonical(cfg.AntiQuality); ok {
		cfg.AntiQuality = c
	} else {
		cfg.AntiQuality = DefaultAntiQuality
	}
	if cfg.Expire <= 0 {
		cfg.Expire = 30 * time.Minute
	}
	// 给一个像客户端的 UA：不少国内接口对 Go-http-client 这种默认 UA 会直接拒绝
	if cfg.UserAgent == "" {
		cfg.UserAgent = defaultUserAgent
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
	id = trimMusicID(id)
	if id == "" {
		return source.LinkInfo{}, apierr.New(apierr.CodeBadRequest, "缺少音乐 ID")
	}
	if !validRID(id) {
		// 见 validRID 的注释：放行等于把一份不相干的音频当成成功返回
		return source.LinkInfo{}, apierr.Newf(apierr.CodeBadRequest, "酷我 id 必须是纯数字，收到 %q", id)
	}
	br, canonical, ok := c.resolveQuality(q)
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
// 走 mlyric 的公开接口，匿名可用、不需要密钥：
//
//	GET {LyricBaseURL}/mobi.s?f=web&type=lyric&lrcx=0&rid=<id>&encode=utf8
//
// 与取链策略无关——anti 策略下同样能取到歌词。
// TODO(逐字)：上游另有 lrcx=1 的逐字版本（<起始,时长> 标记），
// 现在统一按纯文本处理；要补 lxlyric 需先确认标记语义。
func (c *Client) Lyric(ctx context.Context, id string) (source.LyricInfo, error) {
	id = trimMusicID(id)
	if id == "" {
		return source.LyricInfo{}, apierr.New(apierr.CodeBadRequest, "缺少音乐 ID")
	}
	if strings.TrimSpace(c.cfg.LyricBaseURL) == "" {
		return source.LyricInfo{}, apierr.New(apierr.CodeUnavailable, "kw: 未配置歌词地址")
	}

	params := url.Values{}
	params.Set("f", "web")
	params.Set("type", "lyric")
	params.Set("lrcx", "0")
	params.Set("rid", id)
	params.Set("encode", "utf8")

	body, err := c.getURL(ctx, buildURL(c.cfg.LyricBaseURL, lyricPath, params))
	if err != nil {
		return source.LyricInfo{}, err
	}
	lrc, err := ParseLyricPayload(body)
	if err != nil {
		if errors.Is(err, ErrNoLyric) {
			return source.LyricInfo{}, apierr.Newf(apierr.CodeUnavailable, "kw: 曲目 %s 没有歌词", id)
		}
		return source.LyricInfo{}, apierr.Wrap(apierr.CodeUnavailable, "kw: 解析歌词失败", err)
	}
	return source.LyricInfo{Lyric: lrc}, nil
}

// trimMusicID 去掉 id 上可能带的 MUSIC_ 前缀，只留数字。
//
// 搜索接口给的是裸数字，但 v0 契约里 id 的来源不唯一，宽进严出。
func trimMusicID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) >= 6 && strings.EqualFold(id[:6], "MUSIC_") {
		id = id[6:]
	}
	return strings.TrimSpace(id)
}

// validRID 判断酷我的音乐 id 是否合法：纯数字、不超过 12 位。
//
// 这条校验不是洁癖，是实测出来的：antiserver 对**任何** rid 都回 200，
// 而且给的是同一个固定样例文件（`nf/resource/n1/69/32/588957081.mp3`）。
// 一旦把别的平台的 id（比如 QQ 的 songmid）传进来，我们就会「成功地」
// 返回一段不相干的音频，播放器表现是缓冲一下然后换源失败——
// 比直接报错难查得多。宁可 400，也不要 200 + 错文件。
func validRID(id string) bool {
	if id == "" || len(id) > 12 {
		return false
	}
	for i := 0; i < len(id); i++ {
		if id[i] < '0' || id[i] > '9' {
			return false
		}
	}
	return true
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

// get 在 BaseURL 上发请求。
func (c *Client) get(ctx context.Context, path string, params url.Values) ([]byte, error) {
	return c.getURL(ctx, buildURL(c.cfg.BaseURL, path, params))
}

// getURL 是唯一发起 HTTP 请求的地方。
func (c *Client) getURL(ctx context.Context, u string) ([]byte, error) {
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

// supported 返回当前策略真正支持的规范音质。
//
// anti 策略只有一档：该接口忽略 br，返回固定文件。
func (c *Client) supported() []string {
	if c.cfg.Strategy == StrategyAnti {
		return []string{c.cfg.AntiQuality}
	}
	return SupportedQualities()
}

// resolveQuality 在可用集合内回落，并给出对应的 br 参数。
func (c *Client) resolveQuality(req string) (br, canonical string, ok bool) {
	canonical, ok = quality.Resolve(req, c.supported())
	if !ok {
		return "", "", false
	}
	br = brTable[canonical]
	if br == "" {
		return "", "", false
	}
	return br, canonical, true
}

// NormalizeQuality 实现 source.QualityNormalizer。
//
// 让上层用它做缓存键：anti 策略下 128k/320k/flac 指向同一份文件，
// 不该各存一份。
func (c *Client) NormalizeQuality(req string) string {
	if c.cfg.Strategy == StrategyAnti {
		return c.cfg.AntiQuality
	}
	if _, canonical, ok := c.resolveQuality(req); ok {
		return canonical
	}
	return req
}

// Search 实现 source.Searcher：走与取链不同域的公开搜索接口。
//
// 该能力不需要任何密钥，也不依赖取链策略，因此不走 ready() 检查。
func (c *Client) Search(ctx context.Context, keyword string, page, size int) (source.SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return source.SearchResult{}, apierr.New(apierr.CodeBadRequest, "缺少搜索关键词")
	}
	body, err := c.getURL(ctx, c.searchURL(keyword, page, size))
	if err != nil {
		return source.SearchResult{}, err
	}
	res, err := ParseSearch(body)
	if err != nil {
		return source.SearchResult{}, apierr.Wrap(apierr.CodeUnavailable, "kw: 解析搜索结果失败", err)
	}
	return res, nil
}

// 编译期断言：必须实现搜索能力。
var _ source.Searcher = (*Client)(nil)
