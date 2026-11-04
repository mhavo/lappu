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
	"time"

	"github.com/mhavo/lappu/server/internal/store"
)

const testToken = "0123456789abcdef0123456789abcdef"

type env struct {
	t       *testing.T
	h       http.Handler
	dataDir string
	store   *store.Store
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
	return &env{t: t, h: New(cfg), store: s, dataDir: dataDir, quotes: cfg.QuotesFile}
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
	if m["id"] != s.ID || m["created"] != float64(s.Created) || len(m) != 4 || m["expires"] != nil || m["encrypted"] != false {
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
	expect(t, e.do(req{method: "PATCH", target: target, body: `{"content":"v2"}`, header: auth("Password", s.Password)}), 200, `{"encrypted":false,"missing":[],"expires":null}`)
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
	expect(t, e.do(req{method: "GET", target: "/share.php?meta&id=" + id}), 200, `{"id":"a1b2c3d4","created":1729500000,"expires":null,"encrypted":false}`)
	expect(t, e.do(req{method: "PATCH", target: target, body: `{"content":"# updated"}`, header: auth("Password", pw)}), 200, `{"encrypted":false,"missing":[],"expires":null}`)
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

const wantCSP = "default-src 'none'; script-src 'self' 'wasm-unsafe-eval' https://cdn.jsdelivr.net/npm/ https://cdn.jsdelivr.net/gh/highlightjs/; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net/npm/ https://cdn.jsdelivr.net/gh/highlightjs/ https://fonts.googleapis.com; font-src 'self' data: https://fonts.gstatic.com https://cdn.jsdelivr.net/npm/; img-src 'self' data: blob: https:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

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

// Viewer files are revalidated on every use, so a browser never keeps running an old viewer.
func TestStaticFilesRevalidate(t *testing.T) {
	e := newEnv(t)
	for _, target := range []string{"/", "/style.css", "/style.css?v=old", "/sub/a.js"} {
		w := e.do(req{method: "GET", target: target})
		etag := w.Header().Get("ETag")
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-cache" || len(etag) != 18 {
			t.Errorf("%s: %d %v", target, w.Code, w.Header())
			continue
		}
		if w := e.do(req{method: "GET", target: target, header: map[string]string{"If-None-Match": etag}}); w.Code != http.StatusNotModified {
			t.Errorf("%s revalidation: %d", target, w.Code)
		}
	}
	if a, b := e.do(req{method: "GET", target: "/style.css"}).Header().Get("ETag"),
		e.do(req{method: "GET", target: "/sub/a.js"}).Header().Get("ETag"); a == b {
		t.Errorf("same ETag %s for different files", a)
	}
}

// index.html references the viewer's own files with their content version, so a
// new page never loads a stale cached script.
func TestIndexVersionsOwnFiles(t *testing.T) {
	page := `<link href="/style.css"><script src="/sub/a.js"></script><script src="/missing.js"></script>` +
		`<script src="https://cdn.example/x.js"></script><link href="/style.css?v=1"><body>viewer</body>`
	e := newEnv(t, func(c *Config) {
		c.Static = fstest.MapFS{
			"index.html": {Data: []byte(page)},
			"style.css":  {Data: []byte("body{}")},
			"sub/a.js":   {Data: []byte("x")},
		}
	})
	want := `<link href="/style.css?v=` + contentVersion([]byte("body{}")) + `">` +
		`<script src="/sub/a.js?v=` + contentVersion([]byte("x")) + `"></script><script src="/missing.js"></script>` +
		`<script src="https://cdn.example/x.js"></script><link href="/style.css?v=1"><body>viewer</body>`
	expect(t, e.do(req{method: "GET", target: "/"}), 200, want)
	if body := e.do(req{method: "GET", target: "/s/abcdef12"}).Body.String(); body != want {
		t.Errorf("share page:\n%s", body)
	}
}

func TestHideFooter(t *testing.T) {
	page := fstest.MapFS{"index.html": {Data: []byte("<html><body><div id=\"footer\"></div></body></html>")}}
	withPage := func(c *Config) { c.Static = page }

	shown := newEnv(t, withPage)
	if w := shown.do(req{method: "GET", target: "/"}); w.Code != 200 || strings.Contains(w.Body.String(), "no-footer") {
		t.Errorf("default: %d %s", w.Code, w.Body)
	}

	hidden := newEnv(t, withPage, func(c *Config) { c.HideFooter = true })
	w := hidden.do(req{method: "GET", target: "/"})
	expect(t, w, 200, `<html><body class="no-footer"><div id="footer"></div></body></html>`)
	if ct := w.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("content-type %q", ct)
	}
	if w.Header().Get("Content-Security-Policy") != wantCSP {
		t.Errorf("CSP = %q", w.Header().Get("Content-Security-Policy"))
	}
	if w := hidden.do(req{method: "HEAD", target: "/"}); w.Code != 200 || w.Body.Len() != 0 {
		t.Errorf("HEAD: %d %q", w.Code, w.Body)
	}
	// Like the plain file server, /index.html redirects to the rewritten "/".
	if w := hidden.do(req{method: "GET", target: "/index.html"}); w.Code != http.StatusMovedPermanently || w.Header().Get("Location") != "./" {
		t.Errorf("/index.html: %d %v", w.Code, w.Header())
	}
	if w := hidden.do(req{method: "POST", target: "/"}); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.Code)
	}
}

