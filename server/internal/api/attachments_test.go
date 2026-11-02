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

const (
	png = "\x89PNG\r\n\x1a\n"
	// testCT is a SimpleTextCrypt text form (viewer-tests/vectors.json).
	testCT = "AAECAwQFBgcICQoL\ntI+IL8VaAdnNEogID0axa9mnQVF7OBA2siggzwcsWw==\n"
	// cipherBytes stands in for an encrypted attachment: at least IV + tag.
	cipherBytes = "0123456789abcdef0123456789abcdef"
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
		if !encrypted {
			d = png + d
		}
		names = append(names, attName(d, "png"))
	}
	if names == nil {
		names = []string{}
	}
	b, _ := json.Marshal(map[string]any{"content": testCT, "encrypted": encrypted, "attachments": names})
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
	expect(t, e.put(s.ID, names[0], s.Password, png+"one"), 200, "")
	expect(t, e.put(s.ID, names[1], s.Password, png+"two"), 200, "")

	// absent attachments: unchanged
	w := e.patch(s.ID, s.Password, `{"content":"y"}`)
	expect(t, w, 200, `{"encrypted":false,"missing":[],"expires":null}`)
	if w.Header().Get("Content-Type") != "application/json" {
		t.Errorf("content-type %q", w.Header().Get("Content-Type"))
	}
	// drop one, add an undeclared-yet one
	newName := attName("three", "webp")
	w = e.patch(s.ID, s.Password, `{"content":"y","attachments":["`+names[1]+`","`+newName+`"]}`)
	expect(t, w, 200, `{"encrypted":false,"missing":["`+newName+`"],"expires":null}`)
	expect(t, e.do(req{method: "GET", target: "/share.php?att=" + s.ID + "/" + names[0]}), 404, "Not found")
	expect(t, e.do(req{method: "GET", target: "/share.php?att=" + s.ID + "/" + names[1]}), 200, png+"two")
	if _, err := os.Stat(filepath.Join(e.dataDir, s.ID+".att", names[0])); !os.IsNotExist(err) {
		t.Error("file not pruned")
	}
}

func TestPatchEchoesEncrypted(t *testing.T) {
	e := newEnv(t)
	s, _ := e.shareWith(true)
	expect(t, e.patch(s.ID, s.Password, `{"content":`+jsonString(testCT)+`}`), 200, `{"encrypted":true,"missing":[],"expires":null}`)
	expect(t, e.patch(s.ID, s.Password, `{"content":"plain","encrypted":false}`), 200, `{"encrypted":false,"missing":[],"expires":null}`)
}

func TestPutAttachmentRules(t *testing.T) {
	e := newEnv(t)
	s, names := e.shareWith(false, "hello")
	n := names[0]

	// auth
	expect(t, e.do(req{method: "PUT", target: "/share.php?att=" + s.ID + "/" + n, body: "hello",
		header: map[string]string{"Password": s.Password}}), 401, "Invalid token")
	expect(t, e.put(s.ID, n, "", "hello"), 400, "No id or password")
	expect(t, e.put(s.ID, n, "nope", png+"hello"), 401, "Unauthorized")
	expect(t, e.put("abcdef12", n, "x", "hello"), 404, "Not found")
	// malformed target
	expect(t, e.do(req{method: "PUT", target: "/share.php?att=" + s.ID, body: "x", header: auth("Password", s.Password)}), 400, "No id or password")
	expect(t, e.put(s.ID, "../x.png", s.Password, "x"), 400, "No id or password")
	// undeclared
	other := attName("other", "png")
	expect(t, e.put(s.ID, other, s.Password, png+"other"), 400, "Attachment not declared")
	// hash mismatch
	expect(t, e.put(s.ID, n, s.Password, png+"HELLO"), 400, "Hash mismatch")
	if _, err := os.Stat(filepath.Join(e.dataDir, s.ID+".att", n)); err == nil {
		t.Error("mismatching file stored")
	}
	// success, idempotent
	expect(t, e.put(s.ID, n, s.Password, png+"hello"), 200, "")
	expect(t, e.put(s.ID, n, s.Password, png+"hello"), 200, "")
	// PUT without att is not a thing
	expect(t, e.do(req{method: "PUT", target: "/share.php?id=" + s.ID, body: "x", header: auth()}), 405, "Unsupported method")
}

func TestPutAttachmentLimits(t *testing.T) {
	e := newEnv(t)
	e.store.SetAttachmentLimits(12, 20)
	s, names := e.shareWith(false, "12345", "1234", "123", "")
	expect(t, e.put(s.ID, names[0], s.Password, png+"12345"), 413, "Attachment too large")
	expect(t, e.put(s.ID, names[1], s.Password, png+"1234"), 200, "")
	expect(t, e.put(s.ID, names[2], s.Password, png+"123"), 413, "Attachment too large")
	expect(t, e.put(s.ID, names[3], s.Password, png), 200, "")
	assertNoTemp(t, e)
}

func assertNoTemp(t *testing.T, e *env) {
	t.Helper()
	entries, _ := os.ReadDir(e.dataDir)
	for _, en := range entries {
		if strings.HasPrefix(en.Name(), ".") {
			t.Errorf("temp file left: %s", en.Name())
		}
	}
}

