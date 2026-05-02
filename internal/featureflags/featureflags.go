package featureflags

import (
	"context"
	"errors"
	"strings"
	"time"
)

var (
	ErrAlreadyExists   = errors.New("featureflags: flag already exists")
	ErrEmptyKey        = errors.New("featureflags: empty key")
	ErrNilRepository   = errors.New("featureflags: nil repository")
	ErrNotFound        = errors.New("featureflags: flag not found")
	ErrVersionMismatch = errors.New("featureflags: lock version mismatch")
)

// Flag is the typed MVP feature-flag shape persisted by the repository.
type Flag struct {
	Key         string
	Description string
	Enabled     bool
	LockVersion int64
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

func (f Flag) Normalize() (Flag, error) {
	key := strings.TrimSpace(f.Key)
	if key == "" {
		return Flag{}, ErrEmptyKey
	}

	f.Key = key
	f.Description = strings.TrimSpace(f.Description)
	return f, nil
}

// Repository owns CRUD for persisted feature flags. A future Flipt-backed
// implementation can satisfy the same contract without changing callers.
type Repository interface {
	Create(context.Context, Flag) (Flag, error)
	Delete(context.Context, string, int64) error
	Get(context.Context, string) (Flag, error)
	List(context.Context) ([]Flag, error)
	Update(context.Context, Flag) (Flag, error)
}

// EvaluationRequest leaves room for a future Flipt-backed evaluator to use
// subject/context targeting while the SQLite MVP only resolves by Key.
type EvaluationRequest struct {
	Key        string
	SubjectKey string
	Attributes map[string]string
}

// Evaluator is the runtime seam for flag checks.
type Evaluator interface {
	EvaluateBoolean(context.Context, EvaluationRequest) (bool, error)
}

type repositoryEvaluator struct {
	repo Repository
}

func NewRepositoryEvaluator(repo Repository) (Evaluator, error) {
	if repo == nil {
		return nil, ErrNilRepository
	}

	return repositoryEvaluator{repo: repo}, nil
}

func (e repositoryEvaluator) EvaluateBoolean(ctx context.Context, request EvaluationRequest) (bool, error) {
	key := strings.TrimSpace(request.Key)
	if key == "" {
		return false, ErrEmptyKey
	}

	flag, err := e.repo.Get(ctx, key)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}

	return flag.Enabled, nil
}
