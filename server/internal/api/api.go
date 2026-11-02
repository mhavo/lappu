// Package api serves the share.php-compatible HTTP API and the viewer.
package api

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"net/netip"
	"os"
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
	Logger         *slog.Logger
}

type server struct {
	Config
	tokenHash [32]byte
	limiter   *limiter // failed auth
	creates   *limiter // anonymous creates
}

// New returns the complete HTTP handler.
func New(cfg Config) http.Handler {
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	s := &server{
		Config:    cfg,
		tokenHash: sha256.Sum256([]byte(cfg.Token)),
		limiter:   newLimiter(failureBurst, failureWindow, time.Now),
		creates:   newLimiter(anonCreateBurst, anonCreateWindow, time.Now),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/share.php", s.share)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		text(w, http.StatusOK, "ok")
	})
	mux.Handle("/", static(cfg.Static))
	return s.logRequests(securityHeaders(mux))
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if r.URL.Path == "/share.php" || r.URL.Path == "/healthz" {
			h.Set("Content-Security-Policy", apiCSP)
		} else {
			h.Set("Content-Security-Policy", viewerCSP)
		}
		next.ServeHTTP(w, r)
	})
}

func static(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
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
		files.ServeHTTP(w, r)
	})
}

func (s *server) share(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	switch r.Method {
	case http.MethodGet, http.MethodHead:
		switch {
		case q.Has("quote"):
			s.quote(w)
		case q.Has("att"):
			s.getAttachment(w, r, q.Get("att"))
		case q.Has("meta"):
			s.meta(w, q.Get("id"))
		default:
			s.get(w, q.Get("id"))
		}
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
		if share, err = s.Store.CreateFull(req.content, req.expiresIn, req.encrypted != nil && *req.encrypted, req.attachments); err == nil {
			if !trusted {
				s.creates.charge(ip)
			}
			writeJSON(w, http.StatusOK, share)
			return
		}
	case http.MethodPatch:
		var res store.UpdateResult
		if res, err = s.Store.UpdateFull(id, password, req.content, req.encrypted, req.attachments); err == nil {
			writeJSON(w, http.StatusOK, res)
			return
		}
	case http.MethodDelete:
		err = s.Store.Delete(id, password)
	}
	if errors.Is(err, store.ErrUnauthorized) && !trusted {
		s.limiter.charge(ip)
	}
	if errors.Is(err, store.ErrNotCiphertext) || errors.Is(err, store.ErrInvalidAttachments) {
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
	id, name, ok := splitAttachment(v)
	if !ok {
		text(w, http.StatusNotFound, "Not found")
		return
	}
	f, encrypted, err := s.Store.Attachment(id, name)
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

// parseExpiresIn accepts an absent field, null or 0 (never expires) and
// integers from 1 to store.MaxExpiresIn seconds.
func parseExpiresIn(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, true
	}
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil || n < 0 || n > store.MaxExpiresIn {
		return 0, false
	}
	return n, true
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
	content     string
	expiresIn   int64
	encrypted   *bool
	attachments []string // nil = absent
}

// readBody decodes and validates the request body, answering with an error
// itself when it is bad. expiresIn is only read for POST (PATCH ignores it).
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
	if err != nil || body.Content == nil {
		text(w, http.StatusBadRequest, "No content")
		return writeBody{}, false
	}
	req := writeBody{content: *body.Content}
	var ok bool
	if req.encrypted, ok = parseEncrypted(body.Encrypted); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid encrypted"})
		return writeBody{}, false
	}
	if req.attachments, ok = parseAttachments(body.Attachments); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": store.ErrInvalidAttachments.Error()})
		return writeBody{}, false
	}
	if r.Method == http.MethodPost {
		if req.expiresIn, ok = parseExpiresIn(body.ExpiresIn); !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "Invalid expiresIn"})
			return writeBody{}, false
		}
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
