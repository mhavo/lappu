package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"testing/fstest"
	"time"
	"unicode/utf8"

	"github.com/mhavo/lappu/server/internal/store"
)

const sharePageHTML = "<html><head>\n<title>Lappu</title>\n</head><body>viewer</body></html>"

func withSharePage(c *Config) {
	c.Static = fstest.MapFS{"index.html": {Data: []byte(sharePageHTML)}}
}

func TestSharePagePlainShare(t *testing.T) {
	e := newEnv(t, withSharePage)
	e.store.SetClock(func() time.Time { return time.Unix(1_800_000_000, 0) })
	var s store.Share
	json.Unmarshal(e.post(`{"content":"# My *note*\n\nFirst para.\n\nSecond.","expiresIn":3600}`).Body.Bytes(), &s)

	w := e.do(req{method: "GET", target: "/s/" + s.ID})
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	body := w.Body.String()
	for _, want := range []string{
		"<title>My note</title>",
		`<meta property="og:title" content="My note" data-lappu-meta>`,
		`<meta property="og:description" content="First para. Second." data-lappu-meta>`,
		`<meta name="description" content="First para. Second." data-lappu-meta>`,
		`<meta property="og:type" content="article" data-lappu-meta>`,
		`<meta property="og:site_name" content="Lappu" data-lappu-meta>`,
		`<meta name="twitter:card" content="summary" data-lappu-meta>`,
		`<meta property="article:published_time" content="2027-01-15T08:00:00Z" data-lappu-meta>`,
		`<meta property="article:expiration_time" content="2027-01-15T09:00:00Z" data-lappu-meta>`,
		"</head><body>viewer</body></html>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
	if strings.Count(body, "<title>") != 1 || strings.Contains(body, "og:url") || strings.Contains(body, "og:image") {
		t.Errorf("page:\n%s", body)
	}
	h := w.Header()
	if h.Get("Content-Type") != "text/html; charset=utf-8" || h.Get("Cache-Control") != "no-cache" ||
		h.Get("Content-Security-Policy") != wantCSP || h.Get("X-Robots-Tag") != "noindex, nofollow, noarchive" {
		t.Errorf("headers %v", h)
	}

	head := e.do(req{method: "HEAD", target: "/s/" + s.ID})
	if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != h.Get("Content-Length") {
		t.Errorf("HEAD: %d %q %v", head.Code, head.Body, head.Header())
	}
	if w := e.do(req{method: "POST", target: "/s/" + s.ID}); w.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d", w.Code)
	}
}

func TestSharePageEscapesHostileContent(t *testing.T) {
	e := newEnv(t, withSharePage)
	s := e.create("# \"><script>alert(1)</script> &amp; 'x'\n\n<img src=x onerror=alert(2)> \"quoted\" </title>\nnext\tline")
	body := e.do(req{method: "GET", target: "/s/" + s.ID}).Body.String()
	if strings.Contains(body, "<script>") || strings.Contains(body, "<img") || strings.Count(body, "</title>") != 1 {
		t.Fatalf("unescaped:\n%s", body)
	}
	for _, want := range []string{
		`<title>&#34;&gt; &amp; &#39;x&#39;</title>`,
		`<meta property="og:title" content="&#34;&gt; &amp; &#39;x&#39;" data-lappu-meta>`,
		`<meta property="og:description" content="&#34;quoted&#34; next line" data-lappu-meta>`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
}

func TestSharePageWithoutHeading(t *testing.T) {
	e := newEnv(t, withSharePage)
	s := e.create("just text")
	body := e.do(req{method: "GET", target: "/s/" + s.ID}).Body.String()
	if !strings.Contains(body, "<title>Lappu</title>") || strings.Contains(body, "og:title") ||
		!strings.Contains(body, `og:description" content="just text"`) {
		t.Errorf("page:\n%s", body)
	}
}

func TestSharePageEncryptedIsGeneric(t *testing.T) {
	e := newEnv(t, withSharePage)
	var s store.Share
	json.Unmarshal(e.post(`{"content":`+jsonString(testCT)+`,"encrypted":true}`).Body.Bytes(), &s)
	expect(t, e.do(req{method: "GET", target: "/s/" + s.ID}), 200, sharePageHTML)
}

func TestSharePageNotFound(t *testing.T) {
	e := newEnv(t, withSharePage)
	now := int64(1_800_000_000)
	e.store.SetClock(func() time.Time { return time.Unix(now, 0) })
	var s store.Share
	json.Unmarshal(e.post(`{"content":"# Gone","expiresIn":60}`).Body.Bytes(), &s)
	now += 60
	for _, target := range []string{"/s/" + s.ID, "/s/abcdef12", "/s/bad", "/s/abcd%2E1234", "/s/" + strings.Repeat("a", 65)} {
		w := e.do(req{method: "GET", target: target})
		expect(t, w, 404, sharePageHTML)
		if w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
			t.Errorf("%s: %v", target, w.Header())
		}
		if w := e.do(req{method: "HEAD", target: target}); w.Code != 404 || w.Body.Len() != 0 {
			t.Errorf("HEAD %s: %d", target, w.Code)
		}
	}
	for _, target := range []string{"/s/", "/s/" + s.ID + "/x", "/s/abcdef12/"} {
		if w := e.do(req{method: "GET", target: target}); w.Code != 404 || strings.Contains(w.Body.String(), "viewer") {
			t.Errorf("%s: %d %s", target, w.Code, w.Body)
		}
	}
}

