package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"
)

func TestLimiterBlocksAfterBurstAndRefills(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(3, time.Minute, func() time.Time { return now })
	for range 3 {
		if l.blocked("a") {
			t.Fatal("blocked too early")
		}
		l.charge("a")
	}
	if !l.blocked("a") {
		t.Fatal("not blocked after burst")
	}
	if l.blocked("b") {
		t.Fatal("other client blocked")
	}
	now = now.Add(20 * time.Second) // one failure refilled
	if l.blocked("a") {
		t.Fatal("not refilled")
	}
}

func TestLimiterIsBounded(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(1, time.Minute, func() time.Time { return now })
	l.max = 100
	for i := range 1000 {
		now = now.Add(time.Millisecond)
		l.charge(fmt.Sprint("busy", i)) // all empty, none refilled
		if len(l.clients) > l.max {
			t.Fatalf("tracked %d clients, max %d", len(l.clients), l.max)
		}
	}
	if !l.blocked("busy999") {
		t.Error("newest client evicted")
	}
	if _, ok := l.clients["busy0"]; ok {
		t.Error("oldest client kept")
	}
}

func TestLimiterEvictsRefilledFirst(t *testing.T) {
	now := time.Unix(0, 0)
	l := newLimiter(1, time.Minute, func() time.Time { return now })
	l.max = 10
	l.charge("idle") // refilled after a minute, oldest
	now = now.Add(2 * time.Minute)
	for i := range 9 {
		l.charge(fmt.Sprint("busy", i))
	}
	l.charge("new")
	if _, ok := l.clients["idle"]; ok {
		t.Error("refilled client kept")
	}
	for i := range 9 {
		if !l.blocked(fmt.Sprint("busy", i)) {
			t.Errorf("busy%d evicted before a refilled client", i)
		}
	}
}

