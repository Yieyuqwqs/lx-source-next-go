package wy

import (
	"crypto/aes"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// 本文件是纯函数：eapi 参数加密，不做任何 IO。

// eapiKey 是 eapi 的固定密钥（公开常量，来自 NeteaseCloudMusicApi / listen1）。
const eapiKey = "e82ckenh8dichen8"

// eapiSep 是 eapi 明文的固定分隔符。
const eapiSep = "-36cd479b6b5-"

// EapiParams 生成 eapi 接口需要的 params 参数。
//
// 算法（与 NeteaseCloudMusicApi 一致）：
//
//	message = "nobody" + url + "use" + json + "md5forencrypt"
//	digest  = md5(message)
//	plain   = url + "-36cd479b6b5-" + json + "-36cd479b6b5-" + digest
//	params  = upper(hex(AES-128-ECB(eapiKey, plain)))
func EapiParams(urlPath string, payload any) (string, error) {
	if urlPath == "" {
		return "", errors.New("wy: eapi 路径为空")
	}
	text, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("wy: 序列化 eapi 载荷失败: %w", err)
	}

	message := "nobody" + urlPath + "use" + string(text) + "md5forencrypt"
	sum := md5.Sum([]byte(message))
	digest := hex.EncodeToString(sum[:])

	plain := urlPath + eapiSep + string(text) + eapiSep + digest
	enc, err := aesECBEncrypt([]byte(eapiKey), []byte(plain))
	if err != nil {
		return "", err
	}
	return strings.ToUpper(hex.EncodeToString(enc)), nil
}

// aesECBEncrypt 做 AES-ECB + PKCS#7 填充。
func aesECBEncrypt(key, plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("wy: 创建 AES 失败: %w", err)
	}
	padded := pkcs7Pad(plain, block.BlockSize())
	out := make([]byte, len(padded))
	for i := 0; i < len(padded); i += block.BlockSize() {
		block.Encrypt(out[i:], padded[i:])
	}
	return out, nil
}

func pkcs7Pad(b []byte, size int) []byte {
	n := size - len(b)%size
	out := make([]byte, len(b)+n)
	copy(out, b)
	for i := len(b); i < len(out); i++ {
		out[i] = byte(n)
	}
	return out
}
