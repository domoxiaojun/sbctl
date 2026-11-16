package sbctl

import (
	"fmt"
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

func TestLoginRateLimitCoversChangingSourceForSameAccount(t *testing.T) {
	c := testControl(t)
	for i := 0; i < 10; i++ {
		if err := Attempt(func() { reserveLoginAttempt(c.Settings, "admin", fmt.Sprintf("client_%d", i)) }); err != nil {
			t.Fatalf("reservation %d: %v", i, err)
		}
	}
	expectFault(t, "LOGIN_RATE_LIMIT", func() {
		reserveLoginAttempt(c.Settings, "admin", "client_10")
	})
}
