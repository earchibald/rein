package sqlite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/earchibald/rein/internal/featureflags"
)

type FeatureFlagRepository struct {
	store *Store
}

func NewFeatureFlagRepository(store *Store) (*FeatureFlagRepository, error) {
	if store == nil {
		return nil, featureflags.ErrNilRepository
	}

	return &FeatureFlagRepository{store: store}, nil
}

func (r *FeatureFlagRepository) Create(ctx context.Context, flag featureflags.Flag) (featureflags.Flag, error) {
	store, err := r.requireStore()
	if err != nil {
		return featureflags.Flag{}, err
	}

	normalized, err := flag.Normalize()
	if err != nil {
		return featureflags.Flag{}, err
	}

	payload, err := marshalFeatureFlag(normalized)
	if err != nil {
		return featureflags.Flag{}, fmt.Errorf("sqlite: marshal feature flag %q: %w", normalized.Key, err)
	}

	record, err := store.Create(ctx, FeatureFlagKind, normalized.Key, payload)
	if err != nil {
		return featureflags.Flag{}, mapFeatureFlagRepositoryError("create", normalized.Key, err)
	}

	return decodeFeatureFlagRecord(record)
}

func (r *FeatureFlagRepository) Delete(ctx context.Context, key string, expectedLockVersion int64) error {
	store, err := r.requireStore()
	if err != nil {
		return err
	}

	normalizedKey := strings.TrimSpace(key)
	if normalizedKey == "" {
		return featureflags.ErrEmptyKey
	}

	if err := store.Delete(ctx, FeatureFlagKind, normalizedKey, expectedLockVersion); err != nil {
		return mapFeatureFlagRepositoryError("delete", normalizedKey, err)
	}

	return nil
}

func (r *FeatureFlagRepository) Get(ctx context.Context, key string) (featureflags.Flag, error) {
	store, err := r.requireStore()
	if err != nil {
		return featureflags.Flag{}, err
	}

	normalizedKey := strings.TrimSpace(key)
	if normalizedKey == "" {
		return featureflags.Flag{}, featureflags.ErrEmptyKey
	}

	record, err := store.Get(ctx, FeatureFlagKind, normalizedKey)
	if err != nil {
		return featureflags.Flag{}, mapFeatureFlagRepositoryError("get", normalizedKey, err)
	}

	return decodeFeatureFlagRecord(record)
}

func (r *FeatureFlagRepository) List(ctx context.Context) ([]featureflags.Flag, error) {
	store, err := r.requireStore()
	if err != nil {
		return nil, err
	}

	rows, err := store.DB().QueryContext(ctx, `SELECT id, lock_version, payload, created_at, updated_at FROM featureflags ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("sqlite: list feature flags: %w", err)
	}
	defer rows.Close()

	flags := []featureflags.Flag{}
	for rows.Next() {
		var (
			record       Record
			createdAtRaw string
			updatedAtRaw string
		)

		if err := rows.Scan(&record.ID, &record.LockVersion, &record.Payload, &createdAtRaw, &updatedAtRaw); err != nil {
			return nil, fmt.Errorf("sqlite: scan feature flag row: %w", err)
		}

		record.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAtRaw)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse feature flag created_at %q: %w", record.ID, err)
		}
		record.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAtRaw)
		if err != nil {
			return nil, fmt.Errorf("sqlite: parse feature flag updated_at %q: %w", record.ID, err)
		}

		flag, err := decodeFeatureFlagRecord(record)
		if err != nil {
			return nil, err
		}
		flags = append(flags, flag)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlite: iterate feature flags: %w", err)
	}

	return flags, nil
}

func (r *FeatureFlagRepository) Update(ctx context.Context, flag featureflags.Flag) (featureflags.Flag, error) {
	store, err := r.requireStore()
	if err != nil {
		return featureflags.Flag{}, err
	}

	normalized, err := flag.Normalize()
	if err != nil {
		return featureflags.Flag{}, err
	}

	payload, err := marshalFeatureFlag(normalized)
	if err != nil {
		return featureflags.Flag{}, fmt.Errorf("sqlite: marshal feature flag %q: %w", normalized.Key, err)
	}

	record, err := store.Update(ctx, FeatureFlagKind, normalized.Key, normalized.LockVersion, payload)
	if err != nil {
		return featureflags.Flag{}, mapFeatureFlagRepositoryError("update", normalized.Key, err)
	}

	return decodeFeatureFlagRecord(record)
}

func (r *FeatureFlagRepository) requireStore() (*Store, error) {
	if r == nil || r.store == nil {
		return nil, featureflags.ErrNilRepository
	}

	return r.store, nil
}

type featureFlagPayload struct {
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
}

func marshalFeatureFlag(flag featureflags.Flag) (json.RawMessage, error) {
	return json.Marshal(featureFlagPayload{
		Description: flag.Description,
		Enabled:     flag.Enabled,
	})
}

func decodeFeatureFlagRecord(record Record) (featureflags.Flag, error) {
	var payload featureFlagPayload
	if err := json.Unmarshal(record.Payload, &payload); err != nil {
		return featureflags.Flag{}, fmt.Errorf("sqlite: decode feature flag %q: %w", record.ID, err)
	}

	return featureflags.Flag{
		Key:         record.ID,
		Description: payload.Description,
		Enabled:     payload.Enabled,
		LockVersion: record.LockVersion,
		CreatedAt:   record.CreatedAt,
		UpdatedAt:   record.UpdatedAt,
	}, nil
}

func mapFeatureFlagRepositoryError(operation, key string, err error) error {
	switch {
	case errors.Is(err, ErrEmptyID):
		return fmt.Errorf("sqlite: %s feature flag %q: %w", operation, key, featureflags.ErrEmptyKey)
	case errors.Is(err, ErrLockVersionMismatch):
		return fmt.Errorf("sqlite: %s feature flag %q: %w", operation, key, featureflags.ErrVersionMismatch)
	case errors.Is(err, ErrNotFound):
		return fmt.Errorf("sqlite: %s feature flag %q: %w", operation, key, featureflags.ErrNotFound)
	case strings.Contains(err.Error(), "UNIQUE constraint failed: featureflags.id"):
		return fmt.Errorf("sqlite: %s feature flag %q: %w", operation, key, featureflags.ErrAlreadyExists)
	default:
		return fmt.Errorf("sqlite: %s feature flag %q: %w", operation, key, err)
	}
}
