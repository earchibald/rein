package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/storage/sqlite"
)

const (
	// Workflow step inputs carry engine semantics until the protobuf surface grows
	// dedicated workflow-engine fields.
	InputLane              = "lane"
	InputLaneAttach        = "lane_attach"
	InputOperation         = "operation"
	InputBail              = "bail"
	InputOnCancel          = "on_cancel"
	InputBackwardOperation = "backward_operation"
	InputBackwardTo        = "backward_to"

	trunkLane = "trunk"
)

var ErrInvalidWorkflow = errors.New("workflow: invalid workflow")

type CancelPolicy string

const (
	CancelPolicyBail   CancelPolicy = "bail"
	CancelPolicyRewind CancelPolicy = "rewind"
)

type Direction string

const (
	DirectionForward  Direction = "forward"
	DirectionBackward Direction = "backward"
)

type EffectStatus string

const (
	EffectStatusPending EffectStatus = "pending"
	EffectStatusApplied EffectStatus = "applied"
	EffectStatusFailed  EffectStatus = "failed"
)

type StepStatus string

const (
	StepStatusRunning   StepStatus = "running"
	StepStatusSucceeded StepStatus = "succeeded"
	StepStatusFailed    StepStatus = "failed"
)

type Phase struct {
	ID                string
	Name              string
	AdapterID         string
	Lane              string
	LaneAttach        string
	Operation         string
	BackwardOperation string
	BackwardTo        string
	Bail              bool
	OnCancel          CancelPolicy
	Inputs            map[string]string
}

type Lateral struct {
	ID     string
	Attach string
	Phases []Phase
}

type Definition struct {
	Trunk     []Phase
	Laterals  []Lateral
	phaseByID map[string]Phase
}

func (d Definition) Phase(id string) (Phase, bool) {
	phase, ok := d.phaseByID[id]
	return phase, ok
}

func (d Definition) LateralsFor(phaseID string) []Lateral {
	var laterals []Lateral
	for _, lateral := range d.Laterals {
		if lateral.Attach == phaseID {
			laterals = append(laterals, lateral)
		}
	}
	return laterals
}

type TaskStep struct {
	ExecutionID string     `json:"execution_id"`
	WorkflowID  string     `json:"workflow_id"`
	IssueID     string     `json:"issue_id"`
	PhaseID     string     `json:"phase_id"`
	Lane        string     `json:"lane"`
	Direction   Direction  `json:"direction"`
	Operation   string     `json:"operation"`
	Status      StepStatus `json:"status"`
	Sequence    int        `json:"sequence"`
	Error       string     `json:"error,omitempty"`
}

type SideEffect struct {
	ExecutionID string            `json:"execution_id"`
	WorkflowID  string            `json:"workflow_id"`
	IssueID     string            `json:"issue_id"`
	PhaseID     string            `json:"phase_id"`
	Lane        string            `json:"lane"`
	Direction   Direction         `json:"direction"`
	AdapterID   string            `json:"adapter_id"`
	Operation   string            `json:"operation"`
	Status      EffectStatus      `json:"status"`
	Sequence    int               `json:"sequence"`
	Reason      string            `json:"reason,omitempty"`
	TargetPhase string            `json:"target_phase,omitempty"`
	Inputs      map[string]string `json:"inputs,omitempty"`
	Outputs     map[string]string `json:"outputs,omitempty"`
	Error       string            `json:"error,omitempty"`
}

type RuntimeState struct {
	Workflow  *reinv1.Workflow
	Issue     *reinv1.Issue
	Execution *reinv1.Execution
}

type Runner interface {
	Run(context.Context, *RuntimeState, Phase, Direction, *SideEffect) error
}

type Engine struct {
	store *sqlite.Store
}

func New(store *sqlite.Store) *Engine {
	return &Engine{store: store}
}

