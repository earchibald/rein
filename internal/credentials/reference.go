package credentials

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
)

var (
	ErrInvalidReference   = errors.New("credentials: invalid reference")
	ErrUnsupportedScheme  = errors.New("credentials: unsupported scheme")
	ErrCredentialNotFound = errors.New("credentials: not found")
	ErrInvalidScope       = errors.New("credentials: invalid execution scope")
)

type Reference struct {
	raw string
	uri url.URL
}

func ParseReference(raw string) (Reference, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Reference{}, fmt.Errorf("%w: reference is required", ErrInvalidReference)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		return Reference{}, fmt.Errorf("%w: parse %q: %v", ErrInvalidReference, raw, err)
	}
	if parsed.Scheme == "" {
		return Reference{}, fmt.Errorf("%w: scheme is required", ErrInvalidReference)
	}

	parsed.Scheme = strings.ToLower(parsed.Scheme)
	switch parsed.Scheme {
	case "env", "file", "keychain", "libsecret":
	default:
		return Reference{}, fmt.Errorf("%w: %s", ErrUnsupportedScheme, parsed.Scheme)
	}

	return Reference{raw: raw, uri: *parsed}, nil
}

func (r Reference) String() string {
	return r.raw
}

func (r Reference) Scheme() string {
	return r.uri.Scheme
}

func (r Reference) Host() string {
	return r.uri.Host
}

func (r Reference) Path() string {
	return r.uri.Path
}

func (r Reference) Query() url.Values {
	return r.uri.Query()
}

type ExecutionScope struct {
	ProjectID   string
	WorkflowID  string
	ExecutionID string
	LookupEnv   func(string) (string, bool)
}

func (s ExecutionScope) Validate() error {
	if strings.TrimSpace(s.ExecutionID) == "" {
		return fmt.Errorf("%w: execution_id is required", ErrInvalidScope)
	}
	return nil
}

func (s ExecutionScope) lookupEnv(name string) (string, bool) {
	if s.LookupEnv != nil {
		return s.LookupEnv(name)
	}
	return os.LookupEnv(name)
}
