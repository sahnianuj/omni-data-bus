package memstore

import (
	"testing"

	"omnidatabus/internal/store"
	"omnidatabus/internal/store/storetest"
)

func TestMemstore_Contract(t *testing.T) {
	storetest.Run(t, func(_ *testing.T, clock store.Clock) store.Store { return New(clock) })
}
