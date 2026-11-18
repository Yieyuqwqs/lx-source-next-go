package kw

import (
	"testing"

	"github.com/lxsrcs/lx-source-next/pkg/quality"
)

// searchFixture 复刻真实响应的形状：单引号伪 JSON + abslist。
const searchFixture = `{'ARTISTPIC':'','HIT':'3600','HITMODE':'song','TOTAL':'3600','PN':'0','RN':'5','SHOW':'5','abslist':[{'AARTIST':'','ALBUM':'魔杰座','ARTIST':'周杰伦','DC_TARGETID':'474678847','DC_TARGETTYPE':'music','DURATION':'210','FORMATS':'AAC48|MP3128|MP3H|OGG192','SONGNAME':'给我一首歌的时间'},{'ALBUM':'','ARTIST':'某人','DC_TARGETID':'349354644','DURATION':'200','FORMATS':'MP3128','SONGNAME':'另一首'}]}`

func TestParseSearch(t *testing.T) {
	res, err := ParseSearch([]byte(searchFixture))
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if res.Total != 3600 {
		t.Errorf("Total = %d, want 3600", res.Total)
	}
	if len(res.Items) != 2 {
		t.Fatalf("Items = %d, want 2", len(res.Items))
	}

	first := res.Items[0]
	if first.ID != "474678847" {
		t.Errorf("ID = %q", first.ID)
	}
	if first.Name != "给我一首歌的时间" {
		t.Errorf("Name = %q", first.Name)
	}
	if len(first.Ar) != 1 || first.Ar[0].Name != "周杰伦" {
		t.Errorf("Ar = %+v", first.Ar)
	}
	if first.Al.Name != "魔杰座" {
		t.Errorf("Al = %+v", first.Al)
	}
	if first.Dt != 210 {
		t.Errorf("Dt = %d", first.Dt)
	}
	// FORMATS 里 MP3128 与 MP3H 应各映射一档
	got := map[string]bool{}
	for _, f := range first.File {
		got[f.Qual] = true
	}
	if !got[quality.Q128] || !got[quality.Q320] {
		t.Errorf("File = %+v，应包含 128k 与 320k", first.File)
	}
}

func TestParseSearchEmptyResult(t *testing.T) {
	res, err := ParseSearch([]byte(`{'TOTAL':'0','abslist':[]}`))
	if err != nil {
		t.Fatalf("空结果不应报错: %v", err)
	}
	if len(res.Items) != 0 {
		t.Errorf("Items = %d", len(res.Items))
	}
}

func TestParseSearchErrors(t *testing.T) {
	for _, bad := range []string{"", "not a response", `{'abslist':[{'A':'b'}`} {
		if _, err := ParseSearch([]byte(bad)); err == nil {
			t.Errorf("%q 应报错", bad)
		}
	}
}

func TestSearchURL(t *testing.T) {
	c := New(Config{SearchHost: "https://search.invalid"}, nil)
	u := c.searchURL("周杰伦", 2, 20)
	for _, want := range []string{
		"https://search.invalid/r.s",
		"all=%E5%91%A8%E6%9D%B0%E4%BC%A6",
		"ft=music",
		"pn=1", // 页码从 0 开始
		"rn=20",
		"rformat=json",
	} {
		if !containsStr(u, want) {
			t.Errorf("URL 缺少 %q: %s", want, u)
		}
	}

	// 页码/页大小兜底
	u = c.searchURL("x", 0, 0)
	if !containsStr(u, "pn=0") || !containsStr(u, "rn=10") {
		t.Errorf("默认分页不对: %s", u)
	}
	// 页大小上限
	u = c.searchURL("x", 1, 999)
	if !containsStr(u, "rn=10") {
		t.Errorf("超限页大小应回落: %s", u)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}

func TestParseSearchUnescapesEntities(t *testing.T) {
	body := "{'TOTAL':'1','abslist':[{'DC_TARGETID':'1','SONGNAME':'\u82b1\u6d77&nbsp;(DJ\u7248)','ARTIST':'A&amp;B'}]}"
	res, err := ParseSearch([]byte(body))
	if err != nil {
		t.Fatalf("ParseSearch: %v", err)
	}
	if len(res.Items) != 1 {
		t.Fatalf("Items = %d", len(res.Items))
	}
	if got := res.Items[0].Name; got != "\u82b1\u6d77 (DJ\u7248)" {
		t.Errorf("Name = %q，&nbsp; 应被反转义", got)
	}
	if got := res.Items[0].Ar[0].Name; got != "A&B" {
		t.Errorf("Artist = %q，&amp; 应被反转义", got)
	}
}
