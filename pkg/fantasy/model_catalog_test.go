package fantasy

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/config"
)

func TestRefreshModelCatalogWritesCacheWithoutSecrets(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/models" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		gotAuth = r.Header.Get("Authorization")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]string{{"id": "gpt-5.5"}, {"id": "gpt-5.4-mini"}, {"id": "gpt-5.5"}},
		})
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.Providers.OpenAI.APIKey = "secret-key"
	cfg.Providers.OpenAI.APIBase = server.URL
	cachePath := filepath.Join(t.TempDir(), "models.json")

	catalog, err := RefreshModelCatalog(context.Background(), cfg, ModelCatalogRefreshOptions{
		Provider:  "openai",
		Force:     true,
		CachePath: cachePath,
	})
	if err != nil {
		t.Fatalf("RefreshModelCatalog() error: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Fatalf("Authorization = %q, want bearer token", gotAuth)
	}
	if len(catalog.Providers["openai"].Models) != 2 {
		t.Fatalf("expected deduplicated models, got %#v", catalog.Providers["openai"].Models)
	}

	data, err := os.ReadFile(cachePath)
	if err != nil {
		t.Fatalf("ReadFile() error: %v", err)
	}
	if strings.Contains(string(data), "secret-key") {
		t.Fatal("model catalog persisted API key")
	}
}

func TestRefreshModelCatalogUsesFreshCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "models.json")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("fresh cache should avoid network refresh")
	}))
	defer server.Close()

	if err := saveModelCatalog(cachePath, &ModelCatalog{
		FetchedAt:  time.Now().UTC(),
		TTLSeconds: 3600,
		Providers: map[string]ModelCatalogProvider{
			"openai": {Name: "openai", APIBase: server.URL, Models: []ModelCatalogModel{{ID: "cached-model"}}},
		},
	}); err != nil {
		t.Fatalf("saveModelCatalog() error: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.OpenAI.APIKey = "secret-key"
	cfg.Providers.OpenAI.APIBase = server.URL
	catalog, err := RefreshModelCatalog(context.Background(), cfg, ModelCatalogRefreshOptions{Provider: "openai", CachePath: cachePath})
	if err != nil {
		t.Fatalf("RefreshModelCatalog() error: %v", err)
	}
	if got := catalog.Providers["openai"].Models[0].ID; got != "cached-model" {
		t.Fatalf("cached model = %q", got)
	}
}

func TestLoadFreshModelCatalogRejectsStaleCache(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "models.json")
	if err := saveModelCatalog(cachePath, &ModelCatalog{
		FetchedAt:  time.Now().Add(-2 * time.Hour),
		TTLSeconds: int64(time.Hour.Seconds()),
		Providers:  map[string]ModelCatalogProvider{"openai": {Name: "openai"}},
	}); err != nil {
		t.Fatalf("saveModelCatalog() error: %v", err)
	}
	if _, err := LoadFreshModelCatalog(cachePath, time.Hour); err == nil {
		t.Fatal("expected stale cache error")
	}
}

func TestProviderIsFreshReportsProviderFreshness(t *testing.T) {
	now := time.Now().UTC()
	catalog := &ModelCatalog{
		FetchedAt:  now,
		TTLSeconds: int64(time.Hour.Seconds()),
		Providers: map[string]ModelCatalogProvider{
			"fresh": {Name: "fresh", FetchedAt: now, TTLSeconds: int64(time.Hour.Seconds())},
			"stale": {Name: "stale", FetchedAt: now.Add(-2 * time.Hour), TTLSeconds: int64(time.Hour.Seconds())},
		},
	}

	if !catalog.ProviderIsFresh("fresh", time.Hour) {
		t.Fatal("expected fresh provider")
	}
	if catalog.ProviderIsFresh("stale", time.Hour) {
		t.Fatal("expected stale provider")
	}
}

