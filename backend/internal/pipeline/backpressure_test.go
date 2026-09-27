package pipeline

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"omnidatabus/internal/connectors/source"
)

// TestPipeline_AC5_BackpressureStopsReaders: with nobody draining rowsCh, each reader blocks
// after the channel fills; the total reads are at most the channel capacity plus one batch in
// flight per reader. Draining lets them finish.
func TestPipeline_AC5_BackpressureStopsReaders(t *testing.T) {
	const readers = 3
	rows := make(chan *source.Batch, RowsChanCap)
	parts := make([]*memPartition, readers)
	ranges := PlanPartitions(KeyInt, int64(1), int64(900_000), readers)
	var wg sync.WaitGroup
	for i := range readers {
		parts[i] = &memPartition{t: memTable{n: 900_000}, lo: ranges[i].Lo, hi: ranges[i].Hi, failAt: map[int]int{}}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cfg := noSleep()
			cfg.BatchSize = 100
			require.NoError(t, ReadPartition(context.Background(), parts[i], i, []int{0}, nil, 0, cfg, nil, rows))
		}()
	}
	reads := func() int {
		n := 0
		for _, p := range parts {
			p.mu.Lock()
			n += p.reads
			p.mu.Unlock()
		}
		return n
	}
	// Let the readers run into the full channel.
	require.Eventually(t, func() bool { return len(rows) == RowsChanCap }, time.Second, time.Millisecond)
	time.Sleep(50 * time.Millisecond)
	before := reads()
	time.Sleep(50 * time.Millisecond)
	require.Equal(t, before, reads(), "readers are blocked")
	require.LessOrEqual(t, before, RowsChanCap+readers, "≤ cap + 1 batch in flight per reader")

	// Unblock: everything flows through.
	done := make(chan int)
	go func() {
		n := 0
		for b := range rows {
			n += len(b.Rows)
		}
		done <- n
	}()
	wg.Wait()
	close(rows)
	require.Equal(t, 900_000, <-done)
}