func TestSourceURL(t *testing.T) {
	page := fstest.MapFS{"index.html": {Data: []byte("<html><body></body></html>")}}
	e := newEnv(t, func(c *Config) {
		c.Static = page
		c.SourceURL = `https://example.com/a?b=1&c="x"`
	})
	expect(t, e.do(req{method: "GET", target: "/"}), 200,
		`<html><body data-source-url="https://example.com/a?b=1&amp;c=&#34;x&#34;"></body></html>`)

	both := newEnv(t, func(c *Config) {
		c.Static = page
		c.HideFooter = true
		c.SourceURL = "https://example.com"
	})
	expect(t, both.do(req{method: "GET", target: "/"}), 200,
		`<html><body class="no-footer" data-source-url="https://example.com"></body></html>`)
}

func TestHideFooterNeedsOneBodyTag(t *testing.T) {
	for name, html := range map[string]string{
		"none": "<html>viewer</html>",
		"two":  "<body><body>",
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: no panic", name)
				}
			}()
			newEnv(t, func(c *Config) {
				c.Static = fstest.MapFS{"index.html": {Data: []byte(html)}}
				c.HideFooter = true
			})
		}()
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

func (e *env) post(body string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(req{method: "POST", target: "/share.php", body: body, header: auth()})
}

func TestPostExpiresIn(t *testing.T) {
	e := newEnv(t)
	now := int64(1_800_000_000)
	e.store.SetClock(func() time.Time { return time.Unix(now, 0) })

	var resp map[string]any
	w := e.post(`{"content":"x"}`)
	json.Unmarshal(w.Body.Bytes(), &resp)
	if v, ok := resp["expires"]; !ok || v != nil {
		t.Errorf("no expiresIn: expires = %v (present %v)", v, ok)
	}
	for _, body := range []string{`{"content":"x","expiresIn":null}`, `{"content":"x","expiresIn":0}`} {
		resp = nil
		json.Unmarshal(e.post(body).Body.Bytes(), &resp)
		if resp["expires"] != nil {
			t.Errorf("%s: expires = %v", body, resp["expires"])
		}
	}
	for _, secs := range []int64{1, 86400, 31536000} {
		resp = nil
		w = e.post(fmt.Sprintf(`{"content":"x","expiresIn":%d}`, secs))
		json.Unmarshal(w.Body.Bytes(), &resp)
		if w.Code != 200 || resp["expires"] != float64(now+secs) || resp["created"] != float64(now) {
			t.Errorf("expiresIn %d: %d %v", secs, w.Code, resp)
		}
		id := resp["id"].(string)
		w = e.do(req{method: "GET", target: "/share.php?meta&id=" + id})
		var m map[string]any
		json.Unmarshal(w.Body.Bytes(), &m)
		if m["expires"] != float64(now+secs) {
			t.Errorf("meta expires = %v", m["expires"])
		}
	}
}

func TestPostExpiresInInvalid(t *testing.T) {
	e := newEnv(t)
	for _, v := range []string{`-1`, `31536001`, `1.5`, `"60"`, `true`, `[1]`, `1e3`, `99999999999999999999`} {
		w := e.post(`{"content":"x","expiresIn":` + v + `}`)
		var m map[string]string
		json.Unmarshal(w.Body.Bytes(), &m)
		if w.Code != 400 || m["error"] == "" || w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("expiresIn %s: %d %q", v, w.Code, w.Body)
		}
	}
	entries, _ := os.ReadDir(e.dataDir)
	if len(entries) != 0 {
		t.Errorf("invalid requests left %d files", len(entries))
	}
}

