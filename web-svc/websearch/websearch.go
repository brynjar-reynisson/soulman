// Package websearch is a small client for the Brave Search API
// (https://api.search.brave.com), the backing search provider for
// soulman's Web-Search dashboard page. There is exactly one caller
// (web-svc/httpserver's search handler), so this is plain functions on a
// struct rather than an interface — same shape as thinking-svc/llm's
// DeepSeekClient, whose baseURL-injection approach to testability this
// package's tests mirror.
package websearch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// DefaultBaseURL is the production Brave Search API host. Search appends
// the versioned web-search path to whatever baseURL NewClient is given —
// tests pass an httptest.Server URL instead.
const DefaultBaseURL = "https://api.search.brave.com"

// maxResults caps how many results Search returns, enforced on the
// parsed response regardless of what Brave sends back — correctness here
// matters more than trusting an upstream API to honor an implicit limit.
const maxResults = 10

// ErrNoAPIKey is returned by Search when the client was constructed with
// an empty API key. The httpserver handler maps this to a 503, distinct
// from a Brave-side failure (502).
var ErrNoAPIKey = errors.New("brave search api key not configured")

// Result is one Brave Search result, trimmed to what the dashboard shows.
type Result struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// Client calls the Brave Search API's web search endpoint.
type Client struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
}

// NewClient constructs a Client. apiKey may be empty — Search then
// returns ErrNoAPIKey without making any HTTP call, matching the
// non-fatal-if-blank posture web-svc/config uses for BRAVE_SEARCH_API_KEY.
func NewClient(apiKey, baseURL string) *Client {
	return &Client{
		apiKey:     apiKey,
		baseURL:    baseURL,
		httpClient: &http.Client{},
	}
}

type braveSearchResponse struct {
	Web struct {
		Results []struct {
			Title       string `json:"title"`
			URL         string `json:"url"`
			Description string `json:"description"`
		} `json:"results"`
	} `json:"web"`
}

var highlightTagReplacer = strings.NewReplacer("<strong>", "", "</strong>", "")

// Search queries the Brave Search API and returns up to maxResults web
// results. ctx carries the caller's deadline — this package sets no
// timeout of its own beyond what ctx provides, matching how the rest of
// web-svc's outbound HTTP calls (see httpserver.Server.isHealthy) derive
// their timeout from a context the handler builds.
func (c *Client) Search(ctx context.Context, query string) ([]Result, error) {
	if c.apiKey == "" {
		return nil, ErrNoAPIKey
	}

	reqURL := c.baseURL + "/res/v1/web/search?q=" + url.QueryEscape(query)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("websearch: build request: %w", err)
	}
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("X-Subscription-Token", c.apiKey)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("websearch: request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("websearch: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("websearch: brave search status %d", resp.StatusCode)
	}

	var parsed braveSearchResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return nil, fmt.Errorf("websearch: unmarshal response: %w", err)
	}

	results := make([]Result, 0, len(parsed.Web.Results))
	for _, r := range parsed.Web.Results {
		if len(results) >= maxResults {
			break
		}
		results = append(results, Result{
			Title:   r.Title,
			URL:     r.URL,
			Snippet: highlightTagReplacer.Replace(r.Description),
		})
	}

	return results, nil
}
