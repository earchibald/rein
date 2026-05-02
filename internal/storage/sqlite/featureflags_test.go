package sqlite

import (
	"context"
	"errors"
	"testing"

	"github.com/earchibald/rein/internal/featureflags"
)

func TestNewFeatureFlagRepositoryRejectsNilStore(t *testing.T) {
	t.Parallel()

	if _, err := NewFeatureFlagRepository(nil); !errors.Is(err, featureflags.ErrNilRepository) {
		t.Fatalf("NewFeatureFlagRepository() error = %v, want %v", err, featureflags.ErrNilRepository)
	}
}

func TestFeatureFlagRepositoryCRUDAndEvaluation(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	repo := openFeatureFlagRepository(t)
	evaluator, err := featureflags.NewRepositoryEvaluator(repo)
	if err != nil {
		t.Fatalf("NewRepositoryEvaluator() error = %v", err)
	}

	created, err := repo.Create(ctx, featureflags.Flag{
		Key:         " managed-flow ",
		Description: " Gate managed-flow steps ",
		Enabled:     false,
	})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if created.Key != "managed-flow" {
		t.Fatalf("Create() key = %q, want %q", created.Key, "managed-flow")
	}
	if created.Description != "Gate managed-flow steps" {
		t.Fatalf("Create() description = %q", created.Description)
	}
	if created.LockVersion != 1 {
		t.Fatalf("Create() lock version = %d, want 1", created.LockVersion)
	}

	got, err := repo.Get(ctx, "managed-flow")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got != created {
		t.Fatalf("Get() = %+v, want %+v", got, created)
	}

	if enabled, err := evaluator.EvaluateBoolean(ctx, featureflags.EvaluationRequest{
		Key:        "managed-flow",
		SubjectKey: "user-123",
		Attributes: map[string]string{"team": "core"},
	}); err != nil {
		t.Fatalf("EvaluateBoolean() error = %v", err)
	} else if enabled {
		t.Fatalf("EvaluateBoolean() = true, want false")
	}

	if _, err := repo.Create(ctx, featureflags.Flag{Key: "managed-flow", Enabled: true}); !errors.Is(err, featureflags.ErrAlreadyExists) {
		t.Fatalf("Create() duplicate error = %v, want %v", err, featureflags.ErrAlreadyExists)
	}

	second, err := repo.Create(ctx, featureflags.Flag{Key: "adapter-v2", Enabled: true})
	if err != nil {
		t.Fatalf("Create() second flag error = %v", err)
	}

	listed, err := repo.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(listed) != 2 {
		t.Fatalf("List() len = %d, want 2", len(listed))
	}
	if listed[0].Key != "adapter-v2" || listed[1].Key != "managed-flow" {
		t.Fatalf("List() keys = [%s, %s], want [adapter-v2, managed-flow]", listed[0].Key, listed[1].Key)
	}
	if listed[0] != second || listed[1] != created {
		t.Fatalf("List() records = %+v", listed)
	}

	if _, err := repo.Update(ctx, featureflags.Flag{
		Key:         "managed-flow",
		Description: created.Description,
		Enabled:     true,
		LockVersion: 99,
	}); !errors.Is(err, featureflags.ErrVersionMismatch) {
		t.Fatalf("Update() stale lock error = %v, want %v", err, featureflags.ErrVersionMismatch)
	}

	updated, err := repo.Update(ctx, featureflags.Flag{
		Key:         created.Key,
		Description: created.Description,
		Enabled:     true,
		LockVersion: created.LockVersion,
	})
	if err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	if updated.LockVersion != 2 {
		t.Fatalf("Update() lock version = %d, want 2", updated.LockVersion)
	}

	if enabled, err := evaluator.EvaluateBoolean(ctx, featureflags.EvaluationRequest{Key: "managed-flow"}); err != nil {
		t.Fatalf("EvaluateBoolean() after update error = %v", err)
	} else if !enabled {
		t.Fatalf("EvaluateBoolean() after update = false, want true")
	}

	if err := repo.Delete(ctx, created.Key, created.LockVersion); !errors.Is(err, featureflags.ErrVersionMismatch) {
		t.Fatalf("Delete() stale lock error = %v, want %v", err, featureflags.ErrVersionMismatch)
	}

	if err := repo.Delete(ctx, updated.Key, updated.LockVersion); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	if _, err := repo.Get(ctx, updated.Key); !errors.Is(err, featureflags.ErrNotFound) {
		t.Fatalf("Get() after delete error = %v, want %v", err, featureflags.ErrNotFound)
	}
	if enabled, err := evaluator.EvaluateBoolean(ctx, featureflags.EvaluationRequest{Key: updated.Key}); err != nil {
		t.Fatalf("EvaluateBoolean() after delete error = %v", err)
	} else if enabled {
		t.Fatalf("EvaluateBoolean() after delete = true, want false")
	}
}

func openFeatureFlagRepository(tb testing.TB) *FeatureFlagRepository {
	tb.Helper()

	repo, err := NewFeatureFlagRepository(openTestStore(tb, context.Background()))
	if err != nil {
		tb.Fatalf("NewFeatureFlagRepository() error = %v", err)
	}

	return repo
}
