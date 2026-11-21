package permissions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"golang.org/x/crypto/bcrypt"
)

const (
	guestRole        = "guest-readonly"
	adminRole        = "system-admin"
	superSubject     = "local:admin"
	guestProvider    = "platform-guest"
	employeeProvider = "platform-ldap"
	pendingProvider  = "platform-password-required"
)

type accessScope struct {
	Environment string `json:"environment"`
	Cluster     string `json:"cluster,omitempty"`
	Namespace   string `json:"namespace,omitempty"`
	Business    string `json:"business,omitempty"`
}

type platformRole struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Scopes []accessScope    `json:"scopes"`
	Grants []interfaceGrant `json:"grants,omitempty"`
	Fixed  bool             `json:"fixed,omitempty"`
}

type platformGroup struct {
	Description string   `json:"description,omitempty"`
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Roles       []string `json:"roles"`
}

type platformAccount struct {
	ID                string   `json:"id"`
	Username          string   `json:"username"`
	Kind              string   `json:"kind"`
	Disabled          bool     `json:"disabled"`
	DirectoryDisabled bool     `json:"directoryDisabled"`
	MustChange        bool     `json:"mustChangePassword"`
	PasswordHash      string   `json:"passwordHash,omitempty"`
	Generation        string   `json:"generation"`
	Roles             []string `json:"roles"`
	Groups            []string `json:"groups"`
}

type platformState struct {
	Accounts    map[string]platformAccount `json:"accounts"`
	DeletedLDAP map[string]bool            `json:"deletedLDAP,omitempty"`
	Roles       map[string]platformRole    `json:"roles"`
	Groups      map[string]platformGroup   `json:"groups"`
}

func readPlatform(row *sql.Row) (platformState, error) {
	state := platformState{Accounts: map[string]platformAccount{}, DeletedLDAP: map[string]bool{}, Roles: map[string]platformRole{}, Groups: map[string]platformGroup{}}
	var raw []byte
	if err := row.Scan(&raw); err != nil {
		return state, err
	}
	err := json.Unmarshal(raw, &state)
	return state, err
}

func (a *Admin) platform(ctx context.Context) (platformState, error) {
	return readPlatform(a.store.QueryRowContext(ctx, "SELECT document FROM platform_identity WHERE id=1"))
}

func (a *Admin) editPlatform(ctx context.Context, change func(*platformState, *sql.Tx) error) error {
	tx, err := a.store.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	state, err := readPlatform(tx.QueryRowContext(ctx, "SELECT document FROM platform_identity WHERE id=1"))
	if err != nil {
		return err
	}
	if err := change(&state, tx); err != nil {
		return err
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE platform_identity SET document=? WHERE id=1", string(raw)); err != nil {
		return err
	}
	return tx.Commit()
}

func (state platformState) isAdministrator(account platformAccount) bool {
	if account.Kind == "administrator" {
		return true
	}
	if account.Kind != "ldap" {
		return false
	}
	if slices.Contains(account.Roles, adminRole) {
		return true
	}
	return slices.ContainsFunc(account.Groups, func(id string) bool {
		return state.Groups[id].hasAdministratorRole()
	})
}

func (group platformGroup) hasAdministratorRole() bool {
	return slices.Contains(group.Roles, adminRole)
}

func (state platformState) principal(account platformAccount) identity.Principal {
	provider := employeeProvider
	switch account.Kind {
	case "administrator":
		provider = AdminProvider
	case "guest":
		provider = guestProvider
	}
	if account.MustChange {
		provider = pendingProvider
	}
	return identity.Principal{
		Subject: account.ID, DisplayName: account.Username, Provider: provider,
		SessionVersion: account.Generation, Administrator: provider == employeeProvider && state.isAdministrator(account),
	}
}

func versionOf(hash string) string { return fmt.Sprintf("%x", sha256.Sum256([]byte(hash))) }