func Compile(workflow *reinv1.Workflow) (Definition, []*reinv1.ValidationMessage) {
	var messages []*reinv1.ValidationMessage
	if workflow == nil {
		return Definition{}, []*reinv1.ValidationMessage{validationError("workflow", "workflow is required")}
	}
	if strings.TrimSpace(workflow.GetId()) == "" {
		messages = append(messages, validationError("workflow.id", "workflow id is required"))
	}
	if strings.TrimSpace(workflow.GetName()) == "" {
		messages = append(messages, validationError("workflow.name", "workflow name is required"))
	}
	if len(workflow.GetSteps()) == 0 {
		messages = append(messages, validationError("workflow.steps", "workflow must declare at least one step"))
		return Definition{}, messages
	}

	definition := Definition{phaseByID: map[string]Phase{}}
	lateralIndex := map[string]int{}
	seen := map[string]struct{}{}

	for index, step := range workflow.GetSteps() {
		field := fmt.Sprintf("workflow.steps[%d]", index)
		phase, stepMessages := compilePhase(field, step)
		messages = append(messages, stepMessages...)
		if len(stepMessages) > 0 {
			continue
		}
		if _, ok := seen[phase.ID]; ok {
			messages = append(messages, validationError(field+".id", "step id must be unique"))
			continue
		}
		seen[phase.ID] = struct{}{}
		definition.phaseByID[phase.ID] = phase

		if phase.Lane == trunkLane {
			definition.Trunk = append(definition.Trunk, phase)
			continue
		}

		idx, ok := lateralIndex[phase.Lane]
		if !ok {
			definition.Laterals = append(definition.Laterals, Lateral{ID: phase.Lane, Attach: phase.LaneAttach})
			idx = len(definition.Laterals) - 1
			lateralIndex[phase.Lane] = idx
		}
		lateral := &definition.Laterals[idx]
		if lateral.Attach != phase.LaneAttach {
			messages = append(messages, validationError(field+".inputs.lane_attach", fmt.Sprintf("lateral %q must attach to a single trunk phase", phase.Lane)))
			continue
		}
		lateral.Phases = append(lateral.Phases, phase)
	}

	messages = append(messages, validateDefinition(definition)...)
	if len(messages) > 0 {
		return Definition{}, messages
	}
	return definition, nil
}

func (e *Engine) Validate(workflow *reinv1.Workflow, hasAdapter func(string) bool) []*reinv1.ValidationMessage {
	definition, messages := Compile(workflow)
	if len(messages) > 0 {
		return messages
	}
	for _, phase := range definition.Trunk {
		if hasAdapter != nil && !hasAdapter(phase.AdapterID) {
			messages = append(messages, validationError("workflow.trunk", fmt.Sprintf("unknown adapter %q", phase.AdapterID)))
		}
	}
	for _, lateral := range definition.Laterals {
		for _, phase := range lateral.Phases {
			if hasAdapter != nil && !hasAdapter(phase.AdapterID) {
				messages = append(messages, validationError("workflow.laterals", fmt.Sprintf("unknown adapter %q", phase.AdapterID)))
			}
		}
	}
	return messages
}

func (e *Engine) Run(ctx context.Context, state *RuntimeState, runner Runner) error {
	definition, messages := Compile(state.Workflow)
	if len(messages) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidWorkflow, messages[0].GetMessage())
	}

	stack, err := e.completedStack(ctx, definition, state.Execution.GetId())
	if err != nil {
		return err
	}

	for _, phase := range definition.Trunk {
		if containsPhase(stack, phase.ID) {
			continue
		}
		if err := e.runPhase(ctx, state, runner, phase, DirectionForward, "", ""); err != nil {
			if phase.Bail {
				if rewindErr := e.rewind(ctx, state, runner, stack, "", "bail"); rewindErr != nil {
					return errors.Join(err, rewindErr)
				}
			}
			return err
		}
		stack = append(stack, phase)
		for _, lateral := range definition.LateralsFor(phase.ID) {
			for _, lateralPhase := range lateral.Phases {
				if containsPhase(stack, lateralPhase.ID) {
					continue
				}
				if err := e.runPhase(ctx, state, runner, lateralPhase, DirectionForward, "", lateral.Attach); err != nil {
					if lateralPhase.Bail {
						if rewindErr := e.rewind(ctx, state, runner, stack, "", "bail"); rewindErr != nil {
							return errors.Join(err, rewindErr)
						}
					}
					return err
				}
				stack = append(stack, lateralPhase)
			}
		}
	}
	return nil
}

