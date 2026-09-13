package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// These fields come from CPA management GET responses, not plugin state.
type providerCredentialConfig struct {
	APIKey        string                     `json:"api-key"`
	BaseURL       string                     `json:"base-url"`
	ProxyURL      string                     `json:"proxy-url"`
	Prefix        string                     `json:"prefix"`
	Headers       map[string]string          `json:"headers"`
	Name          string                     `json:"name"`
	Disabled      bool                       `json:"disabled"`
	APIKeyEntries []providerCredentialConfig `json:"api-key-entries"`
}

type channelCredential struct {
	ID            string `json:"id"`
	Provider      string `json:"provider"`
	ProviderLabel string `json:"provider_label"`
	Label         string `json:"label"`
	BaseURL       string `json:"base_url"`
	Status        string `json:"status"`
	Disabled      bool   `json:"disabled"`
	Source        string `json:"source"`
}

// Match CLIProxyAPI d1a024e's ConfigSynthesizer and StableIDGenerator. IDs
// must be assigned in configuration order, before any UI sorting/filtering.
func managementChannelCredentials(req pluginapi.ManagementRequest) pluginapi.ManagementResponse {
	var config map[string][]providerCredentialConfig
	if err := json.Unmarshal(req.Body, &config); err != nil || config == nil {
		return managementError(http.StatusBadRequest, "invalid AI Providers configuration")
	}
	counts := make(map[string]int)
	nextID := func(kind string, parts ...string) string {
		var input strings.Builder
		input.WriteString(kind)
		for _, part := range parts {
			input.WriteByte(0)
			input.WriteString(strings.TrimSpace(part))
		}
		sum := sha256.Sum256([]byte(input.String()))
		id := kind + ":" + hex.EncodeToString(sum[:6])
		n := counts[id]
		counts[id]++
		if n > 0 {
			return fmt.Sprintf("%s-%d", id, n)
		}
		return id
	}
	rows := make([]channelCredential, 0)
	add := func(id, provider, name, key, base string) {
		label := "无需 API Key"
		if key = strings.TrimSpace(key); key != "" {
			label = "Key ••••"
			if len(key) > 8 {
				label += key[len(key)-4:]
			}
		}
		rows = append(rows, channelCredential{
			ID: id, Provider: provider, ProviderLabel: name, Label: label,
			BaseURL: credentialDisplayURL(base), Status: "configured", Source: "ai-provider",
		})
	}
	for _, source := range []struct{ endpoint, provider string }{
		{"gemini-api-key", "gemini"}, {"interactions-api-key", "gemini-interactions"},
		{"claude-api-key", "claude"}, {"codex-api-key", "codex"}, {"xai-api-key", "xai"},
	} {
		for _, entry := range config[source.endpoint] {
			if strings.TrimSpace(entry.APIKey) == "" && strings.TrimSpace(entry.BaseURL) == "" {
				continue
			}
			id := nextID(source.provider+":apikey", entry.APIKey, entry.BaseURL, entry.ProxyURL, entry.Prefix, credentialHeaders(entry.Headers))
			add(id, source.provider, source.provider, entry.APIKey, entry.BaseURL)
		}
	}
	for _, entry := range config["openai-compatibility"] {
		// Disabled providers do not synthesize runtime credentials or consume IDs.
		if entry.Disabled {
			continue
		}
		name := strings.TrimSpace(entry.Name)
		provider := strings.ToLower(name)
		if provider == "" {
			provider = "openai-compatibility"
			name = provider
		}
		kind := "openai-compatibility:" + provider
		if provider != "openai-compatibility" && !strings.HasPrefix(provider, "openai-compatible-") {
			provider = "openai-compatible-" + provider
		}
		if len(entry.APIKeyEntries) == 0 {
			add(nextID(kind, entry.BaseURL), provider, name, "", entry.BaseURL)
		}
		for _, key := range entry.APIKeyEntries {
			add(nextID(kind, key.APIKey, entry.BaseURL, key.ProxyURL), provider, name, key.APIKey, entry.BaseURL)
		}
	}
	for _, entry := range config["vertex-api-key"] {
		add(nextID("vertex:apikey", entry.APIKey, entry.BaseURL, entry.ProxyURL), "vertex", "vertex", entry.APIKey, entry.BaseURL)
	}
	// Feed the audit label snapshot before returning; only display fields are
	// retained and nothing from the source configuration is persisted.
	recordChannelLabels(rows)
	// Never persist or echo source configuration; only return display fields.
	return managementJSON(http.StatusOK, rows)
}

func credentialHeaders(headers map[string]string) string {
	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var result strings.Builder
	for _, key := range keys {
		result.WriteString(key)
		result.WriteByte(0)
		result.WriteString(headers[key])
		result.WriteByte(0)
	}
	return result.String()
}

func credentialDisplayURL(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	u.User, u.RawQuery, u.Fragment = nil, "", ""
	u.ForceQuery = false
	return u.String()
}
