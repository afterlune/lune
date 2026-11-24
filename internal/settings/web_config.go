package settings

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	core "github.com/afterlune/luneCore"
	publicmodel "github.com/afterlune/luneCore/model"
)

const WebStartupConfigLimit = 1024 * 1024

var ErrInvalidRemoteURL = errors.New("use a wss:// Core URL without credentials, query, or fragment")

type MCPServerSummary struct {
	Transport         string   `json:"transport"`
	Command           string   `json:"command,omitempty"`
	Args              []string `json:"args,omitempty"`
	EnvKeys           []string `json:"envKeys,omitempty"`
	URL               string   `json:"url,omitempty"`
	ClientMetadataURL string   `json:"clientMetadataURL,omitempty"`
}

type WebConfigView struct {
	Document          map[string]any              `json:"document"`
	SavedKeys         []string                    `json:"savedKeys"`
	HasMCP            bool                        `json:"hasMCP"`
	MCPServers        map[string]MCPServerSummary `json:"mcpServers,omitempty"`
	CanSaveSecrets    bool                        `json:"canSaveSecrets"`
	SecretStorage     string                      `json:"secretStorage"`
	InheritedProfiles []string                    `json:"inheritedProfiles"`
}

type WebConfigImport struct {
	ID             string         `json:"id"`
	Document       map[string]any `json:"document"`
	SuppliedKeys   []string       `json:"suppliedKeys"`
	UnresolvedKeys []string       `json:"unresolvedKeys"`
	HasMCP         bool           `json:"hasMCP"`
}

type WebConnection struct {
	Mode       string `json:"mode"`
	Address    string `json:"address"`
	Name       string `json:"name"`
	Remembered bool   `json:"remembered"`
	Session    string `json:"session,omitempty"`
}

type webCatalog struct {
	models    map[string]any
	providers map[string]map[string]any
	profiles  map[string]map[string]any
}

type webSecretValue struct {
	set      bool
	text     string
	encoding string
	stored   []byte
}

func normalizeWebScope(scope string) (string, error) {
	trimmed := strings.TrimSpace(scope)
	if trimmed == "" {
		return "", nil
	}
	return workspaceConfigKey(trimmed)
}

func NormalizeRemoteURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme != "wss" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrInvalidRemoteURL
	}
	return parsed.String(), nil
}

func cloneMap(src map[string]any) map[string]any {
	if src == nil {
		return map[string]any{}
	}
	data, err := json.Marshal(src)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil || out == nil {
		return map[string]any{}
	}
	return out
}

func checkAllowedFields(obj map[string]any, allowed []string, section string) error {
	for key := range obj {
		if key == "__proto__" || key == "constructor" || key == "prototype" {
			return errors.New("configuration contains a reserved field name")
		}
		matched := false
		for _, field := range allowed {
			if key == field {
				matched = true
				break
			}
		}
		if !matched {
			return fmt.Errorf("use the supported, case-sensitive fields in %s (%s)", section, key)
		}
	}
	return nil
}

func asJSONMap(value any) (map[string]any, error) {
	if value == nil {
		return map[string]any{}, nil
	}
	obj, ok := value.(map[string]any)
	if !ok {
		return nil, errors.New("configuration section must be a JSON object")
	}
	return obj, nil
}

