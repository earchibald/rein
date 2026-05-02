package sqlite

import (
	"context"
	"errors"
	"fmt"
)

var ErrCheckpointBusy = errors.New("sqlite: WAL checkpoint busy")

type CheckpointResult struct {
	Busy               int
	LogFrames          int
	CheckpointedFrames int
}

func CheckpointWAL(ctx context.Context, cfg Config) (CheckpointResult, error) {
	normalized, err := cfg.normalize()
	if err != nil {
		return CheckpointResult{}, err
	}

	db, err := openDB(ctx, normalized)
	if err != nil {
		return CheckpointResult{}, err
	}
	defer db.Close()

	var result CheckpointResult
	if err := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&result.Busy, &result.LogFrames, &result.CheckpointedFrames); err != nil {
		return CheckpointResult{}, fmt.Errorf("sqlite: checkpoint WAL for %q: %w", normalized.Path, err)
	}
	if result.Busy != 0 {
		return result, fmt.Errorf("%w for %q: %d busy connections", ErrCheckpointBusy, normalized.Path, result.Busy)
	}
	return result, nil
}
