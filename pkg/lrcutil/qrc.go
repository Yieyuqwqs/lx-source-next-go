package lrcutil

import (
	"bytes"
	"crypto/des"
	"encoding/xml"
	"io"
	"strings"
)

// QRC 是 QQ 音乐的逐字歌词格式。
//
// 容器：可能以 0x98 0x25 开头表示新版，先用 qrcConvertCode 转回旧版；
// 旧版以 "[offset:" 开头时跳过 11 字节头部；剩余数据补零到 8 的倍数，
// 依次 DES 解密 / 加密 / 解密（密钥各取前 8 字节，ECB 无填充），最后 zlib 解压。
// 明文：XML，逐字内容在 Lyric_* 节点的 LyricContent 里，
// 行格式 [行起,行时]文字(绝对词起,词时)文字(…)…
var (
	qrcOffsetTag = []byte("[offset:")
	qrcKey1      = []byte("!@#)(NHLiuy*$%^&")
	qrcKey2      = []byte("123ZXC!@#)(*$%^&")
	qrcKey3      = []byte("!@#)(*$%^&abcDEF")
)

// qrcHeaderLen 是 "[offset:0]\n" 的长度。旧版 QRC 从这里开始才是密文。
const qrcHeaderLen = 11

// DecodeQRC 解密 QRC 二进制，返回明文 XML 文本。
func DecodeQRC(data []byte) (string, error) {
	if len(data) < 2 {
		return "", ErrFormat
	}
	buf := append([]byte(nil), data...)
	if buf[0] == 0x98 && buf[1] == 0x25 {
		qrcConvertNew(buf)
	}
	body := buf
	if bytes.HasPrefix(buf, qrcOffsetTag) {
		if len(buf) < qrcHeaderLen {
			return "", ErrFormat
		}
		body = buf[qrcHeaderLen:]
	}
	body = padTo8(body)
	if err := desECB(qrcKey1, body, false); err != nil {
		return "", err
	}
	if err := desECB(qrcKey2, body, true); err != nil {
		return "", err
	}
	if err := desECB(qrcKey3, body, false); err != nil {
		return "", err
	}
	out, err := inflate(body)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// ExtractQRCLyric 从 QRC 的 XML 里取出第一条逐字歌词正文。
// 兼容 LyricContent 作为属性与作为子节点两种写法。
func ExtractQRCLyric(doc string) (string, error) {
	dec := xml.NewDecoder(strings.NewReader(doc))
	inLyric := 0
	var attr, text string
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if strings.HasPrefix(t.Name.Local, "Lyric_") {
				for _, a := range t.Attr {
					if strings.EqualFold(a.Name.Local, "LyricContent") && a.Value != "" && attr == "" {
						attr = a.Value
					}
				}
			}
			if strings.EqualFold(t.Name.Local, "LyricContent") {
				inLyric++
			}
		case xml.EndElement:
			if strings.EqualFold(t.Name.Local, "LyricContent") && inLyric > 0 {
				inLyric--
			}
		case xml.CharData:
			if inLyric > 0 && text == "" {
				text = string(t)
			}
		}
	}
	if attr != "" {
		return attr, nil
	}
	if text != "" {
		return text, nil
	}
	return "", ErrFormat
}

// ParseQRCContent 解析 QRC 逐字歌词正文。
// 行： [行起,行时]文字(绝对词起,词时)文字(…)
// 词的时间是绝对时间，这里统一归一化成相对行首。
func ParseQRCContent(content string) (*Kara, error) {
	return parseKara(content, parseQRCLine)
}

func parseQRCLine(line string, k *Kara) (bool, error) {
	start, dur, rem, ok := parseHeader(line)
	if !ok {
		return false, nil
	}
	ln := KLine{Start: start, Dur: dur}
	for len(rem) > 0 {
		open := strings.IndexByte(rem, '(')
		if open < 0 {
			// 没有时间标记的尾巴，整体当作一个词
			if txt := strings.TrimSpace(rem); txt != "" {
				ln.Words = append(ln.Words, Word{Start: 0, Dur: 0, Text: txt})
			}
			break
		}
		text := rem[:open]
		absStart, wd, _, rest, ok := scanGroup(rem[open:], '(', ')')
		if !ok {
			break
		}
		rem = rest
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

// qrcConvertNew 把新版 QRC 就地转回旧版。
func qrcConvertNew(buf []byte) {
	for i := range buf {
		buf[i] ^= qrcConvertCode[(i*i+0x013c1b)%256]
	}
}

// padTo8 零填充到 8 的倍数。
func padTo8(b []byte) []byte {
	if n := len(b) % 8; n != 0 {
		out := make([]byte, len(b)+8-n)
		copy(out, b)
		return out
	}
	return b
}

// desECB 用 key 的前 8 字节做单 DES ECB 就地运算。
func desECB(key, data []byte, encrypt bool) error {
	if len(key) < 8 {
		return ErrFormat
	}
	block, err := des.NewCipher(key[:8])
	if err != nil {
		return err
	}
	for i := 0; i+block.BlockSize() <= len(data); i += block.BlockSize() {
		if encrypt {
			block.Encrypt(data[i:], data[i:])
		} else {
			block.Decrypt(data[i:], data[i:])
		}
	}
	return nil
}
