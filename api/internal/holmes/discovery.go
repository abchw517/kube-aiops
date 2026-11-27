package holmes

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// DiscoveryError describes a failure without exposing credentials or upstream content.
type DiscoveryError struct{ Message string }

func (e *DiscoveryError) Error() string { return e.Message }
func (*DiscoveryError) Unwrap() error   { return ErrUnavailable }

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
		return nil, &DiscoveryError{"Holmes 请求较多，请稍后重试。"}
	}
	endpoint, err := endpointFor(selected, "/api/model")
	if err != nil {
		return nil, &DiscoveryError{"Holmes 服务地址无效，请检查 API Deployment 的服务地址配置。"}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, &DiscoveryError{"Holmes 服务地址无效，请检查 API Deployment 的服务地址配置。"}
	}
	if selected.APIKey != "" {
		req.Header.Set("X-API-Key", selected.APIKey)
	}
	client := http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, &DiscoveryError{"Portal API 未能连接 Holmes 模型接口，请检查 Service 地址、DNS 和网络策略后重试。"}
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusOK:
	case http.StatusUnauthorized, http.StatusForbidden:
		return nil, &DiscoveryError{fmt.Sprintf("Holmes 模型接口鉴权失败（HTTP %d）。请检查 Portal 的 HOLMES_PILOT_API_KEY 或 HOLMES_WIKI_API_KEY 是否与目标服务的 HOLMES_API_KEY 一致。", res.StatusCode)}
	case http.StatusNotFound:
		return nil, &DiscoveryError{"Holmes 模型接口未找到（HTTP 404）。请检查服务根地址和 /api/model 路由。"}
	default:
		return nil, &DiscoveryError{fmt.Sprintf("Holmes 模型接口返回 HTTP %d，请查看 Holmes 服务日志后重试。", res.StatusCode)}
	}

	const limit = 256 << 10
	raw, err := io.ReadAll(io.LimitReader(res.Body, limit+1))
	if err != nil {
		return nil, &DiscoveryError{"读取 Holmes 模型响应中断，请重试。"}
	}
	var payload struct {
		Models json.RawMessage `json:"model_name"`
	}
	invalid := &DiscoveryError{"Holmes 模型响应格式无效，请检查 /api/model 是否返回模型别名列表。"}
	if len(raw) > limit || json.Unmarshal(raw, &payload) != nil || len(payload.Models) == 0 {
		return nil, invalid
	}

	// Holmes 0.48.0 JSON-encodes the list before returning it from the JSON API.
	if payload.Models[0] == '"' {
		var encoded string
		if json.Unmarshal(payload.Models, &encoded) != nil {
			return nil, invalid
		}
		payload.Models = json.RawMessage(encoded)
	}
	var models []string
	if json.Unmarshal(payload.Models, &models) != nil || models == nil || len(models) > 256 {
		return nil, invalid
	}
	out := []string{}
	for _, m := range models {
		if m == strings.TrimSpace(m) && m != "" && len(m) <= 256 {
			out = append(out, m)
		}
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}