func TestPutAttachmentContentChecks(t *testing.T) {
	e := newEnv(t)
	// unencrypted: bytes must match the extension
	s, names := e.shareWith(false, "x")
	text := "Meeting notes"
	bad := attName(text, "png")
	good := attName("GIF89a", "gif")
	b, _ := json.Marshal(map[string]any{"content": "x", "attachments": []string{bad, good, names[0]}})
	var s2 store.Share
	json.Unmarshal(e.post(string(b)).Body.Bytes(), &s2)
	expect(t, e.put(s2.ID, bad, s2.Password, text), 400, "Invalid attachment")
	expect(t, e.put(s2.ID, good, s2.Password, "GIF89a"), 200, "")
	_ = s
	// encrypted: at least IV + tag
	es, enames := e.shareWith(true, cipherBytes[:27], cipherBytes[:28])
	expect(t, e.put(es.ID, enames[0], es.Password, cipherBytes[:27]), 400, "Invalid attachment")
	expect(t, e.put(es.ID, enames[1], es.Password, cipherBytes[:28]), 200, "")
	expect(t, e.do(req{method: "GET", target: "/share.php?att=" + es.ID + "/" + enames[0]}), 404, "Not found")
	assertNoTemp(t, e)
}

func TestPutAttachmentBodyOverLimit(t *testing.T) {
	e := newEnv(t)
	e.store.SetAttachmentLimits(12, 100)
	s, names := e.shareWith(false, "x")
	// the declared hash does not matter: the limit applies while streaming
	expect(t, e.put(s.ID, names[0], s.Password, png+strings.Repeat("z", 1000)), 413, "Attachment too large")
	assertNoTemp(t, e)
}

func TestPutAttachmentExpired(t *testing.T) {
	e := newEnv(t)
	now := int64(1_800_000_000)
	e.store.SetClock(func() time.Time { return time.Unix(now, 0) })
	name := attName(png+"x", "png")
	var s store.Share
	json.Unmarshal(e.post(`{"content":"c","expiresIn":60,"attachments":["`+name+`"]}`).Body.Bytes(), &s)
	expect(t, e.put(s.ID, name, s.Password, png+"x"), 200, "")
	now += 60
	expect(t, e.put(s.ID, name, s.Password, png+"x"), 404, "Not found")
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
	datas := map[string]string{"png": png + "p", "jpg": "\xFF\xD8\xFFj", "jpeg": "\xFF\xD8\xFFjj", "gif": "GIF89ag",
		"webp": "RIFF\x00\x00\x00\x00WEBPw", "avif": "\x00\x00\x00\x18ftypavif\x00\x00\x00\x00avif\x00\x00\x00\x00"}
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
		if w.Header().Get("Cache-Control") != "no-cache" {
			t.Errorf("cache-control %q", w.Header().Get("Cache-Control"))
		}
		if w.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Errorf("corp %q", w.Header().Get("Cross-Origin-Resource-Policy"))
		}
		if w.Header().Get("Content-Disposition") != "" {
			t.Errorf("disposition %q", w.Header().Get("Content-Disposition"))
		}
		// revalidation with the ETag
		etag := w.Header().Get("ETag")
		cond := e.do(req{method: "GET", target: "/share.php?att=" + s.ID + "/" + n, header: map[string]string{"If-None-Match": etag}})
		if etag == "" || cond.Code != 304 {
			t.Errorf("conditional get: %q %d", etag, cond.Code)
		}
		if head := e.do(req{method: "HEAD", target: "/share.php?att=" + s.ID + "/" + n}); head.Code != 200 ||
			head.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" {
			t.Errorf("head: %d %v", head.Code, head.Header())
		}
	}
	for _, target := range []string{
		"/share.php?att=" + s.ID, "/share.php?att=" + s.ID + "/x.png", "/share.php?att=abcdef12/" + names[0],
		"/share.php?att=" + s.ID + "/../" + s.ID + ".json", "/share.php?att=" + s.ID + "/" + strings.Repeat("a", 64) + ".svg",
	} {
		w := e.do(req{method: "GET", target: target})
		expect(t, w, 404, "Not found")
		if w.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: error headers %v", target, w.Header())
		}
	}
}

func TestAttachmentRevalidationSeesDeletion(t *testing.T) {
	e := newEnv(t)
	s, names := e.shareWith(false, "x")
	expect(t, e.put(s.ID, names[0], s.Password, png+"x"), 200, "")
	target := "/share.php?att=" + s.ID + "/" + names[0]
	etag := e.do(req{method: "GET", target: target}).Header().Get("ETag")
	expect(t, e.do(req{method: "DELETE", target: "/share.php?id=" + s.ID, header: auth("Password", s.Password)}), 200, "")
	w := e.do(req{method: "GET", target: target, header: map[string]string{"If-None-Match": etag}})
	expect(t, w, 404, "Not found")
}

func TestGetAttachmentOfEncryptedShareIsOctetStream(t *testing.T) {
	e := newEnv(t)
	s, names := e.shareWith(true, cipherBytes)
	expect(t, e.put(s.ID, names[0], s.Password, cipherBytes), 200, "")
	w := e.do(req{method: "GET", target: "/share.php?att=" + s.ID + "/" + names[0]})
	expect(t, w, 200, cipherBytes)
	if got := w.Header().Get("Content-Disposition"); got != `attachment; filename="attachment.bin"` {
		t.Errorf("disposition %q", got)
	}
	if w.Header().Get("Cross-Origin-Resource-Policy") != "same-origin" || w.Header().Get("Cache-Control") != "no-cache" {
		t.Errorf("headers %v", w.Header())
	}
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
	e.put(s.ID, names[0], s.Password, png+"x")
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
