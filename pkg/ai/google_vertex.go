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
	requestURL, err := buildGoogleVertexURL(baseURL, project, location, req.Model, apiKey, req.Options.Stream)
	if err != nil {
		return NormalizedResult{}, nil, err
	}

	payload := toGoogleRequest(req)
	payloadValue, err := applyPayloadHook(req, payload)
	if err != nil {
		return NormalizedResult{}, nil, err
	}
	data, err := json.Marshal(payload)
	if payloadValue != nil {
		data, err = json.Marshal(payloadValue)
	}
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

	buildRequest := func() (*http.Request, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("create google-vertex request: %w", err)
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
		return httpReq, nil
	}

	maxRetries := retryLimit(req.Options, defaultProviderMaxRetries)
	for attempt := 0; attempt <= maxRetries; attempt++ {
		httpReq, err := buildRequest()
		if err != nil {
			return NormalizedResult{}, nil, err
		}
		resp, err := httpClient.Do(httpReq)
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: err.Error()}, nil, err
			}
			if attempt < maxRetries && shouldRetryHTTPError(err) {
				if sleepErr := sleepWithContext(ctx, retryDelayForAttempt(attempt, 0, defaultProviderBaseDelay)); sleepErr != nil {
					return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: sleepErr.Error()}, nil, sleepErr
				}
				continue
			}
			return NormalizedResult{}, nil, fmt.Errorf("call google-vertex API: %w", err)
		}

		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if hookErr := notifyResponseHook(req, resp); hookErr != nil {
				_ = resp.Body.Close()
				return NormalizedResult{}, nil, hookErr
			}
			body, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			errorText := strings.TrimSpace(string(body))
			if attempt < maxRetries && shouldRetryHTTPStatus(resp.StatusCode, errorText) {
				serverDelay := retryAfterDelay(resp, errorText)
				if err := validateRetryDelay(req.Options, serverDelay); err != nil {
					return NormalizedResult{}, nil, err
				}
				if sleepErr := sleepWithContext(ctx, retryDelayForAttempt(attempt, serverDelay, defaultProviderBaseDelay)); sleepErr != nil {
					return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: sleepErr.Error()}, nil, sleepErr
				}
				continue
			}
			if errorText == "" {
				return NormalizedResult{}, nil, fmt.Errorf("google-vertex API error: %s", resp.Status)
			}
			return NormalizedResult{}, nil, fmt.Errorf("google-vertex API error: %s: %s", resp.Status, errorText)
		}
		if hookErr := notifyResponseHook(req, resp); hookErr != nil {
			_ = resp.Body.Close()
			return NormalizedResult{}, nil, hookErr
		}

		if req.Options.Stream {
			result, events, err := googleSSEToResult(resp.Body)
			_ = resp.Body.Close()
			if err == nil {
				return result, events, nil
			}
			if attempt < maxRetries && isRetriableStreamError(err) {
				if sleepErr := sleepWithContext(ctx, retryDelayForAttempt(attempt, 0, defaultProviderBaseDelay)); sleepErr != nil {
					return NormalizedResult{Role: "assistant", StopReason: "aborted", ErrorMessage: sleepErr.Error()}, nil, sleepErr
				}
				continue
			}
			return NormalizedResult{}, nil, err
		}

		var completion googleResponse
		decodeErr := json.NewDecoder(resp.Body).Decode(&completion)
		_ = resp.Body.Close()
		if decodeErr != nil {
			return NormalizedResult{}, nil, fmt.Errorf("parse google-vertex response: %w", decodeErr)
		}
		result := googleResponseToNormalized(completion)
		return result, AssistantEvents(result.contentBlocks(), result.StopReason), nil
	}

	return NormalizedResult{}, nil, errors.New("google-vertex retry budget exhausted")
}

func buildGoogleVertexURL(baseURL, project, location, model, apiKey string, stream bool) (string, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		baseURL = "https://{location}-aiplatform.googleapis.com"
	}
	baseURL = strings.ReplaceAll(baseURL, "{location}", location)
	baseURL = strings.TrimRight(baseURL, "/")

	var requestURL string
	if strings.Contains(baseURL, "/publishers/google/models/") {
		if stream {
			requestURL = baseURL + ":streamGenerateContent"
		} else {
			requestURL = baseURL + ":generateContent"
		}
	} else {
		suffix := ":generateContent"
		if stream {
			suffix = ":streamGenerateContent"
		}
		requestURL = baseURL + "/v1/projects/" + url.PathEscape(project) + "/locations/" + url.PathEscape(location) +
			"/publishers/google/models/" + url.PathEscape(model) + suffix
	}

	parsed, err := url.Parse(requestURL)
	if err != nil {
		return "", fmt.Errorf("invalid google-vertex base URL: %w", err)
	}
	query := parsed.Query()
	query.Set("key", apiKey)
	if stream {
		query.Set("alt", "sse")
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
