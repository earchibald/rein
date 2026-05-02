package cost

import (
	"fmt"
	"strings"
	"time"
)

type Scope string

const (
	ScopeProject   Scope = "project"
	ScopeIssue     Scope = "issue"
	ScopeExecution Scope = "execution"
)

type Limits struct {
	SoftLimitMicros int64
	HardLimitMicros int64
}

func (l Limits) Validate() error {
	if l.SoftLimitMicros < 0 {
		return fmt.Errorf("cost: soft limit must be >= 0")
	}
	if l.HardLimitMicros < 0 {
		return fmt.Errorf("cost: hard limit must be >= 0")
	}
	if l.HardLimitMicros > 0 && l.SoftLimitMicros > 0 && l.SoftLimitMicros > l.HardLimitMicros {
		return fmt.Errorf("cost: soft limit %d exceeds hard limit %d", l.SoftLimitMicros, l.HardLimitMicros)
	}
	return nil
}

type Usage struct {
	InputTokens      int64 `json:"input_tokens"`
	OutputTokens     int64 `json:"output_tokens"`
	CacheReadTokens  int64 `json:"cache_read_tokens"`
	CacheWriteTokens int64 `json:"cache_write_tokens"`
}

func (u Usage) Validate() error {
	if u.InputTokens < 0 || u.OutputTokens < 0 || u.CacheReadTokens < 0 || u.CacheWriteTokens < 0 {
		return fmt.Errorf("cost: token counts must be >= 0")
	}
	return nil
}

func (u Usage) Add(other Usage) Usage {
	return Usage{
		InputTokens:      u.InputTokens + other.InputTokens,
		OutputTokens:     u.OutputTokens + other.OutputTokens,
		CacheReadTokens:  u.CacheReadTokens + other.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens + other.CacheWriteTokens,
	}
}

func (u Usage) TotalTokens() int64 {
	return u.InputTokens + u.OutputTokens + u.CacheReadTokens + u.CacheWriteTokens
}

type ScopeRef struct {
	Scope Scope
	ID    string
}

type ScopePath struct {
	ProjectID   string
	IssueID     string
	ExecutionID string
}

func (p ScopePath) Refs() []ScopeRef {
	refs := make([]ScopeRef, 0, 3)
	if id := strings.TrimSpace(p.ProjectID); id != "" {
		refs = append(refs, ScopeRef{Scope: ScopeProject, ID: id})
	}
	if id := strings.TrimSpace(p.IssueID); id != "" {
		refs = append(refs, ScopeRef{Scope: ScopeIssue, ID: id})
	}
	if id := strings.TrimSpace(p.ExecutionID); id != "" {
		refs = append(refs, ScopeRef{Scope: ScopeExecution, ID: id})
	}
	return refs
}

type Event struct {
	ID                 string            `json:"id"`
	Sequence           int64             `json:"sequence,omitempty"`
	Name               string            `json:"name"`
	Time               time.Time         `json:"time"`
	ProjectID          string            `json:"project_id"`
	IssueID            string            `json:"issue_id,omitempty"`
	ExecutionID        string            `json:"execution_id,omitempty"`
	WorkflowID         string            `json:"workflow_id,omitempty"`
	AdapterID          string            `json:"adapter_id,omitempty"`
	Currency           string            `json:"currency"`
	CostMicros         int64             `json:"cost_micros"`
	Usage              Usage             `json:"usage"`
	ResourceAttributes map[string]string `json:"resource_attributes,omitempty"`
	Attributes         map[string]string `json:"attributes,omitempty"`
}

func (e Event) Normalize(now time.Time) (Event, error) {
	e.ID = strings.TrimSpace(e.ID)
	e.Name = strings.TrimSpace(e.Name)
	e.ProjectID = strings.TrimSpace(e.ProjectID)
	e.IssueID = strings.TrimSpace(e.IssueID)
	e.ExecutionID = strings.TrimSpace(e.ExecutionID)
	e.WorkflowID = strings.TrimSpace(e.WorkflowID)
	e.AdapterID = strings.TrimSpace(e.AdapterID)
	e.Currency = strings.ToUpper(strings.TrimSpace(e.Currency))

	if e.ID == "" {
		return Event{}, fmt.Errorf("cost: event id is required")
	}
	if e.Name == "" {
		return Event{}, fmt.Errorf("cost: event name is required")
	}
	if e.ProjectID == "" {
		return Event{}, fmt.Errorf("cost: project id is required")
	}
	if e.ExecutionID != "" && e.IssueID == "" {
		return Event{}, fmt.Errorf("cost: execution-scoped event requires issue id")
	}
	if e.Currency == "" {
		return Event{}, fmt.Errorf("cost: currency is required")
	}
	if e.CostMicros < 0 {
		return Event{}, fmt.Errorf("cost: cost micros must be >= 0")
	}
	if err := e.Usage.Validate(); err != nil {
		return Event{}, err
	}
	if e.Time.IsZero() {
		e.Time = now.UTC()
	} else {
		e.Time = e.Time.UTC()
	}

	e.ResourceAttributes = cloneStringMap(e.ResourceAttributes)
	e.Attributes = cloneStringMap(e.Attributes)
	e.ResourceAttributes["service.name"] = "rein"
	e.ResourceAttributes["rein.project.id"] = e.ProjectID
	if e.IssueID != "" {
		e.ResourceAttributes["rein.issue.id"] = e.IssueID
	}
	if e.ExecutionID != "" {
		e.ResourceAttributes["rein.execution.id"] = e.ExecutionID
	}
	if e.WorkflowID != "" {
		e.ResourceAttributes["rein.workflow.id"] = e.WorkflowID
	}
	if e.AdapterID != "" {
		e.ResourceAttributes["rein.adapter.id"] = e.AdapterID
	}

	return e, nil
}

