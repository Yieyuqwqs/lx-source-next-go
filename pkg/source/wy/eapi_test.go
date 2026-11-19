package wy

import (
	"strings"
	"testing"
)

// 黄金值由独立实现算出（Node.js 的 aes-128-ecb + md5），
// 用来校验算法、填充与编码是否与公开实现一致。
const eapiGolden = "FA90B329E9614F79E79598F37DC2EDB430F8378D2A2796338F0BFDEAEF824A22975CDA9D96D79E6DC4A59218CDB8199FE42FBA647FBC921E93931904946F12F96AC448186463D7DEDFC2FE055C22224E009BDE371170404A117AF58CC6493039870B9E718CC604C9C3190067887FC276672190AD54C967EFC8886DA405DEFCBD"

func TestEapiParamsGolden(t *testing.T) {
	got, err := EapiParams("/api/song/enhance/player/url", urlPayload{
		IDs: "[347230]",
		Br:  999000,
	})
	if err != nil {
		t.Fatalf("EapiParams: %v", err)
	}
	if got != eapiGolden {
		t.Errorf("eapi 参数与黄金值不符\n got: %s\nwant: %s", got, eapiGolden)
	}
	if got != strings.ToUpper(got) {
		t.Error("eapi 参数必须是全大写十六进制")
	}
}

func TestEapiParamsEmptyPath(t *testing.T) {
	if _, err := EapiParams("", urlPayload{}); err == nil {
		t.Error("空路径应报错")
	}
}

func TestBitrateFor(t *testing.T) {
	cases := map[string]struct {
		br   int
		qual string
	}{
		"128k":     {128000, "128k"},
		"320k":     {320000, "320k"},
		"flac":     {999000, "flac"},
		"flac24bit": {1999000, "flac24bit"},
		"hires":    {1999000, "flac24bit"}, // 别名
		"nonsense": {128000, "128k"},       // 未知回落
	}
	for in, want := range cases {
		br, q := bitrateFor(in)
		if br != want.br || q != want.qual {
			t.Errorf("bitrateFor(%q) = %d,%q want %d,%q", in, br, q, want.br, want.qual)
		}
	}
}

func TestParsePlayerURL(t *testing.T) {
	ok := `{"data":[{"id":347230,"url":"http://m8.music.126.net/a.mp3","br":320000,"code":200}]}`
	got, err := ParsePlayerURL([]byte(ok))
	if err != nil {
		t.Fatalf("ParsePlayerURL: %v", err)
	}
	if got != "http://m8.music.126.net/a.mp3" {
		t.Errorf("url = %q", got)
	}

	// VIP / 无版权：url 为 null
	_, err = ParsePlayerURL([]byte(`{"data":[{"id":1,"url":null,"br":0,"code":404}]}`))
	if err == nil || !strings.Contains(err.Error(), "会员") {
		t.Errorf("应说明原因，实际 %v", err)
	}
	if _, err := ParsePlayerURL([]byte(`not json`)); err == nil {
		t.Error("非 JSON 应报错")
	}
	if _, err := ParsePlayerURL([]byte(`{"data":[]}`)); err == nil {
		t.Error("空 data 应报错")
	}
}
