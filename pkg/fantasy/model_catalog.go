package fantasy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
)

const (
	modelCatalogFilename   = "provider-models.json"
	defaultModelCatalogTTL = 24 * time.Hour
)

// ModelCatalog is the disk-backed cache of provider model lists.
type ModelCatalog struct {
	FetchedAt  time.Time                       `json:"fetched_at"`
	TTLSeconds int64                           `json:"ttl_seconds"`
	Providers  map[string]ModelCatalogProvider `json:"providers"`
}

// ModelCatalogProvider stores the normalized model IDs for one provider.
type ModelCatalogProvider struct {
	Name       string              `json:"name"`
	APIBase    string              `json:"api_base"`
	FetchedAt  time.Time           `json:"fetched_at"`
	TTLSeconds int64               `json:"ttl_seconds"`
	Models     []ModelCatalogModel `json:"models"`
}

// ModelCatalogModel is the minimal OpenAI-compatible model-list shape we use.
type ModelCatalogModel struct {
	ID string `json:"id"`
}

// ModelCatalogRefreshOptions configures explicit catalog refreshes.
type ModelCatalogRefreshOptions struct {
	Provider   string
	Force      bool
	TTL        time.Duration
	CachePath  string
	HTTPClient *http.Client
}

type openAIModelListResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// ModelCatalogPath returns the canonical provider model-list cache path.
func ModelCatalogPath() (string, error) {
	cacheDir, err := config.CacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(cacheDir, modelCatalogFilename), nil
}

// LoadModelCatalog loads the provider model-list cache from disk.
func LoadModelCatalog(cachePath string) (*ModelCatalog, error) {
	if strings.TrimSpace(cachePath) == "" {
		var err error
		cachePath, err = ModelCatalogPath()
		if err != nil {
			return nil, err
		}
	}
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return nil, err
	}
	var catalog ModelCatalog
	if err := json.Unmarshal(data, &catalog); err != nil {
		return nil, fmt.Errorf("decode model catalog: %w", err)
	}
	if catalog.Providers == nil {
		catalog.Providers = map[string]ModelCatalogProvider{}
	}
	return &catalog, nil
}

// LoadFreshModelCatalog loads the catalog only when it is still inside its TTL.
func LoadFreshModelCatalog(cachePath string, ttl time.Duration) (*ModelCatalog, error) {
	catalog, err := LoadModelCatalog(cachePath)
	if err != nil {
		return nil, err
	}
	if !catalog.IsFresh(ttl) {
		return nil, fmt.Errorf("model catalog is stale")
	}
	return catalog, nil
}

// IsFresh reports whether the catalog is fresh enough to use on the hot path.
func (c *ModelCatalog) IsFresh(ttl time.Duration) bool {
	if c == nil || c.FetchedAt.IsZero() {
		return false
	}
	if ttl <= 0 {
		if c.TTLSeconds > 0 {
			ttl = time.Duration(c.TTLSeconds) * time.Second
		} else {
			ttl = defaultModelCatalogTTL
		}
	}
	return time.Since(c.FetchedAt) <= ttl
}

// ProviderForModel returns the first cached provider that lists modelID exactly.
func (c *ModelCatalog) ProviderForModel(modelID string) string {
	providers := c.ProvidersForModel(modelID, defaultModelCatalogTTL)
	if len(providers) == 0 {
		return ""
	}
	return providers[0]
}

// ProvidersForModel returns fresh cached providers that list modelID exactly.
func (c *ModelCatalog) ProvidersForModel(modelID string, ttl time.Duration) []string {
	if c == nil {
		return nil
	}
	modelID = strings.TrimSpace(modelID)
	if modelID == "" {
		return nil
	}
	names := make([]string, 0, len(c.Providers))
	for name := range c.Providers {
		names = append(names, name)
	}
	sort.Strings(names)
	providers := make([]string, 0, len(names))
	for _, name := range names {
		if c.ProviderHasModel(name, modelID, ttl) {
			providers = append(providers, name)
		}
	}
	return providers
}

