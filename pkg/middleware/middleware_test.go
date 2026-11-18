package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func tag(s string, seen *[]string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			*seen = append(*seen, s)
			next.ServeHTTP(w, r)
		})
	}
}

func TestChainOrder(t *testing.T) {
	var seen []string
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, "handler")
	})
	c := Chain{tag("a", &seen), tag("b", &seen), tag("c", &seen)}
	h := c.Then(final)

	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))

	if got := strings.Join(seen, ","); got != "a,b,c,handler" {
		t.Errorf("执行顺序 = %q, want a,b,c,handler", got)
	}
}

func TestChainSkipsNil(t *testing.T) {
	var seen []string
	c := Chain{tag("a", &seen), nil, tag("b", &seen)}
	if c.Len() != 2 {
		t.Errorf("Len = %d, want 2", c.Len())
	}
	c.Then(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		seen = append(seen, "handler")
	})).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	if got := strings.Join(seen, ","); got != "a,b,handler" {
		t.Errorf("执行顺序 = %q", got)
	}
}

func TestChainAppendDoesNotMutate(t *testing.T) {
	var seen []string
	base := Chain{tag("a", &seen)}
	extended := base.Append(tag("b", &seen))
	if len(base) != 1 {
		t.Errorf("Append 修改了原链，长度 = %d", len(base))
	}
	if len(extended) != 2 {
		t.Errorf("新链长度 = %d, want 2", len(extended))
	}
}
