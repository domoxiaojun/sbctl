package sbctl

import (
	"fmt"
	"net/http/httptest"
	"sync"
	"testing"
)

func TestLoginRateLimitCoversChangingUnknownNames(t *testing.T) {
	c := testControl(t)
	for i := 0; i < 30; i++ {
		err := Attempt(func() { Login(c.Settings, fmt.Sprintf("unknown_%d", i), "review-only-invalid-password", "same-client") })
		if err == nil || PublicFault(err).Code != "LOGIN_FAILED" {
			t.Fatalf("attempt %d: expected failed login, got %v", i, err)
		}
	}
	expectFault(t, "LOGIN_RATE_LIMIT", func() {
		Login(c.Settings, "unknown_30", "review-only-invalid-password", "same-client")
	})
}

func TestLoginConcurrentReservationsStayWithinSourceBudget(t *testing.T) {
	c := testControl(t)
	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = Attempt(func() { reserveLoginAttempt(c.Settings, fmt.Sprintf("unknown_%d", i), "same-client") })
		}(i)
	}
	wg.Wait()
	transaction(c.Settings.ControlDB, false, func(d *DB) {
		if n := integer(d.Value("SELECT count(*) FROM login_failures WHERE subject=?", hash([]byte("login-source:same-client")))); n != 30 {
			t.Fatalf("concurrent source budget should be exactly 30, got %d", n)
		}
	})
}

func TestLoginAccountCeilingBoundsGuessingAcrossSources(t *testing.T) {
	c := testControl(t)
	for i := 0; i < loginAccountLimit; i++ {
		if err := Attempt(func() { reserveLoginAttempt(c.Settings, "admin", fmt.Sprintf("client_%d", i)) }); err != nil {
			t.Fatalf("reservation %d: %v", i, err)
		}
	}
	expectFault(t, "LOGIN_RATE_LIMIT", func() {
		reserveLoginAttempt(c.Settings, "admin", "client_next")
	})
}

func TestLoginGuessesFromOneSourceDoNotLockOutOthers(t *testing.T) {
	c := testControl(t)
	s := c.Settings
	password := "unit-test-admin-password"
	CreateAdmin(s, "admin", "管理员", password)
	for i := 0; i < loginPairLimit; i++ {
		expectFault(t, "LOGIN_FAILED", func() { Login(s, "admin", "wrong-guess", "203.0.113.50") })
	}
	expectFault(t, "LOGIN_RATE_LIMIT", func() { Login(s, "admin", password, "203.0.113.50") })
	if err := Attempt(func() { Login(s, "admin", password, "198.51.100.7") }); err != nil {
		t.Fatalf("administrator was locked out by another source: %v", err)
	}
}

func TestLoginSourceGroupsOneIPv6Subscriber(t *testing.T) {
	if loginSource("2001:db8:1:2::1") != loginSource("2001:db8:1:2:ffff::9") {
		t.Fatal("addresses in one /64 must share a budget")
	}
	if loginSource("2001:db8:1:2::1") == loginSource("2001:db8:1:3::1") {
		t.Fatal("different /64 networks must not share a budget")
	}
	if loginSource("::ffff:203.0.113.5") != "203.0.113.5" || loginSource("not-an-address") != "not-an-address" {
		t.Fatal("IPv4-mapped and opaque sources must stay stable")
	}
}

func TestClientAddressHonoursOnlyTrustedProxies(t *testing.T) {
	trusted := Settings{TrustedProxies: []string{"172.18.0.1", "10.0.0.0/8"}}
	for _, tc := range []struct {
		name, remote string
		forwarded    []string
		settings     Settings
		want         string
	}{
		{"direct client", "203.0.113.9:4000", nil, trusted, "203.0.113.9"},
		{"untrusted peer cannot spoof", "203.0.113.9:4000", []string{"198.51.100.1"}, trusted, "203.0.113.9"},
		{"no proxies configured", "172.18.0.1:4000", []string{"198.51.100.1"}, Settings{}, "172.18.0.1"},
		{"proxy appended the client", "172.18.0.1:4000", []string{"198.51.100.1"}, trusted, "198.51.100.1"},
		{"client-supplied prefix is ignored", "172.18.0.1:4000", []string{"1.2.3.4, 198.51.100.1"}, trusted, "198.51.100.1"},
		{"chain of trusted proxies", "172.18.0.1:4000", []string{"198.51.100.1, 10.1.2.3"}, trusted, "198.51.100.1"},
		{"repeated headers are one chain", "172.18.0.1:4000", []string{"1.2.3.4", "198.51.100.1"}, trusted, "198.51.100.1"},
		{"missing header falls back to the proxy", "172.18.0.1:4000", nil, trusted, "172.18.0.1"},
		{"malformed hop falls back to the proxy", "172.18.0.1:4000", []string{"198.51.100.1, unknown"}, trusted, "172.18.0.1"},
		{"only proxies in the chain", "172.18.0.1:4000", []string{"10.1.2.3"}, trusted, "172.18.0.1"},
	} {
		r := httptest.NewRequest("POST", "/api/v1/session", nil)
		r.RemoteAddr = tc.remote
		for _, v := range tc.forwarded {
			r.Header.Add("X-Forwarded-For", v)
		}
		if got := clientAddress(tc.settings, r); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestTrustedProxySettingRejectsInvalidEntries(t *testing.T) {
	for value, valid := range map[string]bool{"172.18.0.1": true, "10.0.0.0/8": true, "fd00::/8": true, "proxy.internal": false, "10.0.0.0/33": false, "": false} {
		if _, ok := proxyPrefix(value); ok != valid {
			t.Errorf("%q: valid=%v, want %v", value, ok, valid)
		}
	}
}
