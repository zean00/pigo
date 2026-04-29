package ai

import "net/http"

func applyPayloadHook(req CompletionRequest, payload any) (any, error) {
	if req.Options.OnPayload == nil {
		return payload, nil
	}
	return req.Options.OnPayload(payload, req)
}

func notifyResponseHook(req CompletionRequest, resp *http.Response) error {
	if req.Options.OnResponse == nil || resp == nil {
		return nil
	}
	headers := make(map[string]string, len(resp.Header))
	for key, values := range resp.Header {
		if len(values) == 0 {
			continue
		}
		headers[key] = values[0]
	}
	return req.Options.OnResponse(ProviderResponse{
		Status:  resp.StatusCode,
		Headers: headers,
	}, req)
}
