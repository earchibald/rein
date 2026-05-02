package dashboards

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// #nosec G101 -- this is the public header name SigNoz expects, not a credential.
const sigNozAPIKeyHeader = "SIGNOZ-API-KEY"

const dashboardHashTagPrefix = "rein-dashboards-sha256:"

type ApplyOptions struct {
	Plugin     string
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

type ApplyResult struct {
	Plugin   string   `json:"plugin"`
	Provider string   `json:"provider"`
	Created  []string `json:"created"`
	Updated  []string `json:"updated"`
	Skipped  []string `json:"skipped"`
}

type sigNozDashboard struct {
	ID   string         `json:"id"`
	Data map[string]any `json:"data"`
}

func Apply(ctx context.Context, root string, options ApplyOptions) (ApplyResult, error) {
	if strings.TrimSpace(root) == "" {
		return ApplyResult{}, fmt.Errorf("dashboard root is required")
	}
	if strings.TrimSpace(options.Plugin) == "" {
		options.Plugin = DefaultPluginName
	}
	if strings.TrimSpace(options.BaseURL) == "" {
		return ApplyResult{}, fmt.Errorf("SigNoz base URL is required")
	}
	if strings.TrimSpace(options.APIKey) == "" {
		return ApplyResult{}, fmt.Errorf("SigNoz API key is required")
	}

	registry, err := Load(root)
	if err != nil {
		return ApplyResult{}, err
	}
	entry, ok := registry.Entry(options.Plugin)
	if !ok {
		return ApplyResult{}, fmt.Errorf("dashboards plugin %q was not found", options.Plugin)
	}
	if entry.Source.Kind != SourceLocal {
		return ApplyResult{}, remotePluginError(entry)
	}

	client := &sigNozClient{
		baseURL:    strings.TrimRight(options.BaseURL, "/"),
		apiKey:     options.APIKey,
		httpClient: options.HTTPClient,
	}
	existing, err := client.listDashboards(ctx)
	if err != nil {
		return ApplyResult{}, err
	}

	result := ApplyResult{
		Plugin:   entry.Manifest.Name,
		Provider: entry.Manifest.Provider,
	}
	for _, asset := range entry.Manifest.Assets {
		payload, err := loadAsset(filepath.Join(entry.Root, filepath.Clean(asset.Path)), asset)
		if err != nil {
			return ApplyResult{}, err
		}

		existingDashboard, ok := findDashboard(existing, asset)
		if !ok {
			created, err := client.createDashboard(ctx, payload)
			if err != nil {
				return ApplyResult{}, fmt.Errorf("create dashboard %q: %w", asset.ID, err)
			}
			result.Created = append(result.Created, created.ID)
			existing = append(existing, created)
			continue
		}

		match, err := dashboardsMatch(existingDashboard.Data, payload)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("compare dashboard %q: %w", asset.ID, err)
		}
		if match {
			result.Skipped = append(result.Skipped, existingDashboard.ID)
			continue
		}

		updated, err := client.updateDashboard(ctx, existingDashboard.ID, payload)
		if err != nil {
			return ApplyResult{}, fmt.Errorf("update dashboard %q: %w", asset.ID, err)
		}
		result.Updated = append(result.Updated, updated.ID)
		for index := range existing {
			if existing[index].ID == existingDashboard.ID {
				existing[index] = updated
				break
			}
		}
	}

	slices.Sort(result.Created)
	slices.Sort(result.Skipped)
	slices.Sort(result.Updated)
	return result, nil
}

func remotePluginError(entry Entry) error {
	switch entry.Source.Kind {
	case SourceGitHub:
		return fmt.Errorf("dashboards plugin %q is registered from GitHub repo %q, but remote dashboards bootstrap is not wired into the CLI yet", entry.Manifest.Name, entry.Source.Repo)
	case SourceURL:
		return fmt.Errorf("dashboards plugin %q is registered from remote source %q, but remote dashboards bootstrap is not wired into the CLI yet", entry.Manifest.Name, entry.Source.URL)
	default:
		return fmt.Errorf("dashboards plugin %q is not available locally", entry.Manifest.Name)
	}
}

func loadAsset(path string, asset Asset) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read dashboard asset %q: %w", asset.ID, err)
	}

	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return nil, fmt.Errorf("decode dashboard asset %q: %w", asset.ID, err)
	}
	if title := strings.TrimSpace(asset.Title); title != "" {
		if _, ok := payload["title"]; !ok {
			payload["title"] = title
		}
	}
	payload["tags"] = ensureDashboardTags(payload["tags"], asset.ID)
	hashTag, err := dashboardHashTag(payload)
	if err != nil {
		return nil, fmt.Errorf("fingerprint dashboard asset %q: %w", asset.ID, err)
	}
	payload["tags"] = ensureDashboardTags(payload["tags"], asset.ID, hashTag)
	return payload, nil
}

func ensureDashboardTags(current any, assetID string, extraTags ...string) []string {
	tags := map[string]bool{}
	appendDashboardTags(tags, current)

	for _, tag := range []string{"rein", "rein-dashboards", "rein-dashboards:" + assetID} {
		tags[tag] = true
	}
	for _, tag := range extraTags {
		if normalized := strings.TrimSpace(tag); normalized != "" {
			tags[normalized] = true
		}
	}

	ordered := make([]string, 0, len(tags))
	for tag := range tags {
		ordered = append(ordered, tag)
	}
	slices.Sort(ordered)
	return ordered
}

