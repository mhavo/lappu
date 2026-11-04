// Package api serves the share.php-compatible HTTP API and the viewer.
package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/netip"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/mhavo/lappu/server/internal/store"
)

// Per-client limits: failed write attempts (refilled over failureWindow) and,
// in anonymous mode, creates without a valid token (refilled over anonCreateWindow).
const (
	failureBurst     = 10
	failureWindow    = time.Minute
	anonCreateBurst  = 30
	anonCreateWindow = time.Hour
)

// Per-client read limits (share.php ?id, ?meta and ?att, and /s/<id>), the
// defaults of Config.LookupFailureLimit and Config.ReadLimit: lookups that end
// in 404 or an invalid id (id enumeration), and all reads. Requests with the
// upload token are never limited.
const (
	DefaultLookupFailureLimit = 30
	lookupFailureWindow       = 10 * time.Minute
	DefaultReadLimit          = 300
	readWindow                = 5 * time.Minute
)

// viewerCSP limits jsDelivr to npm packages and the highlight.js repo
// (not arbitrary GitHub repos via /gh/).
const viewerCSP = "default-src 'none'; " +
	"script-src 'self' 'wasm-unsafe-eval' https://cdn.jsdelivr.net/npm/ https://cdn.jsdelivr.net/gh/highlightjs/; " +
	"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net/npm/ https://cdn.jsdelivr.net/gh/highlightjs/ https://fonts.googleapis.com; " +
	"font-src 'self' data: https://fonts.gstatic.com https://cdn.jsdelivr.net/npm/; " +
	"img-src 'self' data: blob: https:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// apiCSP applies to API responses, which are never meant to render as documents.
const apiCSP = "default-src 'none'; frame-ancestors 'none'; sandbox"

type Config struct {
	Store          *store.Store
	Token          string // required for writes unless AllowAnonymous
	AllowAnonymous bool
	MaxBodyBytes   int64
	TrustedProxies []netip.Prefix
	QuotesFile     string
	Static         fs.FS
	// HideFooter hides the viewer's bottom line (language switch and app name)
	// by serving index.html with a body class.
	HideFooter bool
	// SourceURL, when set, makes the app name in the viewer's bottom line a link
	// to it (an absolute http(s) URL, validated by the caller).
	SourceURL string
	// LookupFailureLimit and ReadLimit are the per-client read limits
	// (0 = DefaultLookupFailureLimit per 10 minutes, DefaultReadLimit per 5 minutes).
	LookupFailureLimit int
	ReadLimit          int
	Logger             *slog.Logger
	// now is the limiters' clock (tests); nil = time.Now.
	now func() time.Time
}

type server struct {
	Config
	tokenHash [32]byte
	limiter   *limiter // failed auth
	creates   *limiter // anonymous creates
	lookups   *limiter // reads of missing or invalid ids
	reads     *limiter // all limited reads
}

// New returns the complete HTTP handler.
func New(cfg Config) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.now == nil {
		cfg.now = time.Now
	}
	if cfg.LookupFailureLimit <= 0 {
		cfg.LookupFailureLimit = DefaultLookupFailureLimit
	}
	if cfg.ReadLimit <= 0 {
		cfg.ReadLimit = DefaultReadLimit
	}
	s := &server{
		Config:    cfg,
		tokenHash: sha256.Sum256([]byte(cfg.Token)),
		limiter:   newLimiter(failureBurst, failureWindow, cfg.now),
		creates:   newLimiter(anonCreateBurst, anonCreateWindow, cfg.now),
		lookups:   newLimiter(cfg.LookupFailureLimit, lookupFailureWindow, cfg.now),
		reads:     newLimiter(cfg.ReadLimit, readWindow, cfg.now),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/share.php", s.share)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		text(w, http.StatusOK, "ok")
	})
	versions := fileVersions(cfg.Static)
	page := viewerIndex(cfg.Static, versions, cfg.HideFooter, cfg.SourceURL)
	mux.Handle("/", static(cfg.Static, page, versions))
	mux.Handle("/s/", s.limitReads(s.sharePage(page)))
	return s.logRequests(securityHeaders(mux))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		// Shares are private links; see also robots.txt.
		h.Set("X-Robots-Tag", "noindex, nofollow, noarchive")
		if r.URL.Path == "/share.php" || r.URL.Path == "/healthz" {
			h.Set("Content-Security-Policy", apiCSP)
		} else {
			h.Set("Content-Security-Policy", viewerCSP)
		}
		next.ServeHTTP(w, r)
	})
}

