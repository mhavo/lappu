// Package store keeps shares on disk as <id>.md (content) and <id>.json (meta).
package store

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"hash/fnv"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

var (
	ErrNotFound     = errors.New("not found")
	ErrUnauthorized = errors.New("unauthorized")
	ErrInvalidID    = errors.New("invalid id")
	// ErrNotCiphertext is returned when an encrypted share's content is not in
	// the SimpleTextCrypt text form, which would mean a client leaks plaintext.
	ErrNotCiphertext = errors.New("encrypted content must be SimpleTextCrypt base64 text")
	// ErrInvalidAttachments is returned for a bad attachment name or too many names.
	ErrInvalidAttachments = errors.New("invalid attachments")
	// ErrNoContent is returned for a patch without content that is not an
	// expiry-only change (encrypted and attachments need content).
	ErrNoContent = errors.New("no content")
	// ErrNotDeclared is returned when an uploaded attachment is not in the share's list.
	ErrNotDeclared = errors.New("attachment not declared")
	// ErrHashMismatch is returned when the uploaded bytes do not hash to the name.
	ErrHashMismatch = errors.New("attachment hash mismatch")
	// ErrInvalidAttachmentData is returned when an uploaded attachment is too short
	// to be a ciphertext packet or does not match its extension's image format.
	ErrInvalidAttachmentData = errors.New("invalid attachment data")
	// ErrTooLarge is returned when an attachment exceeds the per-file or per-share limit.
	ErrTooLarge = errors.New("attachment too large")
)

// Attachment limits when SetAttachmentLimits is not called.
const (
	DefaultMaxAttachmentBytes      = 10 << 20
	DefaultMaxShareAttachmentBytes = 50 << 20
	// MaxAttachments is the most attachments one share can declare.
	MaxAttachments = 100
)

var attachmentPattern = regexp.MustCompile(`^[a-f0-9]{64}\.(png|jpe?g|gif|webp|avif)$`)

// ValidAttachmentName reports whether name is <sha256 hex>.<image ext>.
func ValidAttachmentName(name string) bool { return attachmentPattern.MatchString(name) }

// AttachmentContentType is the MIME type for a valid attachment name's extension.
func AttachmentContentType(name string) string {
	switch name[strings.LastIndexByte(name, '.')+1:] {
	case "png":
		return "image/png"
	case "jpg", "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	case "webp":
		return "image/webp"
	case "avif":
		return "image/avif"
	}
	return "application/octet-stream"
}

