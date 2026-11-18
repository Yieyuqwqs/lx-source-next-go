package kw

import (
	"encoding/json"
	"errors"
	"fmt"
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

// LyricResult 是解析出的歌词。
type LyricResult struct {
	// Lyric 是普通歌词文本。
	Lyric string
	// TLyric 是翻译歌词，可能为空。
	TLyric string
}

// ParseLyric 解析歌词响应。
//
// 已知形态：
//
//	{"code":200,"data":{"lyric":"[00:01.00]...","tlyric":"..."}}
//	{"code":200,"data":"[00:01.00]..."}
//
// 返回的文本一律原样透出，不在这里做格式转换——
// 格式转换属于 lrcutil 的职责。
func ParseLyric(body []byte) (LyricResult, error) {
	if len(bytesTrimSpace(body)) == 0 {
		return LyricResult{}, errors.New("kw: 空响应")
	}
	var top map[string]any
	if err := json.Unmarshal(body, &top); err != nil {
		return LyricResult{}, fmt.Errorf("kw: 响应不是 JSON: %w", err)
	}
	if code, ok := top["code"]; ok {
		if n, ok := toInt(code); ok && n != 0 && n != 200 {
			return LyricResult{}, fmt.Errorf("kw: 上游返回 code=%d", n)
		}
	}

	var out LyricResult
	switch data := top["data"].(type) {
	case string:
		out.Lyric = data
	case map[string]any:
		out.Lyric = pickString(data, []string{"lyric", "lrc", "content"})
		out.TLyric = pickString(data, []string{"tlyric", "trans", "translation"})
	}
	if out.Lyric == "" {
		out.Lyric = pickString(top, []string{"lyric", "lrc"})
	}
	if out.Lyric == "" {
		return LyricResult{}, ErrUnsupportedShape
	}
	return out, nil
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
