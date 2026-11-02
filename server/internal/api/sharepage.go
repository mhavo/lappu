package api

import (
	"bytes"
	"errors"
	"html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mhavo/lappu/server/internal/store"
)

// excerptRunes is the longest og:description, including the ellipsis.
const excerptRunes = 160

// summaryScanBytes is how much of a note is read for its title and excerpt.
const summaryScanBytes = 64 << 10

var titleElement = regexp.MustCompile(`(?s)<title>.*?</title>`)

// sharePage serves the viewer at /s/<id>. For an existing plain share the
// page's <head> carries the note's title and an excerpt for link previews;
// encrypted shares get the generic page, and invalid, missing or expired ids
// the generic page with 404 (the viewer shows its own not-found message).
func (s *server) sharePage(page []byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			text(w, http.StatusMethodNotAllowed, "Unsupported method")
			return
		}
		id, _ := strings.CutPrefix(r.URL.Path, "/s/")
		if id == "" || strings.Contains(id, "/") {
			http.NotFound(w, r)
			return
		}
		status, body := http.StatusOK, page
		switch m, content, err := s.plainShare(id); {
		case errors.Is(err, store.ErrInvalidID), errors.Is(err, store.ErrNotFound):
			status = http.StatusNotFound
		case err != nil:
			// the viewer loads the note itself and reports the error
			s.Logger.Error("share page", "err", err)
		case content != nil:
			title, excerpt := summarize(content)
			body = withShareMeta(page, title, excerpt, m)
		}
		h := w.Header()
		h.Set("Content-Type", "text/html; charset=utf-8")
		h.Set("Cache-Control", "no-cache")
		h.Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(status)
		if r.Method != http.MethodHead {
			w.Write(body)
		}
	})
}

// plainShare returns the meta and content of an unexpired share; the content
// is nil for an encrypted share.
func (s *server) plainShare(id string) (store.Meta, []byte, error) {
	if !store.ValidID(id) {
		return store.Meta{}, nil, store.ErrInvalidID
	}
	m, err := s.Store.Meta(id)
	if err != nil || m.Encrypted {
		return m, nil, err
	}
	content, err := s.Store.Content(id)
	return m, content, err
}

// withShareMeta returns page with the note title as <title> and link-preview
// metadata right after it (some preview bots only read the start of a page),
// or before </head> when the page has no <title>. The tags carry
// data-lappu-meta, so the viewer replaces them with its own when it renders
// the note. Every value is escaped.
func withShareMeta(page []byte, title, excerpt string, m store.Meta) []byte {
	end := bytes.Index(page, []byte("</head>"))
	if end < 0 {
		return page
	}
	var b strings.Builder
	tag := func(attr, key, value string) {
		if value != "" {
			b.WriteString("\n" + `<meta ` + attr + `="` + key + `" content="` + html.EscapeString(value) + `" data-lappu-meta>`)
		}
	}
	iso := func(seconds int64) string {
		return time.Unix(seconds, 0).UTC().Format(time.RFC3339)
	}
	tag("property", "og:site_name", "Lappu")
	tag("property", "og:type", "article")
	tag("property", "og:title", title)
	tag("name", "description", excerpt)
	tag("property", "og:description", excerpt)
	tag("name", "twitter:card", "summary")
	if m.Created > 0 {
		tag("property", "article:published_time", iso(m.Created))
	}
	if m.Expires != nil {
		tag("property", "article:expiration_time", iso(*m.Expires))
	}
	start, stop := end, end // the part of page that is replaced
	element := ""
	if loc := titleElement.FindIndex(page[:end]); loc != nil {
		start, stop = loc[0], loc[1]
		element = string(page[start:stop])
	}
	if title != "" {
		element = "<title>" + html.EscapeString(title) + "</title>"
	}
	out := append(bytes.Clone(page[:start]), element+b.String()...)
	if start == end {
		out = append(out, '\n')
	}
	return append(out, page[stop:]...)
}

