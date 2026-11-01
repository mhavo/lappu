package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/mhavo/lappu/server/internal/store"
)

const testToken = "0123456789abcdef0123456789abcdef"

type env struct {
	t       *testing.T
	h       http.Handler
	dataDir string
	quotes  string
}

func newEnv(t *testing.T, mod ...func(*Config)) *env {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	s, err := store.New(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Store:          s,
		Token:          testToken,
		MaxBodyBytes:   1024,
		TrustedProxies: []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")},
		QuotesFile:     filepath.Join(dir, "quotes.csv"),
		Static: fstest.MapFS{
			"index.html": {Data: []byte("<html>viewer</html>")},
			"style.css":  {Data: []byte("body{}")},
			"sub/a.js":   {Data: []byte("x")},
		},
	}
	for _, m := range mod {
		m(&cfg)
	}
	return &env{t: t, h: New(cfg), dataDir: dataDir, quotes: cfg.QuotesFile}
}

type req struct {
	method, target, body string
	header               map[string]string
}

func (e *env) do(r req) *httptest.ResponseRecorder {
	e.t.Helper()
	var body io.Reader
	if r.body != "" {
		body = strings.NewReader(r.body)
	}
	hr := httptest.NewRequest(r.method, r.target, body)
	for k, v := range r.header {
		hr.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, hr)
	return w
}

func auth(extra ...string) map[string]string {
	h := map[string]string{"Authorization": "Bearer " + testToken}
	for i := 0; i+1 < len(extra); i += 2 {
		h[extra[i]] = extra[i+1]
	}
	return h
}

func expect(t *testing.T, w *httptest.ResponseRecorder, code int, body string) {
	t.Helper()
	if w.Code != code || w.Body.String() != body {
		t.Fatalf("got %d %q, want %d %q", w.Code, w.Body.String(), code, body)
	}
}

func (e *env) create(content string) store.Share {
	e.t.Helper()
	w := e.do(req{method: "POST", target: "/share.php", body: `{"content":` + jsonString(content) + `}`, header: auth()})
	if w.Code != 200 {
		e.t.Fatalf("create: %d %s", w.Code, w.Body)
	}
	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		e.t.Errorf("create content-type %q", ct)
	}
	var s store.Share
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func jsonString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestPostGetRoundTrip(t *testing.T) {
	e := newEnv(t)
	s := e.create("# <script>alert(1)</script>")
	if s.ID == "" || len(s.Password) != 32 || s.Created == 0 {
		t.Fatalf("bad share %+v", s)
	}
	w := e.do(req{method: "GET", target: "/share.php?id=" + s.ID})
	expect(t, w, 200, "# <script>alert(1)</script>")
	if ct := w.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Errorf("content-type %q", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
}

func TestGetErrors(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do(req{method: "GET", target: "/share.php?id=abcdef12"}), 404, "Not found")
	expect(t, e.do(req{method: "GET", target: "/share.php?id=../../etc/passwd"}), 400, "Invalid id")
	expect(t, e.do(req{method: "GET", target: "/share.php"}), 400, "No id")
}

func TestMeta(t *testing.T) {
	e := newEnv(t)
	s := e.create("x")
	w := e.do(req{method: "GET", target: "/share.php?meta&id=" + s.ID})
	if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %s", w.Code, w.Header())
	}
	var m map[string]any
	json.Unmarshal(w.Body.Bytes(), &m)
	if m["id"] != s.ID || m["created"] != float64(s.Created) || len(m) != 2 {
		t.Errorf("meta = %v", m)
	}
	expectJSON := func(target string, code int, body string) {
		t.Helper()
		w := e.do(req{method: "GET", target: target})
		if w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%s: content-type %q", target, w.Header().Get("Content-Type"))
		}
		expect(t, w, code, body)
	}
	expectJSON("/share.php?meta&id=abcdef12", 404, `{"error":"Not found"}`)
	expectJSON("/share.php?meta", 400, `{"error":"No id"}`)
	expectJSON("/share.php?meta&id=a-b", 400, `{"error":"Invalid id"}`)
}

