package lrcutil

import (
	"bytes"
	"compress/zlib"
	"html"
	"io"
	"strings"
)

// KRC 是酷狗的逐字歌词格式。
//
// 容器：前 4 字节魔数 "krc1"，其后整段用 krcKey 循环异或，再 zlib 压缩。
// 明文：每行 [行起,行时]<词起,词时,0>文字...，词起相对行首，文字为 HTML 转义。
var (
	krcTag = []byte("krc1")
	krcKey = [16]byte{64, 71, 97, 119, 94, 50, 116, 71, 81, 54, 49, 45, 206, 210, 110, 105}
)

// DecodeKRC 解密 KRC 二进制，返回明文字符串。
func DecodeKRC(data []byte) (string, error) {
	if len(data) < len(krcTag) || !bytes.Equal(data[:len(krcTag)], krcTag) {
		return "", ErrFormat
	}
	buf := append([]byte(nil), data[len(krcTag):]...)
	xorKey(buf, krcKey[:])
	out, err := inflate(buf)
	if err != nil {
		return "", err
	}
	return string(out), nil
}

// EncodeKRC 反向打包为 KRC 二进制。
func EncodeKRC(text string) ([]byte, error) {
	body, err := deflate([]byte(text))
	if err != nil {
		return nil, err
	}
	xorKey(body, krcKey[:])
	out := make([]byte, 0, len(krcTag)+len(body))
	out = append(out, krcTag...)
	return append(out, body...), nil
}

// ParseKRC 解析明文 KRC 文本。
func ParseKRC(text string) (*Kara, error) {
	return parseKara(text, parseKRCLine)
}

// ParseKRCBytes 解密并解析 KRC 二进制。
func ParseKRCBytes(data []byte) (*Kara, error) {
	text, err := DecodeKRC(data)
	if err != nil {
		return nil, err
	}
	return ParseKRC(text)
}

func parseKRCLine(line string, k *Kara) (bool, error) {
	start, dur, rem, ok := parseHeader(line)
	if !ok {
		return false, nil
	}
	ln := KLine{Start: start, Dur: dur}
	for len(rem) > 0 && rem[0] == '<' {
		ws, wd, _, rest, ok := scanGroup(rem, '<', '>')
		if !ok {
			break
		}
		next := strings.IndexByte(rest, '<')
		var text string
		if next < 0 {
			text, rem = rest, ""
		} else {
			text, rem = rest[:next], rest[next:]
		}
		text = html.UnescapeString(text)
		if text == "" {
			continue
		}
		ln.Words = append(ln.Words, Word{Start: ws, Dur: wd, Text: text})
	}
	if len(ln.Words) > 0 {
		k.Lines = append(k.Lines, ln)
	}
	return true, nil
}

func xorKey(buf, key []byte) {
	for i := range buf {
		buf[i] ^= key[i%len(key)]
	}
}

func inflate(b []byte) ([]byte, error) {
	zr, err := zlib.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	return io.ReadAll(zr)
}

func deflate(b []byte) ([]byte, error) {
	var out bytes.Buffer
	zw := zlib.NewWriter(&out)
	if _, err := zw.Write(b); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}
