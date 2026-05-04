package researchadapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/badlogic/pigo/pkg/agentcore"
	"github.com/badlogic/pigo/pkg/ai"
	"golang.org/x/net/html"
)

const (
	maxSearchResults      = 30
	defaultSearchResults  = 10
	maxScrapeURLs         = 3
	maxScrapeBytesPerURL  = 250_000
	maxScrapeOutputPerURL = 40_000
	cisaKEVURL            = "https://www.cisa.gov/sites/default/files/feeds/known_exploited_vulnerabilities.json"
	osvBaseURL            = "https://api.osv.dev/v1"
	nvdBaseURL            = "https://services.nvd.nist.gov/rest/json/cves/2.0"
)

var cvePattern = regexp.MustCompile(`(?i)\bCVE-\d{4}-\d{4,}\b`)

func Tools(config Config) ([]agentcore.Tool, []ai.Tool) {
	config = config.Normalized()
	tools := []agentcore.Tool{}
	specs := []ai.Tool{}
	if config.ToolEnabled("research") {
		tools = append(tools, researchTool(config))
		specs = append(specs, researchSpec())
	}
	if config.ToolEnabled("search") {
		tools = append(tools, searchTool(config))
		specs = append(specs, searchSpec())
	}
	if config.ToolEnabled("scrape") {
		tools = append(tools, scrapeTool(config))
		specs = append(specs, scrapeSpec())
	}
	if config.ToolEnabled("security_search") {
		tools = append(tools, securityTool(config))
		specs = append(specs, securitySpec())
	}
	return tools, specs
}

func searchTool(config Config) agentcore.Tool {
	return agentcore.Tool{Name: "search", Execute: func(ctx context.Context, call ai.ContentBlock) agentcore.ToolResult {
		if strings.TrimSpace(config.SearXNGURL) == "" {
			return agentcore.ToolResult{Text: "research search requires PIGO_SEARXNG_URL or SEARXNG_URL", IsError: true}
		}
		queries := stringSliceArg(call.Arguments["queries"])
		if len(queries) == 0 {
			if query, _ := call.Arguments["query"].(string); strings.TrimSpace(query) != "" {
				queries = []string{query}
			}
		}
		if len(queries) == 0 {
			return agentcore.ToolResult{Text: "search requires query or queries", IsError: true}
		}
		maxResults := intArg(call.Arguments["maxResults"], defaultSearchResults)
		if maxResults <= 0 || maxResults > maxSearchResults {
			maxResults = defaultSearchResults
		}
		freshness, _ := call.Arguments["freshness"].(string)
		sourceType, _ := call.Arguments["sourceType"].(string)
		start := time.Now()
		results := make([]searchQueryResult, 0, len(queries))
		for _, query := range queries {
			result, err := runSearXNGSearch(ctx, config, query, maxResults, freshness, sourceType)
			if err != nil {
				results = append(results, searchQueryResult{Query: query, Error: err.Error()})
				continue
			}
			results = append(results, result)
		}
		text := formatSearchResults(results)
		allFailed := true
		for _, result := range results {
			if result.Error == "" {
				allFailed = false
				break
			}
		}
		return agentcore.ToolResult{Text: text, Details: map[string]any{
			"queries":    queries,
			"results":    results,
			"durationMs": time.Since(start).Milliseconds(),
			"searxngUrl": config.SearXNGURL,
			"freshness":  freshness,
			"sourceType": sourceType,
			"maxResults": maxResults,
		}, IsError: allFailed}
	}}
}

type searchQueryResult struct {
	Query string         `json:"query"`
	Items []searchResult `json:"items,omitempty"`
	Error string         `json:"error,omitempty"`
}

type searchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content,omitempty"`
	Engine  string `json:"engine,omitempty"`
}

