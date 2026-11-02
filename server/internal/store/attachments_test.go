package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func attName(data []byte, ext string) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]) + "." + ext
}

func TestValidAttachmentName(t *testing.T) {
	h := strings.Repeat("a", 64)
	for _, ext := range []string{"png", "jpg", "jpeg", "gif", "webp", "avif"} {
		if !ValidAttachmentName(h + "." + ext) {
			t.Errorf("%s rejected", ext)
		}
	}
	for _, n := range []string{"", h, h + ".svg", h + ".PNG", h + ".png ", h + ".png\n",
		strings.Repeat("A", 64) + ".png", strings.Repeat("a", 63) + ".png", strings.Repeat("a", 65) + ".png",
		"../" + h + ".png", h + "/x.png", h + ".png.png", h + ".gif/../x"} {
		if ValidAttachmentName(n) {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestAttachmentContentType(t *testing.T) {
	h := strings.Repeat("0", 64)
	want := map[string]string{"png": "image/png", "jpg": "image/jpeg", "jpeg": "image/jpeg",
		"gif": "image/gif", "webp": "image/webp", "avif": "image/avif"}
	for ext, mime := range want {
		if got := AttachmentContentType(h + "." + ext); got != mime {
			t.Errorf("%s: %s", ext, got)
		}
	}
}

func TestCreateDeclaresAttachments(t *testing.T) {
	s, dir := newStore(t)
	a, b := attName([]byte("a"), "png"), attName([]byte("b"), "jpg")
	sh, err := s.CreateFull("x", 0, false, []string{a, b, a})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sh.Missing, []string{a, b}) {
		t.Errorf("missing %v", sh.Missing)
	}
	if got := readMeta(t, dir, sh.ID)["attachments"]; len(got.([]any)) != 2 {
		t.Errorf("meta attachments %v", got)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, a, []byte("a")); err != nil {
		t.Fatal(err)
	}
	res, err := s.UpdateFull(sh.ID, sh.Password, "y", nil, nil)
	if err != nil || !slices.Equal(res.Missing, []string{b}) {
		t.Errorf("%+v %v", res, err)
	}
	plain, _ := s.CreateFull("x", 0, false, nil)
	if plain.Missing == nil || len(plain.Missing) != 0 {
		t.Errorf("missing %#v, want empty non-nil", plain.Missing)
	}
}

func TestInvalidAttachmentLists(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.CreateFull("x", 0, false, []string{"evil.png"}); !errors.Is(err, ErrInvalidAttachments) {
		t.Errorf("bad name: %v", err)
	}
	var many []string
	for i := range MaxAttachments + 1 {
		many = append(many, attName([]byte{byte(i)}, "png"))
	}
	if _, err := s.CreateFull("x", 0, false, many); !errors.Is(err, ErrInvalidAttachments) {
		t.Errorf("101 names: %v", err)
	}
	if _, err := s.CreateFull("x", 0, false, many[:MaxAttachments]); err != nil {
		t.Errorf("100 names: %v", err)
	}
	// duplicates do not count twice
	dup := append(slices.Clone(many[:MaxAttachments]), many[0])
	if _, err := s.CreateFull("x", 0, false, dup); err != nil {
		t.Errorf("deduped: %v", err)
	}
	sh, _ := s.Create("x", 0)
	if _, err := s.UpdateFull(sh.ID, sh.Password, "y", nil, []string{"../x"}); !errors.Is(err, ErrInvalidAttachments) {
		t.Errorf("update: %v", err)
	}
}

func newShareWith(t *testing.T, s *Store, datas ...string) (Share, []string) {
	t.Helper()
	var names []string
	for _, d := range datas {
		names = append(names, attName([]byte(d), "png"))
	}
	sh, err := s.CreateFull("x", 0, false, names)
	if err != nil {
		t.Fatal(err)
	}
	return sh, names
}

func TestPutAttachment(t *testing.T) {
	s, dir := newStore(t)
	sh, names := newShareWith(t, s, "hello")
	if err := s.PutAttachment(sh.ID, sh.Password, names[0], []byte("hello")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sh.ID+".att", names[0])
	if b, _ := os.ReadFile(path); string(b) != "hello" {
		t.Errorf("content %q", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi.Mode())
	}
	// idempotent
	if err := s.PutAttachment(sh.ID, sh.Password, names[0], []byte("hello")); err != nil {
		t.Errorf("second put: %v", err)
	}
	if stored, err := s.CheckAttachment(sh.ID, sh.Password, names[0]); err != nil || !stored {
		t.Errorf("check: %v %v", stored, err)
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	if len(entries) != 1 {
		t.Errorf("leftover files: %v", entries)
	}
}

func TestPutAttachmentRejections(t *testing.T) {
	s, _ := newStore(t)
	sh, names := newShareWith(t, s, "hello")
	other := attName([]byte("other"), "png")
	if err := s.PutAttachment(sh.ID, "wrong", names[0], []byte("hello")); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("password: %v", err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, other, []byte("other")); !errors.Is(err, ErrNotDeclared) {
		t.Errorf("undeclared: %v", err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, "x.png", nil); !errors.Is(err, ErrNotDeclared) {
		t.Errorf("invalid name: %v", err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, names[0], []byte("HELLO")); !errors.Is(err, ErrHashMismatch) {
		t.Errorf("hash: %v", err)
	}
	if err := s.PutAttachment("abcdef12", "x", names[0], nil); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing share: %v", err)
	}
	if err := s.PutAttachment("../x", "x", names[0], nil); !errors.Is(err, ErrInvalidID) {
		t.Errorf("id: %v", err)
	}
}

func TestAttachmentLimits(t *testing.T) {
	s, _ := newStore(t)
	s.SetAttachmentLimits(5, 8)
	sh, names := newShareWith(t, s, "123456", "12345", "1234", "123")
	if err := s.PutAttachment(sh.ID, sh.Password, names[0], []byte("123456")); !errors.Is(err, ErrTooLarge) {
		t.Errorf("per file: %v", err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, names[1], []byte("12345")); err != nil {
		t.Fatal(err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, names[2], []byte("1234")); !errors.Is(err, ErrTooLarge) {
		t.Errorf("per share: %v", err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, names[3], []byte("123")); err != nil {
		t.Errorf("exactly at the limit: %v", err)
	}
}

func TestAttachmentRead(t *testing.T) {
	s, _ := newStore(t)
	sh, names := newShareWith(t, s, "hello")
	if _, _, err := s.Attachment(sh.ID, names[0]); !errors.Is(err, ErrNotFound) {
		t.Errorf("not stored yet: %v", err)
	}
	s.PutAttachment(sh.ID, sh.Password, names[0], []byte("hello"))
	f, enc, err := s.Attachment(sh.ID, names[0])
	if err != nil || enc {
		t.Fatalf("%v %v", enc, err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != "hello" {
		t.Errorf("%q", b)
	}
	if _, _, err := s.Attachment(sh.ID, "../"+sh.ID+".json"); !errors.Is(err, ErrNotFound) {
		t.Errorf("traversal: %v", err)
	}
}

func TestUpdatePrunesUndeclaredAttachments(t *testing.T) {
	s, dir := newStore(t)
	sh, names := newShareWith(t, s, "one", "two")
	for i, d := range []string{"one", "two"} {
		s.PutAttachment(sh.ID, sh.Password, names[i], []byte(d))
	}
	res, err := s.UpdateFull(sh.ID, sh.Password, "y", nil, []string{names[1]})
	if err != nil || len(res.Missing) != 0 {
		t.Fatalf("%+v %v", res, err)
	}
	att := filepath.Join(dir, sh.ID+".att")
	if _, err := os.Stat(filepath.Join(att, names[0])); !os.IsNotExist(err) {
		t.Error("undeclared file kept")
	}
	if _, err := os.Stat(filepath.Join(att, names[1])); err != nil {
		t.Error("declared file removed")
	}
	// absent list = unchanged
	s.UpdateFull(sh.ID, sh.Password, "z", nil, nil)
	if _, err := os.Stat(filepath.Join(att, names[1])); err != nil {
		t.Error("nil list pruned")
	}
	// empty list = none
	s.UpdateFull(sh.ID, sh.Password, "z", nil, []string{})
	if e, _ := os.ReadDir(att); len(e) != 0 {
		t.Errorf("not pruned: %v", e)
	}
}

func TestEncryptedFlipDropsAttachments(t *testing.T) {
	s, dir := newStore(t)
	sh, names := newShareWith(t, s, "one")
	s.PutAttachment(sh.ID, sh.Password, names[0], []byte("one"))
	yes := true
	res, err := s.UpdateFull(sh.ID, sh.Password, "AAAA", &yes, nil)
	if err != nil || !res.Encrypted || !slices.Equal(res.Missing, names) {
		t.Fatalf("%+v %v", res, err)
	}
	if e, _ := os.ReadDir(filepath.Join(dir, sh.ID+".att")); len(e) != 0 {
		t.Errorf("files kept: %v", e)
	}
}

func TestAttachmentEncryptedFlag(t *testing.T) {
	s, _ := newStore(t)
	name := attName([]byte("cipher"), "png")
	sh, err := s.CreateFull("AAAA", 0, true, []string{name})
	if err != nil {
		t.Fatal(err)
	}
	s.PutAttachment(sh.ID, sh.Password, name, []byte("cipher"))
	f, enc, err := s.Attachment(sh.ID, name)
	if err != nil || !enc {
		t.Fatalf("%v %v", enc, err)
	}
	f.Close()
}

func TestDeleteRemovesAttachments(t *testing.T) {
	s, dir := newStore(t)
	sh, names := newShareWith(t, s, "one")
	s.PutAttachment(sh.ID, sh.Password, names[0], []byte("one"))
	if err := s.Delete(sh.ID, sh.Password); err != nil {
		t.Fatal(err)
	}
	if e, _ := os.ReadDir(dir); len(e) != 0 {
		t.Errorf("left behind: %v", e)
	}
}

func TestExpiryHidesAndCleansAttachments(t *testing.T) {
	s, dir := newStore(t)
	now := time.Unix(1_800_000_000, 0)
	s.SetClock(func() time.Time { return now })
	name := attName([]byte("one"), "png")
	sh, err := s.CreateFull("x", 60, false, []string{name})
	if err != nil {
		t.Fatal(err)
	}
	s.PutAttachment(sh.ID, sh.Password, name, []byte("one"))
	now = now.Add(60 * time.Second)
	if _, _, err := s.Attachment(sh.ID, name); !errors.Is(err, ErrNotFound) {
		t.Errorf("read after expiry: %v", err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, name, []byte("one")); !errors.Is(err, ErrNotFound) {
		t.Errorf("put after expiry: %v", err)
	}
	if n, err := s.DeleteExpired(); err != nil || n != 1 {
		t.Fatalf("%d %v", n, err)
	}
	if e, _ := os.ReadDir(dir); len(e) != 0 {
		t.Errorf("left behind: %v", e)
	}
}

func TestCleanupIgnoresAttDirs(t *testing.T) {
	s, dir := newStore(t)
	orphan := filepath.Join(dir, "abcdef12.att")
	os.MkdirAll(orphan, 0o700)
	if n, err := s.DeleteExpired(); err != nil || n != 0 {
		t.Fatalf("%d %v", n, err)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Error("att dir touched")
	}
}