func TestLimitKey(t *testing.T) {
	cases := map[string]string{
		"203.0.113.5":                "203.0.113.5",
		"2001:db8:1:2:aaaa::1":       "2001:db8:1:2::/64",
		"2001:db8:1:2:ffff:ffff::99": "2001:db8:1:2::/64",
		"::ffff:203.0.113.5":         "203.0.113.5",
		"not-an-ip":                  "not-an-ip",
	}
	for in, want := range cases {
		if got := limitKey(in); got != want {
			t.Errorf("limitKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClientIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	cases := []struct {
		remote, xff, want string
	}{
		{"203.0.113.5:1234", "", "203.0.113.5"},
		{"203.0.113.5:1234", "1.2.3.4", "203.0.113.5"},             // untrusted peer: header ignored
		{"10.0.0.1:1234", "1.2.3.4", "1.2.3.4"},                    // trusted proxy
		{"10.0.0.1:1234", "6.6.6.6, 1.2.3.4, 10.0.0.2", "1.2.3.4"}, // rightmost untrusted hop
		{"10.0.0.1:1234", "10.0.0.3", "10.0.0.3"},                  // all trusted: leftmost
		{"10.0.0.1:1234", "garbage", "10.0.0.1"},
		{"10.0.0.1:1234", "", "10.0.0.1"},
		{"[::1]:80", "", "::1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest("GET", "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := clientIP(r, trusted); got != c.want {
			t.Errorf("clientIP(%s, %q) = %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestReadLimitLookupFailures(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	e := newEnv(t, withSharePage, func(c *Config) {
		c.TrustedProxies = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
		c.LookupFailureLimit = 4
		c.now = func() time.Time { return now }
	})
	s := e.create("# ok")
	from := func(ip, target string) req {
		return req{method: "GET", target: target, header: map[string]string{"X-Forwarded-For": ip}}
	}
	// every kind of failed lookup counts: missing, invalid, /s/, meta and attachments, GET and HEAD
	for _, target := range []string{"/share.php?id=abcdef12", "/share.php?id=bad", "/s/abcdef12",
		"/share.php?meta&id=abcdef12"} {
		if w := e.do(from("198.51.100.1", target)); w.Code != 404 && w.Code != 400 {
			t.Fatalf("%s: %d", target, w.Code)
		}
	}
	for _, target := range []string{"/share.php?id=" + s.ID, "/share.php?meta&id=" + s.ID, "/s/" + s.ID,
		"/share.php?att=abcdef12/x", "/share.php?quote", "/", "/robots.txt", "/healthz"} {
		r := from("198.51.100.1", target)
		want := http.StatusTooManyRequests
		if !strings.HasPrefix(target, "/s/") && !strings.Contains(target, "id=") && !strings.Contains(target, "att=") {
			want = 0 // not a limited endpoint
		}
		w := e.do(r)
		if want != 0 && w.Code != want || want == 0 && w.Code == http.StatusTooManyRequests {
			t.Errorf("%s: %d", target, w.Code)
		}
	}
	if w := e.do(req{method: "HEAD", target: "/s/" + s.ID, header: map[string]string{"X-Forwarded-For": "198.51.100.1"}}); w.Code != 429 {
		t.Errorf("HEAD: %d", w.Code)
	}
	// other clients and the upload token are not affected
	if w := e.do(from("198.51.100.2", "/share.php?id="+s.ID)); w.Code != 200 {
		t.Errorf("other client: %d", w.Code)
	}
	withToken := from("198.51.100.1", "/share.php?meta&id=abcdef12")
	withToken.header["Authorization"] = "Bearer " + testToken
	for range 10 {
		if w := e.do(withToken); w.Code != 404 {
			t.Fatalf("token: %d", w.Code)
		}
	}
	// the window refills (4 per 10 minutes = one per 150 s)
	now = now.Add(150 * time.Second)
	if w := e.do(from("198.51.100.1", "/share.php?id="+s.ID)); w.Code != 200 {
		t.Errorf("after refill: %d", w.Code)
	}
}

func TestReadLimitValidSharesNeverCountAsFailures(t *testing.T) {
	e := newEnv(t, withSharePage, func(c *Config) { c.LookupFailureLimit = 2 })
	s := e.create("# ok")
	for range 20 {
		for _, target := range []string{"/share.php?id=" + s.ID, "/share.php?meta&id=" + s.ID, "/s/" + s.ID} {
			if w := e.do(req{method: "GET", target: target}); w.Code != 200 {
				t.Fatalf("%s: %d", target, w.Code)
			}
		}
	}
}

func TestReadLimitGeneralRate(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	e := newEnv(t, func(c *Config) {
		c.ReadLimit = 5
		c.now = func() time.Time { return now }
	})
	s := e.create("x")
	get := req{method: "GET", target: "/share.php?id=" + s.ID}
	for range 5 {
		expect(t, e.do(get), 200, "x")
	}
	expect(t, e.do(get), 429, "Too many requests")
	// an invalid token is ignored on reads: still limited, never 401
	expect(t, e.do(req{method: "GET", target: get.target, header: map[string]string{"Authorization": "Bearer wrong"}}), 429, "Too many requests")
	expect(t, e.do(req{method: "GET", target: get.target, header: auth()}), 200, "x")
	// writes, static files and /healthz are not read-limited
	if w := e.do(req{method: "GET", target: "/style.css"}); w.Code != 200 {
		t.Errorf("static: %d", w.Code)
	}
	expect(t, e.do(req{method: "GET", target: "/healthz"}), 200, "ok")
	e.create("writes still work")
	now = now.Add(time.Minute) // 5 per 5 minutes = one per minute
	expect(t, e.do(get), 200, "x")
	expect(t, e.do(get), 429, "Too many requests")
}

func TestReadLimitDoesNotChargeFailedAuth(t *testing.T) {
	e := newEnv(t)
	for range failureBurst + 5 {
		e.do(req{method: "GET", target: "/share.php?meta&id=abcdef12", header: map[string]string{"Authorization": "Bearer wrong"}})
	}
	// failed-auth limiter untouched: a write with a wrong token is 401, not 429
	expect(t, e.do(req{method: "POST", target: "/share.php", body: `{"content":"x"}`,
		header: map[string]string{"Authorization": "Bearer wrong"}}), 401, "Invalid token")
}
