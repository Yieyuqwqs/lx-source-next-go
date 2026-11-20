package kw

import (
	"bytes"
	"compress/zlib"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
)

// 本文件是纯函数：输入字节、输出结构，可完全用固定报文测试。

// ErrUnsupportedShape 表示响应结构不在已知形态内。
//
// 遇到它不要猜，说明上游改版了，需要拿新报文来收敛解析。
var ErrUnsupportedShape = errors.New("kw: 无法识别的响应结构")

// ConvertURLResult 是从上游响应里抽出的直链信息。
type ConvertURLResult struct {
	// URL 是播放直链。
	URL string
	// Bitrate 是上游回显的 br 参数，可能为空。
	Bitrate string
	// Duration 是时长（秒），可能为 0。
	Duration int64
}

// urlKeys 是可能的直链字段名。
//
// 之所以列一组：不同版本接口用过 url / link / mp3 / playUrl。
// 真实报文到手后应当收敛成唯一的那个。
var urlKeys = []string{"url", "link", "mp3", "playUrl", "play_url", "src"}

// ParseConvertURL 解析取链响应。
//
// 已知形态（全部覆盖）：
//
//	{"code":200,"data":{"url":"...","bitrate":"320kmp3"}}
//	{"code":200,"data":"https://..."}
//	{"url":"https://..."}
//
// 顶层 code 非 200/0 时直接返回错误，并把上游 msg 带出来。
func ParseConvertURL(body []byte) (ConvertURLResult, error) {
	if len(bytesTrimSpace(body)) == 0 {
		return ConvertURLResult{}, errors.New("kw: 空响应")
	}

	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		return ConvertURLResult{}, fmt.Errorf("kw: 响应不是 JSON: %w", err)
	}

	if code, ok := top["code"]; ok {
		if n, ok := toInt(code); ok && n != 0 && n != 200 {
			msg, _ := top["msg"].(string)
			if msg == "" {
				msg, _ = top["message"].(string)
			}
			return ConvertURLResult{}, fmt.Errorf("kw: 上游返回 code=%d msg=%q", n, msg)
		}
	}

	res := ConvertURLResult{}
	// data 可能是对象，也可能是裸字符串
	switch data := top["data"].(type) {
	case string:
		res.URL = strings.TrimSpace(data)
	case map[string]any:
		res.URL = pickString(data, urlKeys)
		if v, ok := toInt(data["duration"]); ok {
			res.Duration = v
		}
		res.Bitrate = pickString(data, []string{"bitrate", "br"})
	}
	if res.URL == "" {
		res.URL = pickString(top, urlKeys)
	}
	if res.Bitrate == "" {
		res.Bitrate = pickString(top, []string{"bitrate", "br"})
	}
	if res.Duration == 0 {
		if v, ok := toInt(top["duration"]); ok {
			res.Duration = v
		}
	}
	if res.URL == "" {
		return ConvertURLResult{}, ErrUnsupportedShape
	}
	return res, nil
}

// ErrNoLyric 表示上游明确回答「这首歌没有歌词」。
//
// 与解析失败要分开：这是正常业务结果，不是上游改版，
// 上层据此回 503 而不是把空歌词当成成功缓存起来。
var ErrNoLyric = errors.New("kw: 该曲目没有歌词")

// lyricXorKey 是歌词载荷的固定异或密钥。
//
// 注意它只有 7 字节（yeelion = 酷我的运营公司），不是 DES 密钥；
// 早前按「8 字节 DES」去找密钥的方向是错的，真实链路根本没用 DES。
const lyricXorKey = "yeelion"

// lyricWordTag 匹配逐字歌词里的 <起始,时长> 标记。
var lyricWordTag = regexp.MustCompile(`<-?\d+,-?\d+>`)

// lyricTimeTag 用于确认正文里确实有时间轴。
var lyricTimeTag = regexp.MustCompile(`\[\d{1,3}:\d{1,2}[.:]\d{1,3}\]`)

