package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mhavo/lappu/server/internal/store"
)

func attName(data, ext string) string {
	sum := sha256.Sum256([]byte(data))
	return hex.EncodeToString(sum[:]) + "." + ext
}

func (e *env) put(id, name, pw, body string, extra ...string) *httptest.ResponseRecorder {
	e.t.Helper()
	h := map[string]string{"Authorization": "Bearer " + testToken}
	if pw != "" {
		h["Password"] = pw
	}
	return e.do(req{method: "PUT", target: "/share.php?att=" + id + "/" + name, body: body, header: h})
}

// shareWith creates a share declaring the given attachment data.
func (e *env) shareWith(encrypted bool, datas ...string) (store.Share, []string) {
	e.t.Helper()
	var names []string
	for _, d := range datas {
		names = append(names, attName(d, "png"))
	}
	if names == nil {
		names = []string{}
	}
	b, _ := json.Marshal(map[string]any{"content": "AAAA", "encrypted": encrypted, "attachments": names})
	w := e.post(string(b))
	if w.Code != 200 {
		e.t.Fatalf("%d %s", w.Code, w.Body)
	}
	var s store.Share
	json.Unmarshal(w.Body.Bytes(), &s)
	return s, names
}

func TestPostReportsMissingAttachments(t *testing.T) {
	e := newEnv(t)
	s, names := e.shareWith(false, "a", "b")
	if len(s.Missing) != 2 || s.Missing[0] != names[0] {
		t.Errorf("missing %v", s.Missing)
	}
	w := e.post(`{"content":"x"}`)
	if !strings.Contains(w.Body.String(), `"missing":[]`) {
		t.Errorf("no attachments: %s", w.Body)
	}
}

func TestInvalidAttachmentLists(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.MaxBodyBytes = 1 << 20 })
	s := e.create("x")
	var many []string
	for i := range 101 {
		many = append(many, attName(string(rune('a'+i)), "png"))
	}
	tooMany, _ := json.Marshal(map[string]any{"content": "x", "attachments": many})
	for name, body := range map[string]string{
		"bad name":   `{"content":"x","attachments":["evil.png"]}`,
		"svg":        `{"content":"x","attachments":["` + strings.Repeat("a", 64) + `.svg"]}`,
		"not array":  `{"content":"x","attachments":"a"}`,
		"null":       `{"content":"x","attachments":null}`,
		"non-string": `{"content":"x","attachments":[1]}`,
		"101":        string(tooMany),
	} {
		for _, w := range []*httptest.ResponseRecorder{e.post(body), e.patch(s.ID, s.Password, body)} {
			if w.Code != 400 || !strings.Contains(w.Body.String(), `"error"`) || w.Header().Get("Content-Type") != "application/json" {
				t.Errorf("%s: %d %s", name, w.Code, w.Body)
			}
		}
	}
}

func TestPatchResponseAndPruning(t *testing.T) {
	e := newEnv(t)
	s, names := e.shareWith(false, "one", "two")
	expect(t, e.put(s.ID, names[0], s.Password, "one"), 200, "")
	expect(t, e.put(s.ID, names[1], s.Password, "two"), 200, "")

	// absent attachments: unchanged
	w := e.patch(s.ID, s.Password, `{"content":"y"}`)
	expect(t, w, 200, `{"encrypted":false,"missing":[]}`)
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("content-type %q", w.Header().Get("Content-Type"))
	}
	// drop one, add an undeclared-yet one
	newName := attName("three", "webp")
	w = e.patch(s.ID, s.Password, `{"content":"y","attachments":["`+names[1]+`","`+newName+`"]}`)
	expect(t, w, 200, `{"encrypted":false,"missing":["`+newName+`"]}`)
	expect(t, e.do(req{method: "GET", target: "/share.php?att=" + s.ID + "/" + names[0]}), 404, "Not found")
	expect(t, e.do(req{method: "GET", target: "/share.php?att=" + s.ID + "/" + names[1]}), 200, "two")
	if _, err := os.Stat(filepath.Join(e.dataDir, s.ID+".att", names[0])); !os.IsNotExist(err) {
		t.Error("file not pruned")
	}
}