func parseWebCatalog(document map[string]any) (webCatalog, error) {
	if err := checkAllowedFields(document, []string{"models", "runtime", "attachments", "mcpServers"}, "the configuration"); err != nil {
		return webCatalog{}, err
	}
	modelsMap, err := asJSONMap(document["models"])
	if err != nil {
		return webCatalog{}, err
	}
	if err := checkAllowedFields(modelsMap, []string{"providers", "profiles", "defaultProfile"}, "models"); err != nil {
		return webCatalog{}, err
	}
	providersRaw, err := asJSONMap(modelsMap["providers"])
	if err != nil {
		return webCatalog{}, err
	}
	providers := make(map[string]map[string]any, len(providersRaw))
	for name, entry := range providersRaw {
		item, err := asJSONMap(entry)
		if err != nil {
			return webCatalog{}, err
		}
		if err := checkAllowedFields(item, []string{"protocol", "baseURL", "apiKey", "apiKeyEnv", "apiKeyKeyringRef", "serverCompaction"}, "a provider"); err != nil {
			return webCatalog{}, err
		}
		providers[name] = item
	}
	profilesRaw, err := asJSONMap(modelsMap["profiles"])
	if err != nil {
		return webCatalog{}, err
	}
	profiles := make(map[string]map[string]any, len(profilesRaw))
	for name, entry := range profilesRaw {
		item, err := asJSONMap(entry)
		if err != nil {
			return webCatalog{}, err
		}
		if err := checkAllowedFields(item, []string{"provider", "model", "displayName", "contextWindow", "maxOutputTokens", "supportsInputImages", "supportsInputAudio"}, "a model profile"); err != nil {
			return webCatalog{}, err
		}
		profiles[name] = item
	}
	return webCatalog{models: modelsMap, providers: providers, profiles: profiles}, nil
}

func withProvidersAndProfiles(document map[string]any, cat webCatalog, providers map[string]map[string]any) map[string]any {
	out := cloneMap(document)
	if len(cat.models) == 0 && len(providers) == 0 && len(cat.profiles) == 0 {
		return out
	}
	models := cloneMap(cat.models)
	if len(providers) > 0 {
		providersCopy := make(map[string]any, len(providers))
		for k, v := range providers {
			providersCopy[k] = cloneMap(v)
		}
		models["providers"] = providersCopy
	} else {
		delete(models, "providers")
	}
	if len(cat.profiles) > 0 {
		profilesCopy := make(map[string]any, len(cat.profiles))
		for k, v := range cat.profiles {
			profilesCopy[k] = cloneMap(v)
		}
		models["profiles"] = profilesCopy
	}
	out["models"] = models
	return out
}

func mergeConfigMaps(globalDoc, localDoc map[string]any) (map[string]any, error) {
	inherited, err := parseWebCatalog(globalDoc)
	if err != nil {
		return nil, err
	}
	overrides, err := parseWebCatalog(localDoc)
	if err != nil {
		return nil, err
	}
	mergedProviders := make(map[string]any, len(inherited.providers)+len(overrides.providers))
	for k, v := range inherited.providers {
		mergedProviders[k] = cloneMap(v)
	}
	for k, v := range overrides.providers {
		mergedProviders[k] = cloneMap(v)
	}
	mergedProfiles := make(map[string]any, len(inherited.profiles)+len(overrides.profiles))
	for k, v := range inherited.profiles {
		mergedProfiles[k] = cloneMap(v)
	}
	for k, v := range overrides.profiles {
		mergedProfiles[k] = cloneMap(v)
	}
	mergedModels := cloneMap(inherited.models)
	for k, v := range overrides.models {
		mergedModels[k] = v
	}
	if len(mergedProviders) > 0 {
		mergedModels["providers"] = mergedProviders
	}
	if len(mergedProfiles) > 0 {
		mergedModels["profiles"] = mergedProfiles
	}
	mergedRuntime := cloneMap(globalDoc)
	runtimeGlobal, _ := asJSONMap(mergedRuntime["runtime"])
	runtimeLocal, _ := asJSONMap(localDoc["runtime"])
	mergedRuntimeObj := cloneMap(runtimeGlobal)
	for k, v := range runtimeLocal {
		mergedRuntimeObj[k] = v
	}
	attachGlobal, _ := asJSONMap(globalDoc["attachments"])
	attachLocal, _ := asJSONMap(localDoc["attachments"])
	mergedAttach := cloneMap(attachGlobal)
	for k, v := range attachLocal {
		mergedAttach[k] = v
	}
	mcpGlobal, _ := asJSONMap(globalDoc["mcpServers"])
	mcpLocal, _ := asJSONMap(localDoc["mcpServers"])
	mergedMCP := cloneMap(mcpGlobal)
	for k, v := range mcpLocal {
		mergedMCP[k] = v
	}

	out := cloneMap(globalDoc)
	for k, v := range localDoc {
		out[k] = v
	}
	if len(mergedModels) > 0 {
		out["models"] = mergedModels
	}
	if len(mergedRuntimeObj) > 0 {
		out["runtime"] = mergedRuntimeObj
	}
	if len(mergedAttach) > 0 {
		out["attachments"] = mergedAttach
	}
	if len(mergedMCP) > 0 {
		out["mcpServers"] = mergedMCP
	}
	return out, nil
}