// ParseLyricPayload 解析 mlyric 的歌词响应。
//
// 响应是三段式，头部两行以 \r\n\r\n 结束，之后跟 zlib 流：
//
//	TP=content\r\nlrcx=1\r\n\r\n<zlib>
//	TP=none\n                    ← 该曲目没有歌词
//
// zlib 解压出来是 Base64 文本，按 7 字节密钥循环异或后得到 UTF-8 的 LRC。
// 请求里带 lrcx=0 时上游回的是纯文本行（无逐字标记）；
// 若某天又回了逐字版本，这里会把 <起始,时长> 标记剥掉，仍得到可播的 LRC。
func ParseLyricPayload(body []byte) (string, error) {
	if len(body) == 0 {
		return "", errors.New("kw: 空响应")
	}
	switch {
	case bytes.HasPrefix(body, []byte("TP=none")):
		return "", ErrNoLyric
	case !bytes.HasPrefix(body, []byte("TP=content")):
		return "", fmt.Errorf("kw: 歌词响应头非法: %.32q", body)
	}

	sep := bytes.Index(body, []byte("\r\n\r\n"))
	if sep < 0 {
		return "", errors.New("kw: 歌词响应缺少分节")
	}
	zr, err := zlib.NewReader(bytes.NewReader(body[sep+4:]))
	if err != nil {
		return "", fmt.Errorf("kw: 歌词载荷不是 zlib: %w", err)
	}
	defer zr.Close()
	packed, err := io.ReadAll(io.LimitReader(zr, maxBody))
	if err != nil {
		return "", fmt.Errorf("kw: 解压歌词失败: %w", err)
	}

	raw, err := decodeLyricB64(strings.TrimSpace(string(packed)))
	if err != nil {
		return "", err
	}
	for i := range raw {
		raw[i] ^= lyricXorKey[i%len(lyricXorKey)]
	}

	lrc := lyricWordTag.ReplaceAllString(string(raw), "")
	lrc = strings.ReplaceAll(lrc, "\r\n", "\n")
	lrc = strings.TrimSpace(lrc)
	if lrc == "" {
		return "", ErrNoLyric
	}
	if !lyricTimeTag.MatchString(lrc) {
		return "", fmt.Errorf("kw: 歌词里没有时间标签: %.32q", lrc)
	}
	return lrc + "\n", nil
}

// decodeLyricB64 解 Base64，兼容上游偶发省略尾部填充的情况。
func decodeLyricB64(s string) ([]byte, error) {
	if s == "" {
		return nil, errors.New("kw: 歌词载荷为空")
	}
	if raw, err := base64.StdEncoding.DecodeString(s); err == nil {
		return raw, nil
	}
	raw, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(s, "="))
	if err != nil {
		return nil, fmt.Errorf("kw: 歌词 Base64 解码失败: %w", err)
	}
	return raw, nil
}

func pickString(m map[string]any, keys []string) string {
	for _, k := range keys {
		if v, ok := m[k].(string); ok {
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func toInt(v any) (int64, bool) {
	switch t := v.(type) {
	case float64:
		return int64(t), true
	case int:
		return int64(t), true
	case int64:
		return t, true
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n, true
		}
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func bytesTrimSpace(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}

// ParseAntiURL 解析 antiserver 的纯文本响应。
//
// 该接口在 response=url 时直接返回裸链接（可能带换行），不需要任何解密。
// 少数实现会返回 JSON，这里顺带兼容。
func ParseAntiURL(body []byte) (string, error) {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "", errors.New("kw: 空响应")
	}
	if strings.HasPrefix(s, "{") {
		if res, err := ParseConvertURL(body); err == nil {
			return res.URL, nil
		}
	}
	if !strings.HasPrefix(s, "http") {
		return "", fmt.Errorf("kw: 响应不是链接: %.80q", s)
	}
	// 去掉可能追加的额外内容
	if i := strings.IndexAny(s, "\r\n"); i > 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s), nil
}
