package permissions

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"golang.org/x/crypto/bcrypt"
)

var accountNamePattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.@-]{0,127}$`)

type accountView struct {
	ID                string           `json:"id"`
	Username          string           `json:"username"`
	Kind              string           `json:"kind"`
	Disabled          bool             `json:"disabled"`
	DirectoryDisabled bool             `json:"directoryDisabled"`
	MustChange        bool             `json:"mustChangePassword"`
	Roles             []string         `json:"roles"`
	Groups            []string         `json:"groups"`
	Effective         []effectiveScope `json:"effective"`
}

type platformChange struct {
	Action   string
	ID       string
	IDs      []string
	Values   []string
	Name     string
	Kind     string
	Password string
	Scopes   []accessScope
}

type changeResult struct {
	ID     string `json:"id"`
	Reason string `json:"reason,omitempty"`
}

func (a *Admin) handlePlatform(w http.ResponseWriter, r *http.Request, resource string, actor identity.Principal) {
	ctx := r.Context()
	if resource == "operations" {
		if r.Method != http.MethodGet {
			w.WriteHeader(405)
			return
		}
		a.handleOperations(w, r)
		return
	}
	if r.Method == http.MethodGet {
		state, err := a.platform(ctx)
		if err != nil {
			http.Error(w, "系统管理暂不可用", 503)
			return
		}
		if resource == "accounts" {
			users := make([]accountView, 0, len(state.Accounts)+1)
			if _, initial, err := a.store.Admin(ctx); err == nil {
				users = append(users, accountView{ID: superSubject, Username: "admin", Kind: "super", MustChange: initial, Roles: []string{adminRole}})
			}
			for _, account := range state.Accounts {
				users = append(users, accountView{account.ID, account.Username, account.Kind, account.Disabled, account.DirectoryDisabled, account.MustChange, account.Roles, account.Groups, state.effective(account)})
			}
			slices.SortFunc(users, func(a, b accountView) int { return strings.Compare(a.ID, b.ID) })
			adminhttp.WriteJSON(w, users)
			return
		}
		roles := []platformRole{state.role(guestRole), state.role(adminRole)}
		for _, role := range state.Roles {
			roles = append(roles, role)
		}
		groups := make([]platformGroup, 0, len(state.Groups))
		for _, group := range state.Groups {
			groups = append(groups, group)
		}
		slices.SortFunc(roles, func(a, b platformRole) int { return strings.Compare(a.Name, b.Name) })
		slices.SortFunc(groups, func(a, b platformGroup) int { return strings.Compare(a.Name, b.Name) })
		adminhttp.WriteJSON(w, map[string]any{"roles": roles, "groups": groups})
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var input platformChange
	if !adminhttp.Decode(w, r, &input) || len(input.IDs) > 100 || len(input.Values) > 50 || len(input.Scopes) > 100 {
		http.Error(w, "请求无效或超出批量上限", 400)
		return
	}
	if resource == "access" && input.Action == "role-save" {
		for _, scope := range input.Scopes {
			if !validAccessScope(scope) {
				http.Error(w, "授权范围无效。生产和预发布须指定集群、业务线和命名空间", 400)
				return
			}
			if scope.Cluster == "" {
				continue
			}
			if a.clusterEnvironment == nil {
				http.Error(w, "集群目录暂不可用", 503)
				return
			}
			environment, err := a.clusterEnvironment(ctx, scope.Cluster)
			if err != nil {
				http.Error(w, "集群目录暂不可用", 503)
				return
			}
			if environment != scope.Environment {
				http.Error(w, "所选集群不属于该环境", 409)
				return
			}
			if scope.Business != "" {
				if a.namespaceBusiness == nil {
					http.Error(w, "命名空间归属暂不可用", 503)
					return
				}
				assigned, err := a.namespaceBusiness(ctx, scope.Cluster, scope.Namespace, scope.Business)
				if err != nil {
					http.Error(w, "命名空间归属暂不可用", 503)
					return
				}
				if !assigned {
					http.Error(w, "所选命名空间未分配给该业务线", 409)
					return
				}
			}
		}
	}
	var hash string
	if resource == "accounts" && (input.Action == "create" || input.Action == "reset") {
		if !validNewPassword(input.Password) {
			http.Error(w, "临时密码须为 12 至 72 字节", 400)
			return
		}
		encoded, err := bcrypt.GenerateFromPassword([]byte(input.Password), 12)
		if err != nil {
			http.Error(w, "密码处理失败", 503)
			return
		}
		hash = string(encoded)
	}
	batch := ""
	if len(input.IDs) > 0 || input.Action == "group-roles-add" || input.Action == "group-roles-remove" {
		batch = rand.Text()
	}
	ids := input.IDs
	if len(ids) == 0 {
		ids = []string{input.ID}
	}
	if batch != "" && input.Action != "group-roles-add" && input.Action != "group-roles-remove" &&
		input.Action != "join" && input.Action != "leave" && input.Action != "grant" && input.Action != "revoke" {
		http.Error(w, "不支持该批量操作", 400)
		return
	}
	results := make([]changeResult, 0, len(ids))
	action, risk := labelChange(input.Action)
	err := a.editPlatform(ctx, func(state *platformState, tx *sql.Tx) error {
		if actor.Subject != superSubject {
			current := state.Accounts[actor.Subject]
			if current.Kind != "administrator" || current.Disabled || current.MustChange || current.Generation != actor.SessionVersion {
				return identity.ErrUnauthenticated
			}
		}
		if resource == "accounts" && input.Action == "create" {
			var legacy bool
			err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM local_accounts WHERE lower(username)=lower(?))", input.Name).Scan(&legacy)
			if err != nil {
				return err
			}
			if legacy {
				results = append(results, changeResult{ID: "local:" + input.Name, Reason: "账号已存在"})
				return nil
			}
		}
		if batch != "" && resource == "access" {
			for _, role := range uniqueStrings(input.Values) {
				result := applyGroupRole(*state, input, role)
				results = append(results, result)
				event := operation{Actor: actor.Subject, Action: action, Risk: risk, Target: input.ID + " → " + role, Batch: batch, Reason: result.Reason}
				if result.Reason != "" {
					event.Result = "失败"
				}
				if err := writeOperation(ctx, tx, event); err != nil {
					return err
				}
			}
		} else {
			for _, id := range uniqueStrings(ids) {
				input.ID = id
				result := changeResult{ID: id}
				if resource == "accounts" {
					result = applyAccount(*state, input, hash, actor.Subject == superSubject)
				} else {
					result = applyAccess(*state, input)
				}
				results = append(results, result)
				if result.Reason == "" || batch != "" {
					event := operation{Actor: actor.Subject, Action: action, Risk: risk, Target: result.ID, Batch: batch, Reason: result.Reason}
					if result.Reason != "" {
						event.Result = "失败"
					}
					if err := writeOperation(ctx, tx, event); err != nil {
						return err
					}
				}
			}
		}
		if batch != "" {
			success := 0
			for _, result := range results {
				if result.Reason == "" {
					success++
				}
			}
			outcome := "成功"
			if success == 0 {
				outcome = "失败"
			} else if success < len(results) {
				outcome = "部分成功"
			}
			return writeOperation(ctx, tx, operation{Actor: actor.Subject, Action: action, Risk: risk, Target: "批量操作", Batch: batch, Result: outcome})
		}
		return nil
	})
	if errors.Is(err, identity.ErrUnauthenticated) {
		http.Error(w, "会话失效", 401)
		return
	}
	if err != nil {
		http.Error(w, "系统管理暂不可用", 503)
		return
	}
	if batch == "" && len(results) == 1 && results[0].Reason != "" {
		http.Error(w, results[0].Reason, 409)
		return
	}
	adminhttp.WriteJSON(w, results)
}

func labelChange(action string) (string, string) {
	switch action {
	case "create":
		return "创建账号", "高"
	case "delete":
		return "删除管理员", "高"
	case "reset":
		return "重置密码", "高"
	case "disable":
		return "停用账号", "高"
	case "enable":
		return "启用账号", "中"
	case "join":
		return "加入用户组", "中"
	case "leave":
		return "移出用户组", "中"
	case "grant":
		return "授予直接角色", "高"
	case "revoke":
		return "撤销直接角色", "高"
	case "group-roles-add", "group-roles-remove":
		return "变更用户组角色", "高"
	case "role-save", "role-delete":
		return "维护角色", "高"
	default:
		return "维护用户组", "中"
	}
}

func uniqueStrings(values []string) []string {
	result := slices.Clone(values)
	slices.Sort(result)
	return slices.Compact(result)
}

func applyAccount(state platformState, input platformChange, hash string, super bool) changeResult {
	result := changeResult{ID: input.ID}
	fail := func(reason string) changeResult { result.Reason = reason; return result }
	if input.Action == "create" {
		if !accountNamePattern.MatchString(input.Name) || strings.EqualFold(input.Name, "admin") ||
			(input.Kind != "guest" && input.Kind != "administrator") || input.Kind == "administrator" && !super {
			return fail("账号类型、名称或创建权限无效")
		}
		result.ID = "local:" + input.Name
		if _, exists := state.Accounts[result.ID]; exists {
			return fail("账号已存在")
		}
		role := guestRole
		if input.Kind == "administrator" {
			role = adminRole
		}
		state.Accounts[result.ID] = platformAccount{ID: result.ID, Username: input.Name, Kind: input.Kind, PasswordHash: hash, MustChange: true, Generation: rand.Text(), Roles: []string{role}}
		return result
	}
	account, exists := state.Accounts[input.ID]
	if !exists {
		return fail("账号不可修改或不存在")
	}
	if account.Kind == "administrator" && !super {
		return fail("仅超级管理员可维护其他管理员")
	}
	switch input.Action {
	case "delete":
		if account.Kind != "administrator" {
			return fail("仅普通系统管理员可以删除")
		}
		delete(state.Accounts, input.ID)
		return result
	case "reset":
		if account.Kind == "ldap" {
			return fail("LDAP 密码在公司目录修改")
		}
		account.PasswordHash, account.MustChange, account.Generation = hash, true, rand.Text()
	case "disable", "enable":
		if account.Kind == "administrator" {
			return fail("普通管理员支持删除和重置密码")
		}
		disabled := input.Action == "disable"
		if account.Disabled == disabled {
			return fail("账号已处于目标状态")
		}
		account.Disabled, account.Generation = disabled, rand.Text()
	case "join", "leave", "grant", "revoke":
		if account.Kind != "ldap" {
			return fail("本地账号只能使用固定角色")
		}
		if len(input.Values) == 0 {
			return fail("请至少选择一项")
		}
		adding := input.Action == "join" || input.Action == "grant"
		isGroup := input.Action == "join" || input.Action == "leave"
		targets := account.Roles
		if isGroup {
			targets = account.Groups
		}
		for _, value := range uniqueStrings(input.Values) {
			if isGroup && state.Groups[value].ID == "" || !isGroup && (value == adminRole || state.role(value).ID == "") {
				return fail("目标用户组或角色不可分配")
			}
			if !adding && !slices.Contains(targets, value) {
				return fail("当前账号未关联所选项")
			}
		}
		targets = slices.Clone(targets)
		for _, value := range uniqueStrings(input.Values) {
			if adding {
				if !slices.Contains(targets, value) {
					targets = append(targets, value)
				}
			} else {
				targets = slices.DeleteFunc(targets, func(id string) bool { return id == value })
			}
		}
		if isGroup {
			account.Groups = targets
		} else {
			account.Roles = targets
		}
		if input.Action == "revoke" && slices.Contains(input.Values, guestRole) && len(state.roleSources(account)) == 0 {
			return fail("须保留其他直接或继承角色")
		}
	default:
		return fail("不支持该操作")
	}
	state.Accounts[input.ID] = account
	return result
}

func applyGroupRole(state platformState, input platformChange, role string) changeResult {
	result := changeResult{ID: role}
	group := state.Groups[input.ID]
	switch {
	case group.ID == "":
		result.Reason = "用户组不存在"
	case role == adminRole || state.role(role).ID == "":
		result.Reason = "角色不可分配"
	case input.Action == "group-roles-add":
		if slices.Contains(group.Roles, role) {
			result.Reason = "用户组已关联该角色"
			break
		}
		group.Roles = append(group.Roles, role)
	case input.Action == "group-roles-remove":
		if !slices.Contains(group.Roles, role) {
			result.Reason = "用户组未关联该角色"
			break
		}
		group.Roles = slices.DeleteFunc(group.Roles, func(id string) bool { return id == role })
	}
	if result.Reason == "" {
		state.Groups[group.ID] = group
	}
	return result
}

func applyAccess(state platformState, input platformChange) changeResult {
	result := changeResult{ID: input.ID}
	fail := func(reason string) changeResult { result.Reason = reason; return result }
	name := strings.TrimSpace(input.Name)
	switch input.Action {
	case "role-save", "group-save":
		if name == "" || len(name) > 128 {
			return fail("请输入有效名称")
		}
		if result.ID == "" {
			result.ID = rand.Text()
		}
		if input.Action == "role-save" {
			if state.role(input.ID).Fixed || input.ID != "" && state.Roles[input.ID].ID == "" {
				return fail("该角色不可编辑")
			}
			if name == state.role(guestRole).Name || name == state.role(adminRole).Name {
				return fail("角色名称已使用")
			}
			for id, role := range state.Roles {
				if id != result.ID && role.Name == name {
					return fail("角色名称已使用")
				}
			}
			if len(input.Scopes) == 0 {
				return fail("至少配置一项历史诊断查看范围")
			}
			for _, scope := range input.Scopes {
				if !validAccessScope(scope) {
					return fail("授权范围无效，生产与预发布须指定业务线和命名空间")
				}
			}
			state.Roles[result.ID] = platformRole{ID: result.ID, Name: name, Scopes: input.Scopes}
		} else {
			group := state.Groups[input.ID]
			if input.ID != "" && group.ID == "" {
				return fail("用户组不存在")
			}
			for id, other := range state.Groups {
				if id != result.ID && other.Name == name {
					return fail("用户组名称已使用")
				}
			}
			group.ID, group.Name = result.ID, name
			state.Groups[group.ID] = group
		}
	case "role-delete":
		if state.Roles[input.ID].ID == "" {
			return fail("预置角色不可删除，或角色不存在")
		}
		var refs []string
		for _, group := range state.Groups {
			if slices.Contains(group.Roles, input.ID) {
				refs = append(refs, "用户组 "+group.Name)
			}
		}
		for _, account := range state.Accounts {
			if slices.Contains(account.Roles, input.ID) {
				refs = append(refs, "账号 "+account.Username)
			}
		}
		if len(refs) > 0 {
			return fail("请先解除关联：" + strings.Join(uniqueStrings(refs), "、"))
		}
		delete(state.Roles, input.ID)
	case "group-delete":
		group := state.Groups[input.ID]
		if group.ID == "" {
			return fail("用户组不存在")
		}
		var refs []string
		for _, role := range group.Roles {
			refs = append(refs, "角色 "+state.role(role).Name)
		}
		for _, account := range state.Accounts {
			if slices.Contains(account.Groups, input.ID) {
				refs = append(refs, "账号 "+account.Username)
			}
		}
		if len(refs) > 0 {
			return fail("请先解除关联：" + strings.Join(uniqueStrings(refs), "、"))
		}
		delete(state.Groups, input.ID)
	default:
		return fail("不支持该操作")
	}
	return result
}

func (a *Admin) handleOperations(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	rows, err := a.store.QueryContext(r.Context(), `SELECT id,created_at,actor,action,risk,target,result,reason,batch FROM operation_audit
		WHERE (?='' OR action=?) AND (?='' OR risk=?) AND (?='' OR instr(lower(actor),lower(?))>0 OR instr(lower(target),lower(?))>0)
		AND (?='' OR batch=?) AND (?='' OR id<CAST(? AS INTEGER)) ORDER BY id DESC LIMIT 101`, q.Get("action"), q.Get("action"), q.Get("risk"), q.Get("risk"),
		q.Get("account"), q.Get("account"), q.Get("account"), q.Get("batch"), q.Get("batch"), q.Get("before"), q.Get("before"))
	if err != nil {
		http.Error(w, "审计记录暂不可用", 503)
		return
	}
	defer rows.Close()
	items := make([]operation, 0)
	for rows.Next() {
		var item operation
		if rows.Scan(&item.ID, &item.At, &item.Actor, &item.Action, &item.Risk, &item.Target, &item.Result, &item.Reason, &item.Batch) != nil {
			http.Error(w, "审计记录暂不可用", 503)
			return
		}
		items = append(items, item)
	}
	if rows.Err() != nil {
		http.Error(w, "审计记录暂不可用", 503)
		return
	}
	var next int64
	if len(items) > 100 {
		items = items[:100]
		next = items[99].ID
	}
	adminhttp.WriteJSON(w, map[string]any{"items": items, "before": next})
}

func (a *Admin) changePlatformPassword(ctx context.Context, p identity.Principal, current, next string) error {
	state, err := a.platform(ctx)
	if err != nil {
		return err
	}
	account := state.Accounts[p.Subject]
	if account.Generation == "" || account.Generation != p.SessionVersion || account.Kind == "ldap" || account.Disabled ||
		bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(current)) != nil {
		return identity.ErrUnauthenticated
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(next), 12)
	if err != nil {
		return err
	}
	return a.editPlatform(ctx, func(state *platformState, tx *sql.Tx) error {
		account := state.Accounts[p.Subject]
		if account.Generation != p.SessionVersion {
			return identity.ErrUnauthenticated
		}
		account.PasswordHash, account.MustChange, account.Generation = string(hash), false, rand.Text()
		state.Accounts[p.Subject] = account
		return writeOperation(ctx, tx, operation{Actor: p.Subject, Action: "修改密码", Risk: "高", Target: p.Subject})
	})
}
