package diagnosis

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
)

func TestProviderFailureCategories(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
		code   string
	}{
		{"credentials", 401, "private upstream details", "AI_PROVIDER_ACCESS_DENIED"},
		{"rate limit", 429, "", "AI_PROVIDER_RATE_LIMITED"},
		{"timeout", 504, "", "AI_PROVIDER_TIMEOUT"},
		{"invalid model", 400, "", "AI_PROVIDER_REQUEST_REJECTED"},
		{"empty answer", 200, `{"choices":[{"finish_reason":"stop","message":{"content":""}}]}`, "AI_PROVIDER_RESPONSE_INVALID"},
		{"service unavailable", 503, "", "AI_PROVIDER_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer upstream.Close()
			review := &Review{client: upstream.Client()}
			_, err := review.completeAI(t.Context(), kubernetes.EngineAIConfig{Endpoint: upstream.URL, Model: "fixture", Key: "fixture-key"}, "fixture", nil)
			var result *providerError
			if !errors.As(err, &result) || result.status != tc.status || result.Error() != tc.code {
				t.Fatalf("unexpected failure: %v", err)
			}
		})
	}
	if err := classifyProviderTransport(context.DeadlineExceeded); err.Error() != "AI_PROVIDER_TIMEOUT" {
		t.Fatalf("unexpected timeout: %v", err)
	}
	if err := classifyProviderTransport(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected cancellation: %v", err)
	}
}

// TestProviderTimeoutBudget 验证默认值、自定义期限和配置边界。
func TestProviderTimeoutBudget(t *testing.T) {
	for _, tc := range []struct {
		value string
		want  time.Duration
	}{
		{"", 35 * time.Second},
		{" 40s ", 40 * time.Second},
		{"5s", 5 * time.Second},
		{"25", 0},
		{"0s", 0},
		{"41s", 0},
		{"4s", 0},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv("FINDING_AI_PROVIDER_TIMEOUT", tc.value)
			timeout, err := readProviderTimeout()
			if timeout != tc.want || (err != nil) != (tc.want == 0) {
				t.Fatalf("readProviderTimeout() = %v, %v; want %v", timeout, err, tc.want)
			}
		})
	}
}
