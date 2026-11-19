package qq

import "testing"

// TestSign 用固定请求体验证签名（黄金值来自已实测可用的实现）。
func TestSign(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{`{"a":1}`, "zzb6bd64f6db0hlgk5xxulcfggjbyma3gc4c6b6c3"},
		{`{"comm":{"ct":"19"}}`, "zzbaefe6ffapou5ftxvqb37vgtnykahqq666c0934"},
	}
	for _, c := range cases {
		if got := Sign([]byte(c.body)); got != c.want {
			t.Errorf("Sign(%s) = %q, want %q", c.body, got, c.want)
		}
	}
	// 签名只含小写字母数字（'/' 与 '+' 会被去掉）
	if got := Sign([]byte(`{"x":2}`)); len(got) == 0 || got[:3] != "zzb" {
		t.Errorf("签名前缀不对: %q", got)
	}
}

// TestLadderFrom 校验按请求档位降档。
func TestLadderFrom(t *testing.T) {
	if got := ladderFrom("flac24bit"); len(got) != 4 || got[0].prefix != "RS01" {
		t.Errorf("flac24bit 应从 RS01 开始: %+v", got)
	}
	if got := ladderFrom("320k"); len(got) != 2 || got[0].prefix != "M800" {
		t.Errorf("320k 应只剩 M800/M500: %+v", got)
	}
	if got := ladderFrom("看不懂"); len(got) != 1 || got[0].prefix != "M500" {
		t.Errorf("未知音质应回落到 128k: %+v", got)
	}
}
