package sqlite

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
)

var nextStoreID uint64

func openTestStore(tb testing.TB, ctx context.Context) *Store {
	tb.Helper()

	store, err := OpenAndMigrate(ctx, InMemoryConfig(fmt.Sprintf("%s-%d", tb.Name(), atomic.AddUint64(&nextStoreID, 1))))
	if err != nil {
		tb.Fatalf("OpenAndMigrate() error = %v", err)
	}

	tb.Cleanup(func() {
		if closeErr := store.Close(); closeErr != nil {
			tb.Errorf("Close() error = %v", closeErr)
		}
	})

	return store
}
