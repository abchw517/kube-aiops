package portal

import (
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/holmes"
)

func serveAIServices(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	workspace := holmes.WorkspaceFrom(ctx)
	models, err := holmes.ConfiguredModels()
	status := "configured"
	switch {
	case errors.Is(err, holmes.ErrUnconfigured), err == nil && len(models) == 0:
		status = "unconfigured"
	case err != nil:
		status = "invalid"
	}
	switch r.Method {
	case http.MethodGet:
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
		adminhttp.WriteJSON(w, map[string]any{"status": status, "models": models, "connections": connections})
	case http.MethodPost:
		var input struct {
			holmes.Scope
			Version int                `json:"version"`
			Config  holmes.ModelConfig `json:"config"`
		}
		if !adminhttp.Decode(w, r, &input) {
			http.Error(w, "配置请求格式无效", 400)
			return
		}
		action := r.URL.Query().Get("action")
		switch action {
		case "", "recheck":
			if err = holmes.CheckService(ctx, input.Scope); err != nil {
				http.Error(w, "Holmes 就绪检查失败", 503)
				return
			}
			adminhttp.WriteJSON(w, map[string]bool{"ready": true})
		case "discover":
			aliases, err := holmes.DiscoverModels(ctx, input.Scope)
			if err != nil {
				http.Error(w, "无法读取 Holmes 模型列表", 503)
				return
			}
			adminhttp.WriteJSON(w, map[string]any{"models": aliases})
		case "save", "publish":
			if workspace == nil || status != "configured" {
				http.Error(w, "AI 工作区或部署绑定不可用", 503)
				return
			}
			if !slices.ContainsFunc(models, func(m holmes.ConfiguredModel) bool { return m.Scope == input.Scope }) {
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
					if err != nil || !slices.Contains(aliases, cfg.Fast) || !slices.Contains(aliases, cfg.Accurate) {
						http.Error(w, "发布校验失败，Holmes 未返回所选模型。现行版本继续生效。", 409)
						return
					}
				}
				now := time.Now().UTC()
				entry.Data.Effective, entry.Data.EffectiveVersion, entry.Data.PublishedAt = cfg, entry.Version, &now
			}
			if err = holmes.SaveEntry(ctx, workspace, "connection", entry); err != nil {
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

func writeAIError(w http.ResponseWriter, err error) {
	if errors.Is(err, holmes.ErrConflict) {
		http.Error(w, err.Error(), 409)
		return
	}
	http.Error(w, "AI 工作区暂不可用", 503)
}