func (s *Store) webDocument(scope string) (map[string]any, error) {
	var encoded string
	err := s.db.QueryRow(`SELECT document FROM web_configs WHERE scope = ?`, scope).Scan(&encoded)
	if errors.Is(err, sql.ErrNoRows) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read web configuration: %w", err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(encoded), &doc); err != nil || doc == nil {
		return nil, fmt.Errorf("decode web configuration: %w", err)
	}
	return doc, nil
}

func (s *Store) hasWebSecret(scope, name string) (bool, error) {
	var one int
	err := s.db.QueryRow(`SELECT 1 FROM web_secrets WHERE scope = ? AND name = ?`, scope, name).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (s *Store) encodeWebSecret(text string) (string, []byte) {
	if SecretsProtected() {
		if encrypted, err := ProtectSecret(text); err == nil && len(encrypted) > 0 {
			return "dpapi", encrypted
		}
	}
	return "plain", []byte(text)
}

func (s *Store) readWebSecret(scope, name string) (string, bool, error) {
	var encoding string
	var value []byte
	err := s.db.QueryRow(`SELECT encoding, value FROM web_secrets WHERE scope = ? AND name = ?`, scope, name).Scan(&encoding, &value)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("read web secret: %w", err)
	}
	if encoding == "dpapi" {
		plain, decErr := UnprotectSecret(value)
		if decErr != nil {
			return "", false, fmt.Errorf("decrypt web secret: %w", decErr)
		}
		return plain, true, nil
	}
	return string(value), true, nil
}

func (s *Store) WebConfig(scope string) (WebConfigView, error) {
	normalized, err := normalizeWebScope(scope)
	if err != nil {
		return WebConfigView{}, err
	}
	doc, err := s.webDocument(normalized)
	if err != nil {
		return WebConfigView{}, err
	}
	cat, err := parseWebCatalog(doc)
	if err != nil {
		return WebConfigView{}, err
	}
	savedKeys := make([]string, 0, len(cat.providers))
	for name := range cat.providers {
		ok, err := s.hasWebSecret(normalized, "provider:"+name)
		if err != nil {
			return WebConfigView{}, err
		}
		if ok {
			savedKeys = append(savedKeys, name)
		}
	}
	sort.Strings(savedKeys)
	mcpJSON, hasMCP, err := s.readWebSecret(normalized, "mcp")
	if err != nil {
		return WebConfigView{}, err
	}
	var mcpSummaries map[string]MCPServerSummary
	if hasMCP && strings.TrimSpace(mcpJSON) != "" {
		mcpSummaries = summarizeMCPServers(mcpJSON)
	}
	var inheritedProfiles []string
	if normalized != "" {
		globalDoc, err := s.webDocument("")
		if err != nil {
			return WebConfigView{}, err
		}
		globalCat, err := parseWebCatalog(globalDoc)
		if err != nil {
			return WebConfigView{}, err
		}
		for name := range globalCat.profiles {
			inheritedProfiles = append(inheritedProfiles, name)
		}
		sort.Strings(inheritedProfiles)
	}
	storage := "sqlite"
	if SecretsProtected() {
		storage = "encrypted"
	}
	return WebConfigView{
		Document:          doc,
		SavedKeys:         savedKeys,
		HasMCP:            hasMCP,
		MCPServers:        mcpSummaries,
		CanSaveSecrets:    true,
		SecretStorage:     storage,
		InheritedProfiles: inheritedProfiles,
	}, nil
}

