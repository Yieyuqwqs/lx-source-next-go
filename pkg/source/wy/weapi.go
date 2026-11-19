package wy

// 本文件实现网易云网页版的 weapi 协议。
//
// 为什么不只用 eapi：官方 eapi 现在必须登录态（匿名回 url:null / code:-110），
// 而 weapi 对「免费曲目」仍返回可直接下载的地址（实测 128k mp3、ID3 头、下载校验通过）。
// 因此取链顺序是 weapi → 聚合 → eapi，能拿到就不折腾后面的。

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"strings"

	"github.com/lxsrcs/lx-source-next/pkg/apierr"
	"github.com/lxsrcs/lx-source-next/pkg/quality"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

const (
	// weapiPresetKey 是固定的一层 AES 密钥（公开常量）。
	weapiPresetKey = "0CoJUm6Qyw8W8jud"
	// weapiIV 是固定 IV。
	weapiIV = "0102030405060708"
	// weapiAlphabet 是随机密钥的字符集。
	weapiAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	// weapiModulus 是 RSA 公钥模数（公开常量）。
	weapiModulus = "00e0b509f6259df8642dbc35662901477df22677ec152b5ff68ace615bb7b725152b3ab17a876aea8a5aa76d2e417629ec4ee341f56135fccf695280104e0312ecbda92557c93870114af6c9d05c4f7f0c3685b7a46bee255932575cce10b424d813cfe4875d3e82047b97ddef52741d546b8e289dc6935b3ece0462db0a22b8e7"
	// weapiPath 是取链接口路径。
	weapiPath = "/weapi/song/enhance/player/url/v1?csrf_token="
	// weapiAppver 是随 Cookie 下发的客户端版本号，缺了会被判为异常客户端。
	weapiAppver = "appver=2.9.7"
)

var weapiPubExp = big.NewInt(0x10001)

// weapiURLPayload 是取链载荷。用结构体保证字段顺序稳定，便于测试与排查。
type weapiURLPayload struct {
	IDs        string `json:"ids"`
	Level      string `json:"level"`
	EncodeType string `json:"encodeType"`
	CsrfToken  string `json:"csrf_token"`
}

// weapiLevel 把规范音质映射成 weapi 的 level 参数。
//
// 匿名请求即使要 lossless 也只会回 standard，这里如实按请求档位发，
// 最终上报的音质以响应里的 br 为准（绝不谎报无损）。
func weapiLevel(qual string) string {
	c, ok := quality.Canonical(qual)
	if !ok {
		return "standard"
	}
	switch c {
	case quality.Q192:
		return "higher"
	case quality.Q320:
		return "exhigh"
	case quality.QFlac:
		return "lossless"
	case quality.QFlac24:
		return "hires"
	}
	return "standard"
}

// qualityFromBR 把响应里的 br 反解成规范音质。
func qualityFromBR(br int) string {
	switch {
	case br >= 900000:
		return quality.QFlac24
	case br >= 500000:
		return quality.QFlac
	case br >= 300000:
		return quality.Q320
	case br >= 150000:
		return quality.Q192
	case br > 0:
		return quality.Q128
	}
	return quality.Q128
}

// aesCBCEncrypt 做 AES-CBC + PKCS#7，返回 base64。
func aesCBCEncrypt(key, plain []byte) (string, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("wy: 创建 AES 失败: %w", err)
	}
	padded := pkcs7Pad(plain, block.BlockSize())
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, []byte(weapiIV)).CryptBlocks(out, padded)
	return base64.StdEncoding.EncodeToString(out), nil
}

// randomWeapiKey 生成 16 位随机密钥。
func randomWeapiKey() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("wy: 取随机数失败: %w", err)
	}
	out := make([]byte, len(buf))
	for i, b := range buf {
		out[i] = weapiAlphabet[int(b)%len(weapiAlphabet)]
	}
	return string(out), nil
}

// weapiEncSecKey 用 RSA 处理反序后的随机密钥，输出 256 位十六进制。
func weapiEncSecKey(secret string) (string, error) {
	rev := []byte(secret)
	for i, j := 0, len(rev)-1; i < j; i, j = i+1, j-1 {
		rev[i], rev[j] = rev[j], rev[i]
	}
	mod, ok := new(big.Int).SetString(weapiModulus, 16)
	if !ok {
		return "", errors.New("wy: weapi 模数非法")
	}
	m := new(big.Int).SetBytes(rev)
	return fmt.Sprintf("%0256x", new(big.Int).Exp(m, weapiPubExp, mod)), nil
}

// weapiForm 把载荷加密成 params / encSecKey 表单。
func weapiForm(payload any) (url.Values, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("wy: 序列化 weapi 载荷失败: %w", err)
	}
	secret, err := randomWeapiKey()
	if err != nil {
		return nil, err
	}
	first, err := aesCBCEncrypt([]byte(weapiPresetKey), raw)
	if err != nil {
		return nil, err
	}
	params, err := aesCBCEncrypt([]byte(secret), []byte(first))
	if err != nil {
		return nil, err
	}
	enc, err := weapiEncSecKey(secret)
	if err != nil {
		return nil, err
	}
	form := url.Values{}
	form.Set("params", params)
	form.Set("encSecKey", enc)
	return form, nil
}

// ParseWeapiURL 从 weapi 响应里取地址与真实码率。
//
// url 为空是正常业务结果（会员曲目），必须如实说明原因，不能拼猜测地址。
func ParseWeapiURL(body []byte) (link string, br int, err error) {
	var resp struct {
		Code int `json:"code"`
		Data []struct {
			URL *string `json:"url"`
			Br  int     `json:"br"`
			Fee int     `json:"fee"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &resp); err != nil {
		return "", 0, fmt.Errorf("wy: weapi 响应不是 JSON: %w", err)
	}
	if len(resp.Data) == 0 {
		return "", 0, fmt.Errorf("wy: weapi 响应里没有 data（code=%d）", resp.Code)
	}
	d := resp.Data[0]
	if d.URL == nil || strings.TrimSpace(*d.URL) == "" {
		return "", d.Br, fmt.Errorf("wy: weapi 未返回直链（fee=%d，通常是会员曲目或无版权）", d.Fee)
	}
	return strings.TrimSpace(*d.URL), d.Br, nil
}

// urlWeapi 走 weapi 协议取直链。
//
// 匿名请求只对免费曲目返回地址（且最高 128k）；会员曲目会回 url:null，如实报错。
func (c *Client) urlWeapi(ctx context.Context, id, qual string) (source.LinkInfo, error) {
	form, err := weapiForm(weapiURLPayload{
		IDs:        "[" + id + "]",
		Level:      weapiLevel(qual),
		EncodeType: "aac",
	})
	if err != nil {
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeInternal, "wy: 生成 weapi 参数失败", err)
	}

	u := strings.TrimRight(c.cfg.Base, "/") + weapiPath
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeInternal, "wy: 构造 weapi 请求失败", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Referer", c.cfg.Referer)
	req.Header.Set("Cookie", c.cookieHeader()+"; "+weapiAppver)

	body, err := c.post(req)
	if err != nil {
		return source.LinkInfo{}, err
	}
	link, br, err := ParseWeapiURL(body)
	if err != nil {
		return source.LinkInfo{}, apierr.Wrap(apierr.CodeUnavailable, "wy: weapi 取链失败", err)
	}
	return source.LinkInfo{
		Qual: qualityFromBR(br),
		Expi: c.now().Add(c.cfg.Expire).Unix(),
		Link: link,
	}, nil
}