// bodyMarker is replaced in index.html to pass the viewer settings.
const bodyMarker = "<body>"

// viewerIndex reads index.html, adds "?v=<version>" to its references to
// the viewer's own files (so a browser never runs a stale cached copy of a
// script with a newer page) and, with hideFooter or a sourceURL, gives its
// <body> the no-footer class and/or a data-source-url attribute. It panics if
// index.html cannot be read or, when the settings have to be applied, does
// not contain exactly one <body>, so a changed viewer never silently ignores
// the settings.
func viewerIndex(fsys fs.FS, versions map[string]string, hideFooter bool, sourceURL string) []byte {
	b, err := fs.ReadFile(fsys, "index.html")
	if err != nil {
		panic("viewer: " + err.Error())
	}
	b = localReference.ReplaceAllFunc(b, func(m []byte) []byte {
		sub := localReference.FindSubmatch(m)
		if v, ok := versions[string(sub[2])]; ok {
			return append(bytes.Clone(m[:len(m)-1]), `?v=`+v+`"`...)
		}
		return m
	})
	if !hideFooter && sourceURL == "" {
		return b
	}
	if n := bytes.Count(b, []byte(bodyMarker)); n != 1 {
		panic(fmt.Sprintf("viewer settings: index.html has %d %s tags, want exactly 1", n, bodyMarker))
	}
	body := "<body"
	if hideFooter {
		body += ` class="no-footer"`
	}
	if sourceURL != "" {
		body += ` data-source-url="` + html.EscapeString(sourceURL) + `"`
	}
	return bytes.Replace(b, []byte(bodyMarker), []byte(body+">"), 1)
}

// localReference matches a src or href attribute with an absolute path and
// no query or fragment; the second group is the path.
var localReference = regexp.MustCompile(`((?:src|href)=")(/[^"?#]*)"`)

// fileVersions returns a short content hash of every viewer file, keyed by
// its URL path ("/index.js"). It panics if the files cannot be read.
func fileVersions(fsys fs.FS) map[string]string {
	versions := map[string]string{}
	err := fs.WalkDir(fsys, ".", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		versions["/"+name] = contentVersion(b)
		return nil
	})
	if err != nil {
		panic("viewer: " + err.Error())
	}
	return versions
}

func contentVersion(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}

// revalidate makes the browser check a viewer file with the server on every
// use (a 304 when it has not changed) instead of trusting a cached copy:
// the embedded files have no modification time, so without an ETag a
// browser could keep running an old viewer indefinitely.
func revalidate(w http.ResponseWriter, version string) {
	h := w.Header()
	h.Set("Cache-Control", "no-cache")
	h.Set("ETag", `"`+version+`"`)
}

// static serves the viewer files, all with revalidate. index (index.html with
// the versioned references and the viewer settings) is served for "/";
// "/index.html" is still redirected to "/" by the file server, so both paths
// get the same page.
func static(fsys fs.FS, index []byte, versions map[string]string) http.Handler {
	files := http.FileServerFS(fsys)
	indexVersion := contentVersion(index)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			text(w, http.StatusMethodNotAllowed, "Unsupported method")
			return
		}
		// No directory listings; "/" itself is served as index.html.
		if r.URL.Path != "/" && strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		if r.URL.Path == "/" {
			revalidate(w, indexVersion)
			http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(index))
			return
		}
		if v, ok := versions[path.Clean(r.URL.Path)]; ok && r.URL.Path != "/index.html" {
			revalidate(w, v)
		}
		files.ServeHTTP(w, r)
	})
}

func (s *server) share(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		if q.Has("quote") {
			s.quote(w)
			return
		}
		s.limitReads(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case q.Has("att"):
				s.getAttachment(w, r, q.Get("att"))
			case q.Has("meta"):
				s.meta(w, q.Get("id"))
			default:
				s.get(w, q.Get("id"))
			}
		})).ServeHTTP(w, r)
	case http.MethodPut:
		if !q.Has("att") {
			text(w, http.StatusMethodNotAllowed, "Unsupported method")
			return
		}
		s.putAttachment(w, r)
	case http.MethodPost, http.MethodPatch, http.MethodDelete:
		s.write(w, r)
	default:
		text(w, http.StatusMethodNotAllowed, "Unsupported method")
	}
}

