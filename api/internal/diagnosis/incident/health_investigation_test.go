package incident

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/abchw517/kube-aiops/api/internal/holmes"
)

func TestHealthInvestigationFailureKeepsSafeCodeAndTimeoutStatus(t *testing.T) {
	for _, tc := range []struct {
		err            error
		status         int
		code           string
		upstreamStatus int
	}{
		{holmes.ErrUnconfigured, http.StatusServiceUnavailable, "HOLMES_SCOPE_UNCONFIGURED", 0},
		{&holmes.ChatError{Code: "HOLMES_CHAT_AUTH_FAILED", UpstreamStatus: 401}, http.StatusServiceUnavailable, "HOLMES_CHAT_AUTH_FAILED", 401},
		{&holmes.ChatError{Code: "HOLMES_CHAT_TIMEOUT", UpstreamStatus: 504}, http.StatusGatewayTimeout, "HOLMES_CHAT_TIMEOUT", 504},
		{fmt.Errorf("private configuration error"), http.StatusServiceUnavailable, "HOLMES_UNAVAILABLE", 0},
	} {
		t.Run(tc.code, func(t *testing.T) {
			got, upstreamStatus := healthInvestigationFailure(fmt.Errorf("wrapped: %w", tc.err))
			if got.code != tc.code || got.status != tc.status || upstreamStatus != tc.upstreamStatus || got.message == tc.err.Error() && tc.code == "HOLMES_UNAVAILABLE" {
				t.Fatalf("unexpected diagnostic error: %+v, upstream status %d", got, upstreamStatus)
			}
		})
	}
}
