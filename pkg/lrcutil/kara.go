package lrcutil

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// Word 是一个逐字片段。Start 相对所在行的起始时间，单位毫秒。
type Word struct {
	Start int64
	Dur   int64
	Text  string
}

// KLine 是一行逐字歌词。
type KLine struct {
	Start int64
	Dur   int64
	Words []Word
}

// Kara 是一份逐字（卡拉 OK）歌词。
//
// 各平台的原始表示互不相同（见 krc.go / qrc.go / nrc），
// 统一归一化成这个结构后再向外序列化。
type Kara struct {
	Meta  map[string]string
	Lines []KLine
}

// ErrFormat 表示歌词内容不符合预期格式。
var ErrFormat = errors.New("lrcutil: 歌词格式不符合预期")

// NewKara 返回一个空的逐字歌词。
func NewKara() *Kara {
	return &Kara{Meta: make(map[string]string)}
}

// Text 返回该行的纯文本。
func (l KLine) Text() string {
	var b strings.Builder
	for _, w := range l.Words {
		b.WriteString(w.Text)
	}
	return b.String()
}

// End 返回该行的结束时间（绝对毫秒）。
func (l KLine) End() int64 {
	if l.Dur > 0 {
		return l.Start + l.Dur
	}
	if n := len(l.Words); n > 0 {
		last := l.Words[n-1]
		return l.Start + last.Start + last.Dur
	}
	return l.Start
}