func appendDashboardTags(tags map[string]bool, current any) {
	switch value := current.(type) {
	case []string:
		for _, item := range value {
			appendDashboardTag(tags, item)
		}
	case []any:
		for _, item := range value {
			if tag, ok := item.(string); ok {
				appendDashboardTag(tags, tag)
			}
		}
	}
}

func appendDashboardTag(tags map[string]bool, tag string) {
	normalized := strings.TrimSpace(tag)
	if normalized == "" || strings.HasPrefix(normalized, dashboardHashTagPrefix) {
		return
	}
	tags[normalized] = true
}

func dashboardHashTag(payload map[string]any) (string, error) {
	normalized, err := normalizedDashboardPayload(payload)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(normalized)
	return dashboardHashTagPrefix + hex.EncodeToString(sum[:]), nil
}

func dashboardsMatch(existing, desired map[string]any) (bool, error) {
	expectedHashTag, err := dashboardHashTag(desired)
	if err != nil {
		return false, err
	}
	if hasTag(existing["tags"], expectedHashTag) {
		return true, nil
	}

	existingPayload, err := normalizedDashboardPayload(existing)
	if err != nil {
		return false, err
	}
	desiredPayload, err := normalizedDashboardPayload(desired)
	if err != nil {
		return false, err
	}
	return bytes.Equal(existingPayload, desiredPayload), nil
}

func normalizedDashboardPayload(payload map[string]any) ([]byte, error) {
	return json.Marshal(normalizeDashboardValue(payload, ""))
}

func normalizeDashboardValue(value any, field string) any {
	switch typed := value.(type) {
	case map[string]any:
		normalized := make(map[string]any, len(typed))
		for key, item := range typed {
			normalized[key] = normalizeDashboardValue(item, key)
		}
		return normalized
	case []string:
		if field == "tags" {
			return normalizeDashboardTags(typed)
		}
		return append([]string(nil), typed...)
	case []any:
		if field == "tags" {
			return normalizeDashboardTags(typed)
		}
		normalized := make([]any, 0, len(typed))
		for _, item := range typed {
			normalized = append(normalized, normalizeDashboardValue(item, ""))
		}
		return normalized
	default:
		return value
	}
}

func normalizeDashboardTags(current any) []string {
	tags := map[string]bool{}
	appendDashboardTags(tags, current)
	ordered := make([]string, 0, len(tags))
	for tag := range tags {
		ordered = append(ordered, tag)
	}
	slices.Sort(ordered)
	return ordered
}

func findDashboard(existing []sigNozDashboard, asset Asset) (sigNozDashboard, bool) {
	marker := "rein-dashboards:" + asset.ID
	title := strings.TrimSpace(asset.Title)
	for _, dashboard := range existing {
		if hasTag(dashboard.Data["tags"], marker) {
			return dashboard, true
		}
		if title == "" {
			continue
		}
		if dashboardTitle, _ := dashboard.Data["title"].(string); strings.TrimSpace(dashboardTitle) == title {
			return dashboard, true
		}
	}
	return sigNozDashboard{}, false
}

func hasTag(current any, want string) bool {
	switch value := current.(type) {
	case []string:
		for _, item := range value {
			if item == want {
				return true
			}
		}
	case []any:
		for _, item := range value {
			if tag, ok := item.(string); ok && tag == want {
				return true
			}
		}
	}
	return false
}

type sigNozClient struct {
	baseURL    string
	apiKey     string
	httpClient *http.Client
}

func (c *sigNozClient) listDashboards(ctx context.Context) ([]sigNozDashboard, error) {
	var dashboards []sigNozDashboard
	if err := c.do(ctx, http.MethodGet, "/api/v1/dashboards", nil, &dashboards); err != nil {
		return nil, fmt.Errorf("list dashboards: %w", err)
	}
	return dashboards, nil
}

func (c *sigNozClient) createDashboard(ctx context.Context, payload map[string]any) (sigNozDashboard, error) {
	var dashboard sigNozDashboard
	if err := c.do(ctx, http.MethodPost, "/api/v1/dashboards", payload, &dashboard); err != nil {
		return sigNozDashboard{}, err
	}
	return dashboard, nil
}

func (c *sigNozClient) updateDashboard(ctx context.Context, dashboardID string, payload map[string]any) (sigNozDashboard, error) {
	var dashboard sigNozDashboard
	if err := c.do(ctx, http.MethodPut, "/api/v1/dashboards/"+url.PathEscape(dashboardID), payload, &dashboard); err != nil {
		return sigNozDashboard{}, err
	}
	return dashboard, nil
}

func (c *sigNozClient) do(ctx context.Context, method, path string, payload, out any) error {
	if c.httpClient == nil {
		c.httpClient = &http.Client{Timeout: 15 * time.Second}
	}

	var body io.Reader
	if payload != nil {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(raw)
	}

	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	request.Header.Set(sigNozAPIKeyHeader, c.apiKey)
	if payload != nil {
		request.Header.Set("Content-Type", "application/json")
	}

	response, err := c.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(response.Body, 8192))
		return fmt.Errorf("SigNoz API %s %s returned %s: %s", method, path, response.Status, strings.TrimSpace(string(message)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(response.Body).Decode(out)
}