func (e *Engine) Cancel(ctx context.Context, state *RuntimeState, runner Runner, reason string) error {
	definition, messages := Compile(state.Workflow)
	if len(messages) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidWorkflow, messages[0].GetMessage())
	}
	stack, err := e.completedStack(ctx, definition, state.Execution.GetId())
	if err != nil {
		return err
	}
	for len(stack) > 0 {
		phase := stack[len(stack)-1]
		if phase.OnCancel != CancelPolicyRewind {
			break
		}
		if err := e.runPhase(ctx, state, runner, phase, DirectionBackward, "cancel:"+reason, phase.BackwardTo); err != nil {
			return err
		}
		stack = stack[:len(stack)-1]
	}
	return nil
}

func (e *Engine) Rewind(ctx context.Context, state *RuntimeState, runner Runner, targetPhaseID, reason string) error {
	definition, messages := Compile(state.Workflow)
	if len(messages) > 0 {
		return fmt.Errorf("%w: %s", ErrInvalidWorkflow, messages[0].GetMessage())
	}
	stack, err := e.completedStack(ctx, definition, state.Execution.GetId())
	if err != nil {
		return err
	}
	if targetPhaseID != "" && !containsPhase(stack, targetPhaseID) {
		return fmt.Errorf("workflow: phase %q is not active", targetPhaseID)
	}
	return e.rewind(ctx, state, runner, stack, targetPhaseID, "rewind:"+reason)
}

