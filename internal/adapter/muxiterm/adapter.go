package muxiterm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
	"github.com/earchibald/rein/internal/workflow"
)

const (
	AdapterID = "muxiterm"

	inputArgv = "argv"
	inputCWD  = "cwd"
	inputEnv  = "env"
)

var workflowReservedInputs = map[string]struct{}{
	workflow.InputLane:              {},
	workflow.InputLaneAttach:        {},
	workflow.InputOperation:         {},
	workflow.InputBail:              {},
	workflow.InputOnCancel:          {},
	workflow.InputBackwardOperation: {},
	workflow.InputBackwardTo:        {},
}

type Command struct {
	Path string
	Args []string
	Dir  string
	Env  []string
}

type Result struct {
	Stdout string
	Stderr string
}

type Executor interface {
	Run(context.Context, Command) (Result, error)
}

type Adapter struct {
	descriptor *reinv1.Adapter
	binary     string
	executor   Executor
}

func DefaultDescriptor() *reinv1.Adapter {
	return &reinv1.Adapter{
		Id:          AdapterID,
		Name:        "muxiterm",
		Category:    reinv1.AdapterCategory_ADAPTER_CATEGORY_MULTIPLEXER,
		Description: "First-party mux adapter that wraps the muxiterm JSON CLI.",
		Version:     "0.9.0",
		Enabled:     true,
		Capabilities: map[string]string{
			"session.attach": "true",
			"pane.split":     "true",
			"input.send":     "true",
			"tail":           "true",
		},
	}
}

func New() *Adapter {
	return NewWithExecutor("muxiterm", osExecutor{})
}

func NewWithExecutor(binary string, executor Executor) *Adapter {
	if strings.TrimSpace(binary) == "" {
		binary = "muxiterm"
	}
	if executor == nil {
		executor = osExecutor{}
	}
	return &Adapter{
		descriptor: DefaultDescriptor(),
		binary:     binary,
		executor:   executor,
	}
}

func (a *Adapter) WithDescriptor(descriptor *reinv1.Adapter) *Adapter {
	cloned := *a
	if descriptor == nil {
		cloned.descriptor = DefaultDescriptor()
		return &cloned
	}
	cloned.descriptor = proto.Clone(descriptor).(*reinv1.Adapter)
	return &cloned
}

func (a *Adapter) Descriptor() *reinv1.Adapter {
	if a == nil || a.descriptor == nil {
		return DefaultDescriptor()
	}
	return proto.Clone(a.descriptor).(*reinv1.Adapter)
}

func (a *Adapter) Run(ctx context.Context, state *workflow.RuntimeState, phase workflow.Phase, direction workflow.Direction, effect *workflow.SideEffect) error {
	if effect == nil {
		return errors.New("muxiterm side effect is required")
	}

	operation, err := operationForDirection(phase, direction)
	if err != nil {
		return err
	}

	command, err := a.buildCommand(state, phase, direction, operation)
	if err != nil {
		return err
	}

	result, execErr := a.executor.Run(ctx, command)
	outputs, envelopeErr := parseOutputs(command, result.Stdout, result.Stderr)
	if envelopeErr == nil {
		effect.Outputs = outputs
	}

	if execErr != nil {
		if envelopeErr == nil {
			return failureFromOutputs(operation, outputs, execErr)
		}
		return fmt.Errorf("muxiterm %s: %w", operation, execErr)
	}
	if envelopeErr != nil {
		return fmt.Errorf("muxiterm %s: %w", operation, envelopeErr)
	}
	if outputs["ok"] == "false" {
		return failureFromOutputs(operation, outputs, nil)
	}

	effect.Outputs = outputs
	return nil
}

func (a *Adapter) buildCommand(state *workflow.RuntimeState, phase workflow.Phase, direction workflow.Direction, operation string) (Command, error) {
	inputs := phase.Inputs
	positionals, err := parseJSONArray(inputs[inputArgv])
	if err != nil {
		return Command{}, fmt.Errorf("muxiterm argv: %w", err)
	}
	envValues, err := parseJSONObject(inputs[inputEnv])
	if err != nil {
		return Command{}, fmt.Errorf("muxiterm env: %w", err)
	}

	args := append([]string(nil), strings.Fields(operation)...)
	args = append(args, positionals...)

	flagKeys := make([]string, 0, len(inputs))
	for key := range inputs {
		switch key {
		case inputArgv, inputCWD, inputEnv:
			continue
		}
		if _, reserved := workflowReservedInputs[key]; reserved {
			continue
		}
		flagKeys = append(flagKeys, key)
	}
	sort.Strings(flagKeys)
	for _, key := range flagKeys {
		value := strings.TrimSpace(inputs[key])
		if parsed, err := strconv.ParseBool(value); err == nil {
			if parsed {
				args = append(args, "--"+key)
			}
			continue
		}
		args = append(args, "--"+key, value)
	}

	envKeys := make([]string, 0, len(envValues)+7)
	for key := range envValues {
		envKeys = append(envKeys, key)
	}
	addRuntimeEnv(envValues, state, phase, direction, operation)
	envKeys = envKeys[:0]
	for key := range envValues {
		envKeys = append(envKeys, key)
	}
	sort.Strings(envKeys)

	env := make([]string, 0, len(envKeys))
	for _, key := range envKeys {
		env = append(env, key+"="+envValues[key])
	}

	return Command{
		Path: a.binary,
		Args: args,
		Dir:  strings.TrimSpace(inputs[inputCWD]),
		Env:  env,
	}, nil
}