func runSearXNGSearch(ctx context.Context, config Config, query string, maxResults int, freshness, sourceType string) (searchQueryResult, error) {
	endpoint, err := url.Parse(config.SearXNGURL + "/search")
	if err != nil {
		return searchQueryResult{}, err
	}
	values := endpoint.Query()
	values.Set("q", query)
	values.Set("format", "json")
	if freshness != "" && freshness != "any" {
		values.Set("time_range", freshness)
	}
	switch sourceType {
	case "news":
		values.Set("categories", "news")
	case "github":
		values.Set("q", query+" site:github.com")
	}
	endpoint.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return searchQueryResult{}, err
	}
	resp, err := config.client().Do(req)
	if err != nil {
		return searchQueryResult{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return searchQueryResult{}, fmt.Errorf("searxng returned %s", resp.Status)
	}
	var payload struct {
		Results []struct {
			Title   string `json:"title"`
			URL     string `json:"url"`
			Content string `json:"content"`
			Engine  string `json:"engine"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2_000_000)).Decode(&payload); err != nil {
		return searchQueryResult{}, err
	}
	items := make([]searchResult, 0, min(len(payload.Results), maxResults))
	for i, result := range payload.Results {
		if i >= maxResults {
			break
		}
		items = append(items, searchResult{Title: result.Title, URL: result.URL, Content: result.Content, Engine: result.Engine})
	}
	return searchQueryResult{Query: query, Items: items}, nil
}

func scrapeTool(config Config) agentcore.Tool {
	return agentcore.Tool{Name: "scrape", Execute: func(ctx context.Context, call ai.ContentBlock) agentcore.ToolResult {
		urls := stringSliceArg(call.Arguments["urls"])
		if len(urls) == 0 {
			if raw, _ := call.Arguments["url"].(string); strings.TrimSpace(raw) != "" {
				urls = []string{raw}
			}
		}
		if len(urls) == 0 {
			return agentcore.ToolResult{Text: "scrape requires url or urls", IsError: true}
		}
		if len(urls) > maxScrapeURLs {
			return agentcore.ToolResult{Text: fmt.Sprintf("scrape accepts at most %d URLs per call", maxScrapeURLs), IsError: true}
		}
		engine := scrapeEngine(call.Arguments)
		if engine == "obscura" && strings.TrimSpace(config.ObscuraURL) == "" {
			return agentcore.ToolResult{Text: "scrape engine obscura requires PIGO_OBSCURA_URL or OBSCURA_URL", IsError: true}
		}
		options := scrapeOptions{
			Engine:         engine,
			WaitUntil:      stringArg(call.Arguments["waitUntil"], "load"),
			TimeoutSeconds: intArg(call.Arguments["timeout"], 30),
		}
		start := time.Now()
		results := make([]scrapeResult, 0, len(urls))
		for _, rawURL := range urls {
			result := scrapeOne(ctx, config, rawURL, options)
			results = append(results, result)
		}
		text := formatScrapeResults(results)
		allFailed := true
		for _, result := range results {
			if result.Error == "" {
				allFailed = false
				break
			}
		}
		return agentcore.ToolResult{Text: text, Details: map[string]any{
			"urls":       urls,
			"engine":     engine,
			"results":    results,
			"durationMs": time.Since(start).Milliseconds(),
		}, IsError: allFailed}
	}}
}

type scrapeOptions struct {
	Engine         string
	WaitUntil      string
	TimeoutSeconds int
}

type scrapeResult struct {
	URL         string `json:"url"`
	Title       string `json:"title,omitempty"`
	Text        string `json:"text,omitempty"`
	Engine      string `json:"engine,omitempty"`
	ContentType string `json:"contentType,omitempty"`
	Status      int    `json:"status,omitempty"`
	Bytes       int    `json:"bytes,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	Error       string `json:"error,omitempty"`
}

func scrapeOne(ctx context.Context, config Config, rawURL string, options scrapeOptions) scrapeResult {
	if options.Engine == "obscura" {
		return scrapeOneObscura(ctx, config, rawURL, options)
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return scrapeResult{URL: rawURL, Error: "invalid URL"}
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return scrapeResult{URL: rawURL, Error: "unsupported URL scheme"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return scrapeResult{URL: rawURL, Error: err.Error()}
	}
	req.Header.Set("User-Agent", "pigo-research/0.1")
	resp, err := config.client().Do(req)
	if err != nil {
		return scrapeResult{URL: rawURL, Error: err.Error()}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxScrapeBytesPerURL+1))
	if err != nil {
		return scrapeResult{URL: rawURL, Status: resp.StatusCode, Error: err.Error()}
	}
	truncatedInput := len(data) > maxScrapeBytesPerURL
	if truncatedInput {
		data = data[:maxScrapeBytesPerURL]
	}
	contentType := resp.Header.Get("Content-Type")
	title, text := readableText(data, contentType)
	text, truncatedOutput := truncateText(text, maxScrapeOutputPerURL)
	return scrapeResult{
		URL:         parsed.String(),
		Title:       title,
		Text:        text,
		Engine:      "http",
		ContentType: contentType,
		Status:      resp.StatusCode,
		Bytes:       len(data),
		Truncated:   truncatedInput || truncatedOutput,
	}
}

func scrapeEngine(args map[string]any) string {
	engine := strings.ToLower(strings.TrimSpace(stringArg(args["engine"], "")))
	if engine == "obscura" {
		return "obscura"
	}
	if value, ok := args["render"].(bool); ok && value {
		return "obscura"
	}
	return "http"
}

func securityTool(config Config) agentcore.Tool {
	return agentcore.Tool{Name: "security_search", Execute: func(ctx context.Context, call ai.ContentBlock) agentcore.ToolResult {
		query, _ := call.Arguments["query"].(string)
		if strings.TrimSpace(query) == "" {
			return agentcore.ToolResult{Text: "security_search requires query", IsError: true}
		}
		ecosystem, _ := call.Arguments["ecosystem"].(string)
		pkgName, _ := call.Arguments["package"].(string)
		limit := intArg(call.Arguments["limit"], 10)
		if limit <= 0 || limit > 25 {
			limit = 10
		}
		start := time.Now()
		results := securityResults{}
		cves := cvePattern.FindAllString(strings.ToUpper(query), -1)
		for _, cve := range cves {
			if vuln, err := fetchOSVVuln(ctx, config, cve); err == nil {
				results.OSV = append(results.OSV, vuln)
			} else {
				results.Errors = append(results.Errors, "OSV "+cve+": "+err.Error())
			}
			if vulns, err := queryNVD(ctx, config, cve, "", limit); err == nil {
				results.NVD = append(results.NVD, vulns...)
			} else {
				results.Errors = append(results.Errors, "NVD "+cve+": "+err.Error())
			}
		}
		if pkgName != "" && ecosystem != "" {
			if vulns, err := queryOSVPackage(ctx, config, ecosystem, pkgName); err == nil {
				results.OSV = append(results.OSV, vulns...)
			} else {
				results.Errors = append(results.Errors, "OSV package query: "+err.Error())
			}
		}
		if len(cves) == 0 {
			if vulns, err := queryNVD(ctx, config, "", query, limit); err == nil {
				results.NVD = append(results.NVD, vulns...)
			} else {
				results.Errors = append(results.Errors, "NVD keyword query: "+err.Error())
			}
		}
		if kev, err := searchCISAKEV(ctx, config, query, limit); err == nil {
			results.CISAKEV = kev
		} else {
			results.Errors = append(results.Errors, "CISA KEV: "+err.Error())
		}
		results.OSV = limitOSV(uniqueOSV(results.OSV), limit)
		results.NVD = limitNVD(uniqueNVD(results.NVD), limit)
		text := formatSecurityResults(query, results)
		failed := len(results.OSV) == 0 && len(results.NVD) == 0 && len(results.CISAKEV) == 0 && len(results.Errors) > 0
		return agentcore.ToolResult{Text: text, Details: map[string]any{
			"query":      query,
			"ecosystem":  ecosystem,
			"package":    pkgName,
			"results":    results,
			"durationMs": time.Since(start).Milliseconds(),
		}, IsError: failed}
	}}
}

type securityResults struct {
	OSV     []osvVuln `json:"osv,omitempty"`
	NVD     []nvdVuln `json:"nvd,omitempty"`
	CISAKEV []kevVuln `json:"cisaKev,omitempty"`
	Errors  []string  `json:"errors,omitempty"`
}

type osvVuln struct {
	ID       string   `json:"id"`
	Summary  string   `json:"summary,omitempty"`
	Details  string   `json:"details,omitempty"`
	Modified string   `json:"modified,omitempty"`
	Aliases  []string `json:"aliases,omitempty"`
}

type kevVuln struct {
	CVE               string `json:"cveID"`
	VendorProject     string `json:"vendorProject,omitempty"`
	Product           string `json:"product,omitempty"`
	VulnerabilityName string `json:"vulnerabilityName,omitempty"`
	DateAdded         string `json:"dateAdded,omitempty"`
	DueDate           string `json:"dueDate,omitempty"`
	KnownRansomware   string `json:"knownRansomwareCampaignUse,omitempty"`
}

type nvdVuln struct {
	ID           string  `json:"id"`
	Source       string  `json:"sourceIdentifier,omitempty"`
	Published    string  `json:"published,omitempty"`
	LastModified string  `json:"lastModified,omitempty"`
	Status       string  `json:"vulnStatus,omitempty"`
	Description  string  `json:"description,omitempty"`
	Severity     string  `json:"severity,omitempty"`
	Score        float64 `json:"score,omitempty"`
}

func fetchOSVVuln(ctx context.Context, config Config, id string) (osvVuln, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, osvBaseURL+"/vulns/"+url.PathEscape(id), nil)
	if err != nil {
		return osvVuln{}, err
	}
	var vuln osvVuln
	err = doJSON(config.client(), req, nil, &vuln)
	return vuln, err
}

func queryOSVPackage(ctx context.Context, config Config, ecosystem, name string) ([]osvVuln, error) {
	body := map[string]any{"package": map[string]any{"ecosystem": ecosystem, "name": name}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, osvBaseURL+"/query", nil)
	if err != nil {
		return nil, err
	}
	var response struct {
		Vulns []osvVuln `json:"vulns"`
	}
	if err := doJSON(config.client(), req, body, &response); err != nil {
		return nil, err
	}
	return response.Vulns, nil
}

func queryNVD(ctx context.Context, config Config, cveID, keyword string, limit int) ([]nvdVuln, error) {
	endpoint, err := url.Parse(nvdBaseURL)
	if err != nil {
		return nil, err
	}
	values := endpoint.Query()
	values.Set("resultsPerPage", fmt.Sprintf("%d", limit))
	if cveID != "" {
		values.Set("cveId", cveID)
	} else {
		values.Set("keywordSearch", keyword)
	}
	endpoint.RawQuery = values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(config.NVDAPIKey) != "" {
		req.Header.Set("apiKey", strings.TrimSpace(config.NVDAPIKey))
	}
	var payload struct {
		Vulnerabilities []struct {
			CVE struct {
				ID               string `json:"id"`
				SourceIdentifier string `json:"sourceIdentifier"`
				Published        string `json:"published"`
				LastModified     string `json:"lastModified"`
				VulnStatus       string `json:"vulnStatus"`
				Descriptions     []struct {
					Lang  string `json:"lang"`
					Value string `json:"value"`
				} `json:"descriptions"`
				Metrics map[string][]struct {
					CVSSData struct {
						BaseScore    float64 `json:"baseScore"`
						BaseSeverity string  `json:"baseSeverity"`
					} `json:"cvssData"`
				} `json:"metrics"`
			} `json:"cve"`
		} `json:"vulnerabilities"`
	}
	if err := doJSON(config.client(), req, nil, &payload); err != nil {
		return nil, err
	}
	out := make([]nvdVuln, 0, len(payload.Vulnerabilities))
	for _, item := range payload.Vulnerabilities {
		score, severity := nvdScoreAndSeverity(item.CVE.Metrics)
		out = append(out, nvdVuln{
			ID:           item.CVE.ID,
			Source:       item.CVE.SourceIdentifier,
			Published:    item.CVE.Published,
			LastModified: item.CVE.LastModified,
			Status:       item.CVE.VulnStatus,
			Description:  nvdEnglishDescription(item.CVE.Descriptions),
			Severity:     severity,
			Score:        score,
		})
	}
	return out, nil
}

func nvdEnglishDescription(descriptions []struct {
	Lang  string `json:"lang"`
	Value string `json:"value"`
}) string {
	for _, description := range descriptions {
		if strings.EqualFold(description.Lang, "en") {
			return description.Value
		}
	}
	if len(descriptions) > 0 {
		return descriptions[0].Value
	}
	return ""
}

func nvdScoreAndSeverity(metrics map[string][]struct {
	CVSSData struct {
		BaseScore    float64 `json:"baseScore"`
		BaseSeverity string  `json:"baseSeverity"`
	} `json:"cvssData"`
}) (float64, string) {
	for _, key := range []string{"cvssMetricV40", "cvssMetricV31", "cvssMetricV30", "cvssMetricV2"} {
		values := metrics[key]
		if len(values) > 0 {
			return values[0].CVSSData.BaseScore, values[0].CVSSData.BaseSeverity
		}
	}
	return 0, ""
}

func searchCISAKEV(ctx context.Context, config Config, query string, limit int) ([]kevVuln, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cisaKEVURL, nil)
	if err != nil {
		return nil, err
	}
	var payload struct {
		Vulnerabilities []kevVuln `json:"vulnerabilities"`
	}
	if err := doJSON(config.client(), req, nil, &payload); err != nil {
		return nil, err
	}
	terms := queryTerms(query)
	out := []kevVuln{}
	for _, vuln := range payload.Vulnerabilities {
		haystack := strings.ToLower(strings.Join([]string{vuln.CVE, vuln.VendorProject, vuln.Product, vuln.VulnerabilityName}, " "))
		if matchesTerms(haystack, terms) {
			out = append(out, vuln)
		}
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func doJSON(client *http.Client, req *http.Request, body any, out any) error {
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		req.Body = io.NopCloser(bytes.NewReader(data))
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("upstream returned %s", resp.Status)
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 4_000_000)).Decode(out)
}