// normalizeAttachments validates names and removes duplicates, keeping order.
func normalizeAttachments(names []string) ([]string, error) {
	out := make([]string, 0, len(names))
	seen := make(map[string]bool, len(names))
	for _, n := range names {
		if !ValidAttachmentName(n) {
			return nil, ErrInvalidAttachments
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	if len(out) > MaxAttachments {
		return nil, ErrInvalidAttachments
	}
	return out, nil
}

// Sizes of the SimpleTextCrypt packet parts: AES-GCM IV and tag.
const (
	ivBytes  = 12
	tagBytes = 16
	// minEncryptedAttachment is the smallest valid encrypted attachment (IV + tag, empty plaintext).
	minEncryptedAttachment = ivBytes + tagBytes
)

// validCiphertext checks the SimpleTextCrypt text form structurally: without
// whitespace, a 16 character base64 IV (12 bytes) followed by base64 of at
// least a tag (16 bytes). It cannot tell ciphertext from random base64, but
// it rejects ordinary text a client might send by mistake.
func validCiphertext(s string) bool {
	s = strings.Join(strings.Fields(s), "")
	if len(s) <= 16 {
		return false
	}
	iv, err := base64.StdEncoding.DecodeString(s[:16])
	if err != nil || len(iv) != ivBytes {
		return false
	}
	body, err := base64.StdEncoding.DecodeString(s[16:])
	return err == nil && len(body) >= tagBytes
}

// validImage reports whether head (the first bytes of a file) matches the
// magic bytes of the image format of ext.
func validImage(ext string, head []byte) bool {
	switch ext {
	case "png":
		return bytes.HasPrefix(head, []byte("\x89PNG\r\n\x1a\n"))
	case "jpg", "jpeg":
		return bytes.HasPrefix(head, []byte("\xFF\xD8\xFF"))
	case "gif":
		return bytes.HasPrefix(head, []byte("GIF87a")) || bytes.HasPrefix(head, []byte("GIF89a"))
	case "webp":
		return len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP"
	case "avif":
		// ISO BMFF: size, "ftyp", major brand, minor version, compatible brands.
		if len(head) < 16 || string(head[4:8]) != "ftyp" {
			return false
		}
		size := int(min(binary.BigEndian.Uint32(head[:4]), uint32(len(head))))
		isAvif := func(b []byte) bool { return string(b) == "avif" || string(b) == "avis" }
		if isAvif(head[8:12]) {
			return true
		}
		for i := 16; i+4 <= size; i += 4 {
			if isAvif(head[i : i+4]) {
				return true
			}
		}
	}
	return false
}

var idPattern = regexp.MustCompile(`^[A-Za-z0-9]{8,64}$`)

// ValidID reports whether id is safe to use as a file name stem.
func ValidID(id string) bool { return idPattern.MatchString(id) }

// MetaVersion is the schema version written by this server.
const MetaVersion = 2

// Meta is the on-disk metadata of a share. Version 1 (PHP) files only have
// id, password (plaintext) and created; they are migrated on successful auth.
type Meta struct {
	Version        int    `json:"version"`
	ID             string `json:"id"`
	PasswordSHA256 string `json:"password_sha256,omitempty"`
	LegacyPassword string `json:"password,omitempty"`
	Created        int64  `json:"created"`
	Updated        int64  `json:"updated,omitempty"`
	Expires        *int64 `json:"expires"`
	Encrypted      bool   `json:"encrypted"`
	// Attachments are the names the note references; files live in <id>.att/.
	Attachments []string `json:"attachments,omitempty"`
}

// Share is returned once on creation; Password is never stored in plaintext.
type Share struct {
	ID        string `json:"id"`
	Password  string `json:"password"`
	Created   int64  `json:"created"`
	Expires   *int64 `json:"expires"` // unix seconds, null = never
	Encrypted bool   `json:"encrypted"`
	// Missing are the declared attachments that are not stored yet.
	Missing []string `json:"missing"`
}

// UpdateResult is what a successful update reports.
type UpdateResult struct {
	Encrypted bool     `json:"encrypted"`
	Missing   []string `json:"missing"`
	Expires   *int64   `json:"expires"` // unix seconds, null = never
}

// MaxExpiresIn is the longest allowed lifetime of a share, in seconds (365 days).
const MaxExpiresIn = 365 * 24 * 60 * 60

type Store struct {
	dir   string
	locks [256]sync.Mutex
	now   func() time.Time

	maxAttachment, maxShareAttachments int64
}

// New opens (and creates, mode 0700) the data directory.
func New(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return &Store{dir: dir, now: time.Now,
		maxAttachment: DefaultMaxAttachmentBytes, maxShareAttachments: DefaultMaxShareAttachmentBytes}, nil
}

// SetClock replaces the time source; for tests, call before first use.
func (s *Store) SetClock(now func() time.Time) { s.now = now }

// SetAttachmentLimits sets the per-file and per-share byte limits; call before first use.
func (s *Store) SetAttachmentLimits(perFile, perShare int64) {
	s.maxAttachment, s.maxShareAttachments = perFile, perShare
}

// MaxAttachmentBytes is the per-file limit.
func (s *Store) MaxAttachmentBytes() int64 { return s.maxAttachment }

// expired reports whether m has an expiry that has been reached.
func (s *Store) expired(m Meta) bool {
	return m.Expires != nil && s.now().Unix() >= *m.Expires
}

func (s *Store) lock(id string) func() {
	h := fnv.New32a()
	h.Write([]byte(id))
	m := &s.locks[h.Sum32()%uint32(len(s.locks))]
	m.Lock()
	return m.Unlock
}

func (s *Store) contentPath(id string) string { return filepath.Join(s.dir, id+".md") }
func (s *Store) metaPath(id string) string    { return filepath.Join(s.dir, id+".json") }
func (s *Store) attDir(id string) string      { return filepath.Join(s.dir, id+".att") }

func (s *Store) Content(id string) ([]byte, error) {
	if !ValidID(id) {
		return nil, ErrInvalidID
	}
	// A missing meta does not hide the content, but expiry does, and an
	// unreadable meta must not let an expired share through.
	m, err := s.readMeta(id)
	if err == nil && s.expired(m) {
		return nil, ErrNotFound
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	b, err := os.ReadFile(s.contentPath(id))
	return b, notFound(err)
}

func (s *Store) Meta(id string) (Meta, error) {
	if !ValidID(id) {
		return Meta{}, ErrInvalidID
	}
	m, err := s.readMeta(id)
	if err == nil && s.expired(m) {
		return Meta{}, ErrNotFound
	}
	return m, err
}

func (s *Store) readMeta(id string) (Meta, error) {
	b, err := os.ReadFile(s.metaPath(id))
	if err != nil {
		return Meta{}, notFound(err)
	}
	var m Meta
	if err := json.Unmarshal(b, &m); err != nil {
		return Meta{}, fmt.Errorf("meta %s: %w", id, err)
	}
	m.ID = id // the validated file name is authoritative, never the file content
	return m, nil
}

// Create stores a new share. expiresIn is the lifetime in seconds (validated
// by the caller against MaxExpiresIn); 0 means it never expires.
func (s *Store) Create(content string, expiresIn int64) (Share, error) {
	return s.CreateWith(content, expiresIn, false)
}

// CreateWith is Create with the encrypted flag. The server never decrypts: for
// an encrypted share it only checks that content has the ciphertext text form.
func (s *Store) CreateWith(content string, expiresIn int64, encrypted bool) (Share, error) {
	return s.CreateFull(content, expiresIn, encrypted, nil)
}

// CreateFull is CreateWith with the attachment names the note references.
func (s *Store) CreateFull(content string, expiresIn int64, encrypted bool, attachments []string) (Share, error) {
	if encrypted && !validCiphertext(content) {
		return Share{}, ErrNotCiphertext
	}
	attachments, err := normalizeAttachments(attachments)
	if err != nil {
		return Share{}, err
	}
	password := randomHex(16)
	for {
		id := randomHex(12)
		unlock := s.lock(id)
		if _, err := os.Stat(s.metaPath(id)); !errors.Is(err, fs.ErrNotExist) {
			unlock()
			if err != nil {
				return Share{}, err
			}
			continue // collision, practically impossible
		}
		now := s.now().Unix()
		m := Meta{ID: id, PasswordSHA256: hashPassword(password), Created: now, Updated: now, Encrypted: encrypted, Attachments: attachments}
		if expiresIn > 0 {
			exp := now + expiresIn
			m.Expires = &exp
		}
		err := s.writeContent(id, content)
		if err == nil {
			err = s.writeMeta(id, m)
		}
		unlock()
		if err != nil {
			return Share{}, err
		}
		return Share{ID: id, Password: password, Created: now, Expires: m.Expires, Encrypted: encrypted,
			Missing: s.missing(id, attachments)}, nil
	}
}

func (s *Store) Update(id, password, content string) error {
	return s.UpdateWith(id, password, content, nil)
}

// UpdateWith is Update with an optional encrypted flag; nil leaves it unchanged.
// The content is checked against the resulting flag.
func (s *Store) UpdateWith(id, password, content string, encrypted *bool) error {
	_, err := s.UpdateFull(id, password, content, encrypted, nil)
	return err
}

// UpdateFull is UpdateWith plus the full attachment list (nil leaves it
// unchanged). Stored files no longer declared are deleted, and all of them
// when the encrypted flag flips, since stored bytes are in the old form.
func (s *Store) UpdateFull(id, password, content string, encrypted *bool, attachments []string) (UpdateResult, error) {
	return s.Patch(id, password, Patch{Content: &content, Encrypted: encrypted, Attachments: attachments})
}

// Patch is a partial update of a share; nil fields are left unchanged.
// Encrypted and Attachments can only be set together with Content.
type Patch struct {
	Content     *string
	Encrypted   *bool
	Attachments []string
	// ExpiresIn sets a new lifetime counted from now (0 = never expires);
	// the caller checks it against MaxExpiresIn.
	ExpiresIn *int64
}

// Patch applies p like UpdateFull and, with ExpiresIn, changes the expiry.
// Expired shares are not found and cannot be revived.
func (s *Store) Patch(id, password string, p Patch) (UpdateResult, error) {
	if !ValidID(id) {
		return UpdateResult{}, ErrInvalidID
	}
	if p.Content == nil && (p.Encrypted != nil || p.Attachments != nil) {
		return UpdateResult{}, ErrNoContent
	}
	if p.Content == nil && p.ExpiresIn == nil {
		return UpdateResult{}, ErrNoContent
	}
	var names []string
	if p.Attachments != nil {
		var err error
		if names, err = normalizeAttachments(p.Attachments); err != nil {
			return UpdateResult{}, err
		}
	}
	defer s.lock(id)()
	m, err := s.authorize(id, password)
	if err != nil {
		return UpdateResult{}, err
	}
	now := s.now().Unix()
	if p.ExpiresIn != nil {
		m.Expires = nil
		if *p.ExpiresIn > 0 {
			exp := now + *p.ExpiresIn
			m.Expires = &exp
		}
	}
	flipped := false
	if p.Content != nil {
		if p.Encrypted != nil {
			flipped = m.Encrypted != *p.Encrypted
			m.Encrypted = *p.Encrypted
		}
		if m.Encrypted && !validCiphertext(*p.Content) {
			return UpdateResult{}, ErrNotCiphertext
		}
		if p.Attachments != nil {
			m.Attachments = names
		}
		if err := s.writeContent(id, *p.Content); err != nil {
			return UpdateResult{}, err
		}
		m.Updated = now
	}
	if err := s.writeMeta(id, m); err != nil {
		return UpdateResult{}, err
	}
	if p.Content != nil {
		s.pruneAttachments(id, m.Attachments, flipped)
	}
	return UpdateResult{Encrypted: m.Encrypted, Missing: s.missing(id, m.Attachments), Expires: m.Expires}, nil
}

func (s *Store) Delete(id, password string) error {
	if !ValidID(id) {
		return ErrInvalidID
	}
	defer s.lock(id)()
	if m, err := s.readMeta(id); err == nil && s.expired(m) {
		// Behaves as missing, whatever the password; clean up while we are here.
		s.removeFiles(id)
		return ErrNotFound
	}
	if _, err := s.authorize(id, password); err != nil {
		return err
	}
	// Attachments, content, meta: a crash in between leaves an orphan meta, not a readable share.
	if err := s.removeFiles(id); err != nil {
		return err
	}
	return nil
}

// authorize checks password and returns the meta upgraded to the current
// schema. Callers that write the meta back complete the lazy migration.
func (s *Store) authorize(id, password string) (Meta, error) {
	m, err := s.readMeta(id)
	if err != nil {
		return Meta{}, err
	}
	if s.expired(m) {
		return Meta{}, ErrNotFound
	}
	switch {
	case m.PasswordSHA256 != "":
		want, err := hex.DecodeString(m.PasswordSHA256)
		got := sha256.Sum256([]byte(password))
		if err != nil || subtle.ConstantTimeCompare(want, got[:]) != 1 {
			return Meta{}, ErrUnauthorized
		}
	case m.LegacyPassword != "":
		if subtle.ConstantTimeCompare([]byte(m.LegacyPassword), []byte(password)) != 1 {
			return Meta{}, ErrUnauthorized
		}
		m.PasswordSHA256 = hashPassword(password)
		m.LegacyPassword = ""
	default:
		return Meta{}, ErrUnauthorized
	}
	return m, nil
}

// removeFiles deletes everything of a share; the caller holds the id lock.
// Attachments go first, then content, and the meta stays if one fails, so
// readable content or attachments never outlive the meta (which Content
// would serve as an unexpired share).
func (s *Store) removeFiles(id string) error {
	if err := os.RemoveAll(s.attDir(id)); err != nil {
		return err
	}
	if err := os.Remove(s.contentPath(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	if err := os.Remove(s.metaPath(id)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// DeleteExpired removes every expired share and returns how many it removed.
// Unreadable or malformed metas are skipped, never deleted.
func (s *Store) DeleteExpired() (int, error) {
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return 0, err
	}
	n := 0
	var firstErr error
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), tempPrefix) || strings.HasPrefix(e.Name(), ".tmp-") {
			if info, err := e.Info(); err == nil && s.now().Sub(info.ModTime()) > staleTempAge {
				os.Remove(filepath.Join(s.dir, e.Name())) // leftover of a crash
			}
			continue
		}
		id, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !ValidID(id) {
			continue
		}
		unlock := s.lock(id)
		m, err := s.readMeta(id)
		if err == nil && s.expired(m) {
			if err := s.removeFiles(id); err != nil {
				if firstErr == nil {
					firstErr = err
				}
			} else {
				n++
			}
		}
		unlock()
	}
	return n, firstErr
}

// CleanupLoop runs DeleteExpired at once and then every interval until ctx is done.
func (s *Store) CleanupLoop(ctx context.Context, interval time.Duration, logger *slog.Logger) {
	run := func() {
		n, err := s.DeleteExpired()
		if err != nil {
			logger.Error("expired cleanup", "err", err)
		}
		if n > 0 {
			logger.Info("deleted expired shares", "count", n)
		}
	}
	run()
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			run()
		}
	}
}

// missing returns the declared attachments that have no stored file (never nil).
func (s *Store) missing(id string, declared []string) []string {
	out := []string{}
	for _, n := range declared {
		if _, err := os.Stat(filepath.Join(s.attDir(id), n)); err != nil {
			out = append(out, n)
		}
	}
	return out
}

// pruneAttachments deletes stored files that are not declared (all when all is
// set). Failures are not fatal: a leftover file is unreachable through GET.
func (s *Store) pruneAttachments(id string, declared []string, all bool) {
	entries, err := os.ReadDir(s.attDir(id))
	if err != nil {
		return
	}
	keep := map[string]bool{}
	if !all {
		for _, n := range declared {
			keep[n] = true
		}
	}
	for _, e := range entries {
		if !keep[e.Name()] {
			os.Remove(filepath.Join(s.attDir(id), e.Name()))
		}
	}
}

func contains(list []string, name string) bool {
	for _, n := range list {
		if n == name {
			return true
		}
	}
	return false
}

// CheckAttachment authorizes an upload and reports whether the file is
// already stored. It fails with ErrNotDeclared if name is not in the share's list.
func (s *Store) CheckAttachment(id, password, name string) (stored bool, err error) {
	_, stored, err = s.checkAttachment(id, password, name)
	return stored, err
}

func (s *Store) checkAttachment(id, password, name string) (m Meta, stored bool, err error) {
	if !ValidID(id) {
		return Meta{}, false, ErrInvalidID
	}
	defer s.lock(id)()
	m, err = s.authorize(id, password)
	if err != nil {
		return Meta{}, false, err
	}
	if !ValidAttachmentName(name) || !contains(m.Attachments, name) {
		return Meta{}, false, ErrNotDeclared
	}
	_, err = os.Stat(filepath.Join(s.attDir(id), name))
	return m, err == nil, nil
}

// PutAttachment stores data as the declared attachment name; see PutAttachmentFrom.
func (s *Store) PutAttachment(id, password, name string, data []byte) error {
	return s.PutAttachmentFrom(id, password, name, bytes.NewReader(data))
}

// tempPrefix names in-flight uploads in the data directory. The dot prefix
// never matches an ID or an attachment name, so they are never served or counted.
const tempPrefix = ".upload-"

// staleTempAge is how old a leftover temp file must be before cleanup removes it.
const staleTempAge = time.Hour

// PutAttachmentFrom stores r as the declared attachment name after checking
// the password, the size limits, that sha256 of the bytes matches the name and
// that the bytes fit the kind (an image of the extension's format, or at least
// a ciphertext packet for an encrypted share). The body is spooled to a temp
// file without holding the id lock; everything is checked again under it
// before the rename. Storing a file that already exists succeeds without writing.
// Errors from r are returned as they are.
func (s *Store) PutAttachmentFrom(id, password, name string, r io.Reader) error {
	if !ValidID(id) {
		return ErrInvalidID
	}
	if _, stored, err := s.checkAttachment(id, password, name); err != nil || stored {
		return err
	}
	tmp, err := os.CreateTemp(s.dir, tempPrefix+"*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name()) // a no-op after the rename
	h := sha256.New()
	n, err := s.spool(tmp, h, r)
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}

	defer s.lock(id)()
	m, err := s.authorize(id, password)
	if err != nil {
		return err
	}
	if !ValidAttachmentName(name) || !contains(m.Attachments, name) {
		return ErrNotDeclared
	}
	dir := s.attDir(id)
	path := filepath.Join(dir, name)
	if _, err := os.Stat(path); err == nil {
		return nil
	}
	if hex.EncodeToString(h.Sum(nil)) != name[:64] {
		return ErrHashMismatch
	}
	if m.Encrypted {
		if n < minEncryptedAttachment {
			return ErrInvalidAttachmentData
		}
	} else {
		f, err := os.Open(tmp.Name())
		if err != nil {
			return err
		}
		head := make([]byte, 512)
		hn, _ := io.ReadFull(f, head)
		f.Close()
		if !validImage(name[strings.LastIndexByte(name, '.')+1:], head[:hn]) {
			return ErrInvalidAttachmentData
		}
	}
	total := n
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	for _, e := range entries {
		if !ValidAttachmentName(e.Name()) {
			continue
		}
		if info, err := e.Info(); err == nil {
			total += info.Size()
		}
	}
	if total > s.maxShareAttachments {
		return ErrTooLarge
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return err
	}
	if d, derr := os.Open(dir); derr == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

// spool copies r to f and into h, failing with ErrTooLarge past the per-file limit.
func (s *Store) spool(f *os.File, h hash.Hash, r io.Reader) (int64, error) {
	n, err := io.Copy(f, io.LimitReader(io.TeeReader(r, h), s.maxAttachment+1))
	if err != nil {
		return n, err
	}
	if n > s.maxAttachment {
		return n, ErrTooLarge
	}
	return n, f.Sync()
}

// Attachment opens a stored attachment of a live share. encrypted tells
// whether the bytes are a ciphertext packet. The caller closes the file.
func (s *Store) Attachment(id, name string) (f *os.File, encrypted bool, err error) {
	if !ValidID(id) {
		return nil, false, ErrInvalidID
	}
	if !ValidAttachmentName(name) {
		return nil, false, ErrNotFound
	}
	m, err := s.Meta(id) // hides expired shares
	if err != nil {
		return nil, false, err
	}
	if !contains(m.Attachments, name) {
		return nil, false, ErrNotFound
	}
	f, err = os.Open(filepath.Join(s.attDir(id), name))
	if err != nil {
		return nil, false, notFound(err)
	}
	return f, m.Encrypted, nil
}

func (s *Store) writeContent(id, content string) error {
	return writeAtomic(s.contentPath(id), []byte(content))
}

// writeMeta stores m under the already validated id.
func (s *Store) writeMeta(id string, m Meta) error {
	m.Version = MetaVersion
	m.ID = id
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return writeAtomic(s.metaPath(id), b)
}

// writeAtomic writes via a temp file in the same directory, fsyncs it and
// renames it over path, so readers see either the old or the new file.
func writeAtomic(path string, data []byte) (err error) {
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, ".tmp-*") // mode 0600; dot prefix never matches an ID
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(f.Name())
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	if d, derr := os.Open(dir); derr == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

func notFound(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return ErrNotFound
	}
	return err
}

func hashPassword(p string) string {
	sum := sha256.Sum256([]byte(p))
	return hex.EncodeToString(sum[:])
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b) // never returns an error since Go 1.24
	return hex.EncodeToString(b)
}
