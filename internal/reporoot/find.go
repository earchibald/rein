package reporoot

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type NotFoundError struct {
	Start       string
	Marker      string
	Description string
}

func (e *NotFoundError) Error() string {
	label := strings.TrimSpace(e.Description)
	if label == "" {
		label = "repository marker"
	}
	return fmt.Sprintf("%s %q was not found from %q", label, e.Marker, e.Start)
}

func Find(start, marker, description string) (string, error) {
	current := filepath.Clean(start)
	label := strings.TrimSpace(description)
	if label == "" {
		label = "repository marker"
	}

	for {
		candidate := filepath.Join(current, marker)
		if _, err := os.Stat(candidate); err == nil {
			return current, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("stat %q: %w", candidate, err)
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	return "", &NotFoundError{
		Start:       start,
		Marker:      marker,
		Description: label,
	}
}
