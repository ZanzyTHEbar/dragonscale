package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/ZanzyTHEbar/dragonscale/pkg/security"
	jsonv2 "github.com/go-json-experiment/json"
	"github.com/go-json-experiment/json/jsontext"
)

const (
	userAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"
)

var htmlTitleRE = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)

type SearchProvider interface {
	Search(ctx context.Context, query string, count int) (string, error)
}

type BraveSearchProvider struct {
	apiKey string
}

func (p *BraveSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	searchURL := fmt.Sprintf("https://api.search.brave.com/res/v1/web/search?q=%s&count=%d",
		url.QueryEscape(query), count)

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Subscription-Token", p.apiKey)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var searchResp struct {
		Web struct {
			Results []struct {
				Title       string `json:"title"`
				URL         string `json:"url"`
				Description string `json:"description"`
			} `json:"results"`
		} `json:"web"`
	}

	if err := jsonv2.Unmarshal(body, &searchResp); err != nil {
		// Log error body for debugging
		fmt.Printf("Brave API Error Body: %s\n", string(body))
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	results := searchResp.Web.Results
	if len(results) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s", query))
	for i, item := range results {
		if i >= count {
			break
		}
		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, item.Title, item.URL))
		if item.Description != "" {
			lines = append(lines, fmt.Sprintf("   %s", item.Description))
		}
	}

	return strings.Join(lines, "\n"), nil
}

type DuckDuckGoSearchProvider struct{}

func (p *DuckDuckGoSearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	searchURL := fmt.Sprintf("https://html.duckduckgo.com/html/?q=%s", url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	return p.extractResults(string(body), count, query)
}

func (p *DuckDuckGoSearchProvider) extractResults(html string, count int, query string) (string, error) {
	// Simple regex based extraction for DDG HTML
	// Strategy: Find all result containers or key anchors directly

	// Try finding the result links directly first, as they are the most critical
	// Pattern: <a class="result__a" href="...">Title</a>
	// The previous regex was a bit strict. Let's make it more flexible for attributes order/content
	reLink := regexp.MustCompile(`<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>([\s\S]*?)</a>`)
	matches := reLink.FindAllStringSubmatch(html, count+5)

	if len(matches) == 0 {
		return fmt.Sprintf("No results found or extraction failed. Query: %s", query), nil
	}

	var lines []string
	lines = append(lines, fmt.Sprintf("Results for: %s (via DuckDuckGo)", query))

	// Pre-compile snippet regex to run inside the loop
	// We'll search for snippets relative to the link position or just globally if needed
	// But simple global search for snippets might mismatch order.
	// Since we only have the raw HTML string, let's just extract snippets globally and assume order matches (risky but simple for regex)
	// Or better: Let's assume the snippet follows the link in the HTML

	// A better regex approach: iterate through text and find matches in order
	// But for now, let's grab all snippets too
	reSnippet := regexp.MustCompile(`<a class="result__snippet[^"]*".*?>([\s\S]*?)</a>`)
	snippetMatches := reSnippet.FindAllStringSubmatch(html, count+5)

	maxItems := min(len(matches), count)

	for i := 0; i < maxItems; i++ {
		urlStr := matches[i][1]
		title := stripTags(matches[i][2])
		title = strings.TrimSpace(title)

		// URL decoding if needed
		if strings.Contains(urlStr, "uddg=") {
			if u, err := url.QueryUnescape(urlStr); err == nil {
				idx := strings.Index(u, "uddg=")
				if idx != -1 {
					urlStr = u[idx+5:]
				}
			}
		}

		lines = append(lines, fmt.Sprintf("%d. %s\n   %s", i+1, title, urlStr))

		// Attempt to attach snippet if available and index aligns
		if i < len(snippetMatches) {
			snippet := stripTags(snippetMatches[i][1])
			snippet = strings.TrimSpace(snippet)
			if snippet != "" {
				lines = append(lines, fmt.Sprintf("   %s", snippet))
			}
		}
	}

	return strings.Join(lines, "\n"), nil
}

func stripTags(content string) string {
	re := regexp.MustCompile(`<[^>]+>`)
	return re.ReplaceAllString(content, "")
}

type PerplexitySearchProvider struct {
	apiKey string
}

func (p *PerplexitySearchProvider) Search(ctx context.Context, query string, count int) (string, error) {
	searchURL := "https://api.perplexity.ai/chat/completions"

	payload := map[string]interface{}{
		"model": "sonar",
		"messages": []map[string]string{
			{"role": "system", "content": "You are a search assistant. Provide concise search results with titles, URLs, and brief descriptions in the following format:\n1. Title\n   URL\n   Description\n\nDo not add extra commentary."},
			{"role": "user", "content": fmt.Sprintf("Search for: %s. Provide up to %d relevant results.", query, count)},
		},
		"max_tokens": 1000,
	}

	payloadBytes, err := jsonv2.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", searchURL, strings.NewReader(string(payloadBytes)))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("User-Agent", userAgent)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("Perplexity API error: %s", string(body))
	}

	var searchResp struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := jsonv2.Unmarshal(body, &searchResp); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}

	if len(searchResp.Choices) == 0 {
		return fmt.Sprintf("No results for: %s", query), nil
	}

	return fmt.Sprintf("Results for: %s (via Perplexity)\n%s", query, searchResp.Choices[0].Message.Content), nil
}