// ---- Title and excerpt of a note's markdown ----
//
// The title mirrors the viewer, which uses the text of the first rendered
// heading of any level (ATX or setext, as markdown-it parses them), and is
// empty without one. The excerpt is the text of the other blocks. Both are
// a plain-text approximation of the rendered note: inline markup and HTML
// tags are dropped, code blocks and math blocks skipped.

var (
	fenceOpen     = regexp.MustCompile("^(```+|~~~+|\\$\\$)")
	atxHeading    = regexp.MustCompile(`^#{1,6}(?:[ \t]+(.*))?$`)
	atxClosing    = regexp.MustCompile(`(?:^|[ \t]+)#+$`)
	setextLine    = regexp.MustCompile(`^(?:=+|-+)[ \t]*$`)
	thematicBreak = regexp.MustCompile(`^(?:(?:-[ \t]*){3,}|(?:\*[ \t]*){3,}|(?:_[ \t]*){3,})$`)
	quoteMarker   = regexp.MustCompile(`^>[ \t]?`)
	listMarker    = regexp.MustCompile(`^(?:[-*+]|\d{1,9}[.)])(?:[ \t]+|$)(?:\[[ xX]\][ \t]+)?`)
	footnoteDef   = regexp.MustCompile(`^\[\^[^\]\s]+\]:`)
	tableDivider  = regexp.MustCompile(`^\|?[ \t]*:?-+:?[ \t]*(?:\|[ \t]*:?-+:?[ \t]*)*\|?[ \t]*$`)

	inlineFootnote = regexp.MustCompile(`\[\^[^\]]*\]`)
	inlineImage    = regexp.MustCompile(`!\[([^\]]*)\]\([^)]*\)`)
	inlineLink     = regexp.MustCompile(`\[([^\]]*)\](?:\([^)]*\)|\[[^\]]*\])`)
	inlineAutolink = regexp.MustCompile(`<((?:https?|mailto):[^>\s]*)>`)
	inlineHTML     = regexp.MustCompile(`(?s)<!--.*?-->|</?[A-Za-z][^>]*>`)
	// the viewer's DOMPurify drops these elements with their content
	inlineScript = regexp.MustCompile(`(?is)<script\b.*?(?:</script\s*>|$)`)
	inlineStyle  = regexp.MustCompile(`(?is)<style\b.*?(?:</style\s*>|$)`)
	inlineEscape = regexp.MustCompile("\\\\([!-/:-@\\[-`{-~])")
	inlineMarks  = regexp.MustCompile("\\*+|`+|~~|==")
	inlineUnder  = regexp.MustCompile(`(^|[^\p{L}\p{N}])_+|_+([^\p{L}\p{N}]|$)`)
)

type mdBlock struct {
	heading bool
	text    string
}

// summarize returns the note's title and an excerpt of at most excerptRunes runes.
func summarize(content []byte) (title, excerpt string) {
	if len(content) > summaryScanBytes {
		content = content[:summaryScanBytes]
	}
	text := strings.ToValidUTF8(string(content), "")
	foundTitle := false
	var parts []string
	for _, b := range markdownBlocks(text) {
		t := plainInline(b.text)
		switch {
		case b.heading && !foundTitle:
			title, foundTitle = t, true
		case !b.heading && t != "":
			parts = append(parts, t)
		}
	}
	return title, truncateWords(strings.Join(parts, " "), excerptRunes)
}

