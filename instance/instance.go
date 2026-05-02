package instance

import internalinstance "github.com/earchibald/rein/internal/instance"

const (
	DefaultName = internalinstance.DefaultName
	EnvVar      = internalinstance.EnvVar
)

var (
	ErrEmptyName         = internalinstance.ErrEmptyName
	ErrInvalidName       = internalinstance.ErrInvalidName
	ErrMissingStateHome  = internalinstance.ErrMissingStateHome
	ErrRelativeStateHome = internalinstance.ErrRelativeStateHome
)

type Layout struct {
	Name         string
	StateHome    string
	RootDir      string
	SocketPath   string
	DatabasePath string
}

type ResolveOptions struct {
	Name        string
	LookupEnv   func(string) (string, bool)
	UserHomeDir func() (string, error)
}

func Resolve(options ResolveOptions) (Layout, error) {
	layout, err := internalinstance.Resolve(toInternalResolveOptions(options))
	if err != nil {
		return Layout{}, err
	}
	return fromInternalLayout(layout), nil
}

func NewLayout(name, stateHome string) (Layout, error) {
	layout, err := internalinstance.NewLayout(name, stateHome)
	if err != nil {
		return Layout{}, err
	}
	return fromInternalLayout(layout), nil
}

func ValidateName(name string) error {
	return internalinstance.ValidateName(name)
}

func (l Layout) Validate() error {
	return toInternalLayout(l).Validate()
}

func (l Layout) EnsureRootDir() error {
	return toInternalLayout(l).EnsureRootDir()
}

func (l Layout) AutoStartEnabled() bool {
	return toInternalLayout(l).AutoStartEnabled()
}

func fromInternalLayout(layout internalinstance.Layout) Layout {
	return Layout{
		Name:         layout.Name,
		StateHome:    layout.StateHome,
		RootDir:      layout.RootDir,
		SocketPath:   layout.SocketPath,
		DatabasePath: layout.DatabasePath,
	}
}

func toInternalLayout(layout Layout) internalinstance.Layout {
	return internalinstance.Layout{
		Name:         layout.Name,
		StateHome:    layout.StateHome,
		RootDir:      layout.RootDir,
		SocketPath:   layout.SocketPath,
		DatabasePath: layout.DatabasePath,
	}
}

func toInternalResolveOptions(options ResolveOptions) internalinstance.ResolveOptions {
	return internalinstance.ResolveOptions{
		Name:        options.Name,
		LookupEnv:   options.LookupEnv,
		UserHomeDir: options.UserHomeDir,
	}
}