func readableText(data []byte, contentType string) (string, string) {
	if strings.Contains(strings.ToLower(contentType), "html") || bytes.Contains(bytes.ToLower(data[:min(len(data), 512)]), []byte("<html")) {
		root, err := html.Parse(bytes.NewReader(data))
		if err == nil {
			title := strings.TrimSpace(findTitle(root))
			return title, normalizeWhitespace(extractHTMLText(root))
		}
	}
	return "", normalizeWhitespace(string(data))
}

func findTitle(node *html.Node) string {
	if node.Type == html.ElementNode && strings.EqualFold(node.Data, "title") {
		return collectText(node)
	}
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		if title := findTitle(child); title != "" {
			return title
		}
	}
	return ""
}

func extractHTMLText(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "script", "style", "noscript", "svg":
				return
			}
		}
		if n.Type == html.TextNode {
			builder.WriteString(n.Data)
			builder.WriteString(" ")
		}
		if n.Type == html.ElementNode {
			switch strings.ToLower(n.Data) {
			case "p", "br", "div", "section", "article", "li", "h1", "h2", "h3", "h4":
				builder.WriteString("\n")
			}
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}

func collectText(node *html.Node) string {
	var builder strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			builder.WriteString(n.Data)
			builder.WriteString(" ")
		}
		for child := n.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(node)
	return builder.String()
}