func (a *Admin) ValidateSession(ctx context.Context, principal identity.Principal) error {
	if principal.Subject == superSubject && principal.SessionVersion != "" {
		hash, _, err := a.store.Admin(ctx)
		if err != nil {
			return identity.ErrAuthenticationUnavailable
		}
		if versionOf(hash) != principal.SessionVersion {
			return identity.ErrUnauthenticated
		}
		return nil
	}
	if principal.SessionVersion == "" {
		return nil
	}
	state, err := a.platform(ctx)
	if err != nil {
		return identity.ErrAuthenticationUnavailable
	}
	account, exists := state.Accounts[principal.Subject]
	if !exists || account.Disabled || account.DirectoryDisabled || account.Generation != principal.SessionVersion ||
		principal.Provider == employeeProvider && principal.Administrator != state.isAdministrator(account) {
		return identity.ErrUnauthenticated
	}
	return nil
}

func (a *Admin) platformLocal(ctx context.Context, user, password string) (identity.Principal, bool, error) {
	state, err := a.platform(ctx)
	if err != nil {
		return identity.Principal{}, true, identity.ErrAuthenticationUnavailable
	}
	account, exists := state.Accounts["local:"+user]
	if !exists {
		return identity.Principal{}, false, nil
	}
	if account.Disabled || len(password) > 72 || bcrypt.CompareHashAndPassword([]byte(account.PasswordHash), []byte(password)) != nil {
		return identity.Principal{}, true, identity.ErrUnauthenticated
	}
	return state.principal(account), true, nil
}

func (a *Admin) registerEmployee(ctx context.Context, username string, p identity.Principal) (identity.Principal, error) {
	var principal identity.Principal
	err := a.editPlatform(ctx, func(state *platformState, _ *sql.Tx) error {
		account := state.Accounts[p.Subject]
		if state.DeletedLDAP[p.Subject] || account.Disabled {
			return identity.ErrUnauthenticated
		}
		if account.ID == "" {
			account = platformAccount{ID: p.Subject, Kind: "ldap", Generation: rand.Text()}
		}
		if account.DirectoryDisabled {
			account.Generation = rand.Text()
		}
		account.Username, account.DirectoryDisabled = username, false
		if len(state.roleSources(account)) == 0 {
			account.Roles = append(account.Roles, guestRole)
		}
		state.Accounts[account.ID] = account
		principal = state.principal(account)
		return nil
	})
	if errors.Is(err, identity.ErrUnauthenticated) {
		return identity.Principal{}, err
	}
	if err != nil {
		return identity.Principal{}, identity.ErrAuthenticationUnavailable
	}
	return principal, nil
}

func (state platformState) roleSources(account platformAccount) map[string][]string {
	sources := map[string][]string{}
	for _, id := range account.Roles {
		sources[id] = append(sources[id], "直接授予")
	}
	for _, id := range account.Groups {
		group := state.Groups[id]
		for _, role := range group.Roles {
			sources[role] = append(sources[role], "用户组 "+group.Name)
		}
	}
	return sources
}

func (state platformState) role(id string) platformRole {
	switch id {
	case guestRole:
		return platformRole{ID: id, Name: "访客只读", Fixed: true, Scopes: []accessScope{{Environment: "development"}, {Environment: "testing"}}}
	case adminRole:
		return platformRole{ID: id, Name: "系统管理员", Fixed: true}
	default:
		return state.Roles[id]
	}
}

type effectiveScope struct {
	accessScope
	Sources []string `json:"sources"`
}

func (state platformState) effective(account platformAccount) []effectiveScope {
	byScope := map[accessScope][]string{}
	for id, sources := range state.roleSources(account) {
		role := state.role(id)
		for _, scope := range role.Scopes {
			for _, source := range sources {
				byScope[scope] = append(byScope[scope], source+" → "+role.Name)
			}
		}
	}
	out := make([]effectiveScope, 0, len(byScope))
	for scope, sources := range byScope {
		slices.Sort(sources)
		out = append(out, effectiveScope{scope, slices.Compact(sources)})
	}
	slices.SortFunc(out, func(a, b effectiveScope) int {
		return strings.Compare(strings.Join([]string{a.Environment, a.Cluster, a.Business, a.Namespace}, ":"), strings.Join([]string{b.Environment, b.Cluster, b.Business, b.Namespace}, ":"))
	})
	return out
}

