package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "data")
	s, err := New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return s, dir
}

func readMeta(t *testing.T, dir, id string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, id+".json"))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestValidID(t *testing.T) {
	valid := []string{"deadbeef", "ABCdef0123456789", "a1b2c3d4e5f6a1b2c3d4e5f6"}
	invalid := []string{"", "short", "../etc/passwd", "abc-defgh", "abc_defgh", "abcdefgh.json", "abcdefg\x00h", string(make([]byte, 65))}
	for _, id := range valid {
		if !ValidID(id) {
			t.Errorf("ValidID(%q) = false, want true", id)
		}
	}
	for _, id := range invalid {
		if ValidID(id) {
			t.Errorf("ValidID(%q) = true, want false", id)
		}
	}
}

func TestNewCreatesPrivateDir(t *testing.T) {
	_, dir := newStore(t)
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("dir mode = %v, want 0700", fi.Mode().Perm())
	}
}

func TestCreateWritesContentAndHashedMeta(t *testing.T) {
	s, dir := newStore(t)
	share, err := s.Create("# hello", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !ValidID(share.ID) || len(share.ID) != 24 {
		t.Errorf("id %q: want 24 hex chars", share.ID)
	}
	if len(share.Password) != 32 {
		t.Errorf("password %q: want 32 hex chars", share.Password)
	}
	if share.Created == 0 {
		t.Error("created not set")
	}

	content, err := s.Content(share.ID)
	if err != nil || string(content) != "# hello" {
		t.Fatalf("Content = %q, %v", content, err)
	}

	m := readMeta(t, dir, share.ID)
	sum := sha256.Sum256([]byte(share.Password))
	if m["password_sha256"] != hex.EncodeToString(sum[:]) {
		t.Errorf("password_sha256 = %v", m["password_sha256"])
	}
	if _, ok := m["password"]; ok {
		t.Error("plaintext password stored")
	}
	if m["version"] != float64(2) || m["id"] != share.ID || m["encrypted"] != false || m["expires"] != nil {
		t.Errorf("unexpected meta %v", m)
	}
	for _, name := range []string{share.ID + ".md", share.ID + ".json"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if fi.Mode().Perm() != 0o600 {
			t.Errorf("%s mode = %v, want 0600", name, fi.Mode().Perm())
		}
	}
}

func TestCreateGeneratesUniqueIDs(t *testing.T) {
	s, _ := newStore(t)
	a, _ := s.Create("a", 0)
	b, _ := s.Create("b", 0)
	if a.ID == b.ID || a.Password == b.Password {
		t.Error("ids or passwords repeat")
	}
}

func TestNotFound(t *testing.T) {
	s, _ := newStore(t)
	const id = "0123abcd"
	if _, err := s.Content(id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Content err = %v", err)
	}
	if _, err := s.Meta(id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Meta err = %v", err)
	}
	if err := s.Update(id, "pw", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update err = %v", err)
	}
	if err := s.Delete(id, "pw"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete err = %v", err)
	}
}

func TestInvalidIDRejected(t *testing.T) {
	s, _ := newStore(t)
	if _, err := s.Content("../../etc/passwd"); !errors.Is(err, ErrInvalidID) {
		t.Errorf("Content err = %v", err)
	}
	if _, err := s.Meta("x"); !errors.Is(err, ErrInvalidID) {
		t.Errorf("Meta err = %v", err)
	}
	if err := s.Update("a/b", "pw", "x"); !errors.Is(err, ErrInvalidID) {
		t.Errorf("Update err = %v", err)
	}
	if err := s.Delete("a.b", "pw"); !errors.Is(err, ErrInvalidID) {
		t.Errorf("Delete err = %v", err)
	}
}

func TestUpdateAndDeleteCheckPassword(t *testing.T) {
	s, dir := newStore(t)
	share, _ := s.Create("v1", 0)

	if err := s.Update(share.ID, "wrong", "v2"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Update wrong pw err = %v", err)
	}
	if err := s.Update(share.ID, share.Password, "v2"); err != nil {
		t.Fatal(err)
	}
	if c, _ := s.Content(share.ID); string(c) != "v2" {
		t.Errorf("content = %q", c)
	}
	if m := readMeta(t, dir, share.ID); m["updated"] == nil {
		t.Error("updated not set")
	}

	if err := s.Delete(share.ID, "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Delete wrong pw err = %v", err)
	}
	if err := s.Delete(share.ID, share.Password); err != nil {
		t.Fatal(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("files left after delete: %v", entries)
	}
}

// writeLegacy writes a share exactly as the PHP server did.
func writeLegacy(t *testing.T, dir, id, password, content string) {
	t.Helper()
	meta := `{"id":"` + id + `","password":"` + password + `","created":1700000000}`
	if err := os.WriteFile(filepath.Join(dir, id+".md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, id+".json"), []byte(meta), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyMetaLazyMigration(t *testing.T) {
	s, dir := newStore(t)
	const id, pw = "1a2b3c4d", "0123456789abcdef0123456789abcdef"
	writeLegacy(t, dir, id, pw, "old")

	m, err := s.Meta(id)
	if err != nil || m.Created != 1700000000 || m.ID != id {
		t.Fatalf("Meta = %+v, %v", m, err)
	}

	if err := s.Update(id, "wrong", "x"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong pw err = %v", err)
	}
	if _, ok := readMeta(t, dir, id)["password"]; !ok {
		t.Fatal("meta migrated on failed auth")
	}

	if err := s.Update(id, pw, "new"); err != nil {
		t.Fatal(err)
	}
	raw := readMeta(t, dir, id)
	if _, ok := raw["password"]; ok {
		t.Error("plaintext password kept after migration")
	}
	sum := sha256.Sum256([]byte(pw))
	if raw["version"] != float64(2) || raw["password_sha256"] != hex.EncodeToString(sum[:]) || raw["created"] != float64(1700000000) {
		t.Errorf("migrated meta = %v", raw)
	}
	if err := s.Update(id, pw, "newer"); err != nil {
		t.Fatalf("password stops working after migration: %v", err)
	}
}

func TestLegacyEmptyPasswordNeverMatches(t *testing.T) {
	s, dir := newStore(t)
	const id = "1a2b3c4d"
	os.WriteFile(filepath.Join(dir, id+".md"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(dir, id+".json"), []byte(`{"id":"1a2b3c4d"}`), 0o600)
	if err := s.Delete(id, ""); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("err = %v", err)
	}
}

func TestMetaIgnoresUnknownFields(t *testing.T) {
	s, dir := newStore(t)
	const id = "1a2b3c4d"
	os.WriteFile(filepath.Join(dir, id+".md"), []byte("x"), 0o600)
	os.WriteFile(filepath.Join(dir, id+".json"), []byte(`{"version":3,"id":"1a2b3c4d","created":5,"future":{"a":1}}`), 0o600)
	m, err := s.Meta(id)
	if err != nil || m.Created != 5 {
		t.Errorf("Meta = %+v, %v", m, err)
	}
}

func TestConcurrentUpdates(t *testing.T) {
	s, dir := newStore(t)
	share, _ := s.Create("v0", 0)
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if err := s.Update(share.ID, share.Password, string(rune('a'+i))); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Errorf("temp files left behind: %v", entries)
	}
}

func TestMetaIDNeverTakenFromFileContent(t *testing.T) {
	s, dir := newStore(t)
	a, _ := s.Create("a", 0)
	b, _ := s.Create("b", 0)
	bMeta, _ := os.ReadFile(filepath.Join(dir, b.ID+".json"))

	// A's meta claims to be B (copied/tampered), and C's meta claims a traversal path.
	sum := sha256.Sum256([]byte(a.Password))
	os.WriteFile(filepath.Join(dir, a.ID+".json"),
		[]byte(`{"version":2,"id":"`+b.ID+`","password_sha256":"`+hex.EncodeToString(sum[:])+`","created":1}`), 0o600)
	const c, pw = "cccccccc", "secret"
	os.WriteFile(filepath.Join(dir, c+".md"), []byte("c"), 0o600)
	os.WriteFile(filepath.Join(dir, c+".json"), []byte(`{"id":"../escaped","password":"`+pw+`","created":1}`), 0o600)

	if m, err := s.Meta(a.ID); err != nil || m.ID != a.ID {
		t.Errorf("Meta(a).ID = %q, %v", m.ID, err)
	}
	if err := s.Update(a.ID, a.Password, "a2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(c, pw, "c2"); err != nil {
		t.Fatal(err)
	}

	if got, _ := os.ReadFile(filepath.Join(dir, b.ID+".json")); string(got) != string(bMeta) {
		t.Error("B's meta was overwritten through A")
	}
	if readMeta(t, dir, a.ID)["id"] != a.ID || readMeta(t, dir, c)["id"] != c {
		t.Error("meta id not corrected to file name")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escaped.json")); err == nil {
		t.Error("wrote outside the data dir")
	}
}

func fakeClock(s *Store, start int64) *int64 {
	cur := start
	s.SetClock(func() time.Time { return time.Unix(cur, 0) })
	return &cur
}

func TestExpiredShareBehavesAsMissing(t *testing.T) {
	s, dir := newStore(t)
	now := fakeClock(s, 1000)
	share, err := s.Create("x", 60)
	if err != nil {
		t.Fatal(err)
	}
	if share.Expires == nil || *share.Expires != 1060 {
		t.Fatalf("expires = %v", share.Expires)
	}
	*now = 1059
	if _, err := s.Content(share.ID); err != nil {
		t.Fatalf("not yet expired: %v", err)
	}
	*now = 1060 // now >= expires
	if _, err := s.Content(share.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Content: %v", err)
	}
	if _, err := s.Meta(share.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("Meta: %v", err)
	}
	if err := s.Update(share.ID, share.Password, "y"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Update: %v", err)
	}
	if err := s.Delete(share.ID, "wrong"); !errors.Is(err, ErrNotFound) {
		t.Errorf("Delete: %v", err)
	}
	for _, ext := range []string{".md", ".json"} {
		if _, err := os.Stat(filepath.Join(dir, share.ID+ext)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s not removed by Delete: %v", ext, err)
		}
	}
}

func TestLegacyMetaNeverExpires(t *testing.T) {
	s, dir := newStore(t)
	fakeClock(s, 1<<40)
	os.WriteFile(filepath.Join(dir, "legacy01.md"), []byte("old"), 0o600)
	os.WriteFile(filepath.Join(dir, "legacy01.json"), []byte(`{"id":"legacy01","password":"pw","created":5}`), 0o600)
	if _, err := s.Content("legacy01"); err != nil {
		t.Fatal(err)
	}
	m, err := s.Meta("legacy01")
	if err != nil || m.Expires != nil {
		t.Fatalf("%+v %v", m, err)
	}
	if n, err := s.DeleteExpired(); err != nil || n != 0 {
		t.Fatalf("cleanup %d %v", n, err)
	}
}

func TestNoExpiryWhenZero(t *testing.T) {
	s, dir := newStore(t)
	share, _ := s.Create("x", 0)
	if share.Expires != nil {
		t.Fatalf("expires = %v", *share.Expires)
	}
	if v, ok := readMeta(t, dir, share.ID)["expires"]; !ok || v != nil {
		t.Errorf("meta expires = %v (present %v)", v, ok)
	}
}

func TestDeleteExpired(t *testing.T) {
	s, dir := newStore(t)
	now := fakeClock(s, 1000)
	gone, _ := s.Create("a", 10)
	kept, _ := s.Create("b", 100)
	forever, _ := s.Create("c", 0)
	os.WriteFile(filepath.Join(dir, "broken01.json"), []byte(`{not json`), 0o600)
	os.WriteFile(filepath.Join(dir, "broken01.md"), []byte("keep"), 0o600)
	*now = 1050
	n, err := s.DeleteExpired()
	if err != nil || n != 1 {
		t.Fatalf("deleted %d, %v", n, err)
	}
	for _, ext := range []string{".md", ".json"} {
		if _, err := os.Stat(filepath.Join(dir, gone.ID+ext)); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s survived: %v", ext, err)
		}
	}
	for _, name := range []string{kept.ID + ".md", forever.ID + ".json", "broken01.json", "broken01.md"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestCleanupLoopStopsOnCancel(t *testing.T) {
	s, _ := newStore(t)
	share, _ := s.Create("a", 1)
	fakeClock(s, time.Now().Unix()+10)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.CleanupLoop(ctx, time.Hour, slog.New(slog.DiscardHandler)); close(done) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(s.metaPath(share.ID)); errors.Is(err, os.ErrNotExist) {
			break // the startup run removed it
		}
		if time.Now().After(deadline) {
			t.Fatal("startup cleanup did not run")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not stop")
	}
}
