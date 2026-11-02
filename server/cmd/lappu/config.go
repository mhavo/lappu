package main

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

const minTokenLength = 32

const defaultTrustedProxies = "127.0.0.0/8,::1/128,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16,fc00::/7"

type config struct {
	Listen         string
	DataDir        string
	QuotesFile     string
	Token          string
	AllowAnonymous bool
	MaxBodyBytes   int64
	// Attachment limits per file and in total per share.
	MaxAttachmentBytes      int64
	MaxShareAttachmentBytes int64
	TrustedProxies          []netip.Prefix
	HideViewerFooter        bool
}

// envSuffixes are the configuration variables; each is read as LAPPU_<suffix>.
// Before the rename to Lappu they were JSP_<suffix>.
var envSuffixes = []string{
	"LISTEN", "DATA_DIR", "QUOTES_FILE", "ALLOW_ANONYMOUS_UPLOADS",
	"UPLOAD_TOKEN", "UPLOAD_TOKEN_FILE", "MAX_BODY_BYTES", "TRUSTED_PROXIES",
}

// checkOldVariables fails if any pre-rename JSP_* variable is set, so a renamed
// deployment does not silently fall back to defaults (e.g. a different data dir).
func checkOldVariables(getenv func(string) string) error {
	var msgs []string
	for _, suffix := range envSuffixes {
		if strings.TrimSpace(getenv("JSP_"+suffix)) != "" {
			msgs = append(msgs, fmt.Sprintf("JSP_%s is no longer read; rename it to LAPPU_%s", suffix, suffix))
		}
	}
	if len(msgs) > 0 {
		return errors.New(strings.Join(msgs, "; "))
	}
	return nil
}

func loadConfig(getenv func(string) string) (config, error) {
	if err := checkOldVariables(getenv); err != nil {
		return config{}, err
	}
	get := func(key, def string) string {
		if v := strings.TrimSpace(getenv(key)); v != "" {
			return v
		}
		return def
	}
	c := config{
		Listen:     get("LAPPU_LISTEN", ":80"),
		DataDir:    get("LAPPU_DATA_DIR", "/var/www/data"),
		QuotesFile: get("LAPPU_QUOTES_FILE", "/var/www/quotes/quotes.csv"),
	}

	var err error
	if c.AllowAnonymous, err = strconv.ParseBool(get("LAPPU_ALLOW_ANONYMOUS_UPLOADS", "false")); err != nil {
		return c, fmt.Errorf("LAPPU_ALLOW_ANONYMOUS_UPLOADS: %w", err)
	}
	if c.HideViewerFooter, err = strconv.ParseBool(get("LAPPU_HIDE_VIEWER_FOOTER", "false")); err != nil {
		return c, fmt.Errorf("LAPPU_HIDE_VIEWER_FOOTER: %w", err)
	}
	if c.Token, err = loadToken(getenv("LAPPU_UPLOAD_TOKEN"), getenv("LAPPU_UPLOAD_TOKEN_FILE")); err != nil {
		return c, err
	}
	switch {
	case c.Token == "" && !c.AllowAnonymous:
		return c, errors.New("no upload token: set LAPPU_UPLOAD_TOKEN or LAPPU_UPLOAD_TOKEN_FILE " +
			"(or LAPPU_ALLOW_ANONYMOUS_UPLOADS=true to let anyone upload)")
	case c.Token != "" && len(c.Token) < minTokenLength:
		return c, fmt.Errorf("upload token must be at least %d characters", minTokenLength)
	}

	c.MaxBodyBytes, err = strconv.ParseInt(get("LAPPU_MAX_BODY_BYTES", strconv.Itoa(20<<20)), 10, 64)
	if err != nil || c.MaxBodyBytes <= 0 {
		return c, fmt.Errorf("LAPPU_MAX_BODY_BYTES must be a positive integer")
	}

	if c.MaxAttachmentBytes, err = positiveInt(get, "LAPPU_MAX_ATTACHMENT_BYTES", 10<<20); err != nil {
		return c, err
	}
	if c.MaxShareAttachmentBytes, err = positiveInt(get, "LAPPU_MAX_SHARE_ATTACHMENT_BYTES", 50<<20); err != nil {
		return c, err
	}

	for p := range strings.SplitSeq(get("LAPPU_TRUSTED_PROXIES", defaultTrustedProxies), ",") {
		p = strings.TrimSpace(p)
		prefix, err := netip.ParsePrefix(p)
		if err != nil {
			addr, aerr := netip.ParseAddr(p)
			if aerr != nil {
				return c, fmt.Errorf("LAPPU_TRUSTED_PROXIES: %q is not a CIDR or IP", p)
			}
			prefix = netip.PrefixFrom(addr, addr.BitLen())
		}
		c.TrustedProxies = append(c.TrustedProxies, prefix.Masked())
	}
	return c, nil
}

func loadToken(value, file string) (string, error) {
	value = strings.TrimSpace(value)
	if file == "" {
		return value, nil
	}
	if value != "" {
		return "", errors.New("set only one of LAPPU_UPLOAD_TOKEN and LAPPU_UPLOAD_TOKEN_FILE")
	}
	b, err := os.ReadFile(file)
	if err != nil {
		return "", fmt.Errorf("LAPPU_UPLOAD_TOKEN_FILE: %w", err)
	}
	return strings.TrimSpace(string(b)), nil
}

func positiveInt(get func(key, def string) string, key string, def int64) (int64, error) {
	n, err := strconv.ParseInt(get(key, strconv.FormatInt(def, 10)), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return n, nil
}