func summarizeMCPServers(rawJSON string) map[string]MCPServerSummary {
	var parsed map[string]map[string]any
	if err := json.Unmarshal([]byte(rawJSON), &parsed); err != nil || len(parsed) == 0 {
		return nil
	}
	out := make(map[string]MCPServerSummary, len(parsed))
	for name, entry := range parsed {
		transport, _ := entry["transport"].(string)
		summary := MCPServerSummary{Transport: transport}
		if cmd, ok := entry["command"].(string); ok && cmd != "" {
			summary.Command = cmd
		}
		if rawArgs, ok := entry["args"].([]any); ok && len(rawArgs) > 0 {
			args := make([]string, 0, len(rawArgs))
			for _, a := range rawArgs {
				if s, ok := a.(string); ok {
					args = append(args, s)
				}
			}
			if len(args) > 0 {
				summary.Args = args
			}
		}
		if rawEnv, ok := entry["env"].(map[string]any); ok && len(rawEnv) > 0 {
			keys := make([]string, 0, len(rawEnv))
			for k := range rawEnv {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			summary.EnvKeys = keys
		}
		if u, ok := entry["url"].(string); ok && u != "" {
			summary.URL = u
		}
		if cm, ok := entry["clientMetadataURL"].(string); ok && cm != "" {
			summary.ClientMetadataURL = cm
		}
		out[name] = summary
	}
	return out
}

func mergeIncomingMCPServers(incoming map[string]any, existingJSON string) (map[string]any, error) {
	if err := checkAllowedFields(incoming, func() []string {
		keys := make([]string, 0, len(incoming))
		for k := range incoming {
			keys = append(keys, k)
		}
		return keys
	}(), "mcpServers"); err != nil {
		return nil, err
	}
	var oldServers map[string]map[string]any
	if strings.TrimSpace(existingJSON) != "" {
		_ = json.Unmarshal([]byte(existingJSON), &oldServers)
	}
	out := make(map[string]any, len(incoming))
	for name, rawEntry := range incoming {
		entry, err := asJSONMap(rawEntry)
		if err != nil {
			return nil, err
		}
		if err := checkAllowedFields(entry, []string{"transport", "command", "args", "env", "keepEnvKeys", "url", "clientMetadataURL"}, fmt.Sprintf("mcpServers[%q]", name)); err != nil {
			return nil, err
		}
		clean := cloneMap(entry)
		rawKeep := clean["keepEnvKeys"]
		delete(clean, "keepEnvKeys")

		mergedEnv := map[string]string{}
		if oldServer, ok := oldServers[name]; ok {
			if oldEnv, ok := oldServer["env"].(map[string]any); ok {
				if keepList, ok := rawKeep.([]any); ok {
					for _, k := range keepList {
						if keyStr, ok := k.(string); ok {
							if val, exists := oldEnv[keyStr].(string); exists {
								mergedEnv[keyStr] = val
							}
						}
					}
				}
			}
		}
		if rawEnv, hasEnv := clean["env"]; hasEnv && rawEnv != nil {
			envMap, err := asJSONMap(rawEnv)
			if err != nil {
				return nil, err
			}
			for k, v := range envMap {
				strVal, ok := v.(string)
				if !ok {
					return nil, fmt.Errorf("mcpServers[%q].env[%q] must be a string", name, k)
				}
				mergedEnv[k] = strVal
			}
		}
		delete(clean, "env")
		if len(mergedEnv) > 0 {
			clean["env"] = mergedEnv
		}
		out[name] = clean
	}
	return out, nil
}

func (s *Store) PrepareWebImport(rawJSON string) (WebConfigImport, error) {
	if len(rawJSON) > WebStartupConfigLimit {
		return WebConfigImport{}, errors.New("configuration file exceeds 1 MiB")
	}
	var doc map[string]any
	decoder := json.NewDecoder(bytes.NewBufferString(rawJSON))
	if err := decoder.Decode(&doc); err != nil || doc == nil {
		return WebConfigImport{}, errors.New("selected file is not a valid JSON object")
	}
	cat, err := parseWebCatalog(doc)
	if err != nil {
		return WebConfigImport{}, err
	}
	supplied := make([]string, 0)
	unresolved := make([]string, 0)
	publicProviders := make(map[string]map[string]any, len(cat.providers))
	for name, entry := range cat.providers {
		clean := cloneMap(entry)
		apiKey, _ := clean["apiKey"].(string)
		keyringRef, _ := clean["apiKeyKeyringRef"].(string)
		apiKeyEnv, _ := clean["apiKeyEnv"].(string)
		delete(clean, "apiKey")
		delete(clean, "apiKeyKeyringRef")
		if keyringRef != "" && apiKey == "" && apiKeyEnv == "" {
			unresolved = append(unresolved, name)
		}
		if apiKey != "" {
			supplied = append(supplied, name)
		}
		publicProviders[name] = clean
	}
	sort.Strings(supplied)
	sort.Strings(unresolved)
	publicDoc := withProvidersAndProfiles(doc, cat, publicProviders)
	_, hasMCP := publicDoc["mcpServers"]
	delete(publicDoc, "mcpServers")

	id := rand.Text()
	s.importMu.Lock()
	if s.importDrafts == nil {
		s.importDrafts = make(map[string]map[string]any)
	} else {
		clear(s.importDrafts)
	}
	s.importDrafts[id] = cloneMap(doc)
	s.importMu.Unlock()

	return WebConfigImport{
		ID:             id,
		Document:       publicDoc,
		SuppliedKeys:   supplied,
		UnresolvedKeys: unresolved,
		HasMCP:         hasMCP,
	}, nil
}

func (s *Store) resolvedWebConfigMap(scope string, doc map[string]any, pending map[string]webSecretValue) (map[string]any, error) {
	cat, err := parseWebCatalog(doc)
	if err != nil {
		return nil, err
	}
	providers := make(map[string]map[string]any, len(cat.providers))
	readSecret := func(name string) (string, bool, error) {
		if pending != nil {
			if change, ok := pending[name]; ok {
				if !change.set {
					return "", false, nil
				}
				return change.text, true, nil
			}
		}
		return s.readWebSecret(scope, name)
	}
	for name, provider := range cat.providers {
		item := cloneMap(provider)
		delete(item, "apiKeyKeyringRef")
		if envVal, hasEnv := item["apiKeyEnv"]; hasEnv {
			envName, ok := envVal.(string)
			if !ok || strings.TrimSpace(envName) == "" {
				return nil, fmt.Errorf("provider %q apiKeyEnv must be a non-empty variable name", name)
			}
			delete(item, "apiKeyEnv")
			val := os.Getenv(envName)
			if strings.TrimSpace(val) == "" {
				return nil, fmt.Errorf("set %s in the lune web environment or provide an API key", envName)
			}
			item["apiKey"] = val
		} else {
			key, ok, err := readSecret("provider:" + name)
			if err != nil {
				return nil, err
			}
			if ok {
				item["apiKey"] = key
			} else {
				item["apiKey"] = ""
			}
		}
		providers[name] = item
	}
	out := withProvidersAndProfiles(doc, cat, providers)
	if mcpJSON, ok, err := readSecret("mcp"); err != nil {
		return nil, err
	} else if ok && strings.TrimSpace(mcpJSON) != "" {
		var mcpObj map[string]any
		if err := json.Unmarshal([]byte(mcpJSON), &mcpObj); err != nil {
			return nil, fmt.Errorf("decode saved MCP servers: %w", err)
		}
		out["mcpServers"] = mcpObj
	}
	return out, nil
}

func validateMergedWebProject(merged map[string]any) (core.ProjectConfig, bool, error) {
	data, err := json.Marshal(merged)
	if err != nil {
		return core.ProjectConfig{}, false, err
	}
	project := core.DefaultProjectConfig()
	if _, hasMCP := merged["mcpServers"]; hasMCP {
		project.MCPServers = nil
	}
	if err := json.Unmarshal(data, &project); err != nil {
		return core.ProjectConfig{}, false, fmt.Errorf("invalid configuration: %w", err)
	}
	hasProviders := len(project.Models.Providers) > 0
	hasProfiles := len(project.Models.Profiles) > 0
	if !hasProviders && !hasProfiles {
		if _, explicitMCP := merged["mcpServers"]; explicitMCP && len(project.MCPServers) > 0 {
			dummy := core.ProjectConfig{
				Models: publicmodel.Config{
					Providers:      map[string]publicmodel.ProviderConfig{"__mcp_check": {Protocol: publicmodel.ProtocolResponses, BaseURL: "https://example.invalid", APIKey: "placeholder"}},
					Profiles:       map[string]publicmodel.ModelProfile{"__mcp_check": {Provider: "__mcp_check", Model: "placeholder"}},
					DefaultProfile: "__mcp_check",
				},
				MCPServers: project.MCPServers,
			}
			if err := dummy.Validate(); err != nil {
				return core.ProjectConfig{}, false, err
			}
		}
		return project, false, nil
	}
	if hasProviders && !hasProfiles {
		// Allow saving providers before the user finishes adding model profiles,
		// while validating each provider's protocol & URL via a synthetic profile.
		dummy := core.ProjectConfig{
			Models: publicmodel.Config{
				Providers:      project.Models.Providers,
				Profiles:       make(map[string]publicmodel.ModelProfile, len(project.Models.Providers)),
				DefaultProfile: "",
			},
			MCPServers: project.MCPServers,
		}
		for name, p := range project.Models.Providers {
			if dummy.Models.DefaultProfile == "" {
				dummy.Models.DefaultProfile = "__check__" + name
			}
			maxTok := int64(0)
			if p.Protocol == publicmodel.ProtocolAnthropicMessages {
				maxTok = 4096
			}
			dummy.Models.Profiles["__check__"+name] = publicmodel.ModelProfile{
				Provider: name, Model: "validation-placeholder", MaxOutputTokens: maxTok,
			}
		}
		if err := dummy.Validate(); err != nil {
			return core.ProjectConfig{}, false, err
		}
		return project, false, nil
	}
	if err := project.Validate(); err != nil {
		return core.ProjectConfig{}, false, err
	}
	return project, true, nil
}

func (s *Store) SaveWebConfig(scope string, document map[string]any, importID string) error {
	normalized, err := normalizeWebScope(scope)
	if err != nil {
		return err
	}
	doc := cloneMap(document)
	cat, err := parseWebCatalog(doc)
	if err != nil {
		return err
	}
	oldDoc, err := s.webDocument(normalized)
	if err != nil {
		return err
	}
	oldCat, err := parseWebCatalog(oldDoc)
	if err != nil {
		return err
	}

	var importedDoc map[string]any
	if importID != "" {
		s.importMu.Lock()
		importedDoc = s.importDrafts[importID]
		s.importMu.Unlock()
		if importedDoc == nil {
			return errors.New("select the configuration file again")
		}
	}
	incomingCat, _ := parseWebCatalog(importedDoc)

	changes := make(map[string]webSecretValue)
	publicProviders := make(map[string]map[string]any, len(cat.providers))
	for name, entry := range cat.providers {
		clean := cloneMap(entry)
		rawKey, hasRawKey := clean["apiKey"]
		rawRef, hasRef := clean["apiKeyKeyringRef"]
		delete(clean, "apiKey")
		delete(clean, "apiKeyKeyringRef")

		if !hasRawKey && incomingCat.providers[name] != nil {
			if ik, ok := incomingCat.providers[name]["apiKey"]; ok {
				rawKey = ik
				hasRawKey = true
			}
		}
		oldRef, _ := rawRef.(string)
		if (!hasRef || oldRef == "") && incomingCat.providers[name] != nil {
			oldRef, _ = incomingCat.providers[name]["apiKeyKeyringRef"].(string)
		}
		envName, hasEnv := clean["apiKeyEnv"].(string)
		hasSaved, _ := s.hasWebSecret(normalized, "provider:"+name)
		keyStr, _ := rawKey.(string)
		if oldRef != "" && keyStr == "" && strings.TrimSpace(envName) == "" && !hasSaved {
			return errors.New("replace the imported keyring reference with an API key or apiKeyEnv")
		}

		if hasEnv && strings.TrimSpace(envName) != "" {
			changes["provider:"+name] = webSecretValue{set: false}
		} else if hasRawKey {
			str, ok := rawKey.(string)
			if !ok {
				return errors.New("API keys must be strings")
			}
			if strings.ContainsAny(str, "\r\n") {
				return errors.New("API keys must be a single line")
			}
			if str == "" {
				changes["provider:"+name] = webSecretValue{set: false}
			} else {
				enc, val := s.encodeWebSecret(str)
				changes["provider:"+name] = webSecretValue{set: true, text: str, encoding: enc, stored: val}
			}
		}
		publicProviders[name] = clean
	}
	for oldName := range oldCat.providers {
		if _, exists := publicProviders[oldName]; !exists {
			changes["provider:"+oldName] = webSecretValue{set: false}
		}
	}

	publicDoc := withProvidersAndProfiles(doc, cat, publicProviders)
	rawMCP, hasMCPField := publicDoc["mcpServers"]
	delete(publicDoc, "mcpServers")
	if !hasMCPField && importedDoc != nil {
		if im, ok := importedDoc["mcpServers"]; ok {
			rawMCP = im
			hasMCPField = true
		}
	}
	if hasMCPField {
		mcpObj, err := asJSONMap(rawMCP)
		if err != nil {
			return err
		}
		oldMCPJSON, _, _ := s.readWebSecret(normalized, "mcp")
		mergedMCP, err := mergeIncomingMCPServers(mcpObj, oldMCPJSON)
		if err != nil {
			return err
		}
		if len(mergedMCP) == 0 {
			changes["mcp"] = webSecretValue{set: false}
		} else {
			encodedMCP, err := json.Marshal(mergedMCP)
			if err != nil {
				return err
			}
			enc, val := s.encodeWebSecret(string(encodedMCP))
			changes["mcp"] = webSecretValue{set: true, text: string(encodedMCP), encoding: enc, stored: val}
		}
	}

	candidate, err := s.resolvedWebConfigMap(normalized, publicDoc, changes)
	if err != nil {
		return err
	}
	mergedForValidation := candidate
	if normalized != "" {
		globalDoc, err := s.webDocument("")
		if err != nil {
			return err
		}
		globalResolved, err := s.resolvedWebConfigMap("", globalDoc, nil)
		if err != nil {
			return err
		}
		mergedForValidation, err = mergeConfigMaps(globalResolved, candidate)
		if err != nil {
			return err
		}
	}
	if _, _, err := validateMergedWebProject(mergedForValidation); err != nil {
		return err
	}

	publicBytes, err := json.Marshal(publicDoc)
	if err != nil {
		return err
	}
	if len(publicBytes)+1 > WebStartupConfigLimit {
		return errors.New("configuration exceeds 1 MiB")
	}

	now := time.Now().UTC().UnixNano()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(`
		INSERT INTO web_configs(scope, document, updated_at) VALUES (?, ?, ?)
		ON CONFLICT(scope) DO UPDATE SET document = excluded.document, updated_at = excluded.updated_at
	`, normalized, string(publicBytes), now); err != nil {
		return err
	}
	for secretName, change := range changes {
		if !change.set {
			if _, err := tx.Exec(`DELETE FROM web_secrets WHERE scope = ? AND name = ?`, normalized, secretName); err != nil {
				return err
			}
			continue
		}
		if _, err := tx.Exec(`
			INSERT INTO web_secrets(scope, name, encoding, value, updated_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(scope, name) DO UPDATE SET encoding = excluded.encoding, value = excluded.value, updated_at = excluded.updated_at
		`, normalized, secretName, change.encoding, change.stored, now); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if importID != "" {
		s.importMu.Lock()
		delete(s.importDrafts, importID)
		s.importMu.Unlock()
	}
	return nil
}

func (s *Store) ResetWebWorkspaceConfig(scope string) error {
	normalized, err := normalizeWebScope(scope)
	if err != nil || normalized == "" {
		return errors.New("select a workspace to reset")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM web_configs WHERE scope = ?`, normalized); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM web_secrets WHERE scope = ?`, normalized); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) EffectiveWebConfig(workspace string) (core.ProjectConfig, bool, error) {
	normalized, err := normalizeWebScope(workspace)
	if err != nil {
		return core.ProjectConfig{}, false, err
	}
	globalDoc, err := s.webDocument("")
	if err != nil {
		return core.ProjectConfig{}, false, err
	}
	globalResolved, err := s.resolvedWebConfigMap("", globalDoc, nil)
	if err != nil {
		return core.ProjectConfig{}, false, err
	}
	merged := globalResolved
	if normalized != "" {
		localDoc, err := s.webDocument(normalized)
		if err != nil {
			return core.ProjectConfig{}, false, err
		}
		localResolved, err := s.resolvedWebConfigMap(normalized, localDoc, nil)
		if err != nil {
			return core.ProjectConfig{}, false, err
		}
		merged, err = mergeConfigMaps(globalResolved, localResolved)
		if err != nil {
			return core.ProjectConfig{}, false, err
		}
	}
	return validateMergedWebProject(merged)
}

func (s *Store) WebConnections() ([]WebConnection, error) {
	rows, err := s.db.Query(`
		SELECT c.mode, c.address, c.name, coalesce(c.session, ''),
		       EXISTS(SELECT 1 FROM web_secrets s WHERE s.scope = 'remote:' || c.address AND s.name = 'token')
		FROM web_connections c
		ORDER BY c.touched DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list web connections: %w", err)
	}
	defer rows.Close()
	items := make([]WebConnection, 0)
	for rows.Next() {
		var item WebConnection
		var hasToken bool
		if err := rows.Scan(&item.Mode, &item.Address, &item.Name, &item.Session, &hasToken); err != nil {
			return nil, fmt.Errorf("read web connection: %w", err)
		}
		if item.Mode == "remote" {
			item.Remembered = hasToken
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) SaveWebConnection(mode, address, name string, token *string, rememberToken bool) error {
	if mode != "local" && mode != "remote" {
		return errors.New("invalid connection mode")
	}
	normalizedAddr := strings.TrimSpace(address)
	if mode == "local" {
		var err error
		normalizedAddr, err = workspaceConfigKey(normalizedAddr)
		if err != nil {
			return err
		}
	} else {
		var err error
		normalizedAddr, err = NormalizeRemoteURL(normalizedAddr)
		if err != nil {
			return err
		}
	}
	displayName := strings.TrimSpace(name)
	if displayName == "" {
		if mode == "local" {
			displayName = filepath.Base(normalizedAddr)
		} else {
			displayName = normalizedAddr
		}
	}
	now := time.Now().UTC().UnixMilli()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`
		INSERT INTO web_connections(mode, address, name, touched) VALUES (?, ?, ?, ?)
		ON CONFLICT(mode, address) DO UPDATE SET name = excluded.name, touched = excluded.touched
	`, mode, normalizedAddr, displayName, now); err != nil {
		return err
	}
	if mode == "remote" && token != nil {
		secretScope := "remote:" + normalizedAddr
		if !rememberToken || *token == "" {
			if _, err := tx.Exec(`DELETE FROM web_secrets WHERE scope = ? AND name = 'token'`, secretScope); err != nil {
				return err
			}
		} else {
			enc, val := s.encodeWebSecret(*token)
			if _, err := tx.Exec(`
				INSERT INTO web_secrets(scope, name, encoding, value, updated_at) VALUES (?, 'token', ?, ?, ?)
				ON CONFLICT(scope, name) DO UPDATE SET encoding = excluded.encoding, value = excluded.value, updated_at = excluded.updated_at
			`, secretScope, enc, val, now); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (s *Store) DeleteWebConnection(mode, address string) error {
	if mode != "local" && mode != "remote" {
		return errors.New("invalid connection mode")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM web_connections WHERE mode = ? AND address = ?`, mode, address); err != nil {
		return err
	}
	if mode == "remote" {
		if _, err := tx.Exec(`DELETE FROM web_secrets WHERE scope = ?`, "remote:"+address); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) WebRemoteToken(address string) (string, error) {
	normalized, err := NormalizeRemoteURL(address)
	if err != nil {
		return "", err
	}
	token, _, err := s.readWebSecret("remote:"+normalized, "token")
	return token, err
}

func (s *Store) WebLastSession(mode, address string) (string, error) {
	var session string
	err := s.db.QueryRow(`SELECT coalesce(session, '') FROM web_connections WHERE mode = ? AND address = ?`, mode, address).Scan(&session)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return session, err
}

func (s *Store) RememberWebSession(mode, address, sessionID string) error {
	_, err := s.db.Exec(`UPDATE web_connections SET session = ? WHERE mode = ? AND address = ?`, sessionID, mode, address)
	return err
}