func (a *Admin) RecordLogin(ctx context.Context, username, subject string, status int) {
	result := "失败"
	if status == 204 {
		result = "成功"
	}
	// 停用低风险审计时仍更新账号最近登录信息，但不落登录审计记录。
	if status == 204 && subject != "" {
		if _, err := a.store.ExecContext(ctx, `INSERT INTO account_last_login(actor,created_at)
			VALUES (?,?)
			ON CONFLICT(actor) DO UPDATE SET created_at=MAX(account_last_login.created_at,excluded.created_at)`,
			subject, time.Now().Unix()); err != nil {
			slog.WarnContext(ctx, "cannot update account last login", "error", err)
		}
	}
	_ = writeOperation(ctx, a.store, operation{Actor: subject, Action: "登录", Risk: "低", Target: username, Result: result})
}

type operation struct {
	ID     int64  `json:"id"`
	At     int64  `json:"at"`
	Actor  string `json:"actor"`
	Action string `json:"action"`
	Risk   string `json:"risk"`
	Target string `json:"target"`
	Result string `json:"result"`
	Reason string `json:"reason"`
	Batch  string `json:"batch"`
}

type auditExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func writeOperation(ctx context.Context, db auditExecutor, event operation) error {
	_, err := storeOperation(ctx, db, event, false)
	return err
}

// storeOperation optionally issues a stable link for a persisted event.
func storeOperation(ctx context.Context, db auditExecutor, event operation, link bool) (string, error) {
	if event.Result == "" {
		event.Result = "成功"
	}
	reference := ""
	if link {
		reference = rand.Text()
	}
	result, err := db.ExecContext(ctx, `INSERT INTO operation_audit(created_at,actor,action,risk,target,result,reason,batch,reference)
  SELECT ?,?,?,?,?,?,?,?,? WHERE NOT EXISTS
  (SELECT 1 FROM operation_audit_retention WHERE risk=? AND days=0)`,
		time.Now().Unix(), event.Actor, event.Action, event.Risk, event.Target, event.Result, event.Reason, event.Batch, reference, event.Risk)
	if err != nil || !link {
		return "", err
	}
	if count, err := result.RowsAffected(); err != nil || count == 0 {
		return "", err
	}
	return reference, nil
}

func (a *Admin) RecordOperation(ctx context.Context, actor, action, risk, target string) {
	_ = writeOperation(ctx, a.store, operation{Actor: actor, Action: action, Risk: risk, Target: target})
}

// RecordDiagnosticOperation stores an incident mutation atomically with its timeline.
func RecordDiagnosticOperation(ctx context.Context, tx *sql.Tx, actor, action, target string) error {
	label := map[string]string{
		"created": "创建故障", "updated": "更新故障", "correlated": "采集故障证据",
		"merged": "归并故障", "merged_into": "接收归并故障", "split": "拆分故障", "split_created": "创建拆分故障",
		"topology_changed": "维护故障拓扑", "rca_queued": "启动根因分析", "rca_completed": "完成根因分析",
		"rca_canceled": "取消根因分析", "rca_saved": "保存根因分析结论", "signal_received": "接收连接器信号",
		"datasource_saved": "维护诊断数据源", "datasource_checked": "测试诊断数据源", "policy_saved": "维护诊断策略",
	}[action]
	return writeOperation(ctx, tx, operation{Actor: actor, Action: label, Risk: "中", Target: target})
}

func (a *Admin) OperationGroupExists(ctx context.Context, id string) (bool, error) {
	state, err := a.platform(ctx)
	_, found := state.Groups[id]
	return found, err
}
