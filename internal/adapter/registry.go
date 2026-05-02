package adapter

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"google.golang.org/protobuf/proto"

	reinv1 "github.com/earchibald/rein/gen/go/rein/v1"
)

const CurrentDaemonAPIVersion = "rein.v1"

var shaPattern = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)

type Category string

const (
	CategoryCodingAgent Category = "codingAgent"
	CategoryMux         Category = "mux"
	CategoryTracker     Category = "tracker"
	CategoryMessaging   Category = "messaging"
	CategoryProjection  Category = "projection"
)

type Taxonomy struct {
	Marketplace Category
	Proto       reinv1.AdapterCategory
	Alias       string
}

type DiscoveryOptions struct {
	DaemonAPIVersion   string
	AllowUnsignedIndex bool
	TrustedKeys        map[string]ed25519.PublicKey
}

type Registry struct {
	marketplaceName string
	entries         map[string]Entry
}

type Entry struct {
	ID                  string
	MarketplaceCategory Category
	Taxonomy            Taxonomy
	Source              Source
	Descriptor          *reinv1.Adapter
	Requires            []string
	Tail                bool
}

type SourceKind string

const (
	SourceLocal     SourceKind = "local"
	SourceGitHub    SourceKind = "github"
	SourceURL       SourceKind = "url"
	SourceGitSubdir SourceKind = "git-subdir"
	SourceNPM       SourceKind = "npm"
)

type Source struct {
	Kind     SourceKind `json:"kind"`
	Path     string     `json:"path,omitempty"`
	Repo     string     `json:"repo,omitempty"`
	URL      string     `json:"url,omitempty"`
	Ref      string     `json:"ref,omitempty"`
	SHA      string     `json:"sha,omitempty"`
	Package  string     `json:"package,omitempty"`
	Version  string     `json:"version,omitempty"`
	Registry string     `json:"registry,omitempty"`
}

type marketplaceDocument struct {
	Name      string              `json:"name"`
	Plugins   []marketplacePlugin `json:"plugins"`
	Signature *indexSignature     `json:"signature,omitempty"`
}

type marketplacePlugin struct {
	Name             string            `json:"name"`
	Source           json.RawMessage   `json:"source"`
	Description      string            `json:"description,omitempty"`
	Version          string            `json:"version,omitempty"`
	Category         string            `json:"category,omitempty"`
	Strict           *bool             `json:"strict,omitempty"`
	DaemonAPIVersion string            `json:"daemonApiVersion,omitempty"`
	Capabilities     map[string]string `json:"capabilities,omitempty"`
	Tail             *bool             `json:"tail,omitempty"`
	Requires         []string          `json:"requires,omitempty"`
}

type pluginManifest struct {
	Name             string            `json:"name"`
	Description      string            `json:"description,omitempty"`
	Version          string            `json:"version,omitempty"`
	Category         string            `json:"category,omitempty"`
	DaemonAPIVersion string            `json:"daemonApiVersion,omitempty"`
	Capabilities     map[string]string `json:"capabilities,omitempty"`
	Tail             *bool             `json:"tail,omitempty"`
	Requires         []string          `json:"requires,omitempty"`
}

type indexSignature struct {
	Algorithm string `json:"algorithm"`
	KeyID     string `json:"keyId"`
	Value     string `json:"value"`
}

type pluginRecord struct {
	ID                  string
	Description         string
	Version             string
	Category            string
	DaemonAPIVersion    string
	Capabilities        map[string]string
	Tail                *bool
	Requires            []string
	MarketplaceCategory Category
	Source              Source
}