func (s *server) get(w http.ResponseWriter, id string) {
	if id == "" {
		text(w, http.StatusBadRequest, "No id")
		return
	}
	content, err := s.Store.Content(id)
	if err != nil {
		s.storeError(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(content)
}

func (s *server) meta(w http.ResponseWriter, id string) {
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "No id"})
		return
	}
	m, err := s.Store.Meta(id)
	switch {
	case errors.Is(err, store.ErrInvalidID):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid id"})
	case errors.Is(err, store.ErrNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Not found"})
	case err != nil:
		s.Logger.Error("read meta", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "Internal error"})
	default:
		writeJSON(w, http.StatusOK, struct {
			ID        string `json:"id"`
			Created   int64  `json:"created"`
			Expires   *int64 `json:"expires"`
			Encrypted bool   `json:"encrypted"`
		}{m.ID, m.Created, m.Expires, m.Encrypted})
	}
}

type quoteResponse struct {
	Quote  *string `json:"quote"`
	Source *string `json:"source"`
}

// quote returns a random line of the quotes file ("Quote";"Source" per line).
// The file is read on every request so edits apply without a restart.
func (s *server) quote(w http.ResponseWriter) {
	var resp quoteResponse
	if records := readQuotes(s.QuotesFile); len(records) > 0 {
		rec := records[rand.IntN(len(records))]
		resp.Quote = &rec[0]
		if len(rec) > 1 {
			resp.Source = &rec[1]
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

func readQuotes(path string) [][]string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	cr := csv.NewReader(f)
	cr.Comma = ';'
	cr.FieldsPerRecord = -1
	cr.LazyQuotes = true
	var records [][]string
	for {
		rec, err := cr.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue
		}
		if len(rec) == 0 || rec[0] == "" || (len(records) == 0 && isHeader(rec)) {
			continue
		}
		records = append(records, rec)
	}
	return records
}

func isHeader(rec []string) bool {
	return len(rec) == 2 && strings.EqualFold(rec[0], "quote") &&
		(strings.EqualFold(rec[1], "author") || strings.EqualFold(rec[1], "source"))
}

// limitReads applies the per-client read limits to a read of a share: a
// client over either limit gets 429, and a read that ends in 404 or 400 (an
// invalid or missing id) counts as a lookup failure. A handler can mark the
// read free with freeRead, so that it charges neither limit; a blocked client
// still gets 429 for it. A valid upload token exempts the request; an invalid
// one is ignored, as reads need no token.
func (s *server) limitReads(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.validToken(r) {
			next.ServeHTTP(w, r)
			return
		}
		ip := limitKey(clientIP(r, s.TrustedProxies))
		if s.lookups.blocked(ip) || s.reads.blocked(ip) {
			text(w, http.StatusTooManyRequests, "Too many requests")
			return
		}
		free := new(bool)
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), freeReadKey{}, free)))
		if *free {
			return
		}
		s.reads.charge(ip)
		if rec.status == http.StatusNotFound || rec.status == http.StatusBadRequest {
			s.lookups.charge(ip)
		}
	})
}

type freeReadKey struct{}

// freeRead exempts the current read from the limits of limitReads.
func freeRead(r *http.Request) {
	if free, ok := r.Context().Value(freeReadKey{}).(*bool); ok {
		*free = true
	}
}

// gate applies the token check and failed-auth rate limiting shared by all
// writes. ok is false when the response has been written.
func (s *server) gate(w http.ResponseWriter, r *http.Request) (ip string, trusted, ok bool) {
	ip = limitKey(clientIP(r, s.TrustedProxies))
	trusted = s.validToken(r)
	if !trusted {
		if s.limiter.blocked(ip) || (r.Method == http.MethodPost && s.creates.blocked(ip)) {
			text(w, http.StatusTooManyRequests, "Too many requests")
			return ip, trusted, false
		}
		if !s.AllowAnonymous {
			s.limiter.charge(ip)
			s.Logger.Warn("invalid token", "ip", ip)
			text(w, http.StatusUnauthorized, "Invalid token")
			return ip, trusted, false
		}
	}
	return ip, trusted, true
}

