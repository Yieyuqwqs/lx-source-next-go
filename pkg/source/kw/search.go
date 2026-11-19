package kw

import (
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"

	"github.com/lxsrcs/lx-source-next/pkg/quality"
	"github.com/lxsrcs/lx-source-next/pkg/source"
)

// 本文件是纯函数为主：搜索响应解析不涉及 IO，可完全用固定报文测试。

// searchPath 是公开搜索接口的默认路径。
//
// 与取链不同，它挂在 search.kuwo.cn 上，所以单独用一个地址字段。
const defaultSearchHost = "http://search.kuwo.cn"

// 搜索响应里用到的字段名。
const (
	fID       = "DC_TARGETID"
	fName     = "SONGNAME"
	fNameAlt  = "NAME"
	fArtist   = "ARTIST"
	fAlbum    = "ALBUM"
	fDuration = "DURATION"
	fFormats  = "FORMATS"
)

// ParseSearch 解析 kuwo 的公开搜索响应。
//
// 该接口返回的不是合法 JSON：键和值都用单引号，形如
//
//	{'HIT':'3600','abslist':[{'DC_TARGETID':'474678847','ARTIST':'周杰伦'}]}
//
// 因此这里先把单引号归一成双引号再交给 encoding/json。
// 值为空时也可能是 ”，归一后就是 ""，JSON 仍可接受。
func ParseSearch(body []byte) (source.SearchResult, error) {
	raw := strings.TrimSpace(string(body))
	if raw == "" {
		return source.SearchResult{}, errors.New("kw: 空搜索响应")
	}

	var out source.SearchResult
	if n, ok := extractPseudoInt(raw, "TOTAL"); ok {
		out.Total = n
	}
	if n, ok := extractPseudoInt(raw, "HIT"); ok && out.Total == 0 {
		out.Total = n
	}

	// 真实响应里键是被引号包起来的：abslist:[{
	// 所以先定位 abslist，再取它之后的第一个 [。
	token := strings.Index(raw, "abslist")
	if token < 0 {
		// 有 total 但没结果列表，属于合法空结果
		if strings.Contains(raw, "TOTAL") {
			return out, nil
		}
		return source.SearchResult{}, errors.New("kw: 响应里没有 abslist")
	}
	rel := strings.IndexByte(raw[token:], '[')
	if rel < 0 {
		return source.SearchResult{}, errors.New("kw: abslist 后没有数组")
	}
	start := token + rel

	end := matchBracket(raw, start)
	if end < 0 {
		return source.SearchResult{}, errors.New("kw: abslist 未闭合")
	}

	arr := normalizeQuotes(raw[start : end+1])
	var entries []map[string]any
	if err := json.Unmarshal([]byte(arr), &entries); err != nil {
		return source.SearchResult{}, fmt.Errorf("kw: 解析结果列表失败: %w", err)
	}

	out.Items = make([]source.MusicInfo, 0, len(entries))
	for _, e := range entries {
		item := source.MusicInfo{
			ID:   str(e, fID),
			Name: firstStr(e, fName, fNameAlt),
		}
		if artist := str(e, fArtist); artist != "" {
			item.Ar = []source.ArtistInfo{{Name: artist}}
		}
		item.Al = source.AlbumInfo{Name: str(e, fAlbum)}
		if n, err := strconv.ParseUint(str(e, fDuration), 10, 32); err == nil {
			item.Dt = uint32(n)
		}
		item.File = parseFormats(str(e, fFormats))
		if item.ID == "" || item.Name == "" {
			continue
		}
		out.Items = append(out.Items, item)
	}
	if out.Total == 0 {
		out.Total = len(out.Items)
	}
	return out, nil
}

// parseFormats 把 'AAC48|MP3128|MP3H|OGG192' 映射成音质列表。
//
// 只认与我们规范音质能对上的项，其余忽略。
func parseFormats(s string) []source.FileInfo {
	if s == "" {
		return nil
	}
	seen := map[string]bool{}
	out := make([]source.FileInfo, 0, 4)
	add := func(q string) {
		if seen[q] {
			return
		}
		seen[q] = true
		out = append(out, source.FileInfo{Qual: q})
	}
	for _, f := range strings.Split(s, "|") {
		switch strings.ToUpper(strings.TrimSpace(f)) {
		case "MP3128", "MP3_128":
			add(quality.Q128)
		case "MP3H", "MP3320", "MP3_320":
			add(quality.Q320)
		}
	}
	return out
}

// normalizeQuotes 把伪 JSON 的单引号换成双引号。
//
// 已经出现过 \" 转义的场景不在本接口内，因此只需处理 \' 。
func normalizeQuotes(s string) string {
	s = strings.ReplaceAll(s, `\'`, `"`)
	return strings.ReplaceAll(s, `'`, `"`)
}

// matchBracket 从 open 位置开始找与之配对的收尾括号。
func matchBracket(s string, open int) int {
	if open >= len(s) || s[open] != '[' {
		return -1
	}
	depth := 0
	for i := open; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// extractPseudoInt 取形如 'TOTAL':'3600' 的数值。
func extractPseudoInt(s, key string) (int, bool) {
	for _, pat := range []string{`'` + key + `':'`, `"` + key + `":"`} {
		i := strings.Index(s, pat)
		if i < 0 {
			continue
		}
		rest := s[i+len(pat):]
		j := strings.IndexAny(rest, `'"`)
		if j < 0 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(rest[:j]))
		if err != nil {
			continue
		}
		return n, true
	}
	return 0, false
}

func str(m map[string]any, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	s, _ := v.(string)
	// 上游会把歌名里的空格等编码成实体。
	// &nbsp; 反转义后是 U+00A0（不换行空格），与普通空格不等价，
	// 会给搜索匹配和显示添乱，这里统一归一成普通空格。
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, " ", " ")
	return strings.TrimSpace(s)
}

func firstStr(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if s := str(m, k); s != "" {
			return s
		}
	}
	return ""
}

// searchURL 构造搜索请求地址（纯函数，便于测试）。
func (c *Client) searchURL(keyword string, page, size int) string {
	if page <= 0 {
		page = 1
	}
	if size <= 0 {
		size = 10
	}
	if size > 50 {
		size = 50
	}
	q := url.Values{}
	q.Set("all", keyword)
	q.Set("ft", "music")
	q.Set("itemset", "web_2013")
	q.Set("client", "kt")
	q.Set("pn", strconv.Itoa(page-1))
	q.Set("rn", strconv.Itoa(size))
	q.Set("rformat", "json")
	q.Set("encoding", "utf8")

	host := c.cfg.SearchHost
	if host == "" {
		host = defaultSearchHost
	}
	return strings.TrimRight(host, "/") + "/r.s?" + q.Encode()
}
