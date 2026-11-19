package middleware

import (
	"net/http/httptest"
	"testing"
)

// ClientIP decides which address every unauthenticated rate limit is keyed on,
// so it is the difference between a limit and a suggestion. The cases that
// matter are the hostile ones: a client that supplies its own X-Forwarded-For.
func TestClientIP(t *testing.T) {
	cases := []struct {
		name   string
		hops   int
		remote string
		xff    string
		want   string
	}{
		{"no proxy: header ignored entirely", 0, "203.0.113.9:5000", "1.2.3.4", "203.0.113.9"},
		{"no proxy, no header", 0, "203.0.113.9:5000", "", "203.0.113.9"},
		{"one proxy: its entry is believed", 1, "10.0.0.1:443", "198.51.100.7", "198.51.100.7"},
		// The attack. The client sends a forged value; the proxy appends the
		// real address on the right. The forged one must not be used.
		{"one proxy: forged entry on the left is ignored", 1, "10.0.0.1:443", "6.6.6.6, 198.51.100.7", "198.51.100.7"},
		{"one proxy: many forged entries", 1, "10.0.0.1:443", "1.1.1.1, 2.2.2.2, 3.3.3.3, 198.51.100.7", "198.51.100.7"},
		{"two proxies: second from the right", 2, "10.0.0.1:443", "6.6.6.6, 198.51.100.7, 10.0.0.2", "198.51.100.7"},
		{"proxy expected but header missing: socket address", 1, "203.0.113.9:5000", "", "203.0.113.9"},
		{"fewer entries than hops: socket address", 2, "203.0.113.9:5000", "198.51.100.7", "203.0.113.9"},
		{"whitespace is trimmed", 1, "10.0.0.1:443", "6.6.6.6 ,  198.51.100.7  ", "198.51.100.7"},
	}

	defer SetTrustedProxyHops(0)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			SetTrustedProxyHops(tc.hops)
			r := httptest.NewRequest("POST", "/api/auth/otp/request", nil)
			r.RemoteAddr = tc.remote
			if tc.xff != "" {
				r.Header.Set("X-Forwarded-For", tc.xff)
			}
			if got := ClientIP(r); got != tc.want {
				t.Errorf("ClientIP = %q, want %q", got, tc.want)
			}
		})
	}
}

// Rotating the forged header must not produce a new rate-limit key each time.
func TestClientKey_ForgedHeaderDoesNotChangeTheBucket(t *testing.T) {
	SetTrustedProxyHops(1)
	defer SetTrustedProxyHops(0)

	keyFor := func(forged string) string {
		r := httptest.NewRequest("POST", "/api/admin/auth/login", nil)
		r.RemoteAddr = "10.0.0.1:443"
		r.Header.Set("X-Forwarded-For", forged+", 198.51.100.7")
		return clientKey(r)
	}
	a, b := keyFor("1.1.1.1"), keyFor("9.9.9.9")
	if a != b {
		t.Errorf("forged X-Forwarded-For changed the key (%q vs %q): the limit can be bypassed", a, b)
	}
}
