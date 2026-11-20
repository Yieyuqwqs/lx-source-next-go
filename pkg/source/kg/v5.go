package kg

// 酷狗 v5 取链接口（trackercdn v5）与其签名算法。
//
// 签名算法来自官方 Android 客户端脚本的公开实现：
//
//	key        = md5(hash + "57ae12eb6890223e355ccfcb74edf70d1005-0")
//	params     = 全部参数按 key 字典序排序后 k=v 连接（一段给签名、一段给 query）
//	signature  = md5("OIlwieks28dk2k092lksi2UIkp" + params + "OIlwieks28dk2k092lksi2UIkp")
//
// 实测：免费曲目的 128k 返回 status=1 且能下载；高音质是否可用取决于曲目权限，
// 因此上层按 flac24bit → flac → 320k → 128k 逐档回退，并如实上报最终拿到的档位。
//
// 注意：2026-11-21 起匿名请求开始被要求人工验证（status=2「本次请求需要验证」），
// 四个档位会一起失败。这类失败是**请求级**的，重试别的档位没有意义，
// 所以 v5URLFor 会立刻返回，不再空跑三趟。

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/quality"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

const (
	// v5Endpoint 是取链地址。
	v5Endpoint = "https://trackercdn.kugou.com/v5/url"
	// v5SignKey 是签名用的固定前后缀。
	v5SignKey = "OIlwieks28dk2k092lksi2UIkp"
	// v5KeySuffix 是计算 key 参数时的固定后缀。
	v5KeySuffix = "57ae12eb6890223e355ccfcb74edf70d1005-0"
	// v5UA 是 v5 接口要求的客户端标识。
	v5UA = "Android711-1070-12029-14-0-NetMusic-wifi"
)

// v5Quality 把规范音质映射成 v5 的 quality 参数。
//
// 酷狗只有四档：128 / 320 / flac / high（high 就是 24bit 母带版）。
func v5Quality(qual string) string {
	c, ok := quality.Canonical(qual)
	if !ok {
		return "128"
	}
	switch c {
	case quality.Q320:
		return "320"
	case quality.QFlac:
		return "flac"
	case quality.QFlac24:
		return "high"
	}
	return "128"
}

// v5Fallback 返回从请求档位逐档降到 128 的顺序（只降不升）。
func v5Fallback(qual string) []string {
	order := []string{"high", "flac", "320", "128"}
	top := v5Quality(qual)
	for i, q := range order {
		if q == top {
			return order[i:]
		}
	}
	return order[len(order)-1:]
}

// qualityOfV5 把 v5 的 quality 参数反解成规范音质。
func qualityOfV5(q string) string {
	switch q {
	case "high":
		return quality.QFlac24
	case "flac":
		return quality.QFlac
	case "320":
		return quality.Q320
	}
	return quality.Q128
}

// md5Hex 是 md5 十六进制小工具。
func md5Hex(s string) string {
	sum := md5.Sum([]byte(s))
	return hex.EncodeToString(sum[:])
}

// v5Query 生成 v5 请求的 query 串与签名。
func v5Query(hash, vqual string, clientTime int64) (string, error) {
	params := map[string]string{
		"userid":         "0",
		"area_code":      "1",
		"module":         "",
		"hash":           hash,
		"appid":          "1005",
		"ssa_flag":       "is_fromtrack",
		"version":        "12029",
		"vipType":        "6",
		"ptype":          "0",
		"token":          "",
		"mtype":          "3",
		"quality":        vqual,
		"album_audio_id": "0",
		"behavior":       "play",
		"pid":            "2",
		"clienttime":     strconv.FormatInt(clientTime, 10),
		"cmd":            "26",
		"uuid":           "-",
		"mid":            "-",
		"dfid":           "-",
		"clientver":      "12029",
		"pidversion":     "3001",
		"key":            md5Hex(hash + v5KeySuffix),
	}

	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		pairs = append(pairs, k+"="+params[k])
	}
	joined := strings.Join(pairs, "")
	sig := md5Hex(v5SignKey + joined + v5SignKey)

	q := url.Values{}
	for _, k := range keys {
		q.Set(k, params[k])
	}
	q.Set("signature", sig)
	return q.Encode(), nil
}

// v5Response 是 v5 的响应体。
type v5Response struct {
	Status int             `json:"status"`
	URL    json.RawMessage `json:"url"`
	ErrMsg string          `json:"error"`
	// PrivStatus / FailProcess 是新版报文表达「要不要买」的字段：
	// priv_status=0 + fail_process=[pkg,buy] 就是「该曲目需要购买或会员」。
	PrivStatus  *int     `json:"priv_status"`
	FailProcess []string `json:"fail_process"`
}

// ErrVerifyRequired 表示上游要求人工验证（status=2「本次请求需要验证」）。
//
// 这是**请求级**的门槛：换档位、换 hash 都没用，重试只会白跑三趟，
// 所以要立刻向上冒，让上层直接报「该源当前取不到链接」。
var ErrVerifyRequired = errors.New("kg: 上游要求人工验证，匿名取链暂不可用")

// ErrNeedsPurchase 表示该曲目在酷狗需要购买或会员（priv_status=0）。
//
// 同样是请求级的：四个档位一起没戏，别白跑。
var ErrNeedsPurchase = errors.New("kg: 该曲目需要购买或开通会员")

