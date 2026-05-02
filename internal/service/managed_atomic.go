package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/storage/sqlite"
)

func createExecutionAndUpdateIssueAtomic(ctx context.Context, store *sqlite.Store, issueRecord sqlite.Record, issue *reinv1.Issue, execution *reinv1.Execution) (updatedIssueRecord, storedExecutionRecord sqlite.Record, err error) {
	if store == nil {
		return sqlite.Record{}, sqlite.Record{}, errors.New("sqlite store is required")
	}

	issuePayload, err := marshalProto(issue)
	if err != nil {
		return sqlite.Record{}, sqlite.Record{}, err
	}
	executionPayload, err := marshalProto(execution)
	if err != nil {
		return sqlite.Record{}, sqlite.Record{}, err
	}

	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		return sqlite.Record{}, sqlite.Record{}, fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	executionCreatedAt := recordTimestamp(execution.GetCreatedTime())
	executionUpdatedAt := recordTimestamp(execution.GetStartedTime(), execution.GetCreatedTime())
	if _, err := tx.ExecContext(
		ctx,
		`INSERT INTO executions (id, lock_version, payload, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		execution.GetId(),
		1,
		executionPayload,
		executionCreatedAt,
		executionUpdatedAt,
	); err != nil {
		return sqlite.Record{}, sqlite.Record{}, fmt.Errorf("sqlite: create execution %q: %w", execution.GetId(), err)
	}

	if err := updateManagedRecord(ctx, tx, "issues", issue.GetId(), issueRecord.LockVersion, issuePayload, recordTimestamp(issue.GetUpdatedTime())); err != nil {
		return sqlite.Record{}, sqlite.Record{}, err
	}

	if err := tx.Commit(); err != nil {
		return sqlite.Record{}, sqlite.Record{}, fmt.Errorf("commit transaction: %w", err)
	}
	committed = true

	updatedIssueRecord, err = store.Get(ctx, sqlite.IssueKind, issue.GetId())
	if err != nil {
		return sqlite.Record{}, sqlite.Record{}, err
	}
	storedExecutionRecord, err = store.Get(ctx, sqlite.ExecutionKind, execution.GetId())
	if err != nil {
		return sqlite.Record{}, sqlite.Record{}, err
	}
	return updatedIssueRecord, storedExecutionRecord, nil
}

func updateIssueAndExecutionAtomic(ctx context.Context, store *sqlite.Store, issueRecord sqlite.Record, issue *reinv1.Issue, executionRecord sqlite.Record, execution *reinv1.Execution) error {
	if store == nil {
		return errors.New("sqlite store is required")
	}

	issuePayload, err := marshalProto(issue)
	if err != nil {
		return err
	}
	executionPayload, err := marshalProto(execution)
	if err != nil {
		return err
	}

	tx, err := store.DB().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if err := updateManagedRecord(ctx, tx, "issues", issue.GetId(), issueRecord.LockVersion, issuePayload, recordTimestamp(issue.GetUpdatedTime())); err != nil {
		return err
	}
	if err := updateManagedRecord(ctx, tx, "executions", execution.GetId(), executionRecord.LockVersion, executionPayload, recordTimestamp(execution.GetFinishedTime(), execution.GetStartedTime(), execution.GetCreatedTime())); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	committed = true

	return nil
}

func marshalProto(message proto.Message) ([]byte, error) {
	payload, err := protojson.Marshal(message)
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func updateManagedRecord(ctx context.Context, tx *sql.Tx, table, id string, expectedLockVersion int64, payload []byte, updatedAt string) error {
	if expectedLockVersion < 1 {
		return sqlite.ErrLockVersionMismatch
	}

	result, err := tx.ExecContext(
		ctx,
		fmt.Sprintf(`UPDATE %s SET payload = ?, lock_version = lock_version + 1, updated_at = ? WHERE id = ? AND lock_version = ?`, table),
		payload,
		updatedAt,
		id,
		expectedLockVersion,
	)
	if err != nil {
		return fmt.Errorf("sqlite: update %s %q: %w", table, id, err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlite: update %s %q rows affected: %w", table, id, err)
	}
	if rowsAffected == 1 {
		return nil
	}

	var existingID string
	err = tx.QueryRowContext(ctx, fmt.Sprintf(`SELECT id FROM %s WHERE id = ?`, table), id).Scan(&existingID)
	if errors.Is(err, sql.ErrNoRows) {
		return sqlite.ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("sqlite: inspect %s %q after update: %w", table, id, err)
	}
	return sqlite.ErrLockVersionMismatch
}

func recordTimestamp(timestamps ...interface{ AsTime() time.Time }) string {
	for _, candidate := range timestamps {
		if candidate == nil {
			continue
		}
		value := candidate.AsTime().UTC()
		if !value.IsZero() {
			return value.Format(time.RFC3339Nano)
		}
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}