func Load(root string, options DiscoveryOptions) (*Registry, error) {
	var err error
	options, err = options.withRootDefaults(root)
	if err != nil {
		return nil, fmt.Errorf("load trusted keys: %w", err)
	}

	marketplacePath := filepath.Join(root, ".claude-plugin", "marketplace.json")
	raw, err := os.ReadFile(marketplacePath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &Registry{entries: map[string]Entry{}}, nil
		}
		return nil, fmt.Errorf("read marketplace index: %w", err)
	}

	if err := verifyIndexSignature(raw, options); err != nil {
		return nil, err
	}

	var document marketplaceDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, fmt.Errorf("decode marketplace index: %w", err)
	}

	registry := &Registry{
		marketplaceName: document.Name,
		entries:         make(map[string]Entry, len(document.Plugins)),
	}

	for _, plugin := range document.Plugins {
		entry, err := loadPlugin(root, plugin, options)
		if err != nil {
			return nil, fmt.Errorf("load plugin %q: %w", plugin.Name, err)
		}
		if _, exists := registry.entries[entry.ID]; exists {
			return nil, fmt.Errorf("load plugin %q: duplicate adapter id", plugin.Name)
		}
		registry.entries[entry.ID] = entry
	}

	return registry, nil
}

func NormalizeCategory(value string) (Taxonomy, error) {
	normalized := strings.NewReplacer("-", "", "_", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch normalized {
	case "codingagent":
		return Taxonomy{Marketplace: CategoryCodingAgent, Proto: reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT, Alias: aliasFor(value, string(CategoryCodingAgent))}, nil
	case "mux", "multiplexer":
		return Taxonomy{Marketplace: CategoryMux, Proto: reinv1.AdapterCategory_ADAPTER_CATEGORY_MULTIPLEXER, Alias: aliasFor(value, string(CategoryMux))}, nil
	case "tracker":
		return Taxonomy{Marketplace: CategoryTracker, Proto: reinv1.AdapterCategory_ADAPTER_CATEGORY_TRACKER}, nil
	case "messaging", "notification":
		return Taxonomy{Marketplace: CategoryMessaging, Proto: reinv1.AdapterCategory_ADAPTER_CATEGORY_NOTIFICATION, Alias: aliasFor(value, string(CategoryMessaging))}, nil
	case "projection", "reviewagent":
		return Taxonomy{Marketplace: CategoryProjection, Proto: reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT, Alias: aliasFor(value, string(CategoryProjection))}, nil
	default:
		return Taxonomy{}, fmt.Errorf("unsupported category %q", value)
	}
}

func (r *Registry) List(category reinv1.AdapterCategory, enabledOnly bool) []*reinv1.Adapter {
	if r == nil {
		return nil
	}

	ids := make([]string, 0, len(r.entries))
	for id := range r.entries {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	adapters := make([]*reinv1.Adapter, 0, len(ids))
	for _, id := range ids {
		entry := r.entries[id]
		if category != reinv1.AdapterCategory_ADAPTER_CATEGORY_UNSPECIFIED && entry.Descriptor.GetCategory() != category {
			continue
		}
		if enabledOnly && !entry.Descriptor.GetEnabled() {
			continue
		}
		adapters = append(adapters, proto.Clone(entry.Descriptor).(*reinv1.Adapter))
	}

	return adapters
}

func (r *Registry) Get(id string) (*reinv1.Adapter, bool) {
	if r == nil {
		return nil, false
	}

	entry, ok := r.entries[id]
	if !ok {
		return nil, false
	}

	return proto.Clone(entry.Descriptor).(*reinv1.Adapter), true
}

func (r *Registry) Entry(id string) (Entry, bool) {
	if r == nil {
		return Entry{}, false
	}

	entry, ok := r.entries[id]
	if !ok {
		return Entry{}, false
	}

	entry.Descriptor = proto.Clone(entry.Descriptor).(*reinv1.Adapter)
	entry.Requires = append([]string(nil), entry.Requires...)
	return entry, true
}

func ValidateDescriptor(adapter *reinv1.Adapter) []*reinv1.ValidationMessage {
	if adapter == nil {
		return []*reinv1.ValidationMessage{validationError("adapter", "adapter is required")}
	}

	var messages []*reinv1.ValidationMessage
	if strings.TrimSpace(adapter.GetId()) == "" {
		messages = append(messages, validationError("adapter.id", "adapter id is required"))
	}
	if strings.TrimSpace(adapter.GetName()) == "" {
		messages = append(messages, validationError("adapter.name", "adapter name is required"))
	}
	if strings.TrimSpace(adapter.GetVersion()) == "" {
		messages = append(messages, validationError("adapter.version", "adapter version is required"))
	}

	switch adapter.GetCategory() {
	case reinv1.AdapterCategory_ADAPTER_CATEGORY_CODING_AGENT,
		reinv1.AdapterCategory_ADAPTER_CATEGORY_REVIEW_AGENT,
		reinv1.AdapterCategory_ADAPTER_CATEGORY_TRACKER,
		reinv1.AdapterCategory_ADAPTER_CATEGORY_NOTIFICATION,
		reinv1.AdapterCategory_ADAPTER_CATEGORY_MULTIPLEXER:
	default:
		messages = append(messages, validationError("adapter.category", fmt.Sprintf("unsupported adapter category %s", adapter.GetCategory())))
	}

	if tail, ok := adapter.GetCapabilities()["tail"]; ok {
		if _, err := strconv.ParseBool(tail); err != nil {
			messages = append(messages, validationError("adapter.capabilities.tail", "tail must be a boolean string"))
		}
	}

	if requires, ok := adapter.GetCapabilities()["requires"]; ok && strings.TrimSpace(requires) != "" {
		var required []string
		if err := json.Unmarshal([]byte(requires), &required); err != nil {
			messages = append(messages, validationError("adapter.capabilities.requires", "requires must be a JSON array of strings"))
		} else {
			for _, capability := range required {
				if strings.TrimSpace(capability) == "" {
					messages = append(messages, validationError("adapter.capabilities.requires", "requires entries must be non-empty strings"))
					break
				}
			}
		}
	}

	return messages
}

func (o DiscoveryOptions) withDefaults() DiscoveryOptions {
	if strings.TrimSpace(o.DaemonAPIVersion) == "" {
		o.DaemonAPIVersion = CurrentDaemonAPIVersion
	}
	return o
}

func loadPlugin(root string, plugin marketplacePlugin, options DiscoveryOptions) (Entry, error) {
	if strings.TrimSpace(plugin.Name) == "" {
		return Entry{}, errors.New("name is required")
	}

	source, err := parseSource(plugin.Source)
	if err != nil {
		return Entry{}, err
	}

	record := pluginRecord{
		ID:               plugin.Name,
		Description:      plugin.Description,
		Version:          plugin.Version,
		Category:         plugin.Category,
		DaemonAPIVersion: plugin.DaemonAPIVersion,
		Capabilities:     cloneCapabilities(plugin.Capabilities),
		Tail:             cloneBool(plugin.Tail),
		Requires:         append([]string(nil), plugin.Requires...),
		Source:           source,
	}

	strict := true
	if plugin.Strict != nil {
		strict = *plugin.Strict
	}

	if source.Kind == SourceLocal {
		manifest, err := loadPluginManifest(root, plugin.Name, source.Path)
		if err != nil {
			return Entry{}, err
		}
		if manifest != nil {
			if manifest.Name != "" && manifest.Name != plugin.Name {
				return Entry{}, fmt.Errorf("plugin manifest name %q does not match marketplace entry", manifest.Name)
			}
			manifestRecord := pluginRecord{
				ID:               plugin.Name,
				Description:      manifest.Description,
				Version:          manifest.Version,
				Category:         manifest.Category,
				DaemonAPIVersion: manifest.DaemonAPIVersion,
				Capabilities:     cloneCapabilities(manifest.Capabilities),
				Tail:             cloneBool(manifest.Tail),
				Requires:         append([]string(nil), manifest.Requires...),
				Source:           source,
			}
			if strict {
				record = overlayRecord(record, manifestRecord)
			} else {
				record = overlayRecord(manifestRecord, record)
			}
		}
	}

	if strings.TrimSpace(record.Version) == "" {
		return Entry{}, errors.New("version is required")
	}
	if strings.TrimSpace(record.Category) == "" {
		return Entry{}, errors.New("category is required")
	}
	if strings.TrimSpace(record.DaemonAPIVersion) == "" {
		return Entry{}, errors.New("daemonApiVersion is required")
	}
	if record.DaemonAPIVersion != options.DaemonAPIVersion {
		return Entry{}, fmt.Errorf("daemonApiVersion %q does not match daemon %q", record.DaemonAPIVersion, options.DaemonAPIVersion)
	}

	taxonomy, err := NormalizeCategory(record.Category)
	if err != nil {
		return Entry{}, err
	}

	capabilities := cloneCapabilities(record.Capabilities)
	if capabilities == nil {
		capabilities = map[string]string{}
	}
	if len(record.Requires) > 0 {
		encoded, err := json.Marshal(record.Requires)
		if err != nil {
			return Entry{}, fmt.Errorf("encode requires capability: %w", err)
		}
		capabilities["requires"] = string(encoded)
	}
	if record.Tail != nil {
		capabilities["tail"] = strconv.FormatBool(*record.Tail)
	}

	descriptor := &reinv1.Adapter{
		Id:           record.ID,
		Name:         record.ID,
		Category:     taxonomy.Proto,
		Description:  record.Description,
		Version:      record.Version,
		Enabled:      true,
		Capabilities: capabilities,
	}

	return Entry{
		ID:                  record.ID,
		MarketplaceCategory: taxonomy.Marketplace,
		Taxonomy:            taxonomy,
		Source:              source,
		Descriptor:          descriptor,
		Requires:            append([]string(nil), record.Requires...),
		Tail:                record.Tail != nil && *record.Tail,
	}, nil
}

func loadPluginManifest(root, pluginName, sourcePath string) (*pluginManifest, error) {
	pluginRoot := filepath.Join(root, filepath.Clean(sourcePath))
	manifestPath := filepath.Join(pluginRoot, ".claude-plugin", "plugin.json")
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read plugin manifest for %q: %w", pluginName, err)
	}

	var manifest pluginManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return nil, fmt.Errorf("decode plugin manifest for %q: %w", pluginName, err)
	}

	return &manifest, nil
}

