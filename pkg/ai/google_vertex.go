package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const googleVertexAuthenticatedMarker = "<authenticated>"

type googleVertexProvider struct{}

func GoogleVertexProvider() ChatProvider {
	return &googleVertexProvider{}
}

func (provider *googleVertexProvider) Complete(ctx context.Context, req CompletionRequest) (NormalizedResult, []NormalizedEvent, error) {
	providerSpec, hasProviderSpec := ProviderSpecForProvider(req.Provider)
	if req.Options.Stream {
		return NormalizedResult{}, nil, errors.New("streaming is not supported for google-vertex providers yet")
	}

	apiKey := strings.TrimSpace(req.Options.APIKey)
	if apiKey == "" && hasProviderSpec {
		apiKey, _ = ProviderAPIKey(req.Provider)
	}
	if apiKey == googleVertexAuthenticatedMarker {
		return NormalizedResult{}, nil, errors.New("google-vertex ADC credentials are not supported in pigo yet; set GOOGLE_CLOUD_API_KEY")
	}
	if apiKey == "" {
		providerName := strings.TrimSpace(req.Provider)
		if providerName == "" {
			providerName = "google-vertex"
		}
		return NormalizedResult{}, nil, fmt.Errorf("missing API key for provider: %s", providerName)
	}
	if strings.TrimSpace(req.Model) == "" {
		return NormalizedResult{}, nil, errors.New("model is required")
	}

	baseURL := strings.TrimSpace(req.Options.BaseURL)
	if baseURL == "" && hasProviderSpec {
		baseURL = strings.TrimSpace(providerSpec.BaseURL)
	}
	project := strings.TrimSpace(getenv("GOOGLE_CLOUD_PROJECT"))
	if project == "" {
		project = strings.TrimSpace(getenv("GCLOUD_PROJECT"))
	}
	if project == "" {
		return NormalizedResult{}, nil, errors.New("google-vertex requires GOOGLE_CLOUD_PROJECT or GCLOUD_PROJECT")
	}
	location := strings.TrimSpace(getenv("GOOGLE_CLOUD_LOCATION"))
	if location == "" {
		return NormalizedResult{}, nil, errors.New("google-vertex requires GOOGLE_CLOUD_LOCATION")
	}
	requestURL, err := buildGoogleVertexURL(baseURL, project, location, req.Model, apiKey)
	if err != nil {
		return NormalizedResult{}, nil, err
	}

	payload := toGoogleRequest(req)
	data, err := json.Marshal(payload)
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("marshal google-vertex payload: %w", err)
	}

	httpClient := req.Options.HTTPClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if req.Options.Timeout > 0 {
		cloned := *httpClient
		cloned.Timeout = req.Options.Timeout
		httpClient = &cloned
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(data))
	if err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("create google-vertex request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if hasProviderSpec {
		for key, value := range providerSpec.DefaultHeader {
			httpReq.Header.Set(key, value)
		}
	}
	for key, value := range req.Options.Headers {
		httpReq.Header.Set(key, value)
	}

	resp, err := httpClient.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: err.Error()}, nil, err
		}
		return NormalizedResult{}, nil, fmt.Errorf("call google-vertex API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		if len(body) == 0 {
			return NormalizedResult{}, nil, fmt.Errorf("google-vertex API error: %s", resp.Status)
		}
		return NormalizedResult{}, nil, fmt.Errorf("google-vertex API error: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	var completion googleResponse
	if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
		return NormalizedResult{}, nil, fmt.Errorf("parse google-vertex response: %w", err)
	}
	result := googleResponseToNormalized(completion)
	return result, AssistantEvents(result.contentBlocks(), result.StopReason), nil
}

func buildGoogleVertexURL(baseURL, project, location, model, apiKey string) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = "https://{location}-aiplatform.googleapis.com"
	}
	baseURL = strings.ReplaceAll(baseURL, "{location}", location)
	baseURL = strings.TrimRight(baseURL, "/")

	var requestURL string
	if strings.Contains(baseURL, "/publishers/google/models/") {
		requestURL = baseURL + ":generateContent"
	} else {
		requestURL = baseURL + "/v1/projects/" + url.PathEscape(project) + "/locations/" + url.PathEscape(location) +
			"/publishers/google/models/" + url.PathEscape(model) + ":generateContent"
	}

	parsed, err := url.Parse(requestURL)
	if err != nil {
		return "", fmt.Errorf("invalid google-vertex base URL: %w", err)
	}
	query := parsed.Query()
	query.Set("key", apiKey)
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
