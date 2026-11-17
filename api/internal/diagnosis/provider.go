package diagnosis

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
)

type providerError struct {
	code   string
	status int
}

func (e *providerError) Error() string { return e.code }

func isProviderTimeout(err error) bool {
	var networkError net.Error
	return errors.Is(err, context.DeadlineExceeded) ||
		(errors.As(err, &networkError) && networkError.Timeout())
}

func classifyProviderTransport(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return context.Canceled
	case isProviderTimeout(err):
		return &providerError{code: "AI_PROVIDER_TIMEOUT"}
	default:
		return &providerError{code: "AI_PROVIDER_CONNECT_FAILED"}
	}
}

func classifyProviderStatus(status int) *providerError {
	code := "AI_PROVIDER_UNAVAILABLE"
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		code = "AI_PROVIDER_ACCESS_DENIED"
	case http.StatusTooManyRequests:
		code = "AI_PROVIDER_RATE_LIMITED"
	case http.StatusRequestTimeout, http.StatusGatewayTimeout:
		code = "AI_PROVIDER_TIMEOUT"
	default:
		if status >= 300 && status < 500 {
			code = "AI_PROVIDER_REQUEST_REJECTED"
		}
	}
	return &providerError{code: code, status: status}
}

func (f *Review) completeAI(ctx context.Context, ai kubernetes.EngineAIConfig, system string, data []byte) (_ string, failure error) {
	started := time.Now()
	defer func() {
		var upstream *providerError
		if errors.As(failure, &upstream) {
			slog.WarnContext(ctx, "AI provider request failed", "code", upstream.code, "upstream_status", upstream.status, "elapsed_ms", time.Since(started).Milliseconds())
		}
	}()
	payload, _ := json.Marshal(map[string]any{"model": ai.Model, "messages": []map[string]string{
		{"role": "system", "content": system},
		{"role": "user", "content": string(data)},
	}})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ai.Endpoint, bytes.NewReader(payload))
	if err != nil {
		return "", classifyProviderTransport(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+ai.Key)
	res, err := f.client.Do(req)
	if err != nil {
		return "", classifyProviderTransport(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", classifyProviderStatus(res.StatusCode)
	}
	var response struct {
		Choices []struct {
			FinishReason string `json:"finish_reason"`
			Message      struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&response); err != nil {
		if ctx.Err() != nil {
			return "", classifyProviderTransport(ctx.Err())
		}
		if isProviderTimeout(err) {
			return "", classifyProviderTransport(err)
		}
		return "", &providerError{code: "AI_PROVIDER_RESPONSE_INVALID", status: res.StatusCode}
	}
	if len(response.Choices) == 0 || response.Choices[0].FinishReason != "stop" || strings.TrimSpace(response.Choices[0].Message.Content) == "" {
		return "", &providerError{code: "AI_PROVIDER_RESPONSE_INVALID", status: res.StatusCode}
	}
	return response.Choices[0].Message.Content, nil
}