type WebSearchTool struct {
	provider   SearchProvider
	maxResults int
}

type WebSearchToolOptions struct {
	BraveAPIKey          string
	BraveMaxResults      int
	BraveEnabled         bool
	DuckDuckGoMaxResults int
	DuckDuckGoEnabled    bool
	PerplexityAPIKey     string
	PerplexityMaxResults int
	PerplexityEnabled    bool
}

func NewWebSearchTool(opts WebSearchToolOptions) *WebSearchTool {
	var provider SearchProvider
	maxResults := 5

	// Priority: Perplexity > Brave > DuckDuckGo
	if opts.PerplexityEnabled && opts.PerplexityAPIKey != "" {
		provider = &PerplexitySearchProvider{apiKey: opts.PerplexityAPIKey}
		if opts.PerplexityMaxResults > 0 {
			maxResults = opts.PerplexityMaxResults
		}
	} else if opts.BraveEnabled && opts.BraveAPIKey != "" {
		provider = &BraveSearchProvider{apiKey: opts.BraveAPIKey}
		if opts.BraveMaxResults > 0 {
			maxResults = opts.BraveMaxResults
		}
	} else if opts.DuckDuckGoEnabled {
		provider = &DuckDuckGoSearchProvider{}
		if opts.DuckDuckGoMaxResults > 0 {
			maxResults = opts.DuckDuckGoMaxResults
		}
	} else {
		return nil
	}

	return &WebSearchTool{
		provider:   provider,
		maxResults: maxResults,
	}
}

func (t *WebSearchTool) Name() string {
	return "web_search"
}

func (t *WebSearchTool) Description() string {
	return "Search the web for current information. Returns titles, URLs, and snippets from search results."
}

func (t *WebSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "Search query",
			},
			"count": map[string]interface{}{
				"type":        "integer",
				"description": "Number of results (1-10)",
				"minimum":     1.0,
				"maximum":     10.0,
			},
		},
		"required": []string{"query"},
	}
}

// Capabilities declares that WebSearchTool requires network access.
// The Brave Search API key is injected as a header by the SecureBus.
func (t *WebSearchTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		Secrets: []SecretRef{
			{Name: "brave_api_key", InjectAs: "header:X-Subscription-Token", Required: false},
		},
		Network: []EndpointRule{
			{Pattern: "https://api.search.brave.com/**"},
		},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	query, ok := args["query"].(string)
	if !ok {
		return ErrorResult("query is required")
	}

	count := t.maxResults
	if c, ok := args["count"].(float64); ok {
		if int(c) > 0 && int(c) <= 10 {
			count = int(c)
		}
	}

	result, err := t.provider.Search(ctx, query, count)
	if err != nil {
		return ErrorResult(fmt.Sprintf("search failed: %v", err))
	}

	return &ToolResult{
		ForLLM:  result,
		ForUser: result,
	}
}