// parseV5URL 从响应里取地址：url 既可能是字符串，也可能是数组（取最末一个）。
func parseV5URL(body []byte) (string, int, error) {
	var resp v5Response
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", 0, fmt.Errorf("kg: v5 响应不是 JSON: %w", err)
	}
	if resp.Status != 1 {
		msg := strings.TrimSpace(resp.ErrMsg)
		switch {
		// status=2 既可以表示「需要验证」，也可以表示「无权限」，
		// 所以先看文案；只有文案缺失时才按 status=2 保守判定成验证门槛。
		case strings.Contains(msg, "验证") || (resp.Status == 2 && msg == ""):
			if msg == "" {
				msg = "本次请求需要验证"
			}
			return "", resp.Status, fmt.Errorf("%w（status=%d, %s）", ErrVerifyRequired, resp.Status, msg)
		case resp.PrivStatus != nil && *resp.PrivStatus == 0:
			return "", resp.Status, fmt.Errorf("%w（priv_status=0, fail_process=%s）",
				ErrNeedsPurchase, strings.Join(resp.FailProcess, ","))
		}
		if msg == "" {
			msg = "权限不足或曲目无该音质"
		}
		return "", resp.Status, fmt.Errorf("kg: v5 未返回链接（status=%d, %s）", resp.Status, msg)
	}
	raw := strings.TrimSpace(string(resp.URL))
	if raw == "" || raw == "null" {
		return "", resp.Status, fmt.Errorf("kg: v5 响应里没有 url（status=%d）", resp.Status)
	}
	if strings.HasPrefix(raw, "[") {
		var list []string
		if err := json.Unmarshal(resp.URL, &list); err != nil || len(list) == 0 {
			return "", resp.Status, fmt.Errorf("kg: v5 url 数组解析失败")
		}
		link := strings.TrimSpace(list[len(list)-1])
		if link == "" {
			return "", resp.Status, fmt.Errorf("kg: v5 url 数组里没有可用地址（status=%d）", resp.Status)
		}
		return link, resp.Status, nil
	}
	var single string
	if err := json.Unmarshal(resp.URL, &single); err != nil {
		return "", resp.Status, fmt.Errorf("kg: v5 url 字段解析失败: %w", err)
	}
	// `"url":""` 的解码结果是空串（raw 非空、也不是 null），这里必须再挡一次：
	// 否则会带着「成功」把空链接交给上层，播放器拿到 200 却放不出任何东西。
	if link := strings.TrimSpace(single); link != "" {
		return link, resp.Status, nil
	}
	return "", resp.Status, fmt.Errorf("kg: v5 返回了空 url（status=%d）", resp.Status)
}

// urlV5 请求一条直链；失败时返回错误（调用方决定是否降档重试）。
func (c *Client) urlV5(ctx context.Context, hash, vqual string) (string, error) {
	query, err := v5Query(hash, vqual, c.now().Unix())
	if err != nil {
		return "", apierr.Wrap(apierr.CodeInternal, "kg: 构造 v5 请求失败", err)
	}
	u := c.cfg.V5URL
	if u == "" {
		u = v5Endpoint
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u+"?"+query, nil)
	if err != nil {
		return "", apierr.Wrap(apierr.CodeInternal, "kg: 构造 v5 请求失败", err)
	}
	req.Header.Set("User-Agent", v5UA)
	req.Header.Set("Kg-Thash", "5760185")
	req.Header.Set("Kg-Rec", "1")
	req.Header.Set("Kg-Rc", "1")

	resp, err := c.doer.Do(req)
	if err != nil {
		return "", apierr.Wrap(apierr.CodeUnavailable, "kg: 请求 v5 失败", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", apierr.Newf(apierr.CodeUnavailable, "kg: v5 返回 HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return "", apierr.Wrap(apierr.CodeUnavailable, "kg: 读取 v5 响应失败", err)
	}
	link, _, err := parseV5URL(body)
	if err != nil {
		return "", apierr.Wrap(apierr.CodeUnavailable, "kg: v5 取链失败", err)
	}
	return link, nil
}

// hashForQuality 在已知 hash 集合里按档位取对应的 hash；没有就返回空。
func hashForQuality(table map[string]string, vqual string) string {
	if table == nil {
		return ""
	}
	return table[vqual]
}

// v5URLFor 按档位回退取链，返回链接与最终档位。
func (c *Client) v5URLFor(ctx context.Context, table map[string]string, fallbackHash, qual string) (source.LinkInfo, error) {
	var lastErr error
	for _, vq := range v5Fallback(qual) {
		hash := hashForQuality(table, vq)
		if hash == "" {
			hash = fallbackHash
		}
		if hash == "" {
			continue
		}
		link, err := c.urlV5(ctx, hash, vq)
		if err != nil {
			lastErr = err
			if errors.Is(err, ErrVerifyRequired) {
				// 请求级门槛：再试别的档位也没有意义
				return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable,
					"kg: 上游要求人工验证，匿名取链暂不可用（歌词不受影响）", err)
			}
			if errors.Is(err, ErrNeedsPurchase) {
				// 会员曲目：四个档位一起没戏，别白跑三趟
				return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable,
					"kg: 该曲目需要购买或开通会员（匿名只出免费曲目）", err)
			}
			continue
		}
		return source.LinkInfo{
			Qual: qualityOfV5(vq),
			Expi: c.now().Add(c.cfg.Expire).Unix(),
			Link: link,
		}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("kg: 没有任何可用的 hash")
	}
	return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable, "kg: v5 各档位都取不到直链", lastErr)
}