func TestPostErrors(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do(req{method: "POST", target: "/share.php", body: `{"content":"x"}`}), 401, "Invalid token")
	expect(t, e.do(req{method: "POST", target: "/share.php", body: `{"content":"x"}`,
		header: map[string]string{"Authorization": "Bearer wrong"}}), 401, "Invalid token")
	expect(t, e.do(req{method: "POST", target: "/share.php", body: `{"content":"x"}`,
		header: map[string]string{"Authorization": testToken}}), 401, "Invalid token")
	expect(t, e.do(req{method: "POST", target: "/share.php", body: `{}`, header: auth()}), 400, "No content")
	expect(t, e.do(req{method: "POST", target: "/share.php", body: `{"content":1}`, header: auth()}), 400, "No content")
	expect(t, e.do(req{method: "POST", target: "/share.php", body: `{"content":null}`, header: auth()}), 400, "No content")
	expect(t, e.do(req{method: "PATCH", target: "/share.php?id=abcdef12", body: `{"content":null}`, header: auth("Password", "x")}), 400, "No content")
	expect(t, e.do(req{method: "POST", target: "/share.php", body: `not json`, header: auth()}), 400, "No content")
	expect(t, e.do(req{method: "POST", target: "/share.php", header: auth()}), 400, "No content")
	big := `{"content":"` + strings.Repeat("a", 2000) + `"}`
	expect(t, e.do(req{method: "POST", target: "/share.php", body: big, header: auth()}), 413, "Request too large")
}

func TestPatchAndDelete(t *testing.T) {
	e := newEnv(t)
	s := e.create("v1")
	target := "/share.php?id=" + s.ID

	expect(t, e.do(req{method: "PATCH", target: target, body: `{"content":"v2"}`,
		header: map[string]string{"Password": s.Password}}), 401, "Invalid token")
	expect(t, e.do(req{method: "PATCH", target: target, body: `{"content":"v2"}`, header: auth()}), 400, "No id or password")
	expect(t, e.do(req{method: "PATCH", target: "/share.php", body: `{"content":"v2"}`, header: auth("Password", s.Password)}), 400, "No id or password")
	expect(t, e.do(req{method: "PATCH", target: "/share.php?id=a/b/c/d/e", body: `{"content":"v2"}`, header: auth("Password", s.Password)}), 400, "Invalid id")
	expect(t, e.do(req{method: "PATCH", target: target, body: `{}`, header: auth("Password", s.Password)}), 400, "No content")
	expect(t, e.do(req{method: "PATCH", target: target, body: `{"content":"v2"}`, header: auth("Password", "nope")}), 401, "Unauthorized")
	expect(t, e.do(req{method: "PATCH", target: "/share.php?id=abcdef12", body: `{"content":"v2"}`, header: auth("Password", "x")}), 404, "Not found")
	expect(t, e.do(req{method: "PATCH", target: target, body: `{"content":"v2"}`, header: auth("Password", s.Password)}), 200, "")
	expect(t, e.do(req{method: "GET", target: target}), 200, "v2")

	expect(t, e.do(req{method: "DELETE", target: target, header: map[string]string{"Password": s.Password}}), 401, "Invalid token")
	expect(t, e.do(req{method: "DELETE", target: target, header: auth("Password", "nope")}), 401, "Unauthorized")
	expect(t, e.do(req{method: "DELETE", target: "/share.php?id=abcdef12", header: auth("Password", "x")}), 404, "Not found")
	expect(t, e.do(req{method: "DELETE", target: target, header: auth()}), 400, "No id or password")
	expect(t, e.do(req{method: "DELETE", target: target, header: auth("Password", s.Password)}), 200, "")
	expect(t, e.do(req{method: "GET", target: target}), 404, "Not found")
}