func parseSource(raw json.RawMessage) (Source, error) {
	var localPath string
	if err := json.Unmarshal(raw, &localPath); err == nil {
		if err := validateLocalPath(localPath); err != nil {
			return Source{}, err
		}
		return Source{Kind: SourceLocal, Path: localPath}, nil
	}

	var source struct {
		Source   string `json:"source"`
		Repo     string `json:"repo"`
		URL      string `json:"url"`
		Path     string `json:"path"`
		Ref      string `json:"ref"`
		SHA      string `json:"sha"`
		Package  string `json:"package"`
		Version  string `json:"version"`
		Registry string `json:"registry"`
	}
	if err := json.Unmarshal(raw, &source); err != nil {
		return Source{}, fmt.Errorf("decode source: %w", err)
	}

	if source.SHA != "" && !shaPattern.MatchString(source.SHA) {
		return Source{}, fmt.Errorf("invalid source sha %q", source.SHA)
	}

	switch source.Source {
	case "github":
		return Source{Kind: SourceGitHub, Repo: source.Repo, Ref: source.Ref, SHA: source.SHA}, nil
	case "url":
		return Source{Kind: SourceURL, URL: source.URL, Ref: source.Ref, SHA: source.SHA}, nil
	case "git-subdir":
		if err := validateGitSubdirPath(source.Path); err != nil {
			return Source{}, err
		}
		return Source{Kind: SourceGitSubdir, URL: source.URL, Path: source.Path, Ref: source.Ref, SHA: source.SHA}, nil
	case "npm":
		return Source{Kind: SourceNPM, Package: source.Package, Version: source.Version, Registry: source.Registry}, nil
	default:
		return Source{}, fmt.Errorf("unsupported source kind %q", source.Source)
	}
}