// markdownBlocks splits markdown into headings and other text blocks, skipping
// code and math blocks, footnote definitions and thematic breaks.
func markdownBlocks(md string) []mdBlock {
	var blocks []mdBlock
	var para []string
	// a paragraph that starts as a list item or quote cannot become a setext heading here
	paraPlain := true
	fence := ""
	// inside a footnote definition: its first line, lazy continuation lines and
	// indented lines after blank ones belong to it
	footnote, afterBlank := false, false
	flush := func() {
		if len(para) > 0 {
			blocks = append(blocks, mdBlock{text: strings.Join(para, " ")})
		}
		para, paraPlain = nil, true
	}
	for _, line := range strings.Split(strings.ReplaceAll(md, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimLeft(line, " ")
		indented := len(line)-len(trimmed) >= 4
		if fence != "" {
			if !indented && closesFence(strings.TrimRight(trimmed, " \t"), fence) {
				fence = ""
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			flush()
			afterBlank = true
			continue
		}
		if footnote && (indented || !afterBlank) {
			afterBlank = false
			continue
		}
		footnote, afterBlank = false, false
		if !indented && footnoteDef.MatchString(trimmed) {
			flush()
			footnote = true
			continue
		}
		if !indented {
			if f := fenceOpen.FindString(trimmed); f != "" {
				flush()
				// a one-line $$…$$ block closes itself
				if t := strings.TrimRight(trimmed, " \t"); f != "$$" || len(t) < 4 || !strings.HasSuffix(t, "$$") {
					fence = f
				}
				continue
			}
			if m := atxHeading.FindStringSubmatch(strings.TrimRight(trimmed, " \t")); m != nil {
				flush()
				blocks = append(blocks, mdBlock{heading: true, text: atxClosing.ReplaceAllString(strings.TrimSpace(m[1]), "")})
				continue
			}
			if len(para) > 0 && paraPlain && setextLine.MatchString(trimmed) {
				blocks = append(blocks, mdBlock{heading: true, text: strings.Join(para, " ")})
				para = nil
				continue
			}
			if thematicBreak.MatchString(strings.TrimRight(trimmed, " \t")) {
				flush()
				continue
			}
		}
		rest, plain := stripBlockMarkers(trimmed)
		if !plain {
			flush()
			paraPlain = false
		}
		para = append(para, rest)
	}
	flush()
	return blocks
}

func closesFence(line, fence string) bool {
	if fence == "$$" {
		return strings.HasSuffix(line, "$$")
	}
	n := len(line) - len(strings.TrimLeft(line, fence[:1]))
	return n >= len(fence) && strings.TrimSpace(line[n:]) == ""
}

// stripBlockMarkers removes quote and list markers and table pipes; plain is
// false when the line starts a quote, list item or table row.
func stripBlockMarkers(line string) (rest string, plain bool) {
	plain = true
	for {
		if loc := quoteMarker.FindStringIndex(line); loc != nil {
			line = line[loc[1]:]
		} else if loc := listMarker.FindStringIndex(line); loc != nil {
			line = line[loc[1]:]
		} else {
			break
		}
		plain = false
	}
	if strings.HasPrefix(line, "|") {
		if tableDivider.MatchString(line) {
			return "", false
		}
		return strings.ReplaceAll(line, "|", " "), false
	}
	return line, plain
}

// plainInline drops inline markdown and HTML and collapses whitespace.
func plainInline(s string) string {
	s = inlineFootnote.ReplaceAllString(s, "")
	s = inlineImage.ReplaceAllString(s, "$1")
	s = inlineLink.ReplaceAllString(s, "$1")
	s = inlineAutolink.ReplaceAllString(s, "$1")
	s = inlineScript.ReplaceAllString(s, "")
	s = inlineStyle.ReplaceAllString(s, "")
	s = inlineHTML.ReplaceAllString(s, "")
	s = inlineEscape.ReplaceAllString(s, "$1")
	s = inlineMarks.ReplaceAllString(s, "")
	s = inlineUnder.ReplaceAllString(s, "$1$2")
	s = html.UnescapeString(s)
	return strings.Join(strings.Fields(s), " ")
}

// truncateWords shortens s to at most max runes, cutting at a word boundary
// when there is one in the second half and ending with an ellipsis.
func truncateWords(s string, max int) string {
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	cut := string([]rune(s)[:max-1])
	if i := strings.LastIndexByte(cut, ' '); i > len(cut)/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:-–—") + "…"
}
