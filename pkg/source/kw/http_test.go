package kw

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/lxsrcs/lx-source-next/pkg/quality"
)

// TestWithHTTPTestServer 用真实的 httptest 服务器闭合整条请求链路。
//
// 关键点：假服务器会**真的解密我们的 q 参数**并校验字段，
// 因此这个测试同时证明了
//  1. 请求确实发了出去（走真实 http.Client）；
//  2. 我们构造的 DES 密文是对端可解的；
//  3. 参数名与取值符合协议预期；
//  4. 响应解析正确。
func TestWithHTTPTestServer(t *testing.T) {
	var calls atomic.Int64

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)

		if got := r.URL.Query().Get("f"); got != "kuwo" {
			http.Error(w, "缺少 f=kuwo", http.StatusBadRequest)
			return
		}
		q := r.URL.Query().Get("q")
		if q == "" {
			http.Error(w, "缺少 q 参数", http.StatusBadRequest)
			return
		}
		plain, err := DesDecrypt(q, testKey)
		if err != nil {
			http.Error(w, "q 解密失败: "+err.Error(), http.StatusBadRequest)
			return
		}
		v, err := url.ParseQuery(string(plain))
		if err != nil {
			http.Error(w, "q 明文非法", http.StatusBadRequest)
			return
		}
		if v.Get("type") != "convert_url2" {
			http.Error(w, "type 不对", http.StatusBadRequest)
			return
		}
		rid, br := v.Get("rid"), v.Get("br")
		if rid == "" || br == "" {
			http.Error(w, "缺少 rid/br", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":200,"data":{"url":"https://kw.invalid/%s/%s.mp3","bitrate":"%s","duration":248}}`, rid, br, br)
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Key: testKey}, nil) // nil → 使用真实 http.Client

	li, err := c.URL(context.Background(), "456", "flac")
	if err != nil {
		t.Fatalf("URL: %v", err)
	}
	want := "https://kw.invalid/456/" + brFlac + ".mp3"
	if li.Link != want {
		t.Errorf("Link = %q, want %q", li.Link, want)
	}
	if li.Qual != quality.QFlac {
		t.Errorf("Qual = %q", li.Qual)
	}
	if calls.Load() != 1 {
		t.Errorf("上游被调用 %d 次", calls.Load())
	}
}

// TestWithHTTPTestServerUpstreamBreakage 覆盖上游异常时的错误传播。
func TestWithHTTPTestServerUpstreamBreakage(t *testing.T) {
	t.Run("HTTP 500", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "boom", http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := New(Config{BaseURL: srv.URL, Key: testKey}, nil)
		if _, err := c.URL(context.Background(), "1", "320k"); err == nil {
			t.Error("应报错")
		}
	})

	t.Run("返回非 JSON", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, "<html>502 Bad Gateway</html>")
		}))
		defer srv.Close()
		c := New(Config{BaseURL: srv.URL, Key: testKey}, nil)
		if _, err := c.URL(context.Background(), "1", "320k"); err == nil {
			t.Error("应报错")
		}
	})

	t.Run("上游业务错误", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			io.WriteString(w, `{"code":400,"msg":"签名错误"}`)
		}))
		defer srv.Close()
		c := New(Config{BaseURL: srv.URL, Key: testKey}, nil)
		_, err := c.URL(context.Background(), "1", "320k")
		if err == nil {
			t.Fatal("应报错")
		}
		if !strings.Contains(err.Error(), "签名错误") {
			t.Errorf("错误信息应包含上游 msg，实际 %v", err)
		}
	})
}

// TestWithHTTPTestServerLyric 同样闭合歌词链路。
func TestWithHTTPTestServerLyric(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		plain, err := DesDecrypt(r.URL.Query().Get("q"), testKey)
		if err != nil {
			http.Error(w, "q 解密失败", http.StatusBadRequest)
			return
		}
		v, _ := url.ParseQuery(string(plain))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"code":200,"data":{"lyric":"[00:01.00]歌曲 %s","tlyric":"[00:01.00]song %s"}}`, v.Get("rid"), v.Get("rid"))
	}))
	defer srv.Close()

	c := New(Config{BaseURL: srv.URL, Key: testKey, PathLyric: "/mobi.s"}, nil)
	li, err := c.Lyric(context.Background(), "789")
	if err != nil {
		t.Fatalf("Lyric: %v", err)
	}
	if !strings.Contains(li.Lyric, "789") {
		t.Errorf("lyric = %q", li.Lyric)
	}
	if li.TLyric == "" {
		t.Error("应带回翻译歌词")
	}
}
