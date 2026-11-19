package qq

// 取链实现：musics.fcg + zzb 签名 + vkey。
//
// 与旧实现（musicu.fcg 的 req_1）的区别：
//   - 走 musics.fcg，带 sign 查询参数（算法见 sign.go），匿名才拿得到 purl；
//   - 先查曲目信息拿 media_mid / 付费标记 / 试听文件名，再请求 vkey；
//   - 付费曲目匿名只能拿试听片段（RS02 前缀），免费曲目才给完整文件。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/quality"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

const (
	// DefaultMusicURL 是带签名的统一入口。
	DefaultMusicURL = "https://u.y.qq.com/cgi-bin/musics.fcg"
	// defaultUin 是匿名请求用的占位 QQ 号。
	defaultUin = "1535153710"
	// defaultGuid 是客户端 guid。
	defaultGuid = "20211008"
)

// qqFile 是一档音质的文件名规则。
type qqFile struct {
	qual   string
	prefix string
	ext    string
}

// qqLadder 从高到低排列；取链时按请求档位切片。
var qqLadder = []qqFile{
	{quality.QFlac24, "RS01", "flac"},
	{quality.QFlac, "F000", "flac"},
	{quality.Q320, "M800", "mp3"},
	{quality.Q128, "M500", "mp3"},
}

// ladderFrom 返回从请求档位往下的候选。
func ladderFrom(qual string) []qqFile {
	c, ok := quality.Canonical(qual)
	if !ok {
		c = quality.Q128
	}
	for i, f := range qqLadder {
		if f.qual == c {
			return qqLadder[i:]
		}
	}
	return qqLadder[len(qqLadder)-1:]
}

// trackInfo 只保留取链真正要用的字段。
type trackInfo struct {
	MediaMid string
	PayPlay  int
	VS       []string
}

// rawTrackInfo 是 get_song_detail_yqq 的原始结构。
type rawTrackInfo struct {
	Req struct {
		Code int `json:"code"`
		Data struct {
			TrackInfo struct {
				Name string `json:"name"`
				File struct {
					MediaMid string `json:"media_mid"`
				} `json:"file"`
				Pay struct {
					PayPlay int `json:"pay_play"`
				} `json:"pay"`
				VS []string `json:"vs"`
			} `json:"track_info"`
		} `json:"data"`
	} `json:"req"`
}

// uin 返回请求用的 QQ 号。
func (c *Client) uin() string {
	if c.cfg.Uin != "" {
		return c.cfg.Uin
	}
	// 只给了 Cookie 时，从里面解析 QQ 号
	if u := cookieUIN(c.cfg.Cookie); u != "0" {
		return u
	}
	return defaultUin
}

// signedPost 对请求体签名后 POST 到 musics.fcg。
func (c *Client) signedPost(ctx context.Context, body []byte) ([]byte, error) {
	base := c.cfg.MusicURL
	if base == "" {
		base = DefaultMusicURL
	}
	sep := "?"
	if strings.Contains(base, "?") {
		sep = "&"
	}
	u := base + sep + "format=json&sign=" + Sign(body)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeInternal, "qq: 构造请求失败", err)
	}
	req.Header.Set("Content-Type", "application/json")
	c.setHeaders(req)

	resp, err := c.doer.Do(req)
	if err != nil {
		return nil, apierr.Wrap(apierr.CodeUnavailable, "qq: 请求上游失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, apierr.Newf(apierr.CodeUnavailable, "qq: 上游返回 HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxBody))
}

// songInfo 取曲目信息（付费标记 / media_mid / 试听文件名）。
func (c *Client) songInfo(ctx context.Context, mid string) (trackInfo, error) {
	body, err := json.Marshal(map[string]any{
		"comm": map[string]any{"ct": "19", "cv": "1859", "uin": "0"},
		"req": map[string]any{
			"method": "get_song_detail_yqq",
			"module": "music.pf_song_detail_svr",
			"param":  map[string]any{"song_mid": mid, "song_type": 0},
		},
	})
	if err != nil {
		return trackInfo{}, apierr.Wrap(apierr.CodeInternal, "qq: 序列化曲目请求失败", err)
	}
	raw, err := c.signedPost(ctx, body)
	if err != nil {
		return trackInfo{}, err
	}
	var resp rawTrackInfo
	if err := json.Unmarshal(raw, &resp); err != nil {
		return trackInfo{}, apierr.Wrap(apierr.CodeUnavailable, "qq: 曲目信息不是 JSON", err)
	}
	ti := resp.Req.Data.TrackInfo
	if ti.Name == "" && ti.File.MediaMid == "" {
		return trackInfo{}, apierr.New(apierr.CodeUnavailable, "qq: 曲目信息为空（mid 可能无效）")
	}
	out := trackInfo{MediaMid: ti.File.MediaMid, PayPlay: ti.Pay.PayPlay, VS: ti.VS}
	return out, nil
}

// vkeyOnce 请求一次 vkey，返回拼好的直链。
func (c *Client) vkeyOnce(ctx context.Context, mid, filename string) (string, error) {
	uin := c.uin()
	body, err := json.Marshal(map[string]any{
		"comm": map[string]any{
			"authst": c.cfg.Ukey,
			"ct":     "26",
			"cv":     "2010101",
			"qq":     uin,
			"v":      "2010101",
		},
		"req_0": map[string]any{
			"method": "CgiGetVkey",
			"module": "vkey.GetVkeyServer",
			"param": map[string]any{
				"filename":  []string{filename},
				"guid":      defaultGuid,
				"loginflag": 1,
				"platform":  "20",
				"songmid":   []string{mid},
				"songtype":  []int{0},
				"uin":       uin,
			},
		},
	})
	if err != nil {
		return "", apierr.Wrap(apierr.CodeInternal, "qq: 序列化 vkey 请求失败", err)
	}
	raw, err := c.signedPost(ctx, body)
	if err != nil {
		return "", err
	}
	return ParseVkey(raw)
}

// urlTx 是 URL 的实际实现：先正常档位，再试听兜底。
func (c *Client) urlTx(ctx context.Context, mid, qual string) (source.LinkInfo, error) {
	info, err := c.songInfo(ctx, mid)
	if err != nil {
		return source.LinkInfo{}, err
	}
	mediaMid := info.MediaMid
	if mediaMid == "" {
		mediaMid = mid
	}

	// 付费曲目 + 没有凭据：正常档位一定回 104003，别再白打四枪，直接试听
	paidWithoutKey := info.PayPlay == 1 && strings.TrimSpace(c.cfg.Ukey) == ""

	var lastErr error
	if !paidWithoutKey {
		for _, f := range ladderFrom(qual) {
			link, err := c.vkeyOnce(ctx, mid, f.prefix+mediaMid+"."+f.ext)
			if err != nil {
				lastErr = err
				continue
			}
			return source.LinkInfo{
				Qual: f.qual,
				Expi: c.now().Add(c.cfg.Expire).Unix(),
				Link: link,
			}, nil
		}
	}

	// 付费曲目匿名只给试听片段：RS02 + vs 文件名
	if len(info.VS) > 0 && info.VS[0] != "" {
		if link, err := c.vkeyOnce(ctx, mid, "RS02"+info.VS[0]+".mp3"); err == nil {
			return source.LinkInfo{
				Qual: quality.Q128,
				Expi: c.now().Add(c.cfg.Expire).Unix(),
				Link: link,
			}, nil
		}
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("qq: 没有可用的音质档位")
	}
	return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable,
		"qq: 各档位都拿不到直链（付费曲目需 [Tx] Ukey/Uin 才能出完整链接）", lastErr)
}
