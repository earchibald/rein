package dashboards

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	DefaultPluginName      = "rein-dashboards"
	defaultMarketplacePath = ".claude-plugin/dashboards-marketplace.json"
)

type SourceKind string

const (
	SourceLocal  SourceKind = "local"
	SourceGitHub SourceKind = "github"
	SourceURL    SourceKind = "url"
)

type Source struct {
	Kind SourceKind `json:"kind"`
	Path string     `json:"path,omitempty"`
	Repo string     `json:"repo,omitempty"`
	URL  string     `json:"url,omitempty"`
	Ref  string     `json:"ref,omitempty"`
}

type Asset struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

type Manifest struct {
	Name        string  `json:"name"`
	Version     string  `json:"version,omitempty"`
	Description string  `json:"description,omitempty"`
	Provider    string  `json:"provider"`
	Assets      []Asset `json:"dashboards"`
}

type Entry struct {
	Manifest Manifest
	Root     string
	Source   Source
}

type Registry struct {
	entries map[string]Entry
}

type marketplaceDocument struct {
	Name    string              `json:"name"`
	Plugins []marketplacePlugin `json:"plugins"`
}

type marketplacePlugin struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Version     string          `json:"version,omitempty"`
	Provider    string          `json:"provider,omitempty"`
	Source      json.RawMessage `json:"source"`
}

func Load(root string) (*Registry, error) {
	raw, err := os.ReadFile(filepath.Join(root, defaultMarketplacePath))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Registry{entries: map[string]Entry{}}, nil
		}
		return nil, fmt.Errorf("read dashboards marketplace: %w", err)
	}

	var document marketplaceDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode dashboards marketplace: %w", err)
	}

	registry := &Registry{entries: make(map[string]Entry, len(document.Plugins))}
	for _, plugin := range document.Plugins {
		entry, err := loadEntry(root, plugin)
		if err != nil {
			return nil, fmt.Errorf("load dashboards plugin %q: %w", plugin.Name, err)
		}
		registry.entries[entry.Manifest.Name] = entry
	}
	return registry, nil
}

func FindRoot(start string) (string, error) {
	current := filepath.Clean(start)
	for {
		marketplacePath := filepath.Join(current, defaultMarketplacePath)
		if _, err := os.Stat(marketplacePath); err == nil {
			return current, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}
	return "", fmt.Errorf("dashboards marketplace %q was not found from %q", defaultMarketplacePath, start)
}

func (r *Registry) Entry(name string) (Entry, bool) {
	if r == nil {
		return Entry{}, false
	}
	entry, ok := r.entries[name]
	return entry, ok
}

func loadEntry(root string, plugin marketplacePlugin) (Entry, error) {
	source, err := parseSource(plugin.Source)
	if err != nil {
		return Entry{}, err
	}

	manifest := Manifest{
		Name:        strings.TrimSpace(plugin.Name),
		Version:     strings.TrimSpace(plugin.Version),
		Description: strings.TrimSpace(plugin.Description),
		Provider:    strings.TrimSpace(plugin.Provider),
	}
	if manifest.Name == "" {
		return Entry{}, errors.New("name is required")
	}

	entry := Entry{
		Manifest: manifest,
		Source:   source,
	}
	if source.Kind != SourceLocal {
		return finalizeEntry(entry)
	}

	entry.Root = filepath.Join(root, filepath.Clean(source.Path))
	manifestPath := filepath.Join(entry.Root, ".claude-plugin", "plugin.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return Entry{}, fmt.Errorf("read plugin manifest: %w", err)
	}

	var manifestFile Manifest
	if err := json.Unmarshal(raw, &manifestFile); err != nil {
		return Entry{}, fmt.Errorf("decode plugin manifest: %w", err)
	}
	entry.Manifest = overlayManifest(entry.Manifest, manifestFile)
	return finalizeEntry(entry)
}

func finalizeEntry(entry Entry) (Entry, error) {
	if entry.Manifest.Name == "" {
		return Entry{}, errors.New("plugin manifest name is required")
	}
	if entry.Manifest.Provider == "" {
		return Entry{}, errors.New("plugin provider is required")
	}
	for _, asset := range entry.Manifest.Assets {
		if strings.TrimSpace(asset.ID) == "" {
			return Entry{}, errors.New("dashboard asset id is required")
		}
		if strings.TrimSpace(asset.Path) == "" {
			return Entry{}, fmt.Errorf("dashboard asset %q path is required", asset.ID)
		}
	}
	return entry, nil
}

func overlayManifest(base, override Manifest) Manifest {
	if override.Name != "" {
		base.Name = override.Name
	}
	if override.Version != "" {
		base.Version = override.Version
	}
	if override.Description != "" {
		base.Description = override.Description
	}
	if override.Provider != "" {
		base.Provider = override.Provider
	}
	if len(override.Assets) > 0 {
		base.Assets = append([]Asset(nil), override.Assets...)
	}
	return base
}

func parseSource(raw json.RawMessage) (Source, error) {
	if len(raw) == 0 {
		return Source{}, errors.New("source is required")
	}

	var shorthand string
	if err := json.Unmarshal(raw, &shorthand); err == nil {
		value := strings.TrimSpace(shorthand)
		if value == "" {
			return Source{}, errors.New("source is required")
		}
		return Source{Kind: SourceLocal, Path: value}, nil
	}

	var source struct {
		Source string `json:"source"`
		Path   string `json:"path,omitempty"`
		Repo   string `json:"repo,omitempty"`
		URL    string `json:"url,omitempty"`
		Ref    string `json:"ref,omitempty"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return Source{}, fmt.Errorf("decode source: %w", err)
	}

	switch strings.TrimSpace(source.Source) {
	case "local", "":
		if strings.TrimSpace(source.Path) == "" {
			return Source{}, errors.New("local dashboards plugin path is required")
		}
		return Source{Kind: SourceLocal, Path: source.Path}, nil
	case "github":
		if strings.TrimSpace(source.Repo) == "" {
			return Source{}, errors.New("github dashboards plugin repo is required")
		}
		return Source{Kind: SourceGitHub, Repo: source.Repo, Ref: source.Ref}, nil
	case "url":
		if strings.TrimSpace(source.URL) == "" {
			return Source{}, errors.New("remote dashboards plugin url is required")
		}
		return Source{Kind: SourceURL, URL: source.URL, Ref: source.Ref}, nil
	default:
		return Source{}, fmt.Errorf("unsupported dashboards plugin source kind %q", source.Source)
	}
}
