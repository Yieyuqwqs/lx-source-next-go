package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lxsrcs/lx-source-next/pkg/config"
)

// httptestNewRequest 带上自定义请求头。
func httptestNewRequest(path string, hdr map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req
}

// serve 直接把请求喂给装配好的 App。
func serve(app *App, req *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	app.Handler().ServeHTTP(rec, req)
	return rec
}

func newScriptApp(t *testing.T, raw string, opt Options) *App {
	t.Helper()
	if raw == "" {
		raw = "[Apis]\nWebLog = false\n"
	}
	d, err := config.Parse([]byte(raw))
	if err != nil {
		t.Fatalf("解析配置: %v", err)
	}
	holder := config.NewHolder(&config.Snapshot{Dyn: d, LoadedAt: time.Now(), Version: 1})
	return New(Deps{Config: holder, Options: opt})
}

func TestLXScriptServed(t *testing.T) {
	app := newScriptApp(t, "", Options{})
	rec := get(t, app, "/api/v0/lx-script.js")

	if rec.Code != http.StatusOK {
		t.Fatalf("状态码 = %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Content-Type = %q", ct)
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-store" {
		t.Errorf("Cache-Control = %q，脚本内容随请求变化不该被缓存", cc)
	}

	body := rec.Body.String()
	// 脚本必须声明了 kw 源
	if !strings.Contains(body, "kw:") {
		t.Error("脚本里缺少 kw 源声明")
	}
	// 地址必须被替换成本次请求的地址
	if !strings.Contains(body, "http://example.com/api/v0") {
		t.Errorf("API_ADDR 未正确注入")
	}
	// 未启用密钥时不应带 key
	if !strings.Contains(body, "const API_KEY = ''") {
		t.Errorf("未启用密钥时 API_KEY 应为空")
	}
	// 五个平台都必须声明成歌词源（v0.1.9 起酷我也有了）
	for _, pt := range []string{"kw", "kg", "mg", "tx", "wy"} {
		if !strings.Contains(body, pt+": true") {
			t.Errorf("脚本里缺少歌词源 %s", pt)
		}
	}
	// 占位符不应残留
	for _, ph := range lxScriptPlaceholders {
		if strings.Contains(body, ph) {
			t.Errorf("占位符 %s 未被替换", ph)
		}
	}
}

func TestLXScriptAlias(t *testing.T) {
	app := newScriptApp(t, "", Options{})
	rec := get(t, app, "/api/v0/script")
	if rec.Code != http.StatusOK {
		t.Fatalf("别名 /api/v0/script 状态码 = %d", rec.Code)
	}
}

func TestLXScriptInjectsKey(t *testing.T) {
	app := newScriptApp(t, "", Options{})
	rec := get(t, app, "/api/v0/lx-script.js?key=my-secret")
	if !strings.Contains(rec.Body.String(), "const API_KEY = 'my-secret'") {
		t.Error("查询参数密钥未注入")
	}
}

func TestLXScriptKeyEscaping(t *testing.T) {
	app := newScriptApp(t, "", Options{})
	// 恶意 key 试图闭合字符串并注入代码
	malicious := "%27%29%3Balert%281%29%3B%2F%2F"
	rec := get(t, app, "/api/v0/lx-script.js?key="+malicious)
	body := rec.Body.String()

	if strings.Contains(body, "');alert(1);//") {
		t.Fatal("密钥未转义，存在 JS 注入")
	}
	// 注入点应被转义成 \x27 形式
	if !strings.Contains(body, `\x27`) {
		t.Errorf("单引号未被转义，body 片段：%s", snippet(body, "API_KEY"))
	}
}

func TestLXScriptRawDownload(t *testing.T) {
	app := newScriptApp(t, "", Options{})
	rec := get(t, app, "/api/v0/lx-script.js?raw")
	if cd := rec.Header().Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("raw 应触发下载，Content-Disposition = %q", cd)
	}
}

func TestLXScriptTrustProxy(t *testing.T) {
	app := newScriptApp(t, "", Options{TrustProxy: true})

	req := httptestNewRequest("/api/v0/lx-script.js", map[string]string{
		"X-Forwarded-Proto":  "https",
		"X-Forwarded-Host":   "music.example.com",
		"X-Forwarded-Prefix": "/lxs",
	})
	rec := serve(app, req)
	if !strings.Contains(rec.Body.String(), "https://music.example.com/lxs/api/v0") {
		t.Errorf("反向代理地址未生效:%s", snippet(rec.Body.String(), "API_ADDR"))
	}

	// 不信任代理时忽略伪造头
	plain := newScriptApp(t, "", Options{})
	rec = serve(plain, req)
	if strings.Contains(rec.Body.String(), "music.example.com") {
		t.Error("未信任代理时不应采信 X-Forwarded-*")
	}
}

func TestLXScriptAutoKey(t *testing.T) {
	raw := "[Auth.ApiPass]\nEnable = true\nAuto = true\nValue = 'cfg-key'\n[Apis]\nWebLog = false\n"
	app := newScriptApp(t, raw, Options{})
	rec := get(t, app, "/api/v0/lx-script.js")
	if !strings.Contains(rec.Body.String(), "const API_KEY = 'cfg-key'") {
		t.Error("Auto 开启时应注入配置里的密钥")
	}

	// Auto 关闭时不注入
	raw = "[Auth.ApiPass]\nEnable = true\nAuto = false\nValue = 'cfg-key'\n[Apis]\nWebLog = false\n"
	app = newScriptApp(t, raw, Options{})
	rec = get(t, app, "/api/v0/lx-script.js")
	if strings.Contains(rec.Body.String(), "cfg-key") {
		t.Error("Auto 关闭时不应注入密钥")
	}
}

func TestJsString(t *testing.T) {
	cases := map[string]string{
		"":            "",
		"abc":         "abc",
		"a'b":         `a\x27b`,
		"a\"b":        `a\x22b`,
		"a\\b":        `a\\b`,
		"line\nbreak": `line\nbreak`,
	}
	for in, want := range cases {
		if got := jsString(in); got != want {
			t.Errorf("jsString(%q) = %q, want %q", in, got, want)
		}
	}
}

func snippet(body, token string) string {
	i := strings.Index(body, token)
	if i < 0 {
		return "(未找到 " + token + ")"
	}
	end := i + 80
	if end > len(body) {
		end = len(body)
	}
	return body[i:end]
}