// ToLrcx 序列化为 LX 逐字歌词格式：
//
//	[分:秒.毫秒]<相对起始,时长>文字
//	例：[00:00.000]<0,36>测<36,36>试
func (k *Kara) ToLrcx() string {
	var b strings.Builder
	writeKaraMeta(&b, k.Meta)
	for _, ln := range k.Lines {
		b.WriteByte('[')
		b.WriteString(stampMsec(ln.Start))
		b.WriteByte(']')
		for _, w := range ln.Words {
			fmt.Fprintf(&b, "<%d,%d>%s", w.Start, w.Dur, w.Text)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

// ParseLrcx 解析 LX 逐字歌词格式。
func ParseLrcx(src string) (*Kara, error) {
	return parseKara(src, parseLrcxLine)
}

// ToLRC 降级为普通 LRC（丢弃逐字信息）。
func (k *Kara) ToLRC() string {
	lines := make([]Line, 0, len(k.Lines))
	for _, ln := range k.Lines {
		lines = append(lines, Line{Time: ln.Start, Text: ln.Text()})
	}
	var b strings.Builder
	writeKaraMeta(&b, k.Meta)
	b.WriteString(Format(lines))
	return b.String()
}

// stampMsec 输出 [分:秒.毫秒]，毫秒固定三位。
func stampMsec(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	return fmt.Sprintf("%02d:%02d.%03d", ms/60000, (ms%60000)/1000, ms%1000)
}

func writeKaraMeta(b *strings.Builder, meta map[string]string) {
	if len(meta) == 0 {
		return
	}
	// 固定顺序，保证输出稳定可测
	for _, k := range []string{"ti", "ar", "al", "by"} {
		if v, ok := meta[k]; ok && v != "" {
			fmt.Fprintf(b, "[%s:%s]\n", k, v)
		}
	}
	var rest []string
	for k := range meta {
		switch k {
		case "ti", "ar", "al", "by", "offset":
		default:
			rest = append(rest, k)
		}
	}
	sortStrings(rest)
	for _, k := range rest {
		if meta[k] != "" {
			fmt.Fprintf(b, "[%s:%s]\n", k, meta[k])
		}
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// lineParser 解析单行，返回 false 表示该行不是歌词行（可能是标签或空行）。
type lineParser func(line string, k *Kara) (handled bool, err error)

func parseKara(src string, p lineParser) (*Kara, error) {
	k := NewKara()
	for _, raw := range strings.Split(src, "\n") {
		line := strings.TrimSpace(strings.TrimSuffix(raw, "\r"))
		if line == "" {
			continue
		}
		handled, err := p(line, k)
		if err != nil {
			return nil, err
		}
		if !handled {
			parseTag(line, k.Meta)
		}
	}
	if len(k.Lines) == 0 {
		return nil, ErrFormat
	}
	return k, nil
}

// parseTag 解析形如 [ti:标题] 的标签行。
func parseTag(line string, meta map[string]string) bool {
	if len(line) < 4 || line[0] != '[' || line[len(line)-1] != ']' {
		return false
	}
	inner := line[1 : len(line)-1]
	k, v, ok := strings.Cut(inner, ":")
	if !ok {
		return false
	}
	k = strings.ToLower(strings.TrimSpace(k))
	if k == "" {
		return false
	}
	if _, dup := meta[k]; !dup {
		meta[k] = strings.TrimSpace(v)
	}
	return true
}

// parseHeader 解析行首的 [a,b]，返回两个整数与剩余部分。
func parseHeader(line string) (a, b int64, rest string, ok bool) {
	if len(line) < 2 || line[0] != '[' {
		return
	}
	end := strings.IndexByte(line, ']')
	if end < 0 {
		return
	}
	one, two, found := strings.Cut(line[1:end], ",")
	if !found {
		return
	}
	a, ok = scanInt(strings.TrimSpace(one))
	if !ok {
		return
	}
	b, ok = scanInt(strings.TrimSpace(two))
	if !ok {
		return
	}
	return a, b, line[end+1:], true
}

func scanInt(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// splitTimes 解析 "<a,b,c>" 或 "(a,b,c)" 形式的分组，返回三个整数和剩余部分。
// 允许省略第三个字段。
func scanGroup(s string, open, close byte) (a, b, c int64, rest string, ok bool) {
	if len(s) == 0 || s[0] != open {
		return
	}
	end := strings.IndexByte(s, close)
	if end < 0 {
		return
	}
	fields := strings.Split(s[1:end], ",")
	if len(fields) < 2 || len(fields) > 3 {
		return
	}
	a, ok = scanInt(strings.TrimSpace(fields[0]))
	if !ok {
		return
	}
	b, ok = scanInt(strings.TrimSpace(fields[1]))
	if !ok {
		return
	}
	if len(fields) == 3 {
		c, ok = scanInt(strings.TrimSpace(fields[2]))
		if !ok {
			return
		}
	}
	return a, b, c, s[end+1:], true
}

// parseLrcxLine 解析 [分:秒.毫秒]<相对起,时长>文字...
func parseLrcxLine(line string, k *Kara) (bool, error) {
	if len(line) < 2 || line[0] != '[' {
		return false, nil
	}
	end := strings.IndexByte(line, ']')
	if end < 0 {
		return false, nil
	}
	head := line[1:end]
	if strings.ContainsRune(head, ',') {
		return false, nil // 那是 krc/qrc 形状，不是 lrcx
	}
	start, ok := parseStamp(head)
	if !ok {
		return false, nil
	}
	rem := line[end+1:]
	var ln KLine
	ln.Start = start
	for len(rem) > 0 {
		if rem[0] != '<' {
			return false, nil
		}
		ws, wd, _, rest, ok := scanGroup(rem, '<', '>')
		if !ok {
			return false, nil
		}
		// 文字一直取到下一个 '<'
		next := strings.IndexByte(rest, '<')
		var text string
		if next < 0 {
			text, rem = rest, ""
		} else {
			text, rem = rest[:next], rest[next:]
		}
		if text == "" {
			continue
		}
		ln.Words = append(ln.Words, Word{Start: ws, Dur: wd, Text: text})
	}
	ln.Dur = ln.End() - ln.Start
	if len(ln.Words) > 0 {
		k.Lines = append(k.Lines, ln)
	}
	return true, nil
}