func TestSharePageKeepsViewerSettings(t *testing.T) {
	e := newEnv(t, withSharePage, func(c *Config) {
		c.HideFooter = true
		c.SourceURL = "https://example.com"
	})
	s := e.create("# T")
	for _, target := range []string{"/s/" + s.ID, "/s/abcdef12"} {
		if body := e.do(req{method: "GET", target: target}).Body.String(); !strings.Contains(body,
			`<body class="no-footer" data-source-url="https://example.com">viewer</body>`) {
			t.Errorf("%s:\n%s", target, body)
		}
	}
}

func TestRobots(t *testing.T) {
	e := newEnv(t)
	for _, target := range []string{"/", "/s/abcdef12", "/share.php?id=abcdef12", "/healthz", "/style.css"} {
		if got := e.do(req{method: "GET", target: target}).Header().Get("X-Robots-Tag"); got != "noindex, nofollow, noarchive" {
			t.Errorf("%s: X-Robots-Tag %q", target, got)
		}
	}
}

func TestSummarize(t *testing.T) {
	for _, tc := range []struct{ name, md, title, excerpt string }{
		{"atx", "# Hello `world` #\n\ntext", "Hello world", "text"},
		{"any level", "intro\n\n### Deep [link](https://x.example) ###", "Deep link", "intro"},
		{"setext", "Big title\n===\n\nbody", "Big title", "body"},
		{"setext h2", "Sub\n---\nbody", "Sub", "body"},
		{"tag is not a heading", "#tag text\n# Real", "Real", "#tag text"},
		{"empty heading", "#\n# Second", "", ""},
		{"code skipped", "```\n# not a heading\n```\n~~~md\nx\n~~~\n# Yes", "Yes", ""},
		{"math skipped", "$$\nx^2\n$$\n$$ y $$\npara", "", "para"},
		{"frontmatter like markdown-it", "---\ntitle: x\n---\n\n# Name\n\nbody", "title: x", "body"},
		{"lists and quotes", "- [ ] task\n- **bold** item\n> quote\n1. one", "", "task bold item quote one"},
		{"list line is no setext", "- item\n---\nafter", "", "item after"},
		{"tables", "| a | b |\n|---|:-:|\n| 1 | 2 |", "", "a b 1 2"},
		{"inline", `![alt](lappu-att:x.png) <b>bold</b> \*esc\* snake_case _em_ ~~del~~ x[^1] &lt;tag&gt;`, "", "alt bold esc snake_case em del x <tag>"},
		{"footnotes", "# T\n\nLisää tekstiä[^1].\n\n[^1]: Alaviite.\n    jatkuu\nlaiska\n\n    sisennetty\n\n[^long-note]: Toinen.\n\nLoppu.", "T", "Lisää tekstiä. Loppu."},
		{"footnote ref before colon", "a[^1]: not a definition", "", "a: not a definition"},
		{"crlf", "# T\r\n\r\nline one\r\nline two", "T", "line one line two"},
	} {
		title, excerpt := summarize([]byte(tc.md))
		if title != tc.title || excerpt != tc.excerpt {
			t.Errorf("%s: got %q %q, want %q %q", tc.name, title, excerpt, tc.title, tc.excerpt)
		}
	}
}

func TestSummarizeTruncatesAtWord(t *testing.T) {
	_, excerpt := summarize([]byte(strings.Repeat("sana ", 100)))
	if n := utf8.RuneCountInString(excerpt); n > excerptRunes || !strings.HasSuffix(excerpt, "sana…") {
		t.Errorf("%d runes: %q", n, excerpt)
	}
	_, excerpt = summarize([]byte(strings.Repeat("ä", 500)))
	if n := utf8.RuneCountInString(excerpt); n != excerptRunes || !strings.HasSuffix(excerpt, "ä…") {
		t.Errorf("%d runes: %q", n, excerpt)
	}
	if _, excerpt := summarize([]byte("ok\xff\xfe text")); excerpt != "ok text" {
		t.Errorf("invalid UTF-8: %q", excerpt)
	}
}

func TestWithShareMetaPlacement(t *testing.T) {
	m := store.Meta{}
	got := string(withShareMeta([]byte("<head><title>Lappu</title><script></script></head>"), "T", "", m))
	want := `<head><title>T</title>` + "\n" +
		`<meta property="og:site_name" content="Lappu" data-lappu-meta>` + "\n" +
		`<meta property="og:type" content="article" data-lappu-meta>` + "\n" +
		`<meta property="og:title" content="T" data-lappu-meta>` + "\n" +
		`<meta name="twitter:card" content="summary" data-lappu-meta><script></script></head>`
	if got != want {
		t.Errorf("after title:\n%s\nwant\n%s", got, want)
	}
	got = string(withShareMeta([]byte("<head><meta charset=x></head><body>"), "T", "", m))
	if !strings.HasPrefix(got, "<head><meta charset=x><title>T</title>\n<meta ") || !strings.HasSuffix(got, "data-lappu-meta>\n</head><body>") {
		t.Errorf("no title: %s", got)
	}
	if got := string(withShareMeta([]byte("<p>no head</p>"), "T", "", m)); got != "<p>no head</p>" {
		t.Errorf("no head: %s", got)
	}
}