func (c *ModelCatalog) ProviderHasModel(providerName, modelID string, ttl time.Duration) bool {
	if c == nil || !c.providerIsFresh(providerName, ttl) {
		return false
	}
	provider := c.Providers[providerName]
	for _, model := range provider.Models {
		if model.ID == modelID {
			return true
		}
	}
	return false
}

// ProviderIsFresh reports whether one cached provider entry is inside its TTL.
func (c *ModelCatalog) ProviderIsFresh(providerName string, ttl time.Duration) bool {
	return c.providerIsFresh(providerName, ttl)
}

func (c *ModelCatalog) providerIsFresh(providerName string, ttl time.Duration) bool {
	provider, ok := c.Providers[providerName]
	if !ok {
		return false
	}
	fetchedAt := provider.FetchedAt
	if fetchedAt.IsZero() {
		fetchedAt = c.FetchedAt
	}
	if fetchedAt.IsZero() {
		return false
	}
	if ttl <= 0 {
		if provider.TTLSeconds > 0 {
			ttl = time.Duration(provider.TTLSeconds) * time.Second
		} else if c.TTLSeconds > 0 {
			ttl = time.Duration(c.TTLSeconds) * time.Second
		} else {
			ttl = defaultModelCatalogTTL
		}
	}
	return time.Since(fetchedAt) <= ttl
}

// RefreshModelCatalog refreshes configured OpenAI-compatible model lists and
// persists them to disk. It performs network I/O only when explicitly called.
func RefreshModelCatalog(ctx context.Context, cfg *config.Config, opts ModelCatalogRefreshOptions) (*ModelCatalog, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is required")
	}
	if opts.TTL <= 0 {
		opts.TTL = defaultModelCatalogTTL
	}
	if strings.TrimSpace(opts.CachePath) == "" {
		cachePath, err := ModelCatalogPath()
		if err != nil {
			return nil, err
		}
		opts.CachePath = cachePath
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = &http.Client{Timeout: 30 * time.Second}
	}

	entries, err := modelCatalogRefreshEntries(opts.Provider)
	if err != nil {
		return nil, err
	}

	if !opts.Force {
		if catalog, err := LoadFreshModelCatalog(opts.CachePath, opts.TTL); err == nil {
			if catalogHasFreshRefreshableProviders(catalog, cfg, entries, opts.Provider, opts.TTL) {
				return catalog, nil
			}
		}
	}

	catalog := &ModelCatalog{
		FetchedAt:  time.Now().UTC(),
		TTLSeconds: int64(opts.TTL.Seconds()),
		Providers:  map[string]ModelCatalogProvider{},
	}
	if existing, err := LoadModelCatalog(opts.CachePath); err == nil {
		catalog.Providers = existing.Providers
	}

	refreshed := 0
	var errs []string
	for _, entry := range entries {
		if !entry.modelCatalog {
			continue
		}
		providerName := entry.canonicalName()
		base := entry.modelCatalogBase(cfg, opts.Provider)
		key := entry.apiKey(cfg)
		if base == "" || (!entry.modelCatalogPublic && key == "" && providerName != "vllm") {
			if opts.Provider != "" {
				errs = append(errs, fmt.Sprintf("%s is not configured", providerName))
			}
			continue
		}
		provider, err := fetchModelCatalogProvider(ctx, opts.HTTPClient, providerName, base, key, opts.TTL)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", providerName, err))
			continue
		}
		catalog.Providers[providerName] = provider
		refreshed++
	}
	if refreshed == 0 {
		if len(errs) > 0 {
			return nil, fmt.Errorf("refresh model catalog: %s", strings.Join(errs, "; "))
		}
		return nil, fmt.Errorf("refresh model catalog: no configured providers")
	}

	if err := saveModelCatalog(opts.CachePath, catalog); err != nil {
		return nil, err
	}
	return catalog, nil
}