func TestUnsupportedMethod(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do(req{method: "PUT", target: "/share.php"}), 405, "Unsupported method")
}

// TestLegacyPHPShare uses data exactly as written by share.php.
func TestLegacyPHPShare(t *testing.T) {
	e := newEnv(t)
	const id, pw = "a1b2c3d4", "00112233445566778899aabbccddeeff"
	os.WriteFile(filepath.Join(e.dataDir, id+".md"), []byte("# legacy"), 0o644)
	os.WriteFile(filepath.Join(e.dataDir, id+".json"), []byte(`{"id":"a1b2c3d4","password":"`+pw+`","created":1729500000}`), 0o644)
	target := "/share.php?id=" + id

	expect(t, e.do(req{method: "GET", target: target}), 200, "# legacy")
	expect(t, e.do(req{method: "GET", target: "/share.php?meta&id=" + id}), 200, `{"id":"a1b2c3d4","created":1729500000}`)
	expect(t, e.do(req{method: "PATCH", target: target, body: `{"content":"# updated"}`, header: auth("Password", pw)}), 200, "")
	expect(t, e.do(req{method: "GET", target: target}), 200, "# updated")

	var meta map[string]any
	b, _ := os.ReadFile(filepath.Join(e.dataDir, id+".json"))
	json.Unmarshal(b, &meta)
	if meta["version"] != float64(2) || meta["password_sha256"] == nil || meta["password"] != nil {
		t.Errorf("meta not migrated: %v", meta)
	}

	expect(t, e.do(req{method: "DELETE", target: target, header: auth("Password", pw)}), 200, "")
	expect(t, e.do(req{method: "GET", target: target}), 404, "Not found")
}

func TestQuote(t *testing.T) {
	e := newEnv(t)
	get := func() map[string]any {
		t.Helper()
		w := e.do(req{method: "GET", target: "/share.php?quote"})
		if w.Code != 200 || w.Header().Get("Content-Type") != "application/json" {
			t.Fatalf("%d %v", w.Code, w.Header())
		}
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		return m
	}
	if m := get(); m["quote"] != nil || m["source"] != nil || len(m) != 2 {
		t.Errorf("missing file: %v", m)
	}
	os.WriteFile(e.quotes, nil, 0o644)
	if m := get(); m["quote"] != nil {
		t.Errorf("empty file: %v", m)
	}
	os.WriteFile(e.quotes, []byte("\"Quote\";\"Author\"\n\n\"Say \"\"hi\"\"; twice\";\"Me\"\n"), 0o644)
	for range 10 {
		if m := get(); m["quote"] != `Say "hi"; twice` || m["source"] != "Me" {
			t.Fatalf("quote = %v", m)
		}
	}
}

func TestHealthz(t *testing.T) {
	e := newEnv(t)
	expect(t, e.do(req{method: "GET", target: "/healthz"}), 200, "ok")
}

const wantCSP = "default-src 'none'; script-src 'self' https://cdn.jsdelivr.net/npm/ https://cdn.jsdelivr.net/gh/highlightjs/; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net/npm/ https://cdn.jsdelivr.net/gh/highlightjs/ https://fonts.googleapis.com; font-src 'self' data: https://fonts.gstatic.com https://cdn.jsdelivr.net/npm/; img-src 'self' data: https:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

func TestStaticFiles(t *testing.T) {
	e := newEnv(t)
	w := e.do(req{method: "GET", target: "/"})
	expect(t, w, 200, "<html>viewer</html>")
	if w.Header().Get("Content-Security-Policy") != wantCSP {
		t.Errorf("CSP = %q", w.Header().Get("Content-Security-Policy"))
	}
	expect(t, e.do(req{method: "GET", target: "/style.css"}), 200, "body{}")
	if w := e.do(req{method: "GET", target: "/missing.js"}); w.Code != 404 {
		t.Errorf("missing: %d", w.Code)
	}
	if w := e.do(req{method: "GET", target: "/sub/"}); w.Code != 404 {
		t.Errorf("directory listing: %d %s", w.Code, w.Body)
	}
}

