package wy

// 本文件对接第三方签名服务（默认 GD 音乐台），用于在未登录时取网易云直链。
//
// 为什么需要它：官方接口在匿名请求下只返回 url:null、code=-110，
// 而第三方服务替我们完成签名，返回可直接下载的地址（实测 320k / ID3 mp3）。
// 代价是多一个外部依赖，因此它是「可关闭的加速路径」：
// 关掉（AggURL 留空）就退回纯官方接口，行为与从前一致。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/quality"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

// aggSource 是聚合服务里网易云的平台码。
const aggSource = "netease"

// aggTimeout 是单次聚合请求的兜底超时。
const aggTimeout = 6 * time.Second

// DefaultAggURL 是内置的第三方签名服务地址（GD 音乐台）。
//
// 它替我们完成网易云的签名，匿名就能拿到可下载的直链；
// 换服务只要改配置里的 Wy.AggURL，代码不用动。
const DefaultAggURL = "https://music-api.gdstudio.xyz/api.php"

// 聚合服务的 br 取值（与它自己的音质枚举一致）。
const (
	aggBR128  = 128
	aggBR192  = 192
	aggBR320  = 320
	aggBRFlac = 740
	aggBR24   = 999
)

// aggBR 把请求音质映射成聚合服务的 br 参数。
//
// 未知音质按 320 要：宁可多要一档再由对端降级，也不要一律给 128。
func aggBR(qual string) int {
	c, ok := quality.Canonical(qual)
	if !ok {
		return aggBR320
	}
	switch c {
	case quality.Q128:
		return aggBR128
	case quality.Q192:
		return aggBR192
	case quality.Q320:
		return aggBR320
	case quality.QFlac:
		return aggBRFlac
	case quality.QFlac24:
		return aggBR24
	}
	return aggBR320
}

// aggQuality 把聚合服务回显的 br 反解成规范音质。
//
// 一律以对端回显为准：要的是 320 但它只给了 128，就如实报 128k。
func aggQuality(br int) string {
	switch {
	case br >= aggBR24:
		return quality.QFlac24
	case br >= aggBRFlac:
		return quality.QFlac
	case br >= aggBR320:
		return quality.Q320
	case br >= aggBR192:
		return quality.Q192
	case br > 0:
		return quality.Q128
	}
	return quality.Q128
}

// ParseAggURL 解析聚合服务的取链响应。
//
// 形如 {"url":"https://m701.music.126.net/...","br":320,"size":13042460}。
// url 为空表示对端也拿不到（会员曲目 / 无版权），必须如实报错，不能拼猜测地址。
func ParseAggURL(body []byte) (link string, br int, err error) {
	var resp struct {
		URL  string `json:"url"`
		Br   int    `json:"br"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", 0, fmt.Errorf("wy: 聚合响应不是 JSON: %w", err)
	}
	if strings.TrimSpace(resp.URL) == "" {
		return "", resp.Br, fmt.Errorf("wy: 聚合服务未返回直链（br=%d，通常是会员曲目或无版权）", resp.Br)
	}
	return strings.TrimSpace(resp.URL), resp.Br, nil
}

// urlAgg 向聚合服务要一条直链。
//
// 聚合是外部依赖、时延不稳，这里单独给 6 秒子超时：
// 它再慢也不能把整条取链请求拖到客户端超时。
func (c *Client) urlAgg(ctx context.Context, id, qual string) (source.LinkInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, aggTimeout)
	defer cancel()

	q := url.Values{}
	q.Set("types", "url")
	q.Set("source", aggSource)
	q.Set("id", id)
	q.Set("br", strconv.Itoa(aggBR(qual)))

	u := c.cfg.AggURL
	if strings.Contains(u, "?") {
		u += "&" + q.Encode()
	} else {
		u += "?" + q.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeInternal, "wy: 构造聚合请求失败", err)
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	body, err := c.post(req)
	if err != nil {
		return source.LinkInfo{}, err
	}
	link, br, err := ParseAggURL(body)
	if err != nil {
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable, "wy: 聚合取链失败", err)
	}
	return source.LinkInfo{
		Qual: aggQuality(br),
		Expi: c.now().Add(c.cfg.Expire).Unix(),
		Link: link,
	}, nil
}