func TestPatchEchoesEncrypted(t *testing.T) {
	e := newEnv(t)
	s, _ := e.shareWith(true)
	expect(t, e.patch(s.ID, s.Password, `{"content":"AAAA"}`), 200, `{"encrypted":true,"missing":[]}`)
	expect(t, e.patch(s.ID, s.Password, `{"content":"plain","encrypted":false}`), 200, `{"encrypted":false,"missing":[]}`)
}

func TestPutAttachmentRules(t *testing.T) {
	e := newEnv(t)
	s, names := e.shareWith(false, "hello")
	n := names[0]

	// auth
	expect(t, e.do(req{method: "PUT", target: "/share.php?att=" + s.ID + "/" + n, body: "hello",
		header: map[string]string{"Password": s.Password}}), 401, "Invalid token")
	expect(t, e.put(s.ID, n, "", "hello"), 400, "No id or password")
	expect(t, e.put(s.ID, n, "nope", "hello"), 401, "Unauthorized")
	expect(t, e.put("abcdef12", n, "x", "hello"), 404, "Not found")
	// malformed target
	expect(t, e.do(req{method: "PUT", target: "/share.php?att=" + s.ID, body: "x", header: auth("Password", s.Password)}), 400, "No id or password")
	expect(t, e.put(s.ID, "../x.png", s.Password, "x"), 400, "No id or password")
	// undeclared
	other := attName("other", "png")
	expect(t, e.put(s.ID, other, s.Password, "other"), 400, "Attachment not declared")
	// hash mismatch
	expect(t, e.put(s.ID, n, s.Password, "HELLO"), 400, "Hash mismatch")
	if _, err := os.Stat(filepath.Join(e.dataDir, s.ID+".att", n)); err == nil {
		t.Error("mismatching file stored")
	}
	// success, idempotent
	expect(t, e.put(s.ID, n, s.Password, "hello"), 200, "")
	expect(t, e.put(s.ID, n, s.Password, "hello"), 200, "")
	// PUT without att is not a thing
	expect(t, e.do(req{method: "PUT", target: "/share.php?id=" + s.ID, body: "x", header: auth()}), 405, "Unsupported method")
}

func TestPutAttachmentLimits(t *testing.T) {
	e := newEnv(t)
	e.store.SetAttachmentLimits(5, 8)
	s, names := e.shareWith(false, "123456", "12345", "1234")
	expect(t, e.put(s.ID, names[0], s.Password, "123456"), 413, "Attachment too large")
	expect(t, e.put(s.ID, names[1], s.Password, "12345"), 200, "")
	expect(t, e.put(s.ID, names[2], s.Password, "1234"), 413, "Attachment too large")
}

func TestPutAttachmentExpired(t *testing.T) {
	e := newEnv(t)
	now := int64(1_800_000_000)
	e.store.SetClock(func() time.Time { return time.Unix(now, 0) })
	name := attName("x", "png")
	var s store.Share
	json.Unmarshal(e.post(`{"content":"c","expiresIn":60,"attachments":["`+name+`"]}`).Body.Bytes(), &s)
	expect(t, e.put(s.ID, name, s.Password, "x"), 200, "")
	now += 60
	expect(t, e.put(s.ID, name, s.Password, "x"), 404, "Not found")
	expect(t, e.do(req{method: "GET", target: "/share.php?att=" + s.ID + "/" + name}), 404, "Not found")
}

func TestPutAttachmentFailedAuthIsRateLimited(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.Token = ""; c.AllowAnonymous = true })
	var s store.Share
	name := attName("x", "png")
	w := e.do(req{method: "POST", target: "/share.php", body: `{"content":"c","attachments":["` + name + `"]}`})
	json.Unmarshal(w.Body.Bytes(), &s)
	bad := req{method: "PUT", target: "/share.php?att=" + s.ID + "/" + name, body: "x", header: map[string]string{"Password": "wrong"}}
	for range failureBurst {
		expect(t, e.do(bad), 401, "Unauthorized")
	}
	expect(t, e.do(bad), 429, "Too many requests")
}

