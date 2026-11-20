package permissions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
)

type groupAdministratorMember struct {
	ID       string   `json:"id"`
	Username string   `json:"username"`
	Eligible bool     `json:"eligible"`
	Before   []string `json:"before"`
	After    []string `json:"after"`
}

type groupAdministratorPreview struct {
	Version           string                     `json:"version"`
	AddsAdministrator bool                       `json:"addsAdministrator"`
	Members           []groupAdministratorMember `json:"members"`
}

func (state platformState) administratorSources(account platformAccount) []string {
	if !state.isAdministrator(account) {
		return nil
	}
	if account.Kind == "administrator" {
		return []string{"直接授予"}
	}
	return uniqueStrings(state.roleSources(account)[adminRole])
}

func (state platformState) proposedGroup(input platformChange) (platformGroup, error) {
	group := state.Groups[input.ID]
	if input.ID != "" && group.ID == "" {
		return platformGroup{}, errors.New("用户组不存在")
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 128 {
		return group, errors.New("请输入有效名称")
	}
	for id, other := range state.Groups {
		if id != input.ID && other.Name == name {
			return group, errors.New("用户组名称已使用")
		}
	}
	description := strings.TrimSpace(input.Description)
	if len([]rune(description)) > 256 {
		return group, errors.New("用途最多可填写 256 个字符")
	}
	if input.RoleIDs != nil {
		roles := uniqueStrings(*input.RoleIDs)
		for _, role := range roles {
			if state.role(role).ID == "" {
				return group, errors.New("存在不可分配的角色")
			}
		}
		group.Roles = roles
	}
	group.Name, group.Description = name, description
	return group, nil
}

func (state platformState) groupAdministratorImpact(before, after platformGroup) groupAdministratorPreview {
	impact := groupAdministratorPreview{AddsAdministrator: after.hasAdministratorRole(), Members: []groupAdministratorMember{}}
	for _, account := range state.Accounts {
		if before.ID == "" || account.Kind != "ldap" || !slices.Contains(account.Groups, before.ID) {
			continue
		}
		previous := state.administratorSources(account)
		var next []string
		if slices.Contains(account.Roles, adminRole) {
			next = append(next, "直接授予")
		}
		for _, groupID := range account.Groups {
			group := state.Groups[groupID]
			if groupID == before.ID {
				group = after
			}
			if group.hasAdministratorRole() {
				next = append(next, "用户组 "+group.Name)
			}
		}
		impact.Members = append(impact.Members, groupAdministratorMember{
			ID: account.ID, Username: account.Username, Eligible: !account.Disabled && !account.DirectoryDisabled && !account.MustChange,
			Before: previous, After: uniqueStrings(next),
		})
	}
	slices.SortFunc(impact.Members, func(a, b groupAdministratorMember) int { return strings.Compare(a.ID, b.ID) })
	// The version binds the proposed changes and all membership and source details shown in the preview.
	snapshot, _ := json.Marshal(struct {
		Before, After platformGroup
		Members       []groupAdministratorMember
	}{before, after, impact.Members})
	impact.Version = fmt.Sprintf("%x", sha256.Sum256(snapshot))
	return impact
}

func (state platformState) applyGroupAdministratorImpact(impact groupAdministratorPreview) string {
	action := "撤销系统管理员"
	if impact.AddsAdministrator {
		action = "关联系统管理员"
	}
	var changed, retained []string
	for _, member := range impact.Members {
		if (len(member.Before) != 0) != (len(member.After) != 0) {
			account := state.Accounts[member.ID]
			account.Generation = rand.Text()
			state.Accounts[member.ID] = account
			changed = append(changed, member.ID)
		} else if len(member.After) != 0 {
			retained = append(retained, member.ID+"（"+strings.Join(member.After, "、")+"）")
		}
	}
	return fmt.Sprintf("%s · 管理员身份变化 %d 人 %s · 保留管理员来源 %d 人 %s",
		action, len(changed), strings.Join(changed, "、"), len(retained), strings.Join(retained, "、"))
}

func (state platformState) saveGroup(input platformChange, super bool) changeResult {
	result := changeResult{ID: input.ID}
	before := state.Groups[input.ID]
	after, err := state.proposedGroup(input)
	if err != nil {
		result.Reason = err.Error()
		return result
	}
	if !super && (before.hasAdministratorRole() || after.hasAdministratorRole()) {
		result.Reason = "仅超级管理员可维护管理员组"
		result.administratorAudit = "变更管理员组角色"
		return result
	}
	if before.hasAdministratorRole() != after.hasAdministratorRole() {
		impact := state.groupAdministratorImpact(before, after)
		if input.GroupPreview != impact.Version {
			result.Reason = "用户组或成员授权已变化，请重新预览并确认"
			return result
		}
		result.administratorAudit = state.applyGroupAdministratorImpact(impact)
	}
	if after.ID == "" {
		after.ID = rand.Text()
	}
	state.Groups[after.ID] = after
	result.ID = after.ID
	return result
}

func (a *Admin) previewGroupAdministrator(w http.ResponseWriter, ctx context.Context, input platformChange, super bool) {
	if !super {
		http.Error(w, "仅超级管理员可预览管理员组变更", http.StatusForbidden)
		return
	}
	state, err := a.platform(ctx)
	if err != nil {
		http.Error(w, "系统管理暂不可用", http.StatusServiceUnavailable)
		return
	}
	before := state.Groups[input.ID]
	after, err := state.proposedGroup(input)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	if before.hasAdministratorRole() == after.hasAdministratorRole() {
		http.Error(w, "管理员角色没有变更，请刷新后重新核对", http.StatusConflict)
		return
	}
	adminhttp.WriteJSON(w, state.groupAdministratorImpact(before, after))
}

func annotateAdministratorAudit(event *operation, result changeResult) {
	if result.administratorAudit == "" {
		return
	}
	event.Risk = "高"
	if result.Reason == "" {
		event.Reason = result.administratorAudit
	}
}