type Snapshot struct {
	Scope            Scope     `json:"scope"`
	ScopeID          string    `json:"scope_id"`
	Currency         string    `json:"currency"`
	SoftLimitMicros  int64     `json:"soft_limit_micros"`
	HardLimitMicros  int64     `json:"hard_limit_micros"`
	SpentMicros      int64     `json:"spent_micros"`
	Usage            Usage     `json:"usage"`
	EventCount       int64     `json:"event_count"`
	LastEventID      string    `json:"last_event_id,omitempty"`
	LastEventTime    time.Time `json:"last_event_time,omitempty"`
	SoftLimitHitTime time.Time `json:"soft_limit_hit_time,omitempty"`
	HardLimitHitTime time.Time `json:"hard_limit_hit_time,omitempty"`
	UpdatedAt        time.Time `json:"updated_at"`
}

func (s Snapshot) Validate() error {
	if !s.Scope.Valid() {
		return fmt.Errorf("cost: invalid scope %q", s.Scope)
	}
	if strings.TrimSpace(s.ScopeID) == "" {
		return fmt.Errorf("cost: scope id is required")
	}
	if s.Currency != "" && strings.TrimSpace(s.Currency) == "" {
		return fmt.Errorf("cost: currency is required when provided")
	}
	if err := (Limits{SoftLimitMicros: s.SoftLimitMicros, HardLimitMicros: s.HardLimitMicros}).Validate(); err != nil {
		return err
	}
	if s.SpentMicros < 0 || s.EventCount < 0 {
		return fmt.Errorf("cost: snapshot counters must be >= 0")
	}
	return s.Usage.Validate()
}

func (s Scope) Valid() bool {
	switch s {
	case ScopeProject, ScopeIssue, ScopeExecution:
		return true
	default:
		return false
	}
}

func (s Snapshot) SetLimits(limits Limits, at time.Time) (Snapshot, error) {
	if !s.Scope.Valid() {
		return Snapshot{}, fmt.Errorf("cost: invalid scope %q", s.Scope)
	}
	if strings.TrimSpace(s.ScopeID) == "" {
		return Snapshot{}, fmt.Errorf("cost: scope id is required")
	}
	if err := limits.Validate(); err != nil {
		return Snapshot{}, err
	}
	if at.IsZero() {
		at = time.Now().UTC()
	} else {
		at = at.UTC()
	}
	if s.Currency == "" {
		s.Currency = "USD"
	}
	s.UpdatedAt = at
	s.SoftLimitMicros = limits.SoftLimitMicros
	s.HardLimitMicros = limits.HardLimitMicros
	if s.SoftLimited() {
		if s.SoftLimitHitTime.IsZero() {
			s.SoftLimitHitTime = at
		}
	} else {
		s.SoftLimitHitTime = time.Time{}
	}
	if s.HardLimited() {
		if s.HardLimitHitTime.IsZero() {
			s.HardLimitHitTime = at
		}
	} else {
		s.HardLimitHitTime = time.Time{}
	}
	return s, nil
}

func (s Snapshot) Observe(event Event) (Snapshot, error) {
	if !s.Scope.Valid() {
		return Snapshot{}, fmt.Errorf("cost: invalid scope %q", s.Scope)
	}
	if strings.TrimSpace(s.ScopeID) == "" {
		return Snapshot{}, fmt.Errorf("cost: scope id is required")
	}
	if err := s.Validate(); err != nil {
		return Snapshot{}, err
	}
	if s.Currency == "" {
		s.Currency = event.Currency
	}
	if s.Currency != event.Currency {
		return Snapshot{}, fmt.Errorf("cost: currency mismatch for %s %q: have %s, got %s", s.Scope, s.ScopeID, s.Currency, event.Currency)
	}
	s.SpentMicros += event.CostMicros
	s.Usage = s.Usage.Add(event.Usage)
	s.EventCount++
	s.LastEventID = event.ID
	s.LastEventTime = event.Time
	s.UpdatedAt = event.Time
	if s.SoftLimited() && s.SoftLimitHitTime.IsZero() {
		s.SoftLimitHitTime = event.Time
	}
	if s.HardLimited() && s.HardLimitHitTime.IsZero() {
		s.HardLimitHitTime = event.Time
	}
	return s, nil
}

func (s Snapshot) SoftLimited() bool {
	return s.SoftLimitMicros > 0 && s.SpentMicros >= s.SoftLimitMicros
}

func (s Snapshot) HardLimited() bool {
	return s.HardLimitMicros > 0 && s.SpentMicros >= s.HardLimitMicros
}

type Admission struct {
	Allowed   bool
	Warnings  []Snapshot
	BlockedBy Snapshot
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return map[string]string{}
	}
	cloned := make(map[string]string, len(values))
	for key, value := range values {
		cloned[key] = value
	}
	return cloned
}
