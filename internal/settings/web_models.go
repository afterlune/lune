package settings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

const providerModelsResponseLimit = 4 << 20

type ProviderModelInfo struct {
	ID                  string `json:"id"`
	DisplayName         string `json:"displayName,omitempty"`
	ContextWindow       int64  `json:"contextWindow,omitempty"`
	MaxOutputTokens     int64  `json:"maxOutputTokens,omitempty"`
	SupportsInputImages bool   `json:"supportsInputImages,omitempty"`
	SupportsInputAudio  bool   `json:"supportsInputAudio,omitempty"`
}

type rawProviderModel struct {
	ID                   string   `json:"id"`
	DisplayName          string   `json:"display_name"`
	DisplayNameCamel     string   `json:"displayName"`
	Name                 string   `json:"name"`
	ContextWindow        int64    `json:"context_window"`
	ContextWindowCamel   int64    `json:"contextWindow"`
	MaxOutputTokens      int64    `json:"max_output_tokens"`
	MaxOutputTokensCamel int64    `json:"maxOutputTokens"`
	MaxTokens            int64    `json:"max_tokens"`
	InputModalities      []string `json:"input_modalities"`
	InputModalitiesCamel []string `json:"inputModalities"`
}

type ProviderTestInput struct {
	Provider  string
	Protocol  string
	BaseURL   string
	APIKey    *string
	APIKeyEnv string
}

func (s *Store) TestWebProvider(ctx context.Context, scope string, input ProviderTestInput) ([]ProviderModelInfo, error) {
	if ctx == nil {
		return nil, errors.New("request context is required")
	}
	if len(input.Provider) > 120 || len(input.BaseURL) > 2048 || len(input.APIKeyEnv) > 128 {
		return nil, errors.New("provider test input is too long")
	}
	protocol, baseURL, apiKey := "", "", ""
	if strings.TrimSpace(input.Provider) != "" {
		protocol, baseURL, apiKey, _ = s.resolveProviderForFetch(scope, input.Provider)
	}
	if input.Protocol != "" {
		protocol = input.Protocol
	}
	if input.BaseURL != "" {
		baseURL = strings.TrimSpace(input.BaseURL)
	}
	if input.APIKey != nil {
		apiKey = *input.APIKey
	}
	if input.APIKeyEnv != "" {
		if len(input.APIKeyEnv) > 128 || !validEnvironmentVariableName(input.APIKeyEnv) {
			return nil, errors.New("invalid provider environment variable name")
		}
		value, ok := os.LookupEnv(input.APIKeyEnv)
		if !ok || value == "" {
			return nil, errors.New("provider environment variable is not set")
		}
		apiKey = value
	}
	if len(apiKey) > 16*1024 {
		return nil, errors.New("provider credential is too long")
	}
	if protocol != "responses" && protocol != "anthropic-messages" {
		return nil, errors.New("unsupported provider protocol")
	}
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.User != nil || parsed.Fragment != "" {
		return nil, errors.New("use a valid HTTP or HTTPS provider URL without embedded credentials or a fragment")
	}
	client := &http.Client{
		Timeout: 12 * time.Second,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return errors.New("provider redirects are not followed")
		},
	}
	return fetchModelsFromEndpoint(ctx, client, protocol, baseURL, apiKey)
}

func validEnvironmentVariableName(value string) bool {
	if value == "" || !((value[0] >= 'A' && value[0] <= 'Z') || (value[0] >= 'a' && value[0] <= 'z') || value[0] == '_') {
		return false
	}
	for i := 1; i < len(value); i++ {
		ch := value[i]
		if !((ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '_') {
			return false
		}
	}
	return true
}

func candidateModelListURLs(protocol, baseURL string) []string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return nil
	}
	if protocol == "anthropic-messages" {
		if strings.HasSuffix(trimmed, "/v1") {
			return []string{trimmed + "/models"}
		}
		return []string{trimmed + "/v1/models"}
	}
	urls := []string{trimmed + "/models"}
	if !strings.HasSuffix(trimmed, "/v1") {
		urls = append(urls, trimmed+"/v1/models")
	}
	return urls
}