func normalizeWhitespace(text string) string {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := []string{}
	for _, line := range lines {
		line = strings.Join(strings.Fields(line), " ")
		if line != "" {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func truncateText(text string, limit int) (string, bool) {
	if limit <= 0 || len(text) <= limit {
		return text, false
	}
	return text[:limit] + "\n\n... (truncated) ...", true
}

func stringSliceArg(value any) []string {
	switch typed := value.(type) {
	case []string:
		return normalizeStrings(typed)
	case []any:
		out := []string{}
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return normalizeStrings(out)
	default:
		return nil
	}
}

func normalizeStrings(values []string) []string {
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func intArg(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case json.Number:
		n, err := typed.Int64()
		if err == nil {
			return int(n)
		}
	}
	return fallback
}

func stringArg(value any, fallback string) string {
	if text, ok := value.(string); ok {
		text = strings.TrimSpace(text)
		if text != "" {
			return text
		}
	}
	return fallback
}

func queryTerms(query string) []string {
	matches := cvePattern.FindAllString(strings.ToUpper(query), -1)
	if len(matches) > 0 {
		return normalizeStrings(matches)
	}
	terms := []string{}
	for _, field := range strings.Fields(strings.ToLower(query)) {
		field = strings.Trim(field, ".,:;()[]{}")
		if len(field) >= 3 {
			terms = append(terms, field)
		}
	}
	return terms
}

func matchesTerms(haystack string, terms []string) bool {
	if len(terms) == 0 {
		return false
	}
	for _, term := range terms {
		if strings.Contains(haystack, strings.ToLower(term)) {
			return true
		}
	}
	return false
}

func uniqueOSV(values []osvVuln) []osvVuln {
	seen := map[string]bool{}
	out := []osvVuln{}
	for _, value := range values {
		if value.ID == "" || seen[value.ID] {
			continue
		}
		seen[value.ID] = true
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func limitOSV(values []osvVuln, limit int) []osvVuln {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func uniqueNVD(values []nvdVuln) []nvdVuln {
	seen := map[string]bool{}
	out := []nvdVuln{}
	for _, value := range values {
		if value.ID == "" || seen[value.ID] {
			continue
		}
		seen[value.ID] = true
		out = append(out, value)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func limitNVD(values []nvdVuln, limit int) []nvdVuln {
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}