func TestGetAttachment(t *testing.T) {
	e := newEnv(t)
	datas := map[string]string{"png": "p", "jpg": "j", "jpeg": "jj", "gif": "g", "webp": "w", "avif": "a"}
	mimes := map[string]string{"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg",
		"gif": "image/gif", "webp": "image/webp", "avif": "image/avif"}
	var names []string
	for ext, d := range datas {
		names = append(names, attName(d, ext))
	}
	b, _ := json.Marshal(map[string]any{"content": "x", "attachments": names})
	var s store.Share
	json.Unmarshal(e.post(string(b)).Body.Bytes(), &s)
	for ext, d := range datas {
		n := attName(d, ext)
		expect(t, e.do(req{method: "GET", target: "/share.php?att=" + s.ID + "/" + n}), 404, "Not found") // not stored yet
		expect(t, e.put(s.ID, n, s.Password, d), 200, "")
		w := e.do(req{method: "GET", target: "/share.php?att=" + s.ID + "/" + n})
		expect(t, w, 200, d)
		if got := w.Header().Get("Content-Type"); got != mimes[ext] {
			t.Errorf("%s: content-type %q", ext, got)
		}
		if w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Error("missing nosniff")
		}
		if csp := w.Header().Get("Content-Security-Policy"); csp != "default-src 'none'; sandbox" {
			t.Errorf("csp %q", csp)
		}
		if !strings.HasPrefix(w.Header().Get("Cache-Control"), "private, max-age=") {
			t.Errorf("cache-control %q", w.Header().Get("Cache-Control"))
		}
	}
	for _, target := range []string{
		"/share.php?att=" + s.ID, "/share.php?att=" + s.ID + "/x.png", "/share.php?att=abcdef12/" + names[0],
		"/share.php?att=" + s.ID + "/../" + s.ID + ".json", "/share.php?att=" + s.ID + "/" + strings.Repeat("a", 64) + ".svg",
	} {
		expect(t, e.do(req{method: "GET", target: target}), 404, "Not found")
	}
}

func TestGetAttachmentOfEncryptedShareIsOctetStream(t *testing.T) {
	e := newEnv(t)
	s, names := e.shareWith(true, "cipher bytes")
	expect(t, e.put(s.ID, names[0], s.Password, "cipher bytes"), 200, "")
	w := e.do(req{method: "GET", target: "/share.php?att=" + s.ID + "/" + names[0]})
	expect(t, w, 200, "cipher bytes")
	if ct := w.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("content-type %q", ct)
	}
	if w.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("missing nosniff")
	}
}

func TestDeleteRemovesAttachmentDir(t *testing.T) {
	e := newEnv(t)
	s, names := e.shareWith(false, "x")
	e.put(s.ID, names[0], s.Password, "x")
	expect(t, e.do(req{method: "DELETE", target: "/share.php?id=" + s.ID, header: auth("Password", s.Password)}), 200, "")
	if entries, _ := os.ReadDir(e.dataDir); len(entries) != 0 {
		t.Errorf("left behind: %v", entries)
	}
	expect(t, e.do(req{method: "GET", target: "/share.php?att=" + s.ID + "/" + names[0]}), 404, "Not found")
}

func TestViewerCSPAllowsBlobImages(t *testing.T) {
	e := newEnv(t)
	csp := e.do(req{method: "GET", target: "/"}).Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "img-src 'self' data: blob:") {
		t.Errorf("csp %q", csp)
	}
	api := e.do(req{method: "GET", target: "/healthz"}).Header().Get("Content-Security-Policy")
	if strings.Contains(api, "blob:") {
		t.Errorf("api csp %q", api)
	}
}