func (s *Store) resolveProviderForFetch(scope, providerName string) (protocol, baseURL, apiKey string, err error) {
	name := strings.TrimSpace(providerName)
	if name == "" {
		return "", "", "", errors.New("provider name is required")
	}
	normalized, err := normalizeWebScope(scope)
	if err != nil {
		return "", "", "", err
	}
	globalDoc, err := s.webDocument("")
	if err != nil {
		return "", "", "", err
	}
	globalResolved, err := s.resolvedWebConfigMap("", globalDoc, nil)
	if err != nil {
		return "", "", "", err
	}
	merged := globalResolved
	if normalized != "" {
		localDoc, err := s.webDocument(normalized)
		if err != nil {
			return "", "", "", err
		}
		localResolved, err := s.resolvedWebConfigMap(normalized, localDoc, nil)
		if err != nil {
			return "", "", "", err
		}
		merged, err = mergeConfigMaps(globalResolved, localResolved)
		if err != nil {
			return "", "", "", err
		}
	}
	cat, err := parseWebCatalog(merged)
	if err != nil {
		return "", "", "", err
	}
	entry, ok := cat.providers[name]
	if !ok {
		return "", "", "", fmt.Errorf("provider %q is not saved in SQLite settings yet; save it first", name)
	}
	protocol, _ = entry["protocol"].(string)
	baseURL, _ = entry["baseURL"].(string)
	apiKey, _ = entry["apiKey"].(string)
	if strings.TrimSpace(baseURL) == "" {
		return "", "", "", fmt.Errorf("provider %q has no baseURL", name)
	}
	return protocol, baseURL, apiKey, nil
}

func (s *Store) FetchWebProviderModels(ctx context.Context, scope, providerName string) ([]ProviderModelInfo, error) {
	protocol, baseURL, apiKey, err := s.resolveProviderForFetch(scope, providerName)
	if err != nil {
		return nil, err
	}
	return fetchModelsFromEndpoint(ctx, &http.Client{Timeout: 12 * time.Second}, protocol, baseURL, apiKey)
}

func redactSecret(text, secret string) string {
	if secret == "" {
		return text
	}
	return strings.ReplaceAll(text, secret, "[redacted]")
}

func fetchModelsFromEndpoint(ctx context.Context, client *http.Client, protocol, baseURL, apiKey string) ([]ProviderModelInfo, error) {
	urls := candidateModelListURLs(protocol, baseURL)
	if len(urls) == 0 {
		return nil, errors.New("provider baseURL is required")
	}

	var lastErr error
	for idx, endpoint := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return nil, fmt.Errorf("invalid models URL: %s", redactSecret(err.Error(), apiKey))
		}
		req.Header.Set("Accept", "application/json")
		if protocol == "anthropic-messages" {
			req.Header.Set("anthropic-version", "2023-06-01")
			if apiKey != "" {
				req.Header.Set("x-api-key", apiKey)
			}
		} else if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}

		resp, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("request %s failed: %s", endpoint, redactSecret(err.Error(), apiKey))
			continue
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, providerModelsResponseLimit))
		_ = resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read models response: %w", readErr)
			continue
		}
		if resp.StatusCode == http.StatusNotFound && idx+1 < len(urls) {
			lastErr = fmt.Errorf("HTTP %d from %s", resp.StatusCode, endpoint)
			continue
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			snippet := strings.TrimSpace(string(body))
			if len(snippet) > 200 {
				snippet = snippet[:200]
			}
			return nil, fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, redactSecret(snippet, apiKey))
		}

		var parsed struct {
			Data []rawProviderModel `json:"data"`
			Models []rawProviderModel `json:"models"`
		}
		if err := json.Unmarshal(body, &parsed); err != nil {
			return nil, fmt.Errorf("parse provider models JSON: %w", err)
		}

		seen := make(map[string]ProviderModelInfo)
		addModel := func(item rawProviderModel, fallbackNameAsID bool) {
			id := strings.TrimSpace(item.ID)
			if id == "" && fallbackNameAsID {
				id = strings.TrimSpace(item.Name)
			}
			if id == "" {
				return
			}
			if _, exists := seen[id]; exists {
				return
			}
			display := strings.TrimSpace(item.DisplayName)
			if display == "" {
				display = strings.TrimSpace(item.DisplayNameCamel)
			}
			if display == "" {
				display = strings.TrimSpace(item.Name)
			}
			ctxWin := item.ContextWindow
			if ctxWin == 0 {
				ctxWin = item.ContextWindowCamel
			}
			maxOut := item.MaxOutputTokens
			if maxOut == 0 {
				maxOut = item.MaxOutputTokensCamel
			}
			if maxOut == 0 {
				maxOut = item.MaxTokens
			}
			modalities := item.InputModalities
			if len(modalities) == 0 {
				modalities = item.InputModalitiesCamel
			}
			var supportsImg, supportsAudio bool
			for _, m := range modalities {
				switch strings.ToLower(strings.TrimSpace(m)) {
				case "image", "images", "vision":
					supportsImg = true
				case "audio":
					supportsAudio = true
				}
			}
			seen[id] = ProviderModelInfo{
				ID:                  id,
				DisplayName:         display,
				ContextWindow:       ctxWin,
				MaxOutputTokens:     maxOut,
				SupportsInputImages: supportsImg,
				SupportsInputAudio:  supportsAudio,
			}
		}
		for _, item := range parsed.Data {
			addModel(item, false)
		}
		for _, item := range parsed.Models {
			addModel(item, true)
		}
		out := make([]ProviderModelInfo, 0, len(seen))
		for _, item := range seen {
			out = append(out, item)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
		return out, nil
	}
	return nil, lastErr
}
