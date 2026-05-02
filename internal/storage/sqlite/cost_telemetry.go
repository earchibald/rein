package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/earchibald/rein/internal/cost"
)

type CostEventFilter struct {
	ProjectID   string
	IssueID     string
	ExecutionID string
}

func (s *Store) AppendCostEvent(ctx context.Context, event cost.Event) (cost.Event, error) {
	normalized, err := event.Normalize(time.Now().UTC())
	if err != nil {
		return cost.Event{}, err
	}

	resourceAttributes, err := json.Marshal(normalized.ResourceAttributes)
	if err != nil {
		return cost.Event{}, fmt.Errorf("sqlite: marshal cost event resource attributes %q: %w", normalized.ID, err)
	}
	attributes, err := json.Marshal(normalized.Attributes)
	if err != nil {
		return cost.Event{}, fmt.Errorf("sqlite: marshal cost event attributes %q: %w", normalized.ID, err)
	}
	payload, err := json.Marshal(normalized)
	if err != nil {
		return cost.Event{}, fmt.Errorf("sqlite: marshal cost event payload %q: %w", normalized.ID, err)
	}

	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO costeventlog (
event_id, event_name, event_time, project_id, issue_id, execution_id, workflow_id, adapter_id,
currency, cost_micros, input_tokens, output_tokens, cache_read_tokens, cache_write_tokens,
resource_attributes, attributes, payload, created_at
) VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		normalized.ID,
		normalized.Name,
		normalized.Time.Format(time.RFC3339Nano),
		normalized.ProjectID,
		normalized.IssueID,
		normalized.ExecutionID,
		normalized.WorkflowID,
		normalized.AdapterID,
		normalized.Currency,
		normalized.CostMicros,
		normalized.Usage.InputTokens,
		normalized.Usage.OutputTokens,
		normalized.Usage.CacheReadTokens,
		normalized.Usage.CacheWriteTokens,
		string(resourceAttributes),
		string(attributes),
		string(payload),
		normalized.Time.Format(time.RFC3339Nano),
	)
	if err != nil {
		return cost.Event{}, fmt.Errorf("sqlite: append cost event %q: %w", normalized.ID, err)
	}

	sequence, err := result.LastInsertId()
	if err != nil {
		return cost.Event{}, fmt.Errorf("sqlite: inspect cost event insert %q: %w", normalized.ID, err)
	}
	normalized.Sequence = sequence
	return normalized, nil
}

