package kw

import (
	"crypto/des"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
)

// 本文件是纯函数：不做任何 IO，可完全用固定输入测试。

// ErrKey 表示密钥长度不合法。
var ErrKey = errors.New("kw: DES 密钥必须是 8 字节")

// DesEncrypt 用 DES-ECB 加密并做 PKCS#5 填充，结果 Base64 编码。
//
// 酷我的 mobi 接口把查询串加密后放进 q 参数，这里只负责算法本身；
// 密钥不写死在代码里，由配置注入。
func DesEncrypt(plain, key []byte) (string, error) {
	block, err := newBlock(key)
	if err != nil {
		return "", err
	}
	padded := pkcs5Pad(plain, block.BlockSize())
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += block.BlockSize() {
		block.Encrypt(out[i:], padded[i:])
	}
	return base64.StdEncoding.EncodeToString(out), nil
}

// DesDecrypt 是 DesEncrypt 的逆运算，便于自测与排查。
func DesDecrypt(b64 string, key []byte) ([]byte, error) {
	block, err := newBlock(key)
	if err != nil {
		return nil, err
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, fmt.Errorf("kw: Base64 解码失败: %w", err)
	}
	if len(raw) == 0 || len(raw)%block.BlockSize() != 0 {
		return nil, errors.New("kw: 密文长度不是块大小的整数倍")
	}
	out := make([]byte, len(raw))
	for i := 0; i < len(raw); i += block.BlockSize() {
		block.Decrypt(out[i:], raw[i:])
	}
	return pkcs5Unpad(out, block.BlockSize())
}

// BuildMobiQuery 把查询参数序列化、加密、Base64，得到 q 参数的值。
//
// 注意：酷我对参数顺序是否敏感尚未验证。
// url.Values.Encode 会按键名排序，若抓包显示顺序不同，
// 只需在这里改成有序拼接，其余代码不受影响。
func BuildMobiQuery(params url.Values, key []byte) (string, error) {
	if len(params) == 0 {
		return "", errors.New("kw: 查询参数为空")
	}
	return DesEncrypt([]byte(params.Encode()), key)
}

func newBlock(key []byte) (interface {
	BlockSize() int
	Encrypt(dst, src []byte)
	Decrypt(dst, src []byte)
}, error) {
	if len(key) != 8 {
		return nil, fmt.Errorf("%w，当前 %d 字节", ErrKey, len(key))
	}
	return des.NewCipher(key)
}

func pkcs5Pad(b []byte, size int) []byte {
	n := size - len(b)%size
	out := make([]byte, len(b)+n)
	copy(out, b)
	for i := len(b); i < len(out); i++ {
		out[i] = byte(n)
	}
	return out
}

func pkcs5Unpad(b []byte, size int) ([]byte, error) {
	if len(b) == 0 || len(b)%size != 0 {
		return nil, errors.New("kw: 明文长度非法")
	}
	n := int(b[len(b)-1])
	if n == 0 || n > size || n > len(b) {
		return nil, errors.New("kw: 填充字节非法")
	}
	for i := len(b) - n; i < len(b); i++ {
		if int(b[i]) != n {
			return nil, errors.New("kw: 填充不完整")
		}
	}
	return b[:len(b)-n], nil
}
