package featureflags

import (
	"context"
	"errors"
	"testing"
)

func TestFlagNormalizeTrimsFields(t *testing.T) {
	t.Parallel()

	flag, err := (Flag{
		Key:         " managed-flow ",
		Description: " Gate managed flow steps ",
	}).Normalize()
	if err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}

	if flag.Key != "managed-flow" {
		t.Fatalf("Normalize() key = %q, want %q", flag.Key, "managed-flow")
	}
	if flag.Description != "Gate managed flow steps" {
		t.Fatalf("Normalize() description = %q", flag.Description)
	}
}

func TestFlagNormalizeRejectsEmptyKey(t *testing.T) {
	t.Parallel()

	if _, err := (Flag{Key: " \t "}).Normalize(); !errors.Is(err, ErrEmptyKey) {
		t.Fatalf("Normalize() error = %v, want %v", err, ErrEmptyKey)
	}
}

func TestNewRepositoryEvaluatorRejectsNilRepository(t *testing.T) {
	t.Parallel()

	if _, err := NewRepositoryEvaluator(nil); !errors.Is(err, ErrNilRepository) {
		t.Fatalf("NewRepositoryEvaluator() error = %v, want %v", err, ErrNilRepository)
	}
}

func TestRepositoryEvaluatorDefaultsMissingFlagsToDisabled(t *testing.T) {
	t.Parallel()

	evaluator, err := NewRepositoryEvaluator(stubRepository{getErr: ErrNotFound})
	if err != nil {
		t.Fatalf("NewRepositoryEvaluator() error = %v", err)
	}

	enabled, err := evaluator.EvaluateBoolean(context.Background(), EvaluationRequest{
		Key:        "managed-flow",
		SubjectKey: "user-123",
		Attributes: map[string]string{"team": "core"},
	})
	if err != nil {
		t.Fatalf("EvaluateBoolean() error = %v", err)
	}
	if enabled {
		t.Fatalf("EvaluateBoolean() = true, want false")
	}
}

func TestRepositoryEvaluatorReturnsStoredBoolean(t *testing.T) {
	t.Parallel()

	evaluator, err := NewRepositoryEvaluator(stubRepository{
		flag: Flag{Key: "managed-flow", Enabled: true},
	})
	if err != nil {
		t.Fatalf("NewRepositoryEvaluator() error = %v", err)
	}

	enabled, err := evaluator.EvaluateBoolean(context.Background(), EvaluationRequest{Key: "managed-flow"})
	if err != nil {
		t.Fatalf("EvaluateBoolean() error = %v", err)
	}
	if !enabled {
		t.Fatalf("EvaluateBoolean() = false, want true")
	}
}

type stubRepository struct {
	flag   Flag
	getErr error
}

func (s stubRepository) Create(context.Context, Flag) (Flag, error) {
	return Flag{}, errors.New("unexpected Create call")
}

func (s stubRepository) Delete(context.Context, string, int64) error {
	return errors.New("unexpected Delete call")
}

func (s stubRepository) Get(context.Context, string) (Flag, error) {
	if s.getErr != nil {
		return Flag{}, s.getErr
	}
	return s.flag, nil
}

func (s stubRepository) List(context.Context) ([]Flag, error) {
	return nil, errors.New("unexpected List call")
}

func (s stubRepository) Update(context.Context, Flag) (Flag, error) {
	return Flag{}, errors.New("unexpected Update call")
}
