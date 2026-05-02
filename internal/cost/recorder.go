package cost

import (
	"context"
	"fmt"
	"strings"
	"time"
)

type Store interface {
	AppendCostEvent(context.Context, Event) (Event, error)
	GetBudgetSnapshot(context.Context, Scope, string) (Snapshot, bool, error)
	UpsertBudgetSnapshot(context.Context, Snapshot) (Snapshot, error)
}

type Publisher interface {
	Publish(Event)
}

type Recorder struct {
	store     Store
	publisher Publisher
	now       func() time.Time
}

type Option func(*Recorder)

func WithPublisher(publisher Publisher) Option {
	return func(r *Recorder) {
		r.publisher = publisher
	}
}

func WithNow(now func() time.Time) Option {
	return func(r *Recorder) {
		r.now = now
	}
}

func NewRecorder(store Store, options ...Option) *Recorder {
	recorder := &Recorder{
		store: store,
		now:   func() time.Time { return time.Now().UTC() },
	}
	for _, option := range options {
		option(recorder)
	}
	return recorder
}

type RecordResult struct {
	Event     Event
	Snapshots []Snapshot
}

func (r *Recorder) ConfigureBudget(ctx context.Context, scope Scope, scopeID, currency string, limits Limits) (Snapshot, error) {
	if r == nil || r.store == nil {
		return Snapshot{}, fmt.Errorf("cost: recorder store is required")
	}
	if err := limits.Validate(); err != nil {
		return Snapshot{}, err
	}
	scopeID = stringsTrim(scopeID)
	currency = stringsUpperTrim(currency)
	if !scope.Valid() {
		return Snapshot{}, fmt.Errorf("cost: invalid scope %q", scope)
	}
	if scopeID == "" {
		return Snapshot{}, fmt.Errorf("cost: scope id is required")
	}
	if currency == "" {
		currency = "USD"
	}
	current, found, err := r.store.GetBudgetSnapshot(ctx, scope, scopeID)
	if err != nil {
		return Snapshot{}, err
	}
	if !found {
		current = Snapshot{Scope: scope, ScopeID: scopeID, Currency: currency}
	}
	if current.Currency == "" {
		current.Currency = currency
	}
	updated, err := current.SetLimits(limits, r.now())
	if err != nil {
		return Snapshot{}, err
	}
	return r.store.UpsertBudgetSnapshot(ctx, updated)
}

func (r *Recorder) CheckLaunch(ctx context.Context, path ScopePath) (Admission, error) {
	if r == nil || r.store == nil {
		return Admission{}, fmt.Errorf("cost: recorder store is required")
	}
	admission := Admission{Allowed: true}
	for _, ref := range path.Refs() {
		snapshot, found, err := r.store.GetBudgetSnapshot(ctx, ref.Scope, ref.ID)
		if err != nil {
			return Admission{}, err
		}
		if !found {
			continue
		}
		if snapshot.HardLimited() {
			admission.Allowed = false
			admission.BlockedBy = snapshot
			return admission, nil
		}
		if snapshot.SoftLimited() {
			admission.Warnings = append(admission.Warnings, snapshot)
		}
	}
	return admission, nil
}

func (r *Recorder) Record(ctx context.Context, event Event) (RecordResult, error) {
	if r == nil || r.store == nil {
		return RecordResult{}, fmt.Errorf("cost: recorder store is required")
	}
	normalized, err := event.Normalize(r.now())
	if err != nil {
		return RecordResult{}, err
	}
	storedEvent, err := r.store.AppendCostEvent(ctx, normalized)
	if err != nil {
		return RecordResult{}, err
	}
	result := RecordResult{Event: storedEvent}
	for _, ref := range (ScopePath{ProjectID: storedEvent.ProjectID, IssueID: storedEvent.IssueID, ExecutionID: storedEvent.ExecutionID}).Refs() {
		snapshot, found, err := r.store.GetBudgetSnapshot(ctx, ref.Scope, ref.ID)
		if err != nil {
			return RecordResult{}, err
		}
		if !found {
			snapshot = Snapshot{Scope: ref.Scope, ScopeID: ref.ID, Currency: storedEvent.Currency}
		}
		updated, err := snapshot.Observe(storedEvent)
		if err != nil {
			return RecordResult{}, err
		}
		updated, err = r.store.UpsertBudgetSnapshot(ctx, updated)
		if err != nil {
			return RecordResult{}, err
		}
		result.Snapshots = append(result.Snapshots, updated)
	}
	if r.publisher != nil {
		r.publisher.Publish(storedEvent)
	}
	return result, nil
}

func stringsTrim(value string) string {
	return strings.TrimSpace(value)
}

func stringsUpperTrim(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}
