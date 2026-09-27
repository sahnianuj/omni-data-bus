package auth

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"omnidatabus/internal/store/storetest"

	"github.com/stretchr/testify/require"
)

func TestLoginLimiter_AC7_BlocksSixth(t *testing.T) {
	clock := storetest.NewClock()
	l := NewLoginLimiter(clock)
	for i := 0; i < 5; i++ {
		ok, _ := l.Allow("ip|a@b")
		require.True(t, ok, "attempt %d", i+1)
		l.Fail("ip|a@b")
	}
	ok, wait := l.Allow("ip|a@b")
	require.False(t, ok)
	require.Greater(t, wait, time.Duration(0))
	require.LessOrEqual(t, wait, time.Minute)
	ok, _ = l.Allow("other-ip|a@b")
	require.True(t, ok, "keys are independent")
}

func TestLoginLimiter_AC7_WindowResets(t *testing.T) {
	clock := storetest.NewClock()
	l := NewLoginLimiter(clock)
	for i := 0; i < 5; i++ {
		l.Fail("k")
	}
	clock.Advance(61 * time.Second)
	ok, _ := l.Allow("k")
	require.True(t, ok, "minute window passed")
	l.Reset("k")
	ok, _ = l.Allow("k")
	require.True(t, ok)
}

func TestLoginLimiter_AC7_HourlyLimit(t *testing.T) {
	clock := storetest.NewClock()
	l := NewLoginLimiter(clock)
	for i := 0; i < 20; i++ {
		l.Fail("k")
		clock.Advance(2 * time.Minute) // never 5 in one minute
	}
	ok, wait := l.Allow("k")
	require.False(t, ok, "20 per hour")
	require.Greater(t, wait, time.Duration(0))
}

func TestLoginLimiter_EvictsOldestKeys(t *testing.T) {
	l := NewLoginLimiter(storetest.NewClock())
	l.maxKeys = 3
	for i := 0; i < 5; i++ {
		l.Fail(fmt.Sprint(i))
	}
	require.Len(t, l.entries, 3)
	_, kept := l.entries["4"]
	require.True(t, kept)
}

func TestLogin_AC7_ServiceReturnsRateLimited(t *testing.T) {
	f := newFixture(t)
	signup(t, f, "ada@example.com")
	for i := 0; i < 5; i++ {
		_, _ = f.svc.Login(ctx, "ada@example.com", "bad password!!", "", RequestMeta{IP: "5.5.5.5"})
	}
	_, err := f.svc.Login(ctx, "ada@example.com", goodPassword, "", RequestMeta{IP: "5.5.5.5"})
	var rl *RateLimitedError
	require.True(t, errors.As(err, &rl), "got %v", err)
	require.GreaterOrEqual(t, rl.Seconds(), 1)

	f.clock.Advance(61 * time.Second)
	_, err = f.svc.Login(ctx, "ada@example.com", goodPassword, "", RequestMeta{IP: "5.5.5.5"})
	require.NoError(t, err, "allowed again after the window")
}
