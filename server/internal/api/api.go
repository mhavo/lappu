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
	"script-src 'self' https://cdn.jsdelivr.net/npm/ https://cdn.jsdelivr.net/gh/highlightjs/; " +
	"style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net/npm/ https://cdn.jsdelivr.net/gh/highlightjs/ https://fonts.googleapis.com; " +
	"font-src 'self' data: https://fonts.gstatic.com https://cdn.jsdelivr.net/npm/; " +
	"img-src 'self' data: https:; connect-src 'self'; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

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
		case q.Has("meta"):
			s.meta(w, q.Get("id"))
		default:
			s.get(w, q.Get("id"))
		}
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
			ID      string `json:"id"`
			Created int64  `json:"created"`
		}{m.ID, m.Created})
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

func (s *server) write(w http.ResponseWriter, r *http.Request) {
	ip := limitKey(clientIP(r, s.TrustedProxies))
	trusted := s.validToken(r)
	if !trusted {
		if s.limiter.blocked(ip) || (r.Method == http.MethodPost && s.creates.blocked(ip)) {
			text(w, http.StatusTooManyRequests, "Too many requests")
			return
		}
		if !s.AllowAnonymous {
			s.limiter.charge(ip)
			s.Logger.Warn("invalid token", "ip", ip)
			text(w, http.StatusUnauthorized, "Invalid token")
			return
		}
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

	var content string
	if r.Method != http.MethodDelete {
		var ok bool
		if content, ok = s.readContent(w, r); !ok {
			return
		}
	}

	var err error
	switch r.Method {
	case http.MethodPost:
		var share store.Share
		if share, err = s.Store.Create(content); err == nil {
			if !trusted {
				s.creates.charge(ip)
			}
			writeJSON(w, http.StatusOK, share)
			return
		}
	case http.MethodPatch:
		err = s.Store.Update(id, password, content)
	case http.MethodDelete:
		err = s.Store.Delete(id, password)
	}
	if errors.Is(err, store.ErrUnauthorized) && !trusted {
		s.limiter.charge(ip)
	}
	if err != nil {
		s.storeError(w, err)
	}
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

func (s *server) readContent(w http.ResponseWriter, r *http.Request) (string, bool) {
	var body struct {
		Content *string `json:"content"`
	}
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, s.MaxBodyBytes)).Decode(&body)
	if _, tooLarge := errors.AsType[*http.MaxBytesError](err); tooLarge {
		text(w, http.StatusRequestEntityTooLarge, "Request too large")
		return "", false
	}
	if err != nil || body.Content == nil {
		text(w, http.StatusBadRequest, "No content")
		return "", false
	}
	return *body.Content, true
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