func verifyIndexSignature(raw []byte, options DiscoveryOptions) error {
	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("decode marketplace signature envelope: %w", err)
	}

	value, ok := envelope["signature"]
	if !ok {
		if options.AllowUnsignedIndex {
			return nil
		}
		return errors.New("marketplace index signature is required")
	}

	signatureMap, ok := value.(map[string]any)
	if !ok {
		return errors.New("marketplace index signature must be an object")
	}

	algorithm, _ := signatureMap["algorithm"].(string)
	if algorithm != "ed25519" {
		return fmt.Errorf("unsupported marketplace signature algorithm %q", algorithm)
	}

	keyID, _ := signatureMap["keyId"].(string)
	if strings.TrimSpace(keyID) == "" {
		return errors.New("marketplace index signature keyId is required")
	}

	signatureValue, _ := signatureMap["value"].(string)
	if strings.TrimSpace(signatureValue) == "" {
		return errors.New("marketplace index signature value is required")
	}

	publicKey, ok := options.TrustedKeys[keyID]
	if !ok {
		return fmt.Errorf("trusted key %q is not configured", keyID)
	}

	delete(envelope, "signature")
	canonical, err := json.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("canonicalize marketplace index: %w", err)
	}

	signature, err := base64.StdEncoding.DecodeString(signatureValue)
	if err != nil {
		return fmt.Errorf("decode marketplace signature: %w", err)
	}

	if !ed25519.Verify(publicKey, canonical, signature) {
		return errors.New("marketplace index signature verification failed")
	}

	return nil
}

