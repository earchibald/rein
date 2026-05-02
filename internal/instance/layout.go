package instance

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

const (
	DefaultName      = "live"
	EnvVar           = "REIN_INSTANCE"
	rootDirName      = "rein"
	instancesDirName = "instances"
	socketFileName   = "grpc.sock"
	pidFileName      = "daemon.pid"
	storeFileName    = "rein.db"
)

var (
	ErrEmptyName         = errors.New("instance: empty name")
	ErrInvalidName       = errors.New("instance: invalid name")
	ErrMissingStateHome  = errors.New("instance: state home unavailable")
	ErrRelativeStateHome = errors.New("instance: state home must be absolute")

	instanceNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)
)

type Layout struct {
	Name         string
	StateHome    string
	RootDir      string
	SocketPath   string
	PIDPath      string
	DatabasePath string
}

type ResolveOptions struct {
	Name        string
	LookupEnv   func(string) (string, bool)
	UserHomeDir func() (string, error)
}

func Resolve(options ResolveOptions) (Layout, error) {
	name := options.Name
	if name == "" {
		if lookupEnv := options.LookupEnv; lookupEnv != nil {
			if value, ok := lookupEnv(EnvVar); ok && value != "" {
				name = value
			}
		}
	}
	if name == "" {
		name = DefaultName
	}

	stateHome, err := resolveStateHome(options)
	if err != nil {
		return Layout{}, err
	}

	return NewLayout(name, stateHome)
}

func NewLayout(name, stateHome string) (Layout, error) {
	if err := ValidateName(name); err != nil {
		return Layout{}, err
	}
	if stateHome == "" {
		return Layout{}, ErrMissingStateHome
	}
	if !filepath.IsAbs(stateHome) {
		return Layout{}, fmt.Errorf("%w: %q", ErrRelativeStateHome, stateHome)
	}

	stateHome = filepath.Clean(stateHome)
	rootDir := filepath.Join(stateHome, rootDirName, instancesDirName, name)

	layout := Layout{
		Name:         name,
		StateHome:    stateHome,
		RootDir:      rootDir,
		SocketPath:   filepath.Join(rootDir, socketFileName),
		PIDPath:      filepath.Join(rootDir, pidFileName),
		DatabasePath: filepath.Join(rootDir, storeFileName),
	}

	if err := layout.Validate(); err != nil {
		return Layout{}, err
	}

	return layout, nil
}

func ValidateName(name string) error {
	switch {
	case name == "":
		return ErrEmptyName
	case name == "." || name == "..":
		return fmt.Errorf("%w %q", ErrInvalidName, name)
	case !instanceNamePattern.MatchString(name):
		return fmt.Errorf("%w %q", ErrInvalidName, name)
	}

	return nil
}

func (l Layout) Validate() error {
	if err := ValidateName(l.Name); err != nil {
		return err
	}
	if l.StateHome == "" {
		return ErrMissingStateHome
	}
	if !filepath.IsAbs(l.StateHome) {
		return fmt.Errorf("%w: %q", ErrRelativeStateHome, l.StateHome)
	}

	expectedRoot := filepath.Join(filepath.Clean(l.StateHome), rootDirName, instancesDirName, l.Name)
	if filepath.Clean(l.RootDir) != expectedRoot {
		return fmt.Errorf("instance: root dir %q does not match canonical layout %q", l.RootDir, expectedRoot)
	}
	if filepath.Clean(l.SocketPath) != filepath.Join(expectedRoot, socketFileName) {
		return fmt.Errorf("instance: socket path %q does not match canonical layout", l.SocketPath)
	}
	if filepath.Clean(l.PIDPath) != filepath.Join(expectedRoot, pidFileName) {
		return fmt.Errorf("instance: pid path %q does not match canonical layout", l.PIDPath)
	}
	if filepath.Clean(l.DatabasePath) != filepath.Join(expectedRoot, storeFileName) {
		return fmt.Errorf("instance: database path %q does not match canonical layout", l.DatabasePath)
	}

	return nil
}

func (l Layout) EnsureRootDir() error {
	if err := l.Validate(); err != nil {
		return err
	}

	return os.MkdirAll(l.RootDir, 0o755)
}

func (l Layout) AutoStartEnabled() bool {
	return l.Name == DefaultName
}

func resolveStateHome(options ResolveOptions) (string, error) {
	if lookupEnv := options.LookupEnv; lookupEnv != nil {
		if value, ok := lookupEnv("XDG_STATE_HOME"); ok && value != "" {
			return value, nil
		}
	}

	userHomeDir := options.UserHomeDir
	if userHomeDir == nil {
		userHomeDir = os.UserHomeDir
	}

	homeDir, err := userHomeDir()
	if err != nil {
		return "", fmt.Errorf("instance: resolve home dir: %w", err)
	}
	if homeDir == "" {
		return "", ErrMissingStateHome
	}

	return filepath.Join(homeDir, ".local", "state"), nil
}
