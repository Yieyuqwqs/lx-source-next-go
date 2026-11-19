package qq

// QQ 音乐客户端签名（社区称 zzb sign）。
//
// 算法来源：官方客户端脚本的公开实现，实测有效（免费曲目因此能匿名取到 purl）。
// 步骤：
//   1. 请求体做 MD5，取大写十六进制（32 位）；
//   2. 按固定下标抽取两段 8 字符（v / c）；
//   3. 16 组「两个 hex 位 → 一字节」再与固定掩码异或，得到 16 字节；
//   4. 用自定义字母表做类 base64 编码（n）；
//   5. 拼 "zzb" + v + n + c，转小写并去掉 '/' 和 '+'。

import (
	"crypto/md5"
	"encoding/hex"
	"strings"
)

// 固定抽取下标与掩码。
var (
	signVIndex = []int{21, 4, 9, 26, 16, 20, 27, 30}
	signCIndex = []int{18, 11, 3, 2, 1, 7, 6, 25}
	signMask   = []int{212, 45, 80, 68, 195, 163, 163, 203, 157, 220, 254, 91, 204, 79, 104, 6}
	// signAlphabet 是自定义字母表（末尾多一个 '=' 占位）。
	signAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/="
	// hexTable 是十六进制字符 → 数值。
	hexTable = map[byte]int{
		'0': 0, '1': 1, '2': 2, '3': 3, '4': 4, '5': 5, '6': 6, '7': 7,
		'8': 8, '9': 9, 'A': 10, 'B': 11, 'C': 12, 'D': 13, 'E': 14, 'F': 15,
	}
)

// pickChars 按固定下标抽字符。
func pickChars(s string, idx []int) string {
	out := make([]byte, 0, len(idx))
	for _, i := range idx {
		if i < len(s) {
			out = append(out, s[i])
		}
	}
	return string(out)
}

// signBytes 把 16 组 hex 位转成字节并与掩码异或。
func signBytes(s string) []int {
	out := make([]int, 0, len(signMask))
	j := 0
	for i := 0; i+1 < len(s); i += 2 {
		one := hexTable[s[i]]
		two := hexTable[s[i+1]]
		if j >= len(signMask) {
			break
		}
		out = append(out, (one*16^two)^signMask[j])
		j++
	}
	return out
}

// signTriple 把 3 字节编码成 4 个字母表下标。
func signTriple(a, b, c int) []int {
	if b != 0 && c != 0 {
		return []int{a >> 2, ((a & 3) << 4) | (b >> 4), ((b & 15) << 2) | (c >> 6), c & 63}
	}
	return []int{a >> 2, (a & 3) << 4}
}

// signEncode 用自定义字母表编码。
func signEncode(ls []int) string {
	var b strings.Builder
	for i := 0; i < len(ls); i += 3 {
		if i < len(ls)-2 {
			for _, v := range signTriple(ls[i], ls[i+1], ls[i+2]) {
				b.WriteByte(signAlphabet[v])
			}
			continue
		}
		for _, v := range signTriple(ls[i], 0, 0) {
			b.WriteByte(signAlphabet[v])
		}
	}
	return b.String()
}

// Sign 计算 musics.fcg 的 sign 参数。
func Sign(body []byte) string {
	sum := md5.Sum(body)
	up := strings.ToUpper(hex.EncodeToString(sum[:]))
	res := "zzb" + pickChars(up, signVIndex) + signEncode(signBytes(up)) + pickChars(up, signCIndex)
	res = strings.ToLower(res)
	res = strings.ReplaceAll(res, "/", "")
	res = strings.ReplaceAll(res, "+", "")
	return res
}
