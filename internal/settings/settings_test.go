package settings

import (
	"errors"
	"testing"
)

func TestResolveClosestWinsAcrossLayers(t *testing.T) {
	t.Parallel()

	registry := MustRegistry(
		KeySpec{Key: "runner.image", AllowedLayers: []Layer{LayerDaemonGlobal, LayerProject, LayerWorkflow, LayerExecution}},
		KeySpec{Key: "notifications.email", AllowedLayers: []Layer{LayerDaemonGlobal, LayerProject}},
	)

	resolved, err := Resolve(
		registry,
		ScopedValues{
			Layer:   LayerDaemonGlobal,
			ScopeID: DaemonGlobalScopeID,
			Values: map[string]string{
				"runner.image":        "ubuntu-latest",
				"notifications.email": "ops@example.com",
			},
		},
		ScopedValues{
			Layer:   LayerProject,
			ScopeID: "project-1",
			Values: map[string]string{
				"notifications.email": "project@example.com",
			},
		},
		ScopedValues{
			Layer:   LayerWorkflow,
			ScopeID: "workflow-1",
			Values: map[string]string{
				"runner.image": "workflow-image",
			},
		},
		ScopedValues{
			Layer:   LayerExecution,
			ScopeID: "execution-1",
			Values: map[string]string{
				"runner.image": "execution-image",
			},
		},
	)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	if got := resolved["runner.image"]; got.Value != "execution-image" || got.Layer != LayerExecution {
		t.Fatalf("Resolve() runner.image = %+v, want execution override", got)
	}
	if got := resolved["notifications.email"]; got.Value != "project@example.com" || got.Layer != LayerProject {
		t.Fatalf("Resolve() notifications.email = %+v, want project override", got)
	}
}

func TestResolveRejectsIneligibleLayerValue(t *testing.T) {
	t.Parallel()

	registry := MustRegistry(
		KeySpec{Key: "notifications.email", AllowedLayers: []Layer{LayerDaemonGlobal, LayerProject}},
	)

	_, err := Resolve(
		registry,
		ScopedValues{
			Layer:   LayerExecution,
			ScopeID: "execution-1",
			Values: map[string]string{
				"notifications.email": "execution@example.com",
			},
		},
	)
	if !errors.Is(err, ErrLayerNotAllowed) {
		t.Fatalf("Resolve() error = %v, want %v", err, ErrLayerNotAllowed)
	}
}

func TestNormalizeScopeID(t *testing.T) {
	t.Parallel()

	if got, err := NormalizeScopeID(LayerDaemonGlobal, ""); err != nil || got != DaemonGlobalScopeID {
		t.Fatalf("NormalizeScopeID(daemon-global, empty) = %q, %v", got, err)
	}
	if got, err := NormalizeScopeID(LayerProject, "project-1"); err != nil || got != "project-1" {
		t.Fatalf("NormalizeScopeID(project, project-1) = %q, %v", got, err)
	}
	if _, err := NormalizeScopeID(LayerWorkflow, ""); !errors.Is(err, ErrInvalidScopeID) {
		t.Fatalf("NormalizeScopeID(workflow, empty) error = %v, want %v", err, ErrInvalidScopeID)
	}
}
