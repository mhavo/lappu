package main

import (
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const token = "0123456789abcdef0123456789abcdef"

func envFrom(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestConfigDefaults(t *testing.T) {
	c, err := loadConfig(envFrom(map[string]string{"LAPPU_UPLOAD_TOKEN": token}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":80" || c.DataDir != "/var/www/data" || c.QuotesFile != "/var/www/quotes/quotes.csv" ||
		c.Token != token || c.AllowAnonymous || c.MaxBodyBytes != 20<<20 {
		t.Errorf("defaults: %+v", c)
	}
	want := []string{"127.0.0.0/8", "::1/128", "10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "fc00::/7"}
	if len(c.TrustedProxies) != len(want) {
		t.Fatalf("proxies: %v", c.TrustedProxies)
	}
	for i, p := range c.TrustedProxies {
		if p.String() != want[i] {
			t.Errorf("proxy %d = %s, want %s", i, p, want[i])
		}
	}
}

func TestConfigOverrides(t *testing.T) {
	c, err := loadConfig(envFrom(map[string]string{
		"LAPPU_UPLOAD_TOKEN":    token,
		"LAPPU_LISTEN":          "127.0.0.1:8080",
		"LAPPU_DATA_DIR":        "/d",
		"LAPPU_QUOTES_FILE":     "/q.csv",
		"LAPPU_MAX_BODY_BYTES":  "100",
		"LAPPU_TRUSTED_PROXIES": " 10.1.0.0/16 , 192.0.2.1 ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != "127.0.0.1:8080" || c.DataDir != "/d" || c.QuotesFile != "/q.csv" || c.MaxBodyBytes != 100 {
		t.Errorf("%+v", c)
	}
	if len(c.TrustedProxies) != 2 || c.TrustedProxies[1] != netip.MustParsePrefix("192.0.2.1/32") {
		t.Errorf("proxies: %v", c.TrustedProxies)
	}
	c, err = loadConfig(envFrom(map[string]string{"LAPPU_UPLOAD_TOKEN": token, "LAPPU_TRUSTED_PROXIES": ""}))
	if err != nil || len(c.TrustedProxies) == 0 {
		t.Errorf("empty LAPPU_TRUSTED_PROXIES should mean default: %v %v", c.TrustedProxies, err)
	}
}

func TestConfigTokenFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")
	os.WriteFile(path, []byte(token+"\n"), 0o600)
	c, err := loadConfig(envFrom(map[string]string{"LAPPU_UPLOAD_TOKEN_FILE": path}))
	if err != nil || c.Token != token {
		t.Errorf("token %q, err %v", c.Token, err)
	}
}

func TestConfigErrors(t *testing.T) {
	cases := map[string]map[string]string{
		"no token":                   {},
		"short token":                {"LAPPU_UPLOAD_TOKEN": "short"},
		"both token sources":         {"LAPPU_UPLOAD_TOKEN": token, "LAPPU_UPLOAD_TOKEN_FILE": "/x"},
		"missing token file":         {"LAPPU_UPLOAD_TOKEN_FILE": "/does/not/exist"},
		"bad anonymous flag":         {"LAPPU_UPLOAD_TOKEN": token, "LAPPU_ALLOW_ANONYMOUS_UPLOADS": "yes please"},
		"bad body limit":             {"LAPPU_UPLOAD_TOKEN": token, "LAPPU_MAX_BODY_BYTES": "0"},
		"bad attachment limit":       {"LAPPU_UPLOAD_TOKEN": token, "LAPPU_MAX_ATTACHMENT_BYTES": "-1"},
		"bad share attachment limit": {"LAPPU_UPLOAD_TOKEN": token, "LAPPU_MAX_SHARE_ATTACHMENT_BYTES": "x"},
		"bad proxy":                  {"LAPPU_UPLOAD_TOKEN": token, "LAPPU_TRUSTED_PROXIES": "nope"},
		"short anon token":           {"LAPPU_UPLOAD_TOKEN": "short", "LAPPU_ALLOW_ANONYMOUS_UPLOADS": "true"},
	}
	for name, env := range cases {
		if _, err := loadConfig(envFrom(env)); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	_, err := loadConfig(envFrom(map[string]string{}))
	if !strings.Contains(err.Error(), "LAPPU_UPLOAD_TOKEN") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestConfigAnonymousWithoutToken(t *testing.T) {
	c, err := loadConfig(envFrom(map[string]string{"LAPPU_ALLOW_ANONYMOUS_UPLOADS": "true"}))
	if err != nil || !c.AllowAnonymous || c.Token != "" {
		t.Errorf("%+v %v", c, err)
	}
}

func TestConfigRejectsOldJSPVariables(t *testing.T) {
	for _, suffix := range []string{"LISTEN", "DATA_DIR", "QUOTES_FILE", "ALLOW_ANONYMOUS_UPLOADS",
		"UPLOAD_TOKEN", "UPLOAD_TOKEN_FILE", "MAX_BODY_BYTES", "TRUSTED_PROXIES"} {
		_, err := loadConfig(envFrom(map[string]string{"LAPPU_UPLOAD_TOKEN": token, "JSP_" + suffix: "x"}))
		want := "JSP_" + suffix + " is no longer read; rename it to LAPPU_" + suffix
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("JSP_%s: err %v, want %q", suffix, err, want)
		}
	}

	// Reported before other validation, and all offending names are listed.
	_, err := loadConfig(envFrom(map[string]string{"JSP_LISTEN": ":80", "JSP_UPLOAD_TOKEN": "short"}))
	if err == nil || !strings.Contains(err.Error(), "JSP_LISTEN") || !strings.Contains(err.Error(), "JSP_UPLOAD_TOKEN") {
		t.Errorf("err %v", err)
	}

	// Empty (or whitespace-only) old variables are ignored.
	if _, err := loadConfig(envFrom(map[string]string{"LAPPU_UPLOAD_TOKEN": token, "JSP_LISTEN": " "})); err != nil {
		t.Errorf("empty JSP_LISTEN: %v", err)
	}
}

func TestConfigAttachmentLimits(t *testing.T) {
	c, err := loadConfig(envFrom(map[string]string{"LAPPU_UPLOAD_TOKEN": token}))
	if err != nil || c.MaxAttachmentBytes != 10<<20 || c.MaxShareAttachmentBytes != 50<<20 {
		t.Errorf("defaults: %+v %v", c, err)
	}
	c, err = loadConfig(envFrom(map[string]string{"LAPPU_UPLOAD_TOKEN": token,
		"LAPPU_MAX_ATTACHMENT_BYTES": "5", "LAPPU_MAX_SHARE_ATTACHMENT_BYTES": "9"}))
	if err != nil || c.MaxAttachmentBytes != 5 || c.MaxShareAttachmentBytes != 9 {
		t.Errorf("custom: %+v %v", c, err)
	}
}