type WebFetchTool struct {
	maxChars     int
	client       *http.Client
	validateURL  func(string) error
	lookupIPAddr func(context.Context, string) ([]net.IPAddr, error)
	dialContext  func(context.Context, string, string) (net.Conn, error)
}

func NewWebFetchTool(maxChars int) *WebFetchTool {
	if maxChars <= 0 {
		maxChars = 50000
	}
	return &WebFetchTool{
		maxChars: maxChars,
	}
}

func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

func (t *WebFetchTool) Description() string {
	return "Fetch a URL and extract readable content (HTML to text). Use this to get weather info, news, articles, or any web content."
}

func (t *WebFetchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"url": map[string]interface{}{
				"type":        "string",
				"description": "URL to fetch",
			},
			"maxChars": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum characters to extract",
				"minimum":     100.0,
			},
		},
		"required": []string{"url"},
	}
}

// Capabilities declares that WebFetchTool requires broad network access.
// SecureBus capability enforcement can apply SSRF validation before requests
// are made; Execute also validates the initial URL and every redirect target.
func (t *WebFetchTool) Capabilities() ToolCapabilities {
	return ToolCapabilities{
		Network: []EndpointRule{
			{Pattern: "https://**"},
			{Pattern: "http://**"},
		},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	urlStr, ok := args["url"].(string)
	if !ok {
		return ErrorResult("url is required")
	}

	parsedURL, err := url.Parse(urlStr)
	if err != nil {
		return ErrorResult(fmt.Sprintf("invalid URL: %v", err))
	}

	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return ErrorResult("only http/https URLs are allowed")
	}

	if parsedURL.Host == "" {
		return ErrorResult("missing domain in URL")
	}
	if err := t.validateFetchURL(urlStr); err != nil {
		return ErrorResult(fmt.Sprintf("blocked URL: %v", err))
	}

	maxChars := t.maxChars
	if mc, ok := args["maxChars"].(float64); ok {
		if int(mc) > 100 {
			maxChars = int(mc)
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to create request: %v", err))
	}

	req.Header.Set("User-Agent", userAgent)

	client := t.fetchClient()

	resp, err := client.Do(req)
	if err != nil {
		return ErrorResult(fmt.Sprintf("request failed: %v", err))
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read response: %v", err))
	}

	contentType := resp.Header.Get("Content-Type")

	var text, extractor string

	isHTML := strings.Contains(contentType, "text/html") || len(body) > 0 &&
		(strings.HasPrefix(string(body), "<!DOCTYPE") || strings.HasPrefix(strings.ToLower(string(body)), "<html"))

	if strings.Contains(contentType, "application/json") {
		var jsonData interface{}
		if err := jsonv2.Unmarshal(body, &jsonData); err == nil {
			formatted, _ := jsonv2.Marshal(jsonData, jsontext.WithIndent("  "))
			text = string(formatted)
			extractor = "json"
		} else {
			text = string(body)
			extractor = "raw"
		}
	} else if isHTML {
		text = t.extractText(string(body))
		extractor = "text"
	} else {
		text = string(body)
		extractor = "raw"
	}

	truncated := len(text) > maxChars
	if truncated {
		text = text[:maxChars]
	}

	result := map[string]interface{}{
		"url":       urlStr,
		"status":    resp.StatusCode,
		"extractor": extractor,
		"truncated": truncated,
		"length":    len(text),
		"text":      text,
	}

	resultJSON, _ := jsonv2.Marshal(result, jsontext.WithIndent("  "))
	llmContent := formatWebFetchLLMContent(urlStr, resp.StatusCode, extractor, truncated, text, isHTML, string(body))

	return &ToolResult{
		ForLLM:  llmContent,
		ForUser: string(resultJSON),
	}
}

func (t *WebFetchTool) validateFetchURL(rawURL string) error {
	if t.validateURL != nil {
		return t.validateURL(rawURL)
	}
	return security.ValidateURL(rawURL)
}

func (t *WebFetchTool) fetchClient() *http.Client {
	var base *http.Client
	if t.client != nil {
		base = t.client
	} else {
		base = &http.Client{
			Timeout:   60 * time.Second,
			Transport: t.safeTransport(),
		}
	}

	client := *base
	originalCheckRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return fmt.Errorf("stopped after 5 redirects")
		}
		if err := t.validateFetchURL(req.URL.String()); err != nil {
			return fmt.Errorf("redirect blocked: %w", err)
		}
		if originalCheckRedirect != nil {
			return originalCheckRedirect(req, via)
		}
		return nil
	}
	return &client
}

