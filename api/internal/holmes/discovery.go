package holmes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// DiscoverModels only contacts the operator-provisioned service bound to the exact scope.
func DiscoverModels(ctx context.Context, scope Scope) ([]string, error) {
	selected, err := configuredBinding(ctx, scope)
	if err != nil {
		return nil, err
	}
	return discoverModels(ctx, selected)
}

func discoverModels(ctx context.Context, selected binding) ([]string, error) {
	select {
	case active <- struct{}{}:
		defer func() { <-active }()
	default:
		return nil, ErrUnavailable
	}
	endpoint, err := endpointFor(selected, "/api/model")
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, ErrUnavailable
	}
	if selected.APIKey != "" {
		req.Header.Set("X-API-Key", selected.APIKey)
	}
	client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, ErrUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, ErrUnavailable
	}
	var payload struct {
		Models []string `json:"model_name"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 256<<10)).Decode(&payload) != nil || len(payload.Models) > 256 {
		return nil, ErrUnavailable
	}
	out := []string{}
	for _, m := range payload.Models {
		if m == strings.TrimSpace(m) && m != "" && len(m) <= 256 {
			out = append(out, m)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
