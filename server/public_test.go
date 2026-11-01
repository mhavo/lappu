package lappu

import (
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
