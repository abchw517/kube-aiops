package main

import (
	"crypto/rand"
	"encoding/json"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/abchw517/kube-aiops/internal/authorization"
)

const maxRoles = 128

func (a *identityAdmin) handleRoles(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		a.mu.RLock()
		document := struct {
			Roles       []authorization.Role `json:"roles"`
			LegacyRules []authorization.Rule `json:"legacyRules"`
		}{slices.Clone(a.roles), a.basePolicy.Rules}
		a.mu.RUnlock()
		writeAdminJSON(w, document)
		return
	}

	var spec authorization.RoleSpec
	var id string
	var revision int
	switch r.Method {
	case http.MethodPost, http.MethodPut:
		if !decodeAdmin(w, r, &spec) {
			http.Error(w, "角色配置无效。", http.StatusBadRequest)
			return
		}
		spec.Name = strings.TrimSpace(spec.Name)
		spec.Cluster = strings.TrimSpace(spec.Cluster)
		for _, selectors := range [][]string{spec.Subjects, spec.Groups} {
			for index, selector := range selectors {
				if strings.HasPrefix(selector, "ldap:") {
					selectors[index] = strings.ToLower(selector)
				}
			}
		}
	case http.MethodDelete:
	default:
		w.Header().Set("Allow", "GET, POST, PUT, DELETE")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.Method != http.MethodPost {
		args := r.URL.Query()
		id = args.Get("id")
		revision, _ = strconv.Atoi(args.Get("revision"))
		if id == "" || revision < 1 {
			http.Error(w, "缺少角色 ID 或版本。", http.StatusBadRequest)
			return
		}
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	next := slices.Clone(a.roles)
	if r.Method == http.MethodPost {
		if len(next) >= maxRoles {
			http.Error(w, "角色数量已达上限。", http.StatusConflict)
			return
		}
		id = rand.Text()
		next = append(next, authorization.Role{ID: id, Revision: 1, RoleSpec: spec})
	} else {
		index := slices.IndexFunc(next, func(role authorization.Role) bool { return role.ID == id })
		if index < 0 {
			http.Error(w, "该角色已不存在。", http.StatusNotFound)
			return
		}
		if next[index].Revision != revision {
			http.Error(w, "角色已更新，请重新打开后再操作。", http.StatusConflict)
			return
		}
		if r.Method == http.MethodDelete {
			next = slices.Delete(next, index, index+1)
		} else {
			next[index] = authorization.Role{ID: id, Revision: revision + 1, RoleSpec: spec}
		}
	}
	if r.Method != http.MethodDelete {
		for _, role := range next {
			if role.ID != id && strings.EqualFold(role.Name, spec.Name) {
				http.Error(w, "角色名称已存在。", http.StatusConflict)
				return
			}
		}
	}
	policy, err := a.basePolicy.WithRoles(next)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	document, err := json.Marshal(next)
	if err == nil {
		err = a.store.SaveRoles(r.Context(), document)
	}
	if err != nil {
		http.Error(w, "无法保存角色，请稍后重试。", http.StatusServiceUnavailable)
		return
	}
	a.roles, a.policy = next, policy
	slog.InfoContext(r.Context(), "identity administration audit", "operation", "roles", "method", r.Method, "role_id", id)
	w.WriteHeader(http.StatusNoContent)
}