func operationForDirection(phase workflow.Phase, direction workflow.Direction) (string, error) {
	if direction == workflow.DirectionBackward {
		if operation := strings.TrimSpace(phase.BackwardOperation); operation != "" {
			return operation, nil
		}
	}
	if operation := strings.TrimSpace(phase.Operation); operation != "" {
		return operation, nil
	}
	return "", errors.New("muxiterm operation is required")
}

func addRuntimeEnv(env map[string]string, state *workflow.RuntimeState, phase workflow.Phase, direction workflow.Direction, operation string) {
	env["REIN_ADAPTER_ID"] = AdapterID
	env["REIN_PHASE_ID"] = phase.ID
	env["REIN_DIRECTION"] = string(direction)
	env["REIN_OPERATION"] = operation
	if state == nil {
		return
	}
	if state.Execution != nil {
		env["REIN_EXECUTION_ID"] = state.Execution.GetId()
	}
	if state.Issue != nil {
		env["REIN_ISSUE_ID"] = state.Issue.GetId()
	}
	if state.Workflow != nil {
		env["REIN_WORKFLOW_ID"] = state.Workflow.GetId()
	}
}

func parseJSONArray(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var values []string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, errors.New("must be a JSON array of strings")
	}
	return values, nil
}

func parseJSONObject(raw string) (map[string]string, error) {
	if strings.TrimSpace(raw) == "" {
		return map[string]string{}, nil
	}
	var values map[string]string
	if err := json.Unmarshal([]byte(raw), &values); err != nil {
		return nil, errors.New("must be a JSON object of string values")
	}
	return values, nil
}

func parseOutputs(command Command, stdout, stderr string) (map[string]string, error) {
	outputs := map[string]string{
		"command": strings.Join(append([]string{command.Path}, command.Args...), " "),
	}
	if command.Dir != "" {
		outputs["cwd"] = command.Dir
	}
	if strings.TrimSpace(stderr) != "" {
		outputs["stderr"] = strings.TrimSpace(stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		return outputs, nil
	}

	var envelope map[string]any
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		return nil, fmt.Errorf("decode muxiterm response: %w", err)
	}

	outputs["json"] = strings.TrimSpace(stdout)
	for key, value := range envelope {
		switch key {
		case "error":
			encoded, err := json.Marshal(value)
			if err != nil {
				return nil, fmt.Errorf("encode muxiterm error payload: %w", err)
			}
			outputs[key] = string(encoded)
		default:
			outputs[key] = stringify(value)
		}
	}
	return outputs, nil
}

func stringify(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprint(typed)
		}
		return string(encoded)
	}
}

func failureFromOutputs(operation string, outputs map[string]string, fallback error) error {
	code, message, hint := "", "", ""
	if raw := strings.TrimSpace(outputs["error"]); raw != "" {
		var payload struct {
			Code    string `json:"code"`
			Message string `json:"message"`
			Hint    string `json:"hint"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err == nil {
			code = payload.Code
			message = payload.Message
			hint = payload.Hint
		}
	}
	if message == "" && fallback != nil {
		message = fallback.Error()
	}
	if message == "" {
		message = "command failed"
	}

	var builder strings.Builder
	builder.WriteString("muxiterm ")
	builder.WriteString(operation)
	builder.WriteString(": ")
	if code != "" {
		builder.WriteString(code)
		builder.WriteString(": ")
	}
	builder.WriteString(message)
	if hint != "" {
		builder.WriteString(" (")
		builder.WriteString(hint)
		builder.WriteString(")")
	}
	return errors.New(builder.String())
}

type osExecutor struct{}

func (osExecutor) Run(ctx context.Context, command Command) (Result, error) {
	const binary = "muxiterm"
	if path := strings.TrimSpace(command.Path); path != "" && path != binary {
		return Result{}, fmt.Errorf("unsupported muxiterm binary %q", command.Path)
	}

	//nolint:gosec // The adapter's job is to translate workflow inputs into the fixed muxiterm CLI surface.
	cmd := exec.CommandContext(ctx, binary, command.Args...)
	if command.Dir != "" {
		cmd.Dir = command.Dir
	}
	if len(command.Env) > 0 {
		cmd.Env = append(os.Environ(), command.Env...)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return Result{
		Stdout: stdout.String(),
		Stderr: stderr.String(),
	}, err
}