func (s *Store) ListCostEvents(ctx context.Context, filter CostEventFilter) ([]cost.Event, error) {
	clauses := []string{"1 = 1"}
	args := make([]any, 0, 3)
	if value := strings.TrimSpace(filter.ProjectID); value != "" {
		clauses = append(clauses, "project_id = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.IssueID); value != "" {
		clauses = append(clauses, "issue_id = ?")
		args = append(args, value)
	}
	if value := strings.TrimSpace(filter.ExecutionID); value != "" {
		clauses = append(clauses, "execution_id = ?")
		args = append(args, value)
	}

	rows, err := s.db.QueryContext(
		ctx,
		fmt.Sprintf(`SELECT sequence, payload FROM costeventlog WHERE %s ORDER BY sequence`, strings.Join(clauses, " AND ")),
		args...,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list cost events: %w", err)
	}
	defer rows.Close()

	var events []cost.Event
	for rows.Next() {
		var (
			sequence int64
			payload  []byte
		)
		if err := rows.Scan(&sequence, &payload); err != nil {
			return nil, fmt.Errorf("sqlite: scan cost event row: %w", err)
		}
		var event cost.Event
		if err := json.Unmarshal(payload, &event); err != nil {
			return nil, fmt.Errorf("sqlite: decode cost event payload: %w", err)
		}
		event.Sequence = sequence
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate cost events: %w", err)
	}
	return events, nil
}

func (s *Store) GetBudgetSnapshot(ctx context.Context, scope cost.Scope, scopeID string) (cost.Snapshot, bool, error) {
	if !scope.Valid() {
		return cost.Snapshot{}, false, fmt.Errorf("sqlite: invalid budget scope %q", scope)
	}
	scopeID = strings.TrimSpace(scopeID)
	if scopeID == "" {
		return cost.Snapshot{}, false, fmt.Errorf("sqlite: budget scope id is required")
	}

	var (
		snapshot       cost.Snapshot
		lastEventTime  string
		softLimitHitAt string
		hardLimitHitAt string
		updatedAt      string
	)
	err := s.db.QueryRowContext(
		ctx,
		`SELECT scope, scope_id, currency, soft_limit_micros, hard_limit_micros, spent_micros,
input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, event_count,
last_event_id, last_event_time, soft_limit_hit_time, hard_limit_hit_time, updated_at
 FROM budgetstates WHERE scope = ? AND scope_id = ?`,
		string(scope),
		scopeID,
	).Scan(
		&snapshot.Scope,
		&snapshot.ScopeID,
		&snapshot.Currency,
		&snapshot.SoftLimitMicros,
		&snapshot.HardLimitMicros,
		&snapshot.SpentMicros,
		&snapshot.Usage.InputTokens,
		&snapshot.Usage.OutputTokens,
		&snapshot.Usage.CacheReadTokens,
		&snapshot.Usage.CacheWriteTokens,
		&snapshot.EventCount,
		&snapshot.LastEventID,
		&lastEventTime,
		&softLimitHitAt,
		&hardLimitHitAt,
		&updatedAt,
	)
	if err == sql.ErrNoRows {
		return cost.Snapshot{}, false, nil
	}
	if err != nil {
		return cost.Snapshot{}, false, fmt.Errorf("sqlite: get budget snapshot %s %q: %w", scope, scopeID, err)
	}

	if err := parseOptionalBudgetTimes(&snapshot, lastEventTime, softLimitHitAt, hardLimitHitAt, updatedAt); err != nil {
		return cost.Snapshot{}, false, err
	}
	return snapshot, true, nil
}

func (s *Store) UpsertBudgetSnapshot(ctx context.Context, snapshot cost.Snapshot) (cost.Snapshot, error) {
	if err := snapshot.Validate(); err != nil {
		return cost.Snapshot{}, err
	}
	if snapshot.Currency == "" {
		snapshot.Currency = "USD"
	}
	if snapshot.UpdatedAt.IsZero() {
		snapshot.UpdatedAt = time.Now().UTC()
	} else {
		snapshot.UpdatedAt = snapshot.UpdatedAt.UTC()
	}

	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO budgetstates (
scope, scope_id, currency, soft_limit_micros, hard_limit_micros, spent_micros,
input_tokens, output_tokens, cache_read_tokens, cache_write_tokens, event_count,
last_event_id, last_event_time, soft_limit_hit_time, hard_limit_hit_time, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(scope, scope_id) DO UPDATE SET
currency = excluded.currency,
soft_limit_micros = excluded.soft_limit_micros,
hard_limit_micros = excluded.hard_limit_micros,
spent_micros = excluded.spent_micros,
input_tokens = excluded.input_tokens,
output_tokens = excluded.output_tokens,
cache_read_tokens = excluded.cache_read_tokens,
cache_write_tokens = excluded.cache_write_tokens,
event_count = excluded.event_count,
last_event_id = excluded.last_event_id,
last_event_time = excluded.last_event_time,
soft_limit_hit_time = excluded.soft_limit_hit_time,
hard_limit_hit_time = excluded.hard_limit_hit_time,
updated_at = excluded.updated_at`,
		string(snapshot.Scope),
		snapshot.ScopeID,
		snapshot.Currency,
		snapshot.SoftLimitMicros,
		snapshot.HardLimitMicros,
		snapshot.SpentMicros,
		snapshot.Usage.InputTokens,
		snapshot.Usage.OutputTokens,
		snapshot.Usage.CacheReadTokens,
		snapshot.Usage.CacheWriteTokens,
		snapshot.EventCount,
		snapshot.LastEventID,
		formatOptionalTime(snapshot.LastEventTime),
		formatOptionalTime(snapshot.SoftLimitHitTime),
		formatOptionalTime(snapshot.HardLimitHitTime),
		snapshot.UpdatedAt.Format(time.RFC3339Nano),
	)
	if err != nil {
		return cost.Snapshot{}, fmt.Errorf("sqlite: upsert budget snapshot %s %q: %w", snapshot.Scope, snapshot.ScopeID, err)
	}
	return snapshot, nil
}

func parseOptionalBudgetTimes(snapshot *cost.Snapshot, lastEventTime, softLimitHitAt, hardLimitHitAt, updatedAt string) error {
	var err error
	if lastEventTime != "" {
		snapshot.LastEventTime, err = time.Parse(time.RFC3339Nano, lastEventTime)
		if err != nil {
			return fmt.Errorf("sqlite: parse budget last_event_time: %w", err)
		}
	}
	if softLimitHitAt != "" {
		snapshot.SoftLimitHitTime, err = time.Parse(time.RFC3339Nano, softLimitHitAt)
		if err != nil {
			return fmt.Errorf("sqlite: parse budget soft_limit_hit_time: %w", err)
		}
	}
	if hardLimitHitAt != "" {
		snapshot.HardLimitHitTime, err = time.Parse(time.RFC3339Nano, hardLimitHitAt)
		if err != nil {
			return fmt.Errorf("sqlite: parse budget hard_limit_hit_time: %w", err)
		}
	}
	snapshot.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return fmt.Errorf("sqlite: parse budget updated_at: %w", err)
	}
	return nil
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}
