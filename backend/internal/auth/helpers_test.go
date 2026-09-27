package auth

import (
	"testing"
	"time"

	"omnidatabus/internal/store/memstore"
	"omnidatabus/internal/store/storetest"

	"github.com/stretchr/testify/require"
)

// fastHasher keeps tests quick; production uses DefaultArgon2.
var fastHasher = Argon2Hasher{Params: Argon2Params{MemoryKiB: 64, Time: 1, Threads: 1, SaltLen: 16, KeyLen: 32}}

type spyHasher struct {
	Hasher
	verifies int
}

func (s *spyHasher) Verify(encoded, pw string) (bool, error) {
	s.verifies++
	return s.Hasher.Verify(encoded, pw)
}

type fixture struct {
	svc   *Service
	st    *memstore.Store
	clock *storetest.Clock
	spy   *spyHasher
}

func newFixture(t *testing.T) fixture {
	t.Helper()
	clock := storetest.NewClock()
	st := memstore.New(clock)
	spy := &spyHasher{Hasher: fastHasher}
	svc, err := NewService(st, spy, clock, Settings{IdleTTL: 12 * time.Hour, AbsoluteTTL: 7 * 24 * time.Hour, SignupEnabled: true})
	require.NoError(t, err)
	return fixture{svc: svc, st: st, clock: clock, spy: spy}
}

const goodPassword = "correct horse battery staple"
