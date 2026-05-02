package adapter

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type Diagnostic struct {
	Root             string
	MarketplacePath  string
	MarketplaceName  string
	MarketplaceFound bool
	RegistryReady    bool
	Error            string
	Signature        SignatureDiagnostic
	Adapters         []AdapterDiagnostic
}

type SignatureDiagnostic struct {
	Present   bool
	Verified  bool
	Algorithm string
	KeyID     string
	Error     string
}

type AdapterDiagnostic struct {
	Name                 string
	Source               Source
	LocalManifestPath    string
	LocalManifestFound   bool
	Version              string
	Category             string
	DaemonAPIVersion     string
	CompatibleWithDaemon bool
	Error                string
}

func Diagnose(root string, options DiscoveryOptions) Diagnostic {
	options = options.withDefaults()

	diagnostic := Diagnostic{
		Root:            root,
		MarketplacePath: filepath.Join(root, ".claude-plugin", "marketplace.json"),
		RegistryReady:   true,
	}

	raw, err := os.ReadFile(diagnostic.MarketplacePath)
	if err != nil {
		if os.IsNotExist(err) {
			return diagnostic
		}
		diagnostic.RegistryReady = false
		diagnostic.Error = fmt.Sprintf("read marketplace index: %v", err)
		return diagnostic
	}
	diagnostic.MarketplaceFound = true

	diagnostic.Signature = diagnoseSignature(raw, options)
	if diagnostic.Signature.Error != "" {
		diagnostic.RegistryReady = false
		diagnostic.Error = diagnostic.Signature.Error
	}

	var document marketplaceDocument
	if err := json.Unmarshal(raw, &document); err != nil {
		diagnostic.RegistryReady = false
		diagnostic.Error = fmt.Sprintf("decode marketplace index: %v", err)
		return diagnostic
	}
	diagnostic.MarketplaceName = document.Name
	diagnostic.Adapters = make([]AdapterDiagnostic, 0, len(document.Plugins))
	for _, plugin := range document.Plugins {
		adapterDiagnostic := diagnosePlugin(root, plugin, options)
		if adapterDiagnostic.Error != "" {
			diagnostic.RegistryReady = false
			if diagnostic.Error == "" {
				diagnostic.Error = fmt.Sprintf("load plugin %q: %s", plugin.Name, adapterDiagnostic.Error)
			}
		}
		diagnostic.Adapters = append(diagnostic.Adapters, adapterDiagnostic)
	}

	return diagnostic
}

func diagnoseSignature(raw []byte, options DiscoveryOptions) SignatureDiagnostic {
	var diagnostic SignatureDiagnostic

	var envelope map[string]any
	if err := json.Unmarshal(raw, &envelope); err != nil {
		diagnostic.Error = fmt.Sprintf("decode marketplace signature envelope: %v", err)
		return diagnostic
	}

	value, ok := envelope["signature"]
	if !ok {
		if options.AllowUnsignedIndex {
			return diagnostic
		}
		diagnostic.Error = "marketplace index signature is required"
		return diagnostic
	}
	diagnostic.Present = true

	signatureMap, ok := value.(map[string]any)
	if !ok {
		diagnostic.Error = "marketplace index signature must be an object"
		return diagnostic
	}
	diagnostic.Algorithm, _ = signatureMap["algorithm"].(string)
	diagnostic.KeyID, _ = signatureMap["keyId"].(string)

	if err := verifyIndexSignature(raw, options); err != nil {
		diagnostic.Error = err.Error()
		return diagnostic
	}

	diagnostic.Verified = true
	return diagnostic
}

func diagnosePlugin(root string, plugin marketplacePlugin, options DiscoveryOptions) AdapterDiagnostic {
	diagnostic := AdapterDiagnostic{Name: plugin.Name}

	source, sourceErr := parseSource(plugin.Source)
	if sourceErr == nil {
		diagnostic.Source = source
		if source.Kind == SourceLocal {
			diagnostic.LocalManifestPath = filepath.Join(root, filepath.Clean(source.Path), ".claude-plugin", "plugin.json")
			if _, err := os.Stat(diagnostic.LocalManifestPath); err == nil {
				diagnostic.LocalManifestFound = true
			}
		}
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

	if sourceErr == nil && source.Kind == SourceLocal {
		strict := true
		if plugin.Strict != nil {
			strict = *plugin.Strict
		}
		manifest, err := loadPluginManifest(root, plugin.Name, source.Path)
		if err == nil && manifest != nil {
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

	diagnostic.Version = record.Version
	diagnostic.Category = record.Category
	diagnostic.DaemonAPIVersion = record.DaemonAPIVersion
	diagnostic.CompatibleWithDaemon = record.DaemonAPIVersion != "" && record.DaemonAPIVersion == options.DaemonAPIVersion

	if _, err := loadPlugin(root, plugin, options); err != nil {
		diagnostic.Error = err.Error()
	}

	return diagnostic
}