func TestExpiredShareIs404Everywhere(t *testing.T) {
	e := newEnv(t)
	now := int64(1_800_000_000)
	e.store.SetClock(func() time.Time { return time.Unix(now, 0) })
	var s store.Share
	json.Unmarshal(e.post(`{"content":"x","expiresIn":60}`).Body.Bytes(), &s)
	pw := map[string]string{"Password": s.Password}

	expect(t, e.do(req{method: "GET", target: "/share.php?id=" + s.ID}), 200, "x")
	now += 60
	expect(t, e.do(req{method: "GET", target: "/share.php?id=" + s.ID}), 404, "Not found")
	expect(t, e.do(req{method: "GET", target: "/share.php?meta&id=" + s.ID}), 404, `{"error":"Not found"}`)
	expect(t, e.do(req{method: "PATCH", target: "/share.php?id=" + s.ID, body: `{"content":"y"}`, header: auth("Password", s.Password)}), 404, "Not found")
	expect(t, e.do(req{method: "DELETE", target: "/share.php?id=" + s.ID, header: auth("Password", pw["Password"])}), 404, "Not found")
}

func TestPatchExpiresIn(t *testing.T) {
	e := newEnv(t)
	now := int64(1_800_000_000)
	e.store.SetClock(func() time.Time { return time.Unix(now, 0) })
	var s store.Share
	json.Unmarshal(e.post(`{"content":"x","expiresIn":60,"attachments":[]}`).Body.Bytes(), &s)
	expires := func() any { return e.metaOf(s.ID)["expires"] }

	// content only: the expiry stays and is reported
	expect(t, e.patch(s.ID, s.Password, `{"content":"y"}`), 200, `{"encrypted":false,"missing":[],"expires":1800000060}`)
	// expiry only: content is untouched
	now += 10
	expect(t, e.patch(s.ID, s.Password, `{"expiresIn":3600}`), 200, `{"encrypted":false,"missing":[],"expires":1800003610}`)
	expect(t, e.do(req{method: "GET", target: "/share.php?id=" + s.ID}), 200, "y")
	if expires() != float64(1800003610) {
		t.Errorf("meta expires = %v", expires())
	}
	// null and 0 remove the expiry
	for _, v := range []string{"null", "0"} {
		e.patch(s.ID, s.Password, `{"expiresIn":60}`)
		expect(t, e.patch(s.ID, s.Password, `{"expiresIn":`+v+`}`), 200, `{"encrypted":false,"missing":[],"expires":null}`)
		if expires() != nil {
			t.Errorf("%s: meta expires = %v", v, expires())
		}
	}
	// both at once
	expect(t, e.patch(s.ID, s.Password, `{"content":"z","expiresIn":1}`), 200, `{"encrypted":false,"missing":[],"expires":1800000011}`)
	expect(t, e.do(req{method: "GET", target: "/share.php?id=" + s.ID}), 200, "z")
	// an expired share cannot be revived
	now++
	expect(t, e.patch(s.ID, s.Password, `{"expiresIn":0}`), 404, "Not found")
}

func TestPatchExpiresInRejections(t *testing.T) {
	e := newEnv(t)
	now := int64(1_800_000_000)
	e.store.SetClock(func() time.Time { return time.Unix(now, 0) })
	var s store.Share
	json.Unmarshal(e.post(`{"content":"x","expiresIn":60}`).Body.Bytes(), &s)
	for _, v := range []string{`-1`, `31536001`, `1.5`, `"60"`, `true`, `[1]`, `1e3`, `99999999999999999999`} {
		for _, body := range []string{`{"expiresIn":` + v + `}`, `{"content":"new","expiresIn":` + v + `}`} {
			expect(t, e.patch(s.ID, s.Password, body), 400, `{"error":"Invalid expiresIn"}`)
		}
	}
	expect(t, e.patch(s.ID, s.Password, `{}`), 400, "No content")
	expect(t, e.patch(s.ID, s.Password, `{"encrypted":false}`), 400, "No content")
	for _, body := range []string{`{"expiresIn":0,"encrypted":false}`, `{"expiresIn":0,"attachments":[]}`} {
		expect(t, e.patch(s.ID, s.Password, body), 400, `{"error":"encrypted and attachments need content"}`)
	}
	// nothing changed; a wrong password or token is rejected as before
	expect(t, e.patch(s.ID, "nope", `{"expiresIn":0}`), 401, "Unauthorized")
	expect(t, e.do(req{method: "PATCH", target: "/share.php?id=" + s.ID, body: `{"expiresIn":0}`,
		header: map[string]string{"Password": s.Password}}), 401, "Invalid token")
	expect(t, e.do(req{method: "GET", target: "/share.php?id=" + s.ID}), 200, "x")
	if m := e.metaOf(s.ID); m["expires"] != float64(now+60) {
		t.Errorf("expires = %v", m["expires"])
	}
}

