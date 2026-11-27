package portal

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/holmes"
)

type alertScopeValidator func(context.Context, holmes.Scope) error

func serveAIServices(w http.ResponseWriter, r *http.Request, validateAlert alertScopeValidator) {
	ctx := r.Context()
	workspace := holmes.WorkspaceFrom(ctx)
	switch r.Method {
	case http.MethodGet:
		models, err := holmes.ConfiguredModels(ctx)
		status := "configured"
		switch {
		case err != nil:
			status = "invalid"
		case len(models) == 0:
			status = "unconfigured"
		}
		if models == nil {
			models = []holmes.ConfiguredModel{}
		}
		connections := []holmes.Entry[holmes.Connection]{}
		if workspace != nil {
			connections, err = holmes.ListEntries[holmes.Connection](ctx, workspace, "connection")
			if err != nil {
				http.Error(w, "无法读取模型连接", 503)
				return
			}
		}
		pilot := holmes.PilotStatus(ctx)
		adminhttp.WriteJSON(w, map[string]any{"status": status, "models": models, "connections": connections, "pilot": pilot})
	case http.MethodPost:
		var input struct {
			holmes.Scope
			Version int                `json:"version"`
			Config  holmes.ModelConfig `json:"config"`
			Model   string             `json:"model"`
		}
		if !adminhttp.Decode(w, r, &input) {
			http.Error(w, "配置请求格式无效", 400)
			return
		}
		action := r.URL.Query().Get("action")
		switch action {
		case "discover-binding":
			if !slices.Contains([]string{"zabbix", "alertmanager", "finding", "incident", "wiki"}, input.Source) {
				http.Error(w, "请选择绑定类型", 400)
				return
			}
			aliases, err := holmes.DiscoverPortalModels(ctx, input.Source)
			if errors.Is(err, holmes.ErrUnconfigured) {
				http.Error(w, "请先配置独立的 Wiki Holmes 服务", 409)
				return
			}
			if err != nil {
				writeDiscoveryError(w, err)
				return
			}
			adminhttp.WriteJSON(w, map[string]any{"models": aliases})
		case "bind":
			if !validBindingScope(input.Scope) || input.Model == "" || len(input.Model) > 256 {
				http.Error(w, "请选择数据源，填写准确的范围并选择模型别名", 400)
				return
			}
			if input.Source == "zabbix" || input.Source == "alertmanager" {
				if validateAlert == nil || validateAlert(ctx, input.Scope) != nil {
					http.Error(w, "数据源不可用或所属范围已变更，请重新选择", 409)
					return
				}
			}
			if err := holmes.BindPortalScope(ctx, input.Scope, input.Model); err != nil {
				if errors.Is(err, holmes.ErrUnconfigured) {
					http.Error(w, "请先配置独立的 Wiki Holmes 服务", 409)
					return
				}
				if errors.Is(err, holmes.ErrModelNotFound) || errors.Is(err, holmes.ErrAlreadyBound) {
					http.Error(w, err.Error(), 409)
					return
				}
				writeDiscoveryError(w, err)
				return
			}
			adminhttp.WriteJSON(w, map[string]bool{"bound": true})
		case "", "recheck":
			if err := holmes.CheckService(ctx, input.Scope); err != nil {
				http.Error(w, "Holmes 就绪检查失败", 503)
				return
			}
			adminhttp.WriteJSON(w, map[string]bool{"ready": true})
		case "discover":
			aliases, err := holmes.DiscoverModels(ctx, input.Scope)
			if err != nil {
				writeDiscoveryError(w, err)
				return
			}
			adminhttp.WriteJSON(w, map[string]any{"models": aliases})
		case "save", "publish":
			if workspace == nil {
				http.Error(w, "AI 工作区或部署绑定不可用", 503)
				return
			}
			models, err := holmes.ConfiguredModels(ctx)
			if !slices.ContainsFunc(models, func(m holmes.ConfiguredModel) bool { return m.Scope == input.Scope }) {
				if err != nil {
					writeAIError(w, err)
					return
				}
				http.NotFound(w, r)
				return
			}
			id := holmes.ScopeID(input.Scope)
			entry, err := holmes.GetEntry[holmes.Connection](ctx, workspace, "connection", id)
			if err != nil {
				http.Error(w, "无法读取模型连接", 503)
				return
			}
			if input.Version != entry.Version {
				writeAIError(w, holmes.ErrConflict)
				return
			}
			if action == "save" {
				cfg := input.Config
				cfg.Name = strings.TrimSpace(cfg.Name)
				if cfg.Name == "" || len(cfg.Name) > 120 || !validAIProvider(cfg.Provider) || len(cfg.Fast) > 256 || len(cfg.Accurate) > 256 ||
					(!cfg.Disabled && (cfg.Fast == "" || cfg.Accurate == "")) {
					http.Error(w, "请填写名称、提供商和模型档位", 400)
					return
				}
				entry.Data.Scope = input.Scope
				entry.Data.Desired = cfg
			} else {
				if entry.Version == 0 {
					http.Error(w, "请先保存配置草稿", 409)
					return
				}
				cfg := entry.Data.Desired
				if !cfg.Disabled {
					aliases, err := holmes.DiscoverModels(ctx, input.Scope)
					if err != nil {
						writeDiscoveryError(w, err)
						return
					}
					if !slices.Contains(aliases, cfg.Fast) || !slices.Contains(aliases, cfg.Accurate) {
						http.Error(w, "发布校验失败，Holmes 未返回所选模型。现行版本继续生效。", 409)
						return
					}
				}
				now := time.Now().UTC()
				entry.Data.Effective, entry.Data.EffectiveVersion, entry.Data.PublishedAt = cfg, entry.Version, &now
			}
			if err := holmes.SaveEntry(ctx, workspace, "connection", entry); err != nil {
				writeAIError(w, err)
				return
			}
			entry.Version++
			adminhttp.WriteJSON(w, entry)
		default:
			http.Error(w, "不支持的操作", 400)
		}
	default:
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func validAIProvider(value string) bool {
	return slices.Contains([]string{"vLLM", "Ollama", "OpenAI", "DeepSeek", "智谱 AI", "OpenAI 兼容"}, value)
}

func writeDiscoveryError(w http.ResponseWriter, err error) {
	var failure *holmes.DiscoveryError
	if errors.As(err, &failure) {
		http.Error(w, failure.Error(), http.StatusServiceUnavailable)
		return
	}
	writeAIError(w, err)
}

func writeAIError(w http.ResponseWriter, err error) {
	if errors.Is(err, holmes.ErrConflict) {
		http.Error(w, err.Error(), 409)
		return
	}
	http.Error(w, "AI 工作区暂不可用", 503)
}

var bindingID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func validBindingScope(scope holmes.Scope) bool {
	for _, id := range []string{scope.SourceID, scope.Environment, scope.Business, scope.Cluster, scope.Namespace} {
		if id != "" && !bindingID.MatchString(id) {
			return false
		}
	}
	switch scope.Source {
	case "zabbix":
		return scope.SourceID != "" && scope.Environment != "" && scope.Business != "" && scope.Cluster == "" && scope.Namespace == ""
	case "alertmanager":
		return scope.SourceID != "" && scope.Environment != "" && scope.Business != "" && scope.Cluster != ""
	case "finding":
		return scope.Cluster != "" && scope.SourceID == scope.Cluster && scope.Environment == "" && scope.Business == ""
	case "incident":
		return scope.Cluster != "" && scope.SourceID == scope.Cluster
	case "wiki":
		return scope == (holmes.Scope{Source: "wiki", SourceID: "published-excerpts"})
	default:
		return false
	}
}
