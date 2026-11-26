package portal

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"slices"
	"strings"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/holmes"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/sanitizer"
	"github.com/abchw517/kube-aiops/api/internal/wiki"
)

func serveAISkills(w http.ResponseWriter, r *http.Request, wikis *wiki.Service, actor identity.Principal) {
	ctx := r.Context()
	workspace := holmes.WorkspaceFrom(ctx)
	if workspace == nil {
		http.Error(w, "AI 工作区未初始化", 503)
		return
	}
	switch r.Method {
	case http.MethodGet:
		skills, err := holmes.ListEntries[holmes.Skill](ctx, workspace, "skill")
		if err != nil {
			writeAIError(w, err)
			return
		}
		adminhttp.WriteJSON(w, map[string]any{"skills": skills, "actor": actor.Subject})
	case http.MethodPost:
		var input struct {
			ID             string       `json:"id"`
			Version        int          `json:"version"`
			Scope          holmes.Scope `json:"scope"`
			Name           string       `json:"name"`
			Applies        string       `json:"applies"`
			Keyword        string       `json:"keyword"`
			Body           string       `json:"body"`
			ManualID       string       `json:"manualId"`
			ManualRevision int          `json:"manualRevision"`
		}
		if !adminhttp.Decode(w, r, &input) {
			http.Error(w, "技能请求格式无效", 400)
			return
		}
		action := r.URL.Query().Get("action")
		if action == "manual" || action == "upload" {
			models, err := holmes.ConfiguredModels()
			if err != nil || input.Scope.Source == "wiki" || !slices.ContainsFunc(models, func(m holmes.ConfiguredModel) bool { return m.Scope == input.Scope }) {
				http.Error(w, "请先选择有效的诊断范围绑定", 400)
				return
			}
			var revision int
			if action == "manual" {
				if input.ManualRevision <= 0 || input.Scope.Cluster == "" || input.Scope.Namespace == "" {
					http.Error(w, "手册技能需要已发布版本和明确的集群、命名空间", 400)
					return
				}
				doc, err := wikis.SkillManual(ctx, actor, input.ManualID, input.ManualRevision, input.Scope.Cluster, input.Scope.Namespace)
				if err != nil {
					http.Error(w, "该范围内没有可用的已发布排障手册", 404)
					return
				}
				if input.Name == "" {
					input.Name = doc.Title
				}
				if input.Applies == "" {
					input.Applies = doc.Metadata.Applicability
				}
				revision = doc.Revision
				if input.Body == "" {
					input.Body = doc.Metadata.Sections["checks"]
				}
				if input.Body == "" {
					http.Error(w, "手册缺少检查步骤，请填写技能正文", 400)
					return
				}
			} else {
				input.ManualID = ""
			}
			if len(input.Body) > 12<<10 || len(input.Name) > 180 || len(input.Applies) > 320 || len(input.Keyword) > 128 {
				http.Error(w, "技能内容超出长度限制", 400)
				return
			}
			name := strings.TrimSpace(sanitizer.WikiText(input.Name, 180))
			body := strings.TrimSpace(sanitizer.WikiText(input.Body, 12<<10))
			if name == "" || body == "" {
				http.Error(w, "请填写技能名称和正文", 400)
				return
			}
			var id [16]byte
			if _, err := rand.Read(id[:]); err != nil {
				writeAIError(w, err)
				return
			}
			entry := holmes.Entry[holmes.Skill]{ID: hex.EncodeToString(id[:]), Data: holmes.Skill{
				Scope: input.Scope, Name: name, Body: body, Applies: sanitizer.WikiText(input.Applies, 320), Keyword: strings.TrimSpace(input.Keyword),
				ManualID: input.ManualID, ManualRevision: revision, Author: actor.Subject, Status: "review",
			}}
			if err = holmes.SaveEntry(ctx, workspace, "skill", entry); err != nil {
				writeAIError(w, err)
				return
			}
			entry.Version = 1
			adminhttp.WriteJSON(w, entry)
			return
		}
		if action != "approve" && action != "disable" {
			http.Error(w, "不支持的操作", 400)
			return
		}
		entry, err := holmes.GetEntry[holmes.Skill](ctx, workspace, "skill", input.ID)
		if err != nil {
			writeAIError(w, err)
			return
		}
		if entry.Version == 0 {
			http.NotFound(w, r)
			return
		}
		if entry.Version != input.Version {
			writeAIError(w, holmes.ErrConflict)
			return
		}
		skill := &entry.Data
		if action == "approve" {
			if skill.Status != "review" || skill.Author == actor.Subject {
				http.Error(w, "待审核技能须由另一位管理员审核", 403)
				return
			}
			if skill.ManualID != "" {
				if _, err = wikis.SkillManual(ctx, actor, skill.ManualID, skill.ManualRevision, skill.Scope.Cluster, skill.Scope.Namespace); err != nil {
					http.Error(w, "原手册已更新、撤回或超出权限", 409)
					return
				}
			}
			skill.Status, skill.Reviewer = "enabled", actor.Subject
		} else {
			if skill.Status != "enabled" {
				writeAIError(w, holmes.ErrConflict)
				return
			}
			skill.Status = "disabled"
		}
		if err = holmes.SaveEntry(ctx, workspace, "skill", entry); err != nil {
			writeAIError(w, err)
			return
		}
		entry.Version++
		adminhttp.WriteJSON(w, entry)
	default:
		w.Header().Set("Allow", "GET, POST")
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}
