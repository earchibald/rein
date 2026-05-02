package instance

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveUsesFlagThenEnvThenDefault(t *testing.T) {
	t.Parallel()

	lookupEnv := func(key string) (string, bool) {
		switch key {
		case EnvVar:
			return "staging", true
		case "XDG_STATE_HOME":
			return "/state", true
		default:
			return "", false
		}
	}

	layout, err := Resolve(ResolveOptions{
		Name:      "dev",
		LookupEnv: lookupEnv,
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if layout.Name != "dev" {
		t.Fatalf("Resolve() name = %q, want %q", layout.Name, "dev")
	}

	layout, err = Resolve(ResolveOptions{
		LookupEnv: lookupEnv,
	})
	if err != nil {
		t.Fatalf("Resolve() env error = %v", err)
	}
	if layout.Name != "staging" {
		t.Fatalf("Resolve() env name = %q, want %q", layout.Name, "staging")
	}

	layout, err = Resolve(ResolveOptions{
		LookupEnv: func(key string) (string, bool) {
			if key == "XDG_STATE_HOME" {
				return "/state", true
			}
			return "", false
		},
	})
	if err != nil {
		t.Fatalf("Resolve() default error = %v", err)
	}
	if layout.Name != DefaultName {
		t.Fatalf("Resolve() default name = %q, want %q", layout.Name, DefaultName)
	}
}

func TestResolveFallsBackToHomeDirStatePath(t *testing.T) {
	t.Parallel()

	layout, err := Resolve(ResolveOptions{
		UserHomeDir: func() (string, error) {
			return filepath.Join(string(filepath.Separator), "Users", "tester"), nil
		},
	})
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}

	want := filepath.Join(string(filepath.Separator), "Users", "tester", ".local", "state", "rein", "instances", DefaultName)
	if layout.RootDir != want {
		t.Fatalf("Resolve() root dir = %q, want %q", layout.RootDir, want)
	}
}

func TestResolveRejectsMissingStateHome(t *testing.T) {
	t.Parallel()

	_, err := Resolve(ResolveOptions{
		UserHomeDir: func() (string, error) {
			return "", nil
		},
	})
	if !errors.Is(err, ErrMissingStateHome) {
		t.Fatalf("Resolve() error = %v, want %v", err, ErrMissingStateHome)
	}
}

func TestNewLayoutRejectsInvalidInputs(t *testing.T) {
	t.Parallel()

	if _, err := NewLayout("", "/state"); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("NewLayout() empty name error = %v, want %v", err, ErrEmptyName)
	}
	if _, err := NewLayout("../oops", "/state"); !errors.Is(err, ErrInvalidName) {
		t.Fatalf("NewLayout() invalid name error = %v, want %v", err, ErrInvalidName)
	}
	if _, err := NewLayout("live", "relative"); !errors.Is(err, ErrRelativeStateHome) {
		t.Fatalf("NewLayout() relative state home error = %v, want %v", err, ErrRelativeStateHome)
	}
}

func TestLayoutValidateRejectsMismatchedPaths(t *testing.T) {
	t.Parallel()

	layout := Layout{
		Name:         "live",
		StateHome:    "/state",
		RootDir:      "/state/rein/instances/live",
		SocketPath:   "/state/rein/instances/live/custom.sock",
		DatabasePath: "/state/rein/instances/live/rein.db",
	}

	if err := layout.Validate(); err == nil {
		t.Fatal("Validate() error = nil, want non-nil")
	}
}

func TestLayoutEnsureRootDirCreatesCanonicalDirectory(t *testing.T) {
	t.Parallel()

	layout, err := NewLayout("dev", t.TempDir())
	if err != nil {
		t.Fatalf("NewLayout() error = %v", err)
	}

	if err := layout.EnsureRootDir(); err != nil {
		t.Fatalf("EnsureRootDir() error = %v", err)
	}
	if info, err := os.Stat(layout.RootDir); err != nil {
		t.Fatalf("os.Stat(%q) error = %v", layout.RootDir, err)
	} else if !info.IsDir() {
		t.Fatalf("%q is not a directory", layout.RootDir)
	}
}

func TestLayoutAutoStartEnabledOnlyForLive(t *testing.T) {
	t.Parallel()

	live, err := NewLayout("live", "/state")
	if err != nil {
		t.Fatalf("NewLayout(live) error = %v", err)
	}
	dev, err := NewLayout("dev", "/state")
	if err != nil {
		t.Fatalf("NewLayout(dev) error = %v", err)
	}

	if !live.AutoStartEnabled() {
		t.Fatal("live instance should auto-start")
	}
	if dev.AutoStartEnabled() {
		t.Fatal("non-live instance should not auto-start")
	}
}