func TestResolveProviderUsesFreshModelCatalogFallback(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cachePath, err := ModelCatalogPath()
	if err != nil {
		t.Fatalf("ModelCatalogPath() error: %v", err)
	}
	if err := saveModelCatalog(cachePath, &ModelCatalog{
		FetchedAt:  time.Now().UTC(),
		TTLSeconds: int64(defaultModelCatalogTTL.Seconds()),
		Providers: map[string]ModelCatalogProvider{
			"opencode-go": {Name: "opencode-go", Models: []ModelCatalogModel{{ID: "minimax-m2.7"}}},
		},
	}); err != nil {
		t.Fatalf("saveModelCatalog() error: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.OpenCode.APIKey = "opencode-key"
	key, base, _ := resolveProvider(cfg, "", "minimax-m2.7")
	if key != "opencode-key" {
		t.Fatalf("key = %q", key)
	}
	if base != "https://opencode.ai/zen/go/v1" {
		t.Fatalf("base = %q", base)
	}
}

func TestResolveProviderDoesNotUsePublicCatalogWithoutCredentials(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cachePath, err := ModelCatalogPath()
	if err != nil {
		t.Fatalf("ModelCatalogPath() error: %v", err)
	}
	if err := saveModelCatalog(cachePath, &ModelCatalog{
		FetchedAt:  time.Now().UTC(),
		TTLSeconds: int64(defaultModelCatalogTTL.Seconds()),
		Providers: map[string]ModelCatalogProvider{
			"opencode-go": {Name: "opencode-go", FetchedAt: time.Now().UTC(), Models: []ModelCatalogModel{{ID: "public-model"}}},
		},
	}); err != nil {
		t.Fatalf("saveModelCatalog() error: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.OpenRouter.APIKey = "openrouter-key"
	key, base, _ := resolveProvider(cfg, "", "public-model")
	if key != "openrouter-key" {
		t.Fatalf("key = %q", key)
	}
	if base != "https://openrouter.ai/api/v1" {
		t.Fatalf("base = %q", base)
	}
}

func TestResolveProviderTriesNextCachedProviderWhenFirstIsUnconfigured(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	cachePath, err := ModelCatalogPath()
	if err != nil {
		t.Fatalf("ModelCatalogPath() error: %v", err)
	}
	now := time.Now().UTC()
	if err := saveModelCatalog(cachePath, &ModelCatalog{
		FetchedAt:  now,
		TTLSeconds: int64(defaultModelCatalogTTL.Seconds()),
		Providers: map[string]ModelCatalogProvider{
			"openai":       {Name: "openai", FetchedAt: now, Models: []ModelCatalogModel{{ID: "shared-new-model"}}},
			"opencode-zen": {Name: "opencode-zen", FetchedAt: now, Models: []ModelCatalogModel{{ID: "shared-new-model"}}},
		},
	}); err != nil {
		t.Fatalf("saveModelCatalog() error: %v", err)
	}

	cfg := config.DefaultConfig()
	cfg.Providers.OpenCode.APIKey = "opencode-key"
	key, base, _ := resolveProvider(cfg, "", "shared-new-model")
	if key != "opencode-key" {
		t.Fatalf("key = %q", key)
	}
	if base != "https://opencode.ai/zen/v1" {
		t.Fatalf("base = %q", base)
	}
}

func TestPartialRefreshDoesNotFreshenUnrelatedProviders(t *testing.T) {
	cachePath := filepath.Join(t.TempDir(), "models.json")
	old := time.Now().Add(-2 * time.Hour).UTC()
	if err := saveModelCatalog(cachePath, &ModelCatalog{
		FetchedAt:  old,
		TTLSeconds: int64(time.Hour.Seconds()),
		Providers: map[string]ModelCatalogProvider{
			"openrouter": {Name: "openrouter", FetchedAt: old, TTLSeconds: int64(time.Hour.Seconds()), Models: []ModelCatalogModel{{ID: "stale-model"}}},
		},
	}); err != nil {
		t.Fatalf("saveModelCatalog() error: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{{"id": "fresh-model"}}})
	}))
	defer server.Close()

	cfg := config.DefaultConfig()
	cfg.Providers.OpenAI.APIKey = "secret-key"
	cfg.Providers.OpenAI.APIBase = server.URL
	catalog, err := RefreshModelCatalog(context.Background(), cfg, ModelCatalogRefreshOptions{Provider: "openai", Force: true, CachePath: cachePath, TTL: time.Hour})
	if err != nil {
		t.Fatalf("RefreshModelCatalog() error: %v", err)
	}
	if !catalog.ProviderHasModel("openai", "fresh-model", time.Hour) {
		t.Fatal("expected fresh OpenAI model")
	}
	if catalog.ProviderHasModel("openrouter", "stale-model", time.Hour) {
		t.Fatal("stale OpenRouter provider was incorrectly freshened")
	}
}

func TestBroadRefreshRequiresEveryRefreshableProviderFresh(t *testing.T) {
	now := time.Now().UTC()
	catalog := &ModelCatalog{
		FetchedAt:  now,
		TTLSeconds: int64(time.Hour.Seconds()),
		Providers: map[string]ModelCatalogProvider{
			"openrouter": {Name: "openrouter", APIBase: "https://openrouter.ai/api/v1", FetchedAt: now.Add(-2 * time.Hour), TTLSeconds: int64(time.Hour.Seconds())},
		},
	}
	cfg := config.DefaultConfig()
	cfg.Providers.OpenRouter.APIKey = "openrouter-key"
	entries := []*providerEntry{findProviderByName("openrouter")}

	if catalogHasFreshRefreshableProviders(catalog, cfg, entries, "", time.Hour) {
		t.Fatal("expected stale provider to force broad refresh")
	}
	catalog.Providers["openrouter"] = ModelCatalogProvider{Name: "openrouter", APIBase: "https://openrouter.ai/api/v1", FetchedAt: now, TTLSeconds: int64(time.Hour.Seconds())}
	if !catalogHasFreshRefreshableProviders(catalog, cfg, entries, "", time.Hour) {
		t.Fatal("expected fresh provider to allow cache reuse")
	}
}

func TestCatalogReuseRequiresMatchingAPIBase(t *testing.T) {
	now := time.Now().UTC()
	catalog := &ModelCatalog{
		FetchedAt:  now,
		TTLSeconds: int64(time.Hour.Seconds()),
		Providers: map[string]ModelCatalogProvider{
			"openai": {Name: "openai", APIBase: "https://old.example/v1", FetchedAt: now, TTLSeconds: int64(time.Hour.Seconds())},
		},
	}
	cfg := config.DefaultConfig()
	cfg.Providers.OpenAI.APIKey = "openai-key"
	cfg.Providers.OpenAI.APIBase = "https://new.example/v1"
	entries := []*providerEntry{findProviderByName("openai")}

	if catalogHasFreshRefreshableProviders(catalog, cfg, entries, "", time.Hour) {
		t.Fatal("expected API base change to force refresh")
	}
}

func TestOpenCodeCatalogBaseOverrideOnlyAppliesToScopedProvider(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Providers.OpenCode.APIBase = "https://custom.example/v1"
	goEntry := findProviderByName("opencode-go")
	zenEntry := findProviderByName("opencode-zen")

	if got := goEntry.modelCatalogBase(cfg, "opencode-go"); got != "https://custom.example/v1" {
		t.Fatalf("scoped go base = %q", got)
	}
	if got := zenEntry.modelCatalogBase(cfg, "opencode-go"); got != "https://opencode.ai/zen/v1" {
		t.Fatalf("unscoped zen base = %q", got)
	}
}