func (e *Engine) ListTaskSteps(ctx context.Context, executionID string) ([]TaskStep, error) {
	records, err := e.store.List(ctx, sqlite.TaskStepKind, sqlite.ListOptions{JSONEquals: map[string]string{"execution_id": executionID}})
	if err != nil {
		return nil, err
	}
	steps := make([]TaskStep, 0, len(records))
	for _, record := range records {
		var step TaskStep
		if err := json.Unmarshal(record.Payload, &step); err != nil {
			return nil, fmt.Errorf("workflow: decode task step %q: %w", record.ID, err)
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func (e *Engine) ListSideEffects(ctx context.Context, executionID string) ([]SideEffect, error) {
	records, err := e.store.List(ctx, sqlite.SideEffectKind, sqlite.ListOptions{JSONEquals: map[string]string{"execution_id": executionID}})
	if err != nil {
		return nil, err
	}
	effects := make([]SideEffect, 0, len(records))
	for _, record := range records {
		var effect SideEffect
		if err := json.Unmarshal(record.Payload, &effect); err != nil {
			return nil, fmt.Errorf("workflow: decode side effect %q: %w", record.ID, err)
		}
		effects = append(effects, effect)
	}
	return effects, nil
}

func (e *Engine) completedStack(ctx context.Context, definition Definition, executionID string) ([]Phase, error) {
	steps, err := e.ListTaskSteps(ctx, executionID)
	if err != nil {
		return nil, err
	}
	var stack []Phase
	for _, step := range steps {
		phase, ok := definition.Phase(step.PhaseID)
		if !ok || step.Status != StepStatusSucceeded {
			continue
		}
		switch step.Direction {
		case DirectionForward:
			stack = append(stack, phase)
		case DirectionBackward:
			if len(stack) == 0 {
				continue
			}
			stack = stack[:len(stack)-1]
		}
	}
	return stack, nil
}

func (e *Engine) rewind(ctx context.Context, state *RuntimeState, runner Runner, stack []Phase, targetPhaseID, reason string) error {
	for len(stack) > 0 {
		phase := stack[len(stack)-1]
		if targetPhaseID != "" && phase.ID == targetPhaseID {
			break
		}
		if err := e.runPhase(ctx, state, runner, phase, DirectionBackward, reason, phase.BackwardTo); err != nil {
			return err
		}
		stack = stack[:len(stack)-1]
	}
	return nil
}

func (e *Engine) runPhase(ctx context.Context, state *RuntimeState, runner Runner, phase Phase, direction Direction, reason, target string) error {
	sequence, err := e.nextSequence(ctx, state.Execution.GetId())
	if err != nil {
		return err
	}
	operation := phase.Operation
	if direction == DirectionBackward {
		operation = phase.BackwardOperation
		if operation == "" {
			operation = "rewind"
		}
	}

	step := TaskStep{
		ExecutionID: state.Execution.GetId(),
		WorkflowID:  state.Workflow.GetId(),
		IssueID:     state.Issue.GetId(),
		PhaseID:     phase.ID,
		Lane:        phase.Lane,
		Direction:   direction,
		Operation:   operation,
		Status:      StepStatusRunning,
		Sequence:    sequence,
	}
	stepRecord, err := e.createTaskStep(ctx, step)
	if err != nil {
		return err
	}

	effect := SideEffect{
		ExecutionID: state.Execution.GetId(),
		WorkflowID:  state.Workflow.GetId(),
		IssueID:     state.Issue.GetId(),
		PhaseID:     phase.ID,
		Lane:        phase.Lane,
		Direction:   direction,
		AdapterID:   phase.AdapterID,
		Operation:   operation,
		Status:      EffectStatusPending,
		Sequence:    sequence,
		Reason:      reason,
		TargetPhase: target,
		Inputs:      cloneStringMap(phase.Inputs),
	}
	effectRecord, err := e.createSideEffect(ctx, effect)
	if err != nil {
		return err
	}

	shouldInvokeRunner := direction == DirectionForward || phase.BackwardOperation != ""
	if shouldInvokeRunner && runner != nil {
		err = runner.Run(ctx, state, phase, direction, &effect)
	}
	if err != nil {
		effect.Status = EffectStatusFailed
		effect.Error = err.Error()
		step.Status = StepStatusFailed
		step.Error = err.Error()
	} else {
		effect.Status = EffectStatusApplied
		step.Status = StepStatusSucceeded
	}

	if _, updateErr := e.updateSideEffect(ctx, effectRecord, effect); updateErr != nil {
		return updateErr
	}
	if _, updateErr := e.updateTaskStep(ctx, stepRecord, step); updateErr != nil {
		return updateErr
	}
	return err
}

func (e *Engine) nextSequence(ctx context.Context, executionID string) (int, error) {
	effects, err := e.ListSideEffects(ctx, executionID)
	if err != nil {
		return 0, err
	}
	return len(effects) + 1, nil
}

func (e *Engine) createTaskStep(ctx context.Context, step TaskStep) (sqlite.Record, error) {
	payload, err := json.Marshal(step)
	if err != nil {
		return sqlite.Record{}, fmt.Errorf("workflow: encode task step: %w", err)
	}
	return e.store.Create(ctx, sqlite.TaskStepKind, recordID(step.ExecutionID, step.Sequence, step.Direction, step.PhaseID, "step"), payload)
}

func (e *Engine) updateTaskStep(ctx context.Context, record sqlite.Record, step TaskStep) (sqlite.Record, error) {
	payload, err := json.Marshal(step)
	if err != nil {
		return sqlite.Record{}, fmt.Errorf("workflow: encode task step: %w", err)
	}
	return e.store.Update(ctx, sqlite.TaskStepKind, record.ID, record.LockVersion, payload)
}

func (e *Engine) createSideEffect(ctx context.Context, effect SideEffect) (sqlite.Record, error) {
	payload, err := json.Marshal(effect)
	if err != nil {
		return sqlite.Record{}, fmt.Errorf("workflow: encode side effect: %w", err)
	}
	return e.store.Create(ctx, sqlite.SideEffectKind, recordID(effect.ExecutionID, effect.Sequence, effect.Direction, effect.PhaseID, "effect"), payload)
}

func (e *Engine) updateSideEffect(ctx context.Context, record sqlite.Record, effect SideEffect) (sqlite.Record, error) {
	payload, err := json.Marshal(effect)
	if err != nil {
		return sqlite.Record{}, fmt.Errorf("workflow: encode side effect: %w", err)
	}
	return e.store.Update(ctx, sqlite.SideEffectKind, record.ID, record.LockVersion, payload)
}

func compilePhase(field string, step *reinv1.WorkflowStep) (Phase, []*reinv1.ValidationMessage) {
	var messages []*reinv1.ValidationMessage
	if step == nil {
		return Phase{}, []*reinv1.ValidationMessage{validationError(field, "step is required")}
	}
	phase := Phase{
		ID:        strings.TrimSpace(step.GetId()),
		Name:      strings.TrimSpace(step.GetName()),
		AdapterID: strings.TrimSpace(step.GetAdapterId()),
		Operation: strings.TrimSpace(step.GetInputs()[InputOperation]),
		Inputs:    cloneStringMap(step.GetInputs()),
	}
	if phase.Operation == "" {
		phase.Operation = phase.ID
	}
	lane := strings.TrimSpace(step.GetInputs()[InputLane])
	if lane == "" {
		lane = trunkLane
	}
	phase.Lane = lane
	phase.LaneAttach = strings.TrimSpace(step.GetInputs()[InputLaneAttach])
	phase.BackwardOperation = strings.TrimSpace(step.GetInputs()[InputBackwardOperation])
	phase.BackwardTo = strings.TrimSpace(step.GetInputs()[InputBackwardTo])
	if phase.BackwardTo == "" && phase.Lane != trunkLane {
		phase.BackwardTo = phase.LaneAttach
	}
	if phase.BackwardTo == "" && phase.Lane == trunkLane && len(step.GetDependsOn()) > 0 {
		phase.BackwardTo = step.GetDependsOn()[0]
	}

	if phase.ID == "" {
		messages = append(messages, validationError(field+".id", "step id is required"))
	}
	if phase.Name == "" {
		messages = append(messages, validationError(field+".name", "step name is required"))
	}
	if phase.AdapterID == "" {
		messages = append(messages, validationError(field+".adapter_id", "step adapter_id is required"))
	}
	if phase.Lane != trunkLane && phase.LaneAttach == "" {
		messages = append(messages, validationError(field+".inputs.lane_attach", "lateral steps require lane_attach"))
	}

	if raw := strings.TrimSpace(step.GetInputs()[InputBail]); raw != "" {
		bail, err := strconv.ParseBool(raw)
		if err != nil {
			messages = append(messages, validationError(field+".inputs.bail", "bail must be a boolean"))
		} else {
			phase.Bail = bail
		}
	}

	rawCancel := strings.TrimSpace(step.GetInputs()[InputOnCancel])
	switch rawCancel {
	case "", string(CancelPolicyBail):
		phase.OnCancel = CancelPolicyBail
	case string(CancelPolicyRewind):
		phase.OnCancel = CancelPolicyRewind
	default:
		messages = append(messages, validationError(field+".inputs.on_cancel", "on_cancel must be bail or rewind"))
	}
	return phase, messages
}

func validateDefinition(definition Definition) []*reinv1.ValidationMessage {
	var messages []*reinv1.ValidationMessage
	if len(definition.Trunk) == 0 {
		messages = append(messages, validationError("workflow.steps", "workflow must declare at least one trunk step"))
		return messages
	}

	for index, phase := range definition.Trunk {
		if phase.LaneAttach != "" {
			messages = append(messages, validationError("workflow.trunk", fmt.Sprintf("trunk step %q cannot declare lane_attach", phase.ID)))
		}
		if index == 0 {
			continue
		}
	}

	seenAttach := map[string]struct{}{}
	for _, phase := range definition.Trunk {
		seenAttach[phase.ID] = struct{}{}
	}
	for _, lateral := range definition.Laterals {
		if _, ok := seenAttach[lateral.Attach]; !ok {
			messages = append(messages, validationError("workflow.laterals", fmt.Sprintf("lateral %q attaches to unknown trunk phase %q", lateral.ID, lateral.Attach)))
			continue
		}
		if len(lateral.Phases) == 0 {
			messages = append(messages, validationError("workflow.laterals", fmt.Sprintf("lateral %q must contain at least one step", lateral.ID)))
			continue
		}
		for _, phase := range lateral.Phases {
			if phase.LaneAttach != lateral.Attach {
				messages = append(messages, validationError("workflow.laterals", fmt.Sprintf("lateral %q must attach all steps to %q", lateral.ID, lateral.Attach)))
			}
		}
	}
	return messages
}

func validationError(field, message string) *reinv1.ValidationMessage {
	return &reinv1.ValidationMessage{Severity: reinv1.ValidationMessage_SEVERITY_ERROR, Field: field, Message: message}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	cloned := make(map[string]string, len(keys))
	for _, key := range keys {
		cloned[key] = values[key]
	}
	return cloned
}

func containsPhase(phases []Phase, id string) bool {
	for _, phase := range phases {
		if phase.ID == id {
			return true
		}
	}
	return false
}

func recordID(executionID string, sequence int, direction Direction, phaseID, suffix string) string {
	return fmt.Sprintf("%s-%03d-%s-%s-%s", executionID, sequence, direction, phaseID, suffix)
}
