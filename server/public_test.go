package lappu

import (
	"bytes"
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

func TestPublicContainsViewer(t *testing.T) {
	for _, name := range []string{"index.html", "index.js", "style.css"} {
		if _, err := fs.Stat(Public(), name); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// The server passes the viewer settings (hidden footer, source link) by rewriting the single <body> tag of index.html.
func TestPublicIndexHasOneBodyTag(t *testing.T) {
	b, err := fs.ReadFile(Public(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	if n := bytes.Count(b, []byte("<body>")); n != 1 {
		t.Errorf("index.html has %d <body> tags, want 1", n)
	}
}

// Share pages (/s/<id>) replace the <title> and add link-preview metadata before </head>.
func TestPublicIndexHasOneHeadAndTitle(t *testing.T) {
	b, err := fs.ReadFile(Public(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, tag := range []string{"</head>", "<title>", `<meta name="robots" content="noindex, nofollow, noarchive">`} {
		if n := bytes.Count(b, []byte(tag)); n != 1 {
			t.Errorf("index.html has %d %s, want 1", n, tag)
		}
	}
}

// The viewer is also served at /s/<id>, so its own files must be referenced by absolute paths.
func TestPublicIndexUsesAbsolutePaths(t *testing.T) {
	b, err := fs.ReadFile(Public(), "index.html")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range regexp.MustCompile(`(?:src|href)="([^"]*)"`).FindAllSubmatch(b, -1) {
		if ref := string(m[1]); !strings.HasPrefix(ref, "/") && !strings.HasPrefix(ref, "https://") {
			t.Errorf("relative reference %q", ref)
		}
	}
}

func TestPublicRobotsDisallowsAll(t *testing.T) {
	b, err := fs.ReadFile(Public(), "robots.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(b, []byte("User-agent: *\nDisallow: /\n")) || !bytes.Contains(b, []byte("User-agent: TelegramBot\n")) {
		t.Errorf("robots.txt:\n%s", b)
	}
}