func saveModelCatalog(cachePath string, catalog *ModelCatalog) error {
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		return fmt.Errorf("create model catalog cache dir: %w", err)
	}
	data, err := json.MarshalIndent(catalog, "", "  ")
	if err != nil {
		return fmt.Errorf("encode model catalog: %w", err)
	}
	return os.WriteFile(cachePath, data, 0o600)
}

func fetchModelCatalogProvider(ctx context.Context, client *http.Client, providerName, apiBase, apiKey string, ttl time.Duration) (ModelCatalogProvider, error) {
	endpoint := strings.TrimRight(apiBase, "/") + "/models"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return ModelCatalogProvider{}, err
	}
	if apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	}
	res, err := client.Do(req)
	if err != nil {
		return ModelCatalogProvider{}, err
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return ModelCatalogProvider{}, fmt.Errorf("GET /models returned %s", res.Status)
	}
	var parsed openAIModelListResponse
	if err := json.NewDecoder(res.Body).Decode(&parsed); err != nil {
		return ModelCatalogProvider{}, fmt.Errorf("decode /models response: %w", err)
	}
	models := make([]ModelCatalogModel, 0, len(parsed.Data))
	seen := map[string]bool{}
	for _, item := range parsed.Data {
		id := strings.TrimSpace(item.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		models = append(models, ModelCatalogModel{ID: id})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return ModelCatalogProvider{
		Name:       providerName,
		APIBase:    apiBase,
		FetchedAt:  time.Now().UTC(),
		TTLSeconds: int64(ttl.Seconds()),
		Models:     models,
	}, nil
}

func modelCatalogRefreshEntries(provider string) ([]*providerEntry, error) {
	provider = canonicalProviderName(provider)
	if provider == "" || provider == "all" {
		entries := make([]*providerEntry, 0, len(providerRegistry))
		for i := range providerRegistry {
			if providerRegistry[i].modelCatalog {
				entries = append(entries, &providerRegistry[i])
			}
		}
		return entries, nil
	}
	entry := findProviderByName(provider)
	if entry == nil {
		return nil, fmt.Errorf("unknown provider %q", provider)
	}
	return []*providerEntry{entry}, nil
}

func catalogHasFreshRefreshableProviders(catalog *ModelCatalog, cfg *config.Config, entries []*providerEntry, requestedProvider string, ttl time.Duration) bool {
	refreshable := 0
	for _, entry := range entries {
		if entry == nil || !entry.modelCatalog {
			continue
		}
		providerName := entry.canonicalName()
		base := entry.modelCatalogBase(cfg, requestedProvider)
		key := entry.apiKey(cfg)
		if base == "" || (!entry.modelCatalogPublic && key == "" && providerName != "vllm") {
			continue
		}
		refreshable++
		if strings.TrimRight(catalog.Providers[providerName].APIBase, "/") != strings.TrimRight(base, "/") {
			return false
		}
		if !catalog.providerIsFresh(providerName, ttl) {
			return false
		}
	}
	return refreshable > 0
}

func canonicalProviderName(provider string) string {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" {
		return ""
	}
	if entry := findProviderByName(provider); entry != nil {
		return entry.canonicalName()
	}
	return provider
}

func (e *providerEntry) canonicalName() string {
	if e == nil || len(e.names) == 0 {
		return ""
	}
	return e.names[0]
}

func (e *providerEntry) modelCatalogBase(cfg *config.Config, requestedProvider string) string {
	if e == nil {
		return ""
	}
	// OpenCode Go and Zen share one config field today. For broad refreshes,
	// use each tier's default endpoint so a custom base intended for one tier
	// does not poison the other tier's cached model list. Explicit scoped
	// refreshes still honor the configured override.
	if strings.HasPrefix(e.canonicalName(), "opencode-") && cfg != nil && strings.TrimSpace(cfg.Providers.OpenCode.APIBase) != "" {
		requested := canonicalProviderName(requestedProvider)
		if requested != e.canonicalName() {
			return e.defaultBase
		}
	}
	return e.resolveBase(cfg)
}