func TestPatchExpiryKeepsEncryptedShare(t *testing.T) {
	e := newEnv(t)
	w := e.post(`{"content":` + jsonString(testCT) + `,"encrypted":true}`)
	var s store.Share
	json.Unmarshal(w.Body.Bytes(), &s)
	expect(t, e.patch(s.ID, s.Password, `{"expiresIn":null}`), 200, `{"encrypted":true,"missing":[],"expires":null}`)
	expect(t, e.do(req{method: "GET", target: "/share.php?id=" + s.ID}), 200, testCT)
}

func (e *env) patch(id, pw, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.do(req{method: "PATCH", target: "/share.php?id=" + id, body: body, header: auth("Password", pw)})
}

func (e *env) metaOf(id string) map[string]any {
	e.t.Helper()
	var m map[string]any
	json.Unmarshal(e.do(req{method: "GET", target: "/share.php?meta&id=" + id}).Body.Bytes(), &m)
	return m
}

func TestEncryptedShare(t *testing.T) {
	e := newEnv(t)
	const ct = "AAECAwQFBgcICQoL\ntI+IL8VaAdnNEogID0axa9mnQVF7OBA2siggzwcsWw==\n"

	// default: not encrypted, and the POST response says so
	var plain map[string]any
	json.Unmarshal(e.post(`{"content":"hello"}`).Body.Bytes(), &plain)
	if v, ok := plain["encrypted"]; !ok || v != false {
		t.Errorf("plain post response: %v", plain)
	}

	w := e.post(`{"content":` + jsonString(ct) + `,"encrypted":true}`)
	var s map[string]any
	json.Unmarshal(w.Body.Bytes(), &s)
	if w.Code != 200 || s["encrypted"] != true {
		t.Fatalf("post: %d %s", w.Code, w.Body)
	}
	id, pw := s["id"].(string), s["password"].(string)
	if e.metaOf(id)["encrypted"] != true {
		t.Error("meta not encrypted")
	}
	expect(t, e.do(req{method: "GET", target: "/share.php?id=" + id}), 200, ct)

	// absent keeps the flag, content is still checked
	expect(t, e.patch(id, pw, `{"content":"plaintext!"}`), 400, `{"error":"encrypted content must be SimpleTextCrypt base64 text"}`)
	if w := e.patch(id, pw, `{"content":`+jsonString(testCT)+`}`); w.Code != 200 || e.metaOf(id)["encrypted"] != true {
		t.Errorf("patch keep: %d %v", w.Code, e.metaOf(id))
	}
	// explicit false turns it off
	if w := e.patch(id, pw, `{"content":"# plain","encrypted":false}`); w.Code != 200 || e.metaOf(id)["encrypted"] != false {
		t.Errorf("patch off: %d %v", w.Code, e.metaOf(id))
	}
	// and back on, with plaintext content rejected and nothing changed
	if w := e.patch(id, pw, `{"content":"# plain","encrypted":true}`); w.Code != 400 {
		t.Errorf("patch on with plaintext: %d", w.Code)
	}
	expect(t, e.do(req{method: "GET", target: "/share.php?id=" + id}), 200, "# plain")
	if e.metaOf(id)["encrypted"] != false {
		t.Error("rejected patch changed the flag")
	}
}

func TestEncryptedRejections(t *testing.T) {
	e := newEnv(t)
	for _, body := range []string{
		`{"content":"","encrypted":true}`,
		`{"content":"# hi there","encrypted":true}`,
		`{"content":"abc-def","encrypted":true}`,
		`{"content":"Meeting notes for Tuesday","encrypted":true}`,
		`{"content":"QUJD","encrypted":true}`,
	} {
		w := e.post(body)
		if w.Code != 400 || !strings.Contains(w.Body.String(), `"error"`) {
			t.Errorf("%s: %d %s", body, w.Code, w.Body)
		}
	}
	for _, v := range []string{`"yes"`, `1`, `null`, `[]`} {
		w := e.post(`{"content":` + jsonString(testCT) + `,"encrypted":` + v + `}`)
		if w.Code != 400 || w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("encrypted %s: %d %s", v, w.Code, w.Body)
		}
	}
	s := e.create("x")
	if w := e.patch(s.ID, s.Password, `{"content":"x","encrypted":"no"}`); w.Code != 400 {
		t.Errorf("patch non-bool: %d", w.Code)
	}
}