func validationError(field, message string) *reinv1.ValidationMessage {
	return &reinv1.ValidationMessage{
		Severity: reinv1.ValidationMessage_SEVERITY_ERROR,
		Field:    field,
		Message:  message,
	}
}

func overlayRecord(base, override pluginRecord) pluginRecord {
	if strings.TrimSpace(override.Description) != "" {
		base.Description = override.Description
	}
	if strings.TrimSpace(override.Version) != "" {
		base.Version = override.Version
	}
	if strings.TrimSpace(override.Category) != "" {
		base.Category = override.Category
	}
	if strings.TrimSpace(override.DaemonAPIVersion) != "" {
		base.DaemonAPIVersion = override.DaemonAPIVersion
	}
	if len(override.Capabilities) > 0 {
		if base.Capabilities == nil {
			base.Capabilities = map[string]string{}
		}
		for key, value := range override.Capabilities {
			base.Capabilities[key] = value
		}
	}
	if override.Tail != nil {
		base.Tail = cloneBool(override.Tail)
	}
	if len(override.Requires) > 0 {
		base.Requires = append([]string(nil), override.Requires...)
	}
	return base
}

func cloneCapabilities(capabilities map[string]string) map[string]string {
	if len(capabilities) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(capabilities))
	for key, value := range capabilities {
		cloned[key] = value
	}
	return cloned
}

func cloneBool(value *bool) *bool {
	if value == nil {
		return nil
	}

	cloned := *value
	return &cloned
}

func validateLocalPath(path string) error {
	if !strings.HasPrefix(path, "./") {
		return fmt.Errorf("relative source path %q must start with ./", path)
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("relative source path %q must not be absolute", path)
	}

	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("relative source path %q must stay within the marketplace root", path)
	}

	return nil
}

func validateGitSubdirPath(path string) error {
	if path == "" {
		return errors.New("git-subdir source path is required")
	}
	if filepath.IsAbs(path) {
		return fmt.Errorf("git-subdir path %q must not be absolute", path)
	}

	clean := filepath.Clean(path)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return fmt.Errorf("git-subdir path %q must stay within the repository root", path)
	}

	return nil
}

func aliasFor(got, canonical string) string {
	if strings.EqualFold(strings.TrimSpace(got), canonical) {
		return ""
	}
	return got
}