func (t *WebFetchTool) safeTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConns = 10
	transport.IdleConnTimeout = 30 * time.Second
	transport.DisableCompression = false
	transport.TLSHandshakeTimeout = 15 * time.Second
	transport.Proxy = nil
	transport.DialContext = t.safeDialContext
	return transport
}

func (t *WebFetchTool) safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, fmt.Errorf("invalid dial address %q: %w", address, err)
	}

	if security.IsBlockedHost(host) {
		return nil, fmt.Errorf("%w: host %q is blocked", security.ErrBlockedURL, host)
	}

	ips, err := t.lookupHostIPAddrs(ctx, host)
	if err != nil {
		return nil, fmt.Errorf("%w: DNS resolution failed for %q: %v", security.ErrBlockedURL, host, err)
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("%w: DNS resolution returned no IPs for %q", security.ErrBlockedURL, host)
	}

	for _, ip := range ips {
		if security.IsBlockedIP(ip.IP) {
			return nil, fmt.Errorf("%w: resolved IP %s is in a blocked range", security.ErrBlockedURL, ip.IP.String())
		}
	}

	return t.dialValidatedAddress(ctx, network, net.JoinHostPort(ips[0].IP.String(), port))
}

func (t *WebFetchTool) lookupHostIPAddrs(ctx context.Context, host string) ([]net.IPAddr, error) {
	if t.lookupIPAddr != nil {
		return t.lookupIPAddr(ctx, host)
	}
	return net.DefaultResolver.LookupIPAddr(ctx, host)
}

func (t *WebFetchTool) dialValidatedAddress(ctx context.Context, network, address string) (net.Conn, error) {
	if t.dialContext != nil {
		return t.dialContext(ctx, network, address)
	}
	dialer := &net.Dialer{}
	return dialer.DialContext(ctx, network, address)
}

func formatWebFetchLLMContent(urlStr string, status int, extractor string, truncated bool, text string, isHTML bool, rawHTML string) string {
	sections := []string{fmt.Sprintf("Fetched %s (status %d, extractor: %s, truncated: %v)", urlStr, status, extractor, truncated)}

	if isHTML {
		if title := extractHTMLTitle(rawHTML); title != "" {
			sections = append(sections, "Title: "+title)
		}
	}

	if trimmed := strings.TrimSpace(text); trimmed != "" {
		sections = append(sections, "Content:\n"+trimmed)
	}

	return strings.Join(sections, "\n\n")
}

func extractHTMLTitle(htmlContent string) string {
	m := htmlTitleRE.FindStringSubmatch(htmlContent)
	if len(m) < 2 {
		return ""
	}
	title := stripTags(m[1])
	title = strings.Join(strings.Fields(title), " ")
	return strings.TrimSpace(title)
}

func (t *WebFetchTool) extractText(htmlContent string) string {
	re := regexp.MustCompile(`<script[\s\S]*?</script>`)
	result := re.ReplaceAllLiteralString(htmlContent, "")
	re = regexp.MustCompile(`<style[\s\S]*?</style>`)
	result = re.ReplaceAllLiteralString(result, "")
	re = regexp.MustCompile(`<[^>]+>`)
	result = re.ReplaceAllLiteralString(result, " ")

	result = strings.TrimSpace(result)

	re = regexp.MustCompile(`\s+`)
	result = re.ReplaceAllLiteralString(result, " ")

	lines := strings.Split(result, "\n")
	var cleanLines []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			cleanLines = append(cleanLines, line)
		}
	}

	return strings.Join(cleanLines, "\n")
}
