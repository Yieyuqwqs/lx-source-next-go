package qq

// QQ 歌词是匿名可用的：fcg_query_lyric_new 直接返回 base64 的 LRC 与翻译。
// 这条通道不依赖 vkey，也不需要登录态。

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

// lyricRaw 是歌词接口的原始响应。
type lyricRaw struct {
	Retcode int    `json:"retcode"`
	Lyric   string `json:"lyric"`
	Trans   string `json:"trans"`
}

// decodeLyricField 解 base64（接口给的是 base64 文本）。
//
// 不是 base64 时按原样返回：上游偶尔直接给明文，没必要因此报错。
func decodeLyricField(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if b, err := base64.StdEncoding.DecodeString(s); err == nil {
		return strings.TrimSpace(string(b))
	}
	return s
}

// Lyric 实现 source.Source：取歌词（含翻译）。
func (c *Client) Lyric(ctx context.Context, id string) (source.LyricInfo, error) {
	mid := strings.TrimSpace(id)
	if mid == "" {
		return source.LyricInfo{}, apierr.New(apierr.CodeBadRequest, "缺少 QQ 音乐 songmid")
	}

	q := url.Values{}
	q.Set("songmid", mid)
	q.Set("format", "json")
	q.Set("g_tk", "5381")
	q.Set("loginUin", "0")
	q.Set("hostUin", "0")
	q.Set("inCharset", "utf8")
	q.Set("outCharset", "utf-8")
	q.Set("notice", "0")
	q.Set("platform", "yqq")
	q.Set("needNewCode", "0")

	base := c.cfg.SearchBase
	if base == "" {
		base = DefaultSearchBase
	}
	u := strings.TrimRight(base, "/") + "/lyric/fcgi-bin/fcg_query_lyric_new.fcg?" + q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return source.LyricInfo{}, apierr.Wrap(apierr.CodeInternal, "qq: 构造歌词请求失败", err)
	}
	c.setHeaders(req)

	body, err := c.do(req)
	if err != nil {
		return source.LyricInfo{}, err
	}
	var raw lyricRaw
	if err := json.Unmarshal(body, &raw); err != nil {
		return source.LyricInfo{}, apierr.Wrap(apierr.CodeUnavailable, "qq: 歌词响应不是 JSON", err)
	}
	lrc := decodeLyricField(raw.Lyric)
	if lrc == "" {
		return source.LyricInfo{}, apierr.Newf(apierr.CodeUnavailable, "qq: 该曲没有歌词（retcode=%d）", raw.Retcode)
	}
	return source.LyricInfo{Lyric: lrc, TLyric: decodeLyricField(raw.Trans)}, nil
}
