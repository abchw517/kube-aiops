package portal

import (
	"errors"
	"net/http"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/holmes"
)

func serveAIServices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		models, err := holmes.ConfiguredModels()
		status := "configured"
		switch {
		case errors.Is(err, holmes.ErrUnconfigured), err == nil && len(models) == 0:
			status = "unconfigured"
		case err != nil:
			status = "invalid"
		}
		if models == nil {
			models = []holmes.ConfiguredModel{}
		}
		adminhttp.WriteJSON(w, struct {
			Status string                   `json:"status"`
			Models []holmes.ConfiguredModel `json:"models"`
		}{status, models})
	case http.MethodPost:
		var scope holmes.Scope
		if !adminhttp.Decode(w, r, &scope) {
			http.Error(w, "配置请求格式无效", http.StatusBadRequest)
			return
		}
		if err := holmes.CheckService(r.Context(), scope); err != nil {
			http.Error(w, "Holmes 健康检查失败，请核对范围绑定和服务连通性。", http.StatusServiceUnavailable)
			return
		}
		adminhttp.WriteJSON(w, map[string]bool{"ready": true})
	default:
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
