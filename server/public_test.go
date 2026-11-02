package lappu

import (
	"bytes"
	"io/fs"
	"testing"
)

func TestPublicContainsViewer(t *testing.T) {
	for _, name := range []string{"index.html", "index.js", "style.css"} {
		if _, err := fs.Stat(Public(), name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The server hides the viewer footer by rewriting the single <body> tag of index.html.
func TestPublicIndexHasOneBodyTag(t *testing.T) {
	b, err := fs.ReadFile(Public(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(b, []byte("<body>")); n != 1 {
		t.Errorf("index.html has %d <body> tags, want 1", n)
	}
}
