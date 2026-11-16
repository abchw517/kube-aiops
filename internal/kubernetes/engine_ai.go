package kubernetes

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var (
	ErrEngineUnavailable = errors.New("cannot read K8sGPT Engine configuration")
	ErrEnginePermission  = errors.New("K8sGPT Engine or Secret read denied")
	ErrEngineConfig      = errors.New("invalid or unsupported K8sGPT Engine AI configuration")
	ErrEngineEndpoint    = errors.New("K8sGPT Engine AI endpoint is not approved")
	ErrEngineSecret      = errors.New("cannot read K8sGPT Engine Secret or configured key")
)

func engineReadError(err, fallback error) error {
	var status *APIError
	if errors.As(err, &status) && (status.StatusCode == 403 || status.StatusCode == 401) {
		return ErrEnginePermission
	}
	return fallback
}

var engineObjectName = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

type EngineAIConfig struct {
	Endpoint string
	Model    string
	Key      string
}

// EngineAI reads the configured Engine and its referenced Secret every time.
func (c *Client) EngineAI(ctx context.Context, approvedEndpoint string) (EngineAIConfig, error) {
	ns := url.PathEscape(c.config.K8sGPTNS)
	var engine struct {
		Spec struct {
			AI struct {
				Enabled bool
				Backend string
				Model   string
				BaseURL string
				Secret  struct{ Name, Key string }
			}
		}
	}
	if err := c.getJSON(ctx, "/apis/core.k8sgpt.ai/v1alpha1/namespaces/"+ns+"/k8sgpts/"+url.PathEscape(c.config.K8sGPTName), &engine); err != nil {
		return EngineAIConfig{}, engineReadError(err, ErrEngineUnavailable)
	}
	ai := engine.Spec.AI
	model := strings.TrimSpace(ai.Model)
	if !ai.Enabled || ai.Backend != "openai" || model == "" || !engineObjectName.MatchString(ai.Secret.Name) || ai.Secret.Key == "" {
		return EngineAIConfig{}, ErrEngineConfig
	}
	base := strings.TrimSpace(ai.BaseURL)
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return EngineAIConfig{}, ErrEngineConfig
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/chat/completions") {
		u.Path += "/chat/completions"
	}
	if approvedEndpoint == "" {
		approvedEndpoint = "https://api.openai.com/v1/chat/completions"
	}
	if u.String() != approvedEndpoint {
		return EngineAIConfig{}, ErrEngineEndpoint
	}
	var secret struct{ Data map[string][]byte }
	if err := c.getJSON(ctx, "/api/v1/namespaces/"+ns+"/secrets/"+url.PathEscape(ai.Secret.Name), &secret); err != nil {
		return EngineAIConfig{}, engineReadError(err, ErrEngineSecret)
	}
	key := strings.TrimSpace(string(secret.Data[ai.Secret.Key]))
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return EngineAIConfig{}, ErrEngineSecret
	}
	return EngineAIConfig{Endpoint: u.String(), Model: model, Key: key}, nil
}
