package lrcutil

import "strings"

// NRC 是网易云的逐字歌词格式。
//
// 明文：每行 [行起,行时](绝对词起,词时[,额外])文字…，词的时间是绝对时间。
// 这里统一归一化成相对行首。

// ParseNRC 解析 NRC 文本。
func ParseNRC(text string) (*Kara, error) {
	return parseKara(text, parseNRCLine)
}

func parseNRCLine(line string, k *Kara) (bool, error) {
	start, dur, rem, ok := parseHeader(line)
	if !ok {
		return false, nil
	}
	ln := KLine{Start: start, Dur: dur}
	for len(rem) > 0 && rem[0] == '(' {
		absStart, wd, _, rest, ok := scanGroup(rem, '(', ')')
		if !ok {
			break
		}
		next := strings.IndexByte(rest, '(')
		var text string
		if next < 0 {
			text, rem = rest, ""
		} else {
			text, rem = rest[:next], rest[next:]
		}
		if text == "" {
			continue
		}
		ln.Words = append(ln.Words, Word{Start: absStart - start, Dur: wd, Text: text})
	}
	if len(ln.Words) > 0 {
		k.Lines = append(k.Lines, ln)
	}
	return true, nil
}