func TestSecurityHeadersEverywhere(t *testing.T) {
	e := newEnv(t)
	for _, target := range []string{"/", "/healthz", "/share.php?id=abcdef12", "/share.php?quote", "/nope"} {
		w := e.do(req{method: "GET", target: target})
		if w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Errorf("%s: headers %v", target, w.Header())
		}
	}
}

func TestRateLimitFailedAuth(t *testing.T) {
	e := newEnv(t)
	bad := req{method: "POST", target: "/share.php", body: `{"content":"x"}`, header: map[string]string{"Authorization": "Bearer wrong"}}
	for range failureBurst {
		expect(t, e.do(bad), 401, "Invalid token")
	}
	expect(t, e.do(bad), 429, "Too many requests")
	// A valid token is never throttled, even from the same client.
	e.create("still works")
	// The peer (192.0.2.1) is not a trusted proxy, so a spoofed header changes nothing.
	spoofed := bad
	spoofed.header = map[string]string{"Authorization": "Bearer wrong", "X-Forwarded-For": "198.51.100.7"}
	expect(t, e.do(spoofed), 429, "Too many requests")
}

func TestRateLimitUsesForwardedForFromTrustedProxy(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")} })
	bad := func(ip string) req {
		return req{method: "POST", target: "/share.php", body: `{"content":"x"}`,
			header: map[string]string{"Authorization": "Bearer wrong", "X-Forwarded-For": ip}}
	}
	for range failureBurst {
		e.do(bad("198.51.100.1"))
	}
	expect(t, e.do(bad("198.51.100.1")), 429, "Too many requests")
	expect(t, e.do(bad("198.51.100.2")), 401, "Invalid token")
}

func TestRateLimitCountsWrongSharePasswordsWhenAnonymous(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Token = ""; c.AllowAnonymous = true })
	w := e.do(req{method: "POST", target: "/share.php", body: `{"content":"x"}`})
	if w.Code != 200 {
		t.Fatalf("anonymous post: %d %s", w.Code, w.Body)
	}
	var s store.Share
	json.Unmarshal(w.Body.Bytes(), &s)
	bad := req{method: "DELETE", target: "/share.php?id=" + s.ID, header: map[string]string{"Password": "wrong"}}
	for range failureBurst {
		expect(t, e.do(bad), 401, "Unauthorized")
	}
	expect(t, e.do(bad), 429, "Too many requests")
}

func TestAnonymousModeStillAcceptsToken(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.AllowAnonymous = true })
	e.create("x")
	w := e.do(req{method: "POST", target: "/share.php", body: `{"content":"x"}`})
	if w.Code != 200 {
		t.Fatalf("anonymous post: %d %s", w.Code, w.Body)
	}
}

func TestAnonymousCreatesAreLimitedPerClient(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.AllowAnonymous = true })
	anon := req{method: "POST", target: "/share.php", body: `{"content":"x"}`}
	for range anonCreateBurst {
		if w := e.do(anon); w.Code != 200 {
			t.Fatalf("anonymous create: %d %s", w.Code, w.Body)
		}
	}
	expect(t, e.do(anon), 429, "Too many requests")
	e.create("token is never limited")
}

func TestRateLimitGroupsIPv6By64(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")} })
	bad := func(ip string) req {
		return req{method: "POST", target: "/share.php", body: `{"content":"x"}`,
			header: map[string]string{"Authorization": "Bearer wrong", "X-Forwarded-For": ip}}
	}
	for i := range failureBurst {
		e.do(bad(fmt.Sprintf("2001:db8:1:2::%x", i+1)))
	}
	expect(t, e.do(bad("2001:db8:1:2:ffff::1")), 429, "Too many requests")
	expect(t, e.do(bad("2001:db8:1:3::1")), 401, "Invalid token")
}
