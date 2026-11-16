package kubernetes

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strings"
)

var engineObjectName = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

type EngineAIConfig struct {
	Endpoint string
	Model    string
	Key      string
}

// EngineAI reads the configured Engine and its referenced Secret every time.
func (c *Client) EngineAI(ctx context.Context) (EngineAIConfig, error) {
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
		return EngineAIConfig{}, errors.New("cannot read K8sGPT Engine configuration")
	}
	ai := engine.Spec.AI
	model := strings.TrimSpace(ai.Model)
	if !ai.Enabled || ai.Backend != "openai" || model == "" || !engineObjectName.MatchString(ai.Secret.Name) || ai.Secret.Key == "" {
		return EngineAIConfig{}, errors.New("invalid or unsupported K8sGPT Engine AI configuration")
	}
	base := strings.TrimSpace(ai.BaseURL)
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.ForceQuery || u.RawQuery != "" || u.Fragment != "" {
		return EngineAIConfig{}, errors.New("invalid K8sGPT Engine AI endpoint")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/chat/completions") {
		u.Path += "/chat/completions"
	}
	var secret struct{ Data map[string][]byte }
	if err := c.getJSON(ctx, "/api/v1/namespaces/"+ns+"/secrets/"+url.PathEscape(ai.Secret.Name), &secret); err != nil {
		return EngineAIConfig{}, errors.New("cannot read K8sGPT Engine Secret")
	}
	key := strings.TrimSpace(string(secret.Data[ai.Secret.Key]))
	if key == "" || strings.ContainsAny(key, "\r\n") {
		return EngineAIConfig{}, errors.New("invalid K8sGPT Engine Secret key")
	}
	return EngineAIConfig{Endpoint: u.String(), Model: model, Key: key}, nil
}