func (s *server) write(w http.ResponseWriter, r *http.Request) {
	ip, trusted, ok := s.gate(w, r)
	if !ok {
		return
	}

	var id, password string
	if r.Method != http.MethodPost {
		id, password = r.URL.Query().Get("id"), r.Header.Get("Password")
		if id == "" || password == "" {
			text(w, http.StatusBadRequest, "No id or password")
			return
		}
		if !store.ValidID(id) {
			text(w, http.StatusBadRequest, "Invalid id")
			return
		}
	}

	var req writeBody
	if r.Method != http.MethodDelete {
		if req, ok = s.readBody(w, r); !ok {
			return
		}
	}

	var err error
	switch r.Method {
	case http.MethodPost:
		var share store.Share
		if share, err = s.Store.CreateFull(*req.content, req.expiresIn, req.encrypted != nil && *req.encrypted, req.attachments); err == nil {
			if !trusted {
				s.creates.charge(ip)
			}
			writeJSON(w, http.StatusOK, share)
			return
		}
	case http.MethodPatch:
		var res store.UpdateResult
		patch := store.Patch{Content: req.content, Encrypted: req.encrypted, Attachments: req.attachments}
		if req.setExpiry {
			patch.ExpiresIn = &req.expiresIn
		}
		if res, err = s.Store.Patch(id, password, patch); err == nil {
			writeJSON(w, http.StatusOK, res)
			return
		}
	case http.MethodDelete:
		err = s.Store.Delete(id, password)
	}
	if errors.Is(err, store.ErrUnauthorized) && !trusted {
		s.limiter.charge(ip)
	}
	if errors.Is(err, store.ErrNotCiphertext) || errors.Is(err, store.ErrInvalidAttachments) || errors.Is(err, store.ErrNoContent) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err != nil {
		s.storeError(w, err)
	}
}

// putAttachment stores the raw request body as a declared attachment.
func (s *server) putAttachment(w http.ResponseWriter, r *http.Request) {
	ip, trusted, ok := s.gate(w, r)
	if !ok {
		return
	}
	id, name, ok := splitAttachment(r.URL.Query().Get("att"))
	password := r.Header.Get("Password")
	if !ok || password == "" {
		text(w, http.StatusBadRequest, "No id or password")
		return
	}
	fail := func(err error) {
		if errors.Is(err, store.ErrUnauthorized) && !trusted {
			s.limiter.charge(ip)
		}
		s.attachmentError(w, err)
	}
	// Authorize before reading the body so unauthenticated clients cannot make us buffer it.
	stored, err := s.Store.CheckAttachment(id, password, name)
	if err != nil {
		fail(err)
		return
	}
	if stored {
		w.WriteHeader(http.StatusOK)
		return
	}
	// The store streams the body to a temp file; the reader enforces the limit
	// and the store checks hash and content before storing.
	body := http.MaxBytesReader(w, r.Body, s.Store.MaxAttachmentBytes())
	if err := s.Store.PutAttachmentFrom(id, password, name, body); err != nil {
		if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
			text(w, http.StatusRequestEntityTooLarge, "Attachment too large")
			return
		}
		if isStore(err) {
			fail(err)
			return
		}
		text(w, http.StatusBadRequest, "Bad body")
		return
	}
	w.WriteHeader(http.StatusOK)
}

// isStore reports whether err is one the store reports itself, rather than a
// failure reading the request body or the file system.
func isStore(err error) bool {
	for _, e := range []error{store.ErrInvalidID, store.ErrNotFound, store.ErrUnauthorized, store.ErrNotDeclared,
		store.ErrHashMismatch, store.ErrTooLarge, store.ErrInvalidAttachmentData} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

func (s *server) attachmentError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotDeclared):
		text(w, http.StatusBadRequest, "Attachment not declared")
	case errors.Is(err, store.ErrHashMismatch):
		text(w, http.StatusBadRequest, "Hash mismatch")
	case errors.Is(err, store.ErrInvalidAttachmentData):
		text(w, http.StatusBadRequest, "Invalid attachment")
	case errors.Is(err, store.ErrTooLarge):
		text(w, http.StatusRequestEntityTooLarge, "Attachment too large")
	default:
		s.storeError(w, err)
	}
}

// splitAttachment splits "<id>/<name>"; both parts are validated.
func splitAttachment(v string) (id, name string, ok bool) {
	id, name, found := strings.Cut(v, "/")
	return id, name, found && store.ValidID(id) && store.ValidAttachmentName(name)
}

func (s *server) getAttachment(w http.ResponseWriter, r *http.Request, v string) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	h.Set("Cross-Origin-Resource-Policy", "same-origin")
	// Revalidate on every use so that deleting or expiring a share takes effect at once.
	h.Set("Cache-Control", "no-cache")
	id, name, found := strings.Cut(v, "/")
	if !found || !store.ValidID(id) {
		text(w, http.StatusNotFound, "Not found")
		return
	}
	f, encrypted, err := s.Store.Attachment(id, name)
	if err == nil || errors.Is(err, store.ErrNoAttachment) {
		// The share exists: a note with many images, or with images not yet
		// uploaded, must not use up the read limits. Probing ids stays limited.
		freeRead(r)
	}
	if err != nil {
		s.storeError(w, err)
		return
	}
	defer f.Close()
	if encrypted {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", `attachment; filename="attachment.bin"`)
	} else {
		h.Set("Content-Type", store.AttachmentContentType(name))
	}
	h.Set("ETag", `"`+name[:64]+`"`) // content addressed
	http.ServeContent(w, r, "", time.Time{}, f)
}

