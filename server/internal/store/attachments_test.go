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

const (
	png = "\x89PNG\r\n\x1a\n"
	jpg = "\xFF\xD8\xFF"
	// realCiphertext is a SimpleTextCrypt text form (viewer-tests/vectors.json).
	realCiphertext = "AAECAwQFBgcICQoL\ntI+IL8VaAdnNEogID0axa9mnQVF7OBA2siggzwcsWw==\n"
)

const cipherBytes = "0123456789abcdef0123456789abcdef" // 32 bytes: IV, body and tag

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
	a, b := attName([]byte(png+"a"), "png"), attName([]byte("b"), "jpg")
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
	if err := s.PutAttachment(sh.ID, sh.Password, a, []byte(png+"a")); err != nil {
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
		names = append(names, attName([]byte(png+d), "png"))
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
	if err := s.PutAttachment(sh.ID, sh.Password, names[0], []byte(png+"hello")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, sh.ID+".att", names[0])
	if b, _ := os.ReadFile(path); string(b) != png+"hello" {
		t.Errorf("content %q", b)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode %v", fi.Mode())
	}
	if fi, _ := os.Stat(filepath.Dir(path)); fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode %v", fi.Mode())
	}
	// idempotent
	if err := s.PutAttachment(sh.ID, sh.Password, names[0], []byte(png+"hello")); err != nil {
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
	other := attName([]byte(png+"other"), "png")
	if err := s.PutAttachment(sh.ID, "wrong", names[0], []byte(png+"hello")); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("password: %v", err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, other, []byte(png+"other")); !errors.Is(err, ErrNotDeclared) {
		t.Errorf("undeclared: %v", err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, "x.png", nil); !errors.Is(err, ErrNotDeclared) {
		t.Errorf("invalid name: %v", err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, names[0], []byte(png+"HELLO")); !errors.Is(err, ErrHashMismatch) {
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
	s.SetAttachmentLimits(12, 20)
	sh, names := newShareWith(t, s, "12345", "1234", "123", "")
	put := func(i int, d string) error { return s.PutAttachment(sh.ID, sh.Password, names[i], []byte(png+d)) }
	if err := put(0, "12345"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("per file: %v", err)
	}
	if err := put(1, "1234"); err != nil {
		t.Fatal(err)
	}
	if err := put(2, "123"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("per share: %v", err)
	}
	if err := put(3, ""); err != nil {
		t.Errorf("exactly at the limit: %v", err)
	}
	assertNoTemp(t, s)
}

func assertNoTemp(t *testing.T, s *Store) {
	t.Helper()
	entries, _ := os.ReadDir(s.dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			t.Errorf("temp file left: %s", e.Name())
		}
	}
}

func TestPutAttachmentEncryptedMinimumSize(t *testing.T) {
	s, _ := newStore(t)
	short, ok := []byte(strings.Repeat("x", 27)), []byte(strings.Repeat("y", 28))
	names := []string{attName(short, "png"), attName(ok, "png")}
	sh, err := s.CreateFull(realCiphertext, 0, true, names)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, names[0], short); !errors.Is(err, ErrInvalidAttachmentData) {
		t.Errorf("27 bytes: %v", err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, names[1], ok); err != nil {
		t.Errorf("28 bytes: %v", err)
	}
	assertNoTemp(t, s)
}

func TestPutAttachmentMagicBytes(t *testing.T) {
	avif := func(major, compat string) string {
		return "\x00\x00\x00\x18ftyp" + major + "\x00\x00\x00\x00" + compat + "\x00\x00\x00\x00"
	}
	cases := []struct {
		ext, data string
		ok        bool
	}{
		{"png", png + "x", true}, {"png", "GIF89a", false}, {"png", "\x89PNG", false},
		{"jpg", jpg + "x", true}, {"jpeg", jpg + "x", true}, {"jpg", png, false},
		{"gif", "GIF87a..", true}, {"gif", "GIF89a..", true}, {"gif", "GIF88a..", false},
		{"webp", "RIFF\x01\x02\x03\x04WEBPVP8 ", true}, {"webp", "RIFF\x01\x02\x03\x04WAVEfmt ", false},
		{"avif", avif("avif", "mif1"), true}, {"avif", avif("mif1", "avis"), true},
		{"avif", avif("mif1", "heic"), false}, {"avif", "\x00\x00\x00\x18moov" + strings.Repeat("avif", 4), false},
		{"avif", "ftyp", false}, {"png", "", false}, {"png", "Meeting notes", false},
	}
	for _, c := range cases {
		s, _ := newStore(t)
		name := attName([]byte(c.data), c.ext)
		sh, err := s.CreateFull("x", 0, false, []string{name})
		if err != nil {
			t.Fatal(err)
		}
		err = s.PutAttachment(sh.ID, sh.Password, name, []byte(c.data))
		if c.ok != (err == nil) || (!c.ok && !errors.Is(err, ErrInvalidAttachmentData)) {
			t.Errorf("%s %q: %v", c.ext, c.data, err)
		}
		if stored, _ := s.CheckAttachment(sh.ID, sh.Password, name); stored != c.ok {
			t.Errorf("%s %q: stored %v", c.ext, c.data, stored)
		}
		assertNoTemp(t, s)
	}
}

func TestPutAttachmentFromStreamErrors(t *testing.T) {
	s, _ := newStore(t)
	sh, names := newShareWith(t, s, "hello")
	boom := errors.New("boom")
	r := io.MultiReader(strings.NewReader(png), iotestErrReader{boom})
	if err := s.PutAttachmentFrom(sh.ID, sh.Password, names[0], r); !errors.Is(err, boom) {
		t.Errorf("read error: %v", err)
	}
	assertNoTemp(t, s)
	// bad hash, undeclared and unauthorized also leave no temp file
	s.PutAttachment(sh.ID, sh.Password, names[0], []byte(png+"nope"))
	s.PutAttachment(sh.ID, "wrong", names[0], []byte(png+"hello"))
	assertNoTemp(t, s)
}

type iotestErrReader struct{ err error }

func (r iotestErrReader) Read([]byte) (int, error) { return 0, r.err }

func TestTempFilesNeverServedOrCounted(t *testing.T) {
	s, dir := newStore(t)
	s.SetAttachmentLimits(100, 30)
	sh, names := newShareWith(t, s, "hello")
	att := filepath.Join(dir, sh.ID+".att")
	os.MkdirAll(att, 0o700)
	os.WriteFile(filepath.Join(att, ".upload-big"), make([]byte, 1000), 0o600)
	if err := s.PutAttachment(sh.ID, sh.Password, names[0], []byte(png+"hello")); err != nil {
		t.Errorf("temp file counted: %v", err)
	}
	if _, _, err := s.Attachment(sh.ID, ".upload-big"); !errors.Is(err, ErrNotFound) {
		t.Errorf("temp file served: %v", err)
	}
}

func TestDeleteExpiredRemovesStaleTempFiles(t *testing.T) {
	s, dir := newStore(t)
	stale, fresh := filepath.Join(dir, ".upload-old"), filepath.Join(dir, ".upload-new")
	staleTmp := filepath.Join(dir, ".tmp-old")
	for _, p := range []string{stale, fresh, staleTmp} {
		os.WriteFile(p, []byte("x"), 0o600)
	}
	old := time.Now().Add(-2 * time.Hour)
	os.Chtimes(stale, old, old)
	os.Chtimes(staleTmp, old, old)
	if _, err := s.DeleteExpired(); err != nil {
		t.Fatal(err)
	}
	for p, want := range map[string]bool{stale: false, staleTmp: false, fresh: true} {
		if _, err := os.Stat(p); (err == nil) != want {
			t.Errorf("%s exists=%v, want %v", filepath.Base(p), err == nil, want)
		}
	}
}

func TestAttachmentRead(t *testing.T) {
	s, _ := newStore(t)
	sh, names := newShareWith(t, s, "hello")
	if _, _, err := s.Attachment(sh.ID, names[0]); !errors.Is(err, ErrNotFound) {
		t.Errorf("not stored yet: %v", err)
	}
	s.PutAttachment(sh.ID, sh.Password, names[0], []byte(png+"hello"))
	f, enc, err := s.Attachment(sh.ID, names[0])
	if err != nil || enc {
		t.Fatalf("%v %v", enc, err)
	}
	b, _ := io.ReadAll(f)
	f.Close()
	if string(b) != png+"hello" {
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
		s.PutAttachment(sh.ID, sh.Password, names[i], []byte(png+d))
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
	s.PutAttachment(sh.ID, sh.Password, names[0], []byte(png+"one"))
	yes := true
	res, err := s.UpdateFull(sh.ID, sh.Password, realCiphertext, &yes, nil)
	if err != nil || !res.Encrypted || !slices.Equal(res.Missing, names) {
		t.Fatalf("%+v %v", res, err)
	}
	if e, _ := os.ReadDir(filepath.Join(dir, sh.ID+".att")); len(e) != 0 {
		t.Errorf("files kept: %v", e)
	}
}

func TestAttachmentEncryptedFlag(t *testing.T) {
	s, _ := newStore(t)
	name := attName([]byte(cipherBytes), "png")
	sh, err := s.CreateFull(realCiphertext, 0, true, []string{name})
	if err != nil {
		t.Fatal(err)
	}
	s.PutAttachment(sh.ID, sh.Password, name, []byte(cipherBytes))
	f, enc, err := s.Attachment(sh.ID, name)
	if err != nil || !enc {
		t.Fatalf("%v %v", enc, err)
	}
	f.Close()
}

func TestDeleteRemovesAttachments(t *testing.T) {
	s, dir := newStore(t)
	sh, names := newShareWith(t, s, "one")
	s.PutAttachment(sh.ID, sh.Password, names[0], []byte(png+"one"))
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
	name := attName([]byte(png+"one"), "png")
	sh, err := s.CreateFull("x", 60, false, []string{name})
	if err != nil {
		t.Fatal(err)
	}
	s.PutAttachment(sh.ID, sh.Password, name, []byte(png+"one"))
	now = now.Add(60 * time.Second)
	if _, _, err := s.Attachment(sh.ID, name); !errors.Is(err, ErrNotFound) {
		t.Errorf("read after expiry: %v", err)
	}
	if err := s.PutAttachment(sh.ID, sh.Password, name, []byte(png+"one")); !errors.Is(err, ErrNotFound) {
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

func TestValidCiphertext(t *testing.T) {
	iv := "AAECAwQFBgcICQoL"
	tag := strings.Repeat("A", 22) + "==" // 16 bytes
	for name, c := range map[string]struct {
		in string
		ok bool
	}{
		"vector":            {realCiphertext, true},
		"no newline":        {iv + tag, true},
		"wrapped":           {iv + "\n" + tag[:10] + "\r\n " + tag[10:] + "\n", true},
		"plain sentence":    {"Meeting notes for Tuesday", false},
		"empty":             {"", false},
		"iv only":           {iv, false},
		"short body":        {iv + "QUJD", false},
		"15 byte body":      {iv + strings.Repeat("A", 20), false},
		"bad padding":       {iv + strings.Repeat("A", 22), false},
		"short iv":          {"AAECAwQFBgcICQ==" + tag, false},
		"non base64":        {iv + "!!!!" + tag, false},
		"url-safe alphabet": {iv + strings.Repeat("-", 22) + "==", false},
	} {
		if got := validCiphertext(c.in); got != c.ok {
			t.Errorf("%s: %v, want %v", name, got, c.ok)
		}
	}
	s, _ := newStore(t)
	if _, err := s.CreateWith("Meeting notes for Tuesday", 0, true); !errors.Is(err, ErrNotCiphertext) {
		t.Errorf("create: %v", err)
	}
	sh, err := s.CreateWith(realCiphertext, 0, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Update(sh.ID, sh.Password, "QUJD"); !errors.Is(err, ErrNotCiphertext) {
		t.Errorf("update: %v", err)
	}
}
