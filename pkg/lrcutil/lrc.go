// Package lrcutil 提供歌词解析与格式转换。
//
// 设计约束（对应重构要求）：
//   - 全部是纯函数：无网络 IO、无全局状态、可并发调用；
//   - 只依赖标准库，不引入任何第三方包；
//   - 时间统一用毫秒 int64。
package lrcutil

import (
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Line 是一行歌词，Time 为起始时间（毫秒）。
type Line struct {
	Time int64
	Text string
}

// Lyric 是一份解析后的歌词。
type Lyric struct {
	// Meta 保存 [ti:]、[ar:]、[offset:] 等标签，键为小写标签名。
	Meta map[string]string
	// Lines 已按 Time 升序稳定排序。
	Lines []Line
}

// ErrSyntax 表示输入无法解析为歌词。
var ErrSyntax = errors.New("lrcutil: 歌词格式错误")

// Parse 解析标准 LRC 文本。
//
// 支持的写法：
//
//	[mm:ss]        [mm:ss.xx]     [mm:ss.xxx]
//	[hh:mm:ss.xx]
//	[mm:ss:xx]     网易云偶发的双冒号形式，末段按百分秒处理
//
// 一行可带多个时间戳，会展开成多行。
// [offset:] 标签只记录到 Meta，不会自动应用，见 Offset。
func Parse(src string) (*Lyric, error) {
	l := &Lyric{Meta: make(map[string]string)}
	for _, raw := range strings.Split(src, "\n") {
		l.parseLine(strings.TrimSuffix(raw, "\r"))
	}
	if len(l.Lines) == 0 && len(l.Meta) == 0 {
		return nil, ErrSyntax
	}
	sort.SliceStable(l.Lines, func(i, j int) bool {
		return l.Lines[i].Time < l.Lines[j].Time
	})
	return l, nil
}

func (l *Lyric) parseLine(line string) {
	rest := line
	var stamps []int64
	for strings.HasPrefix(rest, "[") {
		end := strings.IndexByte(rest, ']')
		if end < 0 {
			return
		}
		inner := rest[1:end]
		rest = rest[end+1:]
		if t, ok := parseStamp(inner); ok {
			stamps = append(stamps, t)
			continue
		}
		if k, v, ok := strings.Cut(inner, ":"); ok {
			k = strings.ToLower(strings.TrimSpace(k))
			if k != "" {
				if _, dup := l.Meta[k]; !dup {
					l.Meta[k] = strings.TrimSpace(v)
				}
			}
		}
	}
	text := strings.TrimSpace(rest)
	if text == "" || len(stamps) == 0 {
		return
	}
	for _, t := range stamps {
		l.Lines = append(l.Lines, Line{Time: t, Text: text})
	}
}

// Format 把歌词行序列化为标准 LRC，百分秒格式。
func Format(lines []Line) string {
	var b strings.Builder
	for _, ln := range lines {
		b.WriteByte('[')
		b.WriteString(formatStamp(ln.Time))
		b.WriteString("] ")
		b.WriteString(ln.Text)
		b.WriteByte('\n')
	}
	return b.String()
}

// Offset 返回 [offset:] 标签的值（毫秒），缺失或非法时返回 0。
//
// Parse 不会自动应用它：offset 的符号约定在各实现间并不一致，
// 交给调用方决定方向，避免猜错。用 Shift 施加偏移。
func (l *Lyric) Offset() int64 {
	if l == nil {
		return 0
	}
	v, ok := l.Meta["offset"]
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// Shift 返回把每个时间戳都加上 d 毫秒的新切片，负数会被截到 0。
func Shift(lines []Line, d int64) []Line {
	out := make([]Line, len(lines))
	for i, ln := range lines {
		t := ln.Time + d
		if t < 0 {
			t = 0
		}
		out[i] = Line{Time: t, Text: ln.Text}
	}
	return out
}

func formatStamp(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%02d:%02d.%02d", ms/60000, (ms%60000)/1000, (ms%1000)/10)
}

func parseStamp(s string) (int64, bool) {
	s = strings.TrimSpace(s)
	parts := strings.Split(s, ":")
	switch len(parts) {
	case 2:
		mm, ok := atoi(parts[0])
		if !ok {
			return 0, false
		}
		sec, ms, ok := splitSec(parts[1])
		if !ok {
			return 0, false
		}
		return mm*60000 + sec*1000 + ms, true
	case 3:
		a, ok := atoi(parts[0])
		if !ok {
			return 0, false
		}
		b, ok := atoi(parts[1])
		if !ok {
			return 0, false
		}
		last := parts[2]
		if !strings.ContainsRune(last, '.') && len(last) <= 3 {
			// 网易云偶发的 [mm:ss:cc]：末段是百分秒
			ms, ok := fracToMs(last)
			if !ok {
				return 0, false
			}
			return a*60000 + b*1000 + ms, true
		}
		// [hh:mm:ss.xx]
		ss, ms, ok := splitSec(last)
		if !ok {
			return 0, false
		}
		return a*3600000 + b*60000 + ss*1000 + ms, true
	}
	return 0, false
}

func atoi(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// splitSec 拆分 "ss" 或 "ss.frac"。
func splitSec(s string) (sec, ms int64, ok bool) {
	if i := strings.IndexByte(s, '.'); i >= 0 {
		sec, ok = atoi(s[:i])
		if !ok {
			return 0, 0, false
		}
		ms, ok = fracToMs(s[i+1:])
		if !ok {
			return 0, 0, false
		}
		return sec, ms, true
	}
	sec, ok = atoi(s)
	if !ok {
		return 0, 0, false
	}
	return sec, 0, true
}

// fracToMs 把小数部分右补零成毫秒：5->500，05->50，505->505，5050->505。
func fracToMs(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	if len(s) > 3 {
		s = s[:3]
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, false
		}
	}
	for len(s) < 3 {
		s += "0"
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}