// validToken compares the bearer token in constant time.
func (s *server) validToken(r *http.Request) bool {
	if s.Token == "" {
		return false
	}
	got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return false
	}
	sum := sha256.Sum256([]byte(got))
	return subtle.ConstantTimeCompare(sum[:], s.tokenHash[:]) == 1
}

// parseExpiresIn accepts an absent field (set is false), null or 0 (never
// expires) and integers from 1 to store.MaxExpiresIn seconds.
func parseExpiresIn(raw json.RawMessage) (n int64, set, ok bool) {
	if len(raw) == 0 {
		return 0, false, true
	}
	if string(raw) == "null" {
		return 0, true, true
	}
	if err := json.Unmarshal(raw, &n); err != nil || n < 0 || n > store.MaxExpiresIn {
		return 0, false, false
	}
	return n, true, true
}

// parseEncrypted accepts an absent field (nil: unchanged / false) or a JSON
// boolean; null and every other type are rejected.
func parseEncrypted(raw json.RawMessage) (*bool, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	var b bool
	if err := json.Unmarshal(raw, &b); err != nil || string(raw) == "null" {
		return nil, false
	}
	return &b, true
}

// writeBody is the decoded JSON body of POST and PATCH.
type writeBody struct {
	content     *string // nil only for an expiry-only PATCH
	expiresIn   int64
	setExpiry   bool // expiresIn was given (null counts)
	encrypted   *bool
	attachments []string // nil = absent
}

// readBody decodes and validates the request body, answering with an error
// itself when it is bad. POST needs content; PATCH needs content or
// expiresIn, and encrypted/attachments only go with content.
func (s *server) readBody(w http.ResponseWriter, r *http.Request) (writeBody, bool) {
	var body struct {
		Content     *string         `json:"content"`
		ExpiresIn   json.RawMessage `json:"expiresIn"`
		Encrypted   json.RawMessage `json:"encrypted"`
		Attachments json.RawMessage `json:"attachments"`
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.MaxBodyBytes)).Decode(&body)
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		text(w, http.StatusRequestEntityTooLarge, "Request too large")
		return writeBody{}, false
	}
	if err != nil || (body.Content == nil && (r.Method == http.MethodPost || len(body.ExpiresIn) == 0)) {
		text(w, http.StatusBadRequest, "No content")
		return writeBody{}, false
	}
	req := writeBody{content: body.Content}
	var ok bool
	if req.encrypted, ok = parseEncrypted(body.Encrypted); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid encrypted"})
		return writeBody{}, false
	}
	if req.attachments, ok = parseAttachments(body.Attachments); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": store.ErrInvalidAttachments.Error()})
		return writeBody{}, false
	}
	if req.expiresIn, req.setExpiry, ok = parseExpiresIn(body.ExpiresIn); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid expiresIn"})
		return writeBody{}, false
	}
	if req.content == nil && (req.encrypted != nil || req.attachments != nil) {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "encrypted and attachments need content"})
		return writeBody{}, false
	}
	return req, true
}

// parseAttachments accepts an absent field (nil) or an array of strings
// (an empty array is non-nil); null and other types are rejected.
func parseAttachments(raw json.RawMessage) ([]string, bool) {
	if len(raw) == 0 {
		return nil, true
	}
	var names []string
	if err := json.Unmarshal(raw, &names); err != nil || names == nil {
		return nil, false
	}
	return names, true
}

func (s *server) storeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrInvalidID):
		text(w, http.StatusBadRequest, "Invalid id")
	case errors.Is(err, store.ErrNotFound):
		text(w, http.StatusNotFound, "Not found")
	case errors.Is(err, store.ErrUnauthorized):
		text(w, http.StatusUnauthorized, "Unauthorized")
	default:
		s.Logger.Error("store", "err", err)
		text(w, http.StatusInternalServerError, "Internal error")
	}
}

func text(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(code)
	io.WriteString(w, msg)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		code, b = http.StatusInternalServerError, []byte(`{"error":"Internal error"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	w.Write(b)
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func (s *server) logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.Logger.Info("request", "method", r.Method, "path", r.URL.Path, "status", rec.status,
			"ip", clientIP(r, s.TrustedProxies), "duration", time.Since(start))
	})
}
