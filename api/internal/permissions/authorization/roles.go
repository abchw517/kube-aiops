package authorization

import (
	"fmt"
	"slices"
	"strings"
)

// Role assigns permission modules to identity selectors within one cluster.
type Role struct {
	ID       string `json:"id"`
	Revision int    `json:"revision"`
	RoleSpec
}

type RoleSpec struct {
	Name       string       `json:"name"`
	Subjects   []string     `json:"subjects"`
	Groups     []string     `json:"groups"`
	Modules    []Capability `json:"modules"`
	Cluster    string       `json:"cluster"`
	Namespaces []string     `json:"namespaces"`
}

func (role RoleSpec) rules() ([]Rule, error) {
	if strings.Contains(role.Cluster, ",") {
		ids := strings.Split(role.Cluster, ",")
		if len(ids) > 64 {
			return nil, fmt.Errorf("每个角色最多选择 64 个实例")
		}
		seen := map[string]bool{}
		var rules []Rule
		for _, raw := range ids {
			id := strings.TrimSpace(raw)
			if seen[id] {
				continue
			}
			seen[id] = true
			instance := role
			instance.Cluster = id
			scoped, err := instance.rules()
			if err != nil {
				return nil, err
			}
			rules = append(rules, scoped...)
		}
		return rules, nil
	}

	if validateSelector("role name", role.Name) != nil || len(role.Name) > 128 {
		return nil, fmt.Errorf("角色名称不能为空，长度不能超过 128 字节")
	}
	members := len(role.Subjects) + len(role.Groups)
	if members > 64 {
		return nil, fmt.Errorf("每个角色最多指定 64 个用户或组")
	}
	for _, selectors := range [][]string{role.Subjects, role.Groups} {
		for _, selector := range selectors {
			provider, name, ok := strings.Cut(selector, ":")
			if !ok || (provider != "local" && provider != "ldap") || name == "" || name != strings.TrimSpace(name) {
				return nil, fmt.Errorf("身份需要使用有效的本地用户名、组名或 LDAP DN")
			}
		}
	}
	if err := (Scope{Cluster: role.Cluster}).Validate(); err != nil {
		return nil, fmt.Errorf("请选择一个有效的授权范围")
	}
	if len(role.Namespaces) > 64 || (len(role.Namespaces) > 1 && slices.Contains(role.Namespaces, GlobalCluster)) {
		return nil, fmt.Errorf("请选择最多 64 个命名空间，或单独填写 *")
	}
	scopes := make([]Scope, 0, len(role.Namespaces)+1)
	for _, namespace := range role.Namespaces {
		scope := Scope{Cluster: role.Cluster, Namespace: namespace}
		if err := scope.Validate(); namespace == "" || err != nil {
			return nil, fmt.Errorf("命名空间无效")
		}
		scopes = append(scopes, scope)
	}
	if len(role.Modules) == 0 || len(role.Modules) > len(knownCapabilities) {
		return nil, fmt.Errorf("请选择至少一个有效权限模块")
	}

	rules := make([]Rule, 0, len(role.Modules))
	seen := make(map[Capability]bool, len(role.Modules))
	for _, module := range role.Modules {
		if module.Validate() != nil || seen[module] {
			return nil, fmt.Errorf("权限模块无效或重复")
		}
		catalogModule := module == CapabilityEnvironmentsManage || module == CapabilityBusinessLinesManage
		if role.Cluster == GlobalCluster && !catalogModule {
			return nil, fmt.Errorf("请选择一个具体集群")
		}
		seen[module] = true
		rule := Rule{Effect: EffectAllow, Subjects: role.Subjects, Groups: role.Groups, Capabilities: []Capability{module}, Scopes: scopes}
		switch module {
		case CapabilityEnvironmentsManage, CapabilityBusinessLinesManage:
			if role.Cluster != GlobalCluster || len(scopes) != 0 {
				return nil, fmt.Errorf("环境和业务线管理需要平台范围")
			}
			rule.Scopes = []Scope{GlobalScope()}
		case CapabilityClustersList:
			entry := rule
			entry.Scopes = []Scope{GlobalScope()}
			entry.collectionOnly = true
			rules = append(rules, entry)
			rule.Scopes = []Scope{ClusterScope(role.Cluster)}
		case CapabilityNamespacesList:
			rule.Scopes = []Scope{ClusterScope(role.Cluster)}
		case CapabilityFindingsList, CapabilityFindingsSummary:
			if len(scopes) == 0 {
				return nil, fmt.Errorf("诊断列表和汇总需要指定命名空间")
			}
			// A namespace-scoped role can open the unfiltered Portal view, but each
			// result must pass the separate namespace-scoped check.
			entry := rule
			entry.Scopes = []Scope{ClusterScope(role.Cluster)}
			entry.collectionOnly = true
			rules = append(rules, entry)
		default:
			if len(scopes) == 0 {
				return nil, fmt.Errorf("诊断详情、资源详情和关联证据需要指定命名空间")
			}
		}
		if members > 0 {
			if err := validateRule(rule); err != nil {
				return nil, fmt.Errorf("用户或组无效，请检查格式和长度")
			}
		}
		rules = append(rules, rule)
	}
	if members == 0 {
		return nil, nil
	}
	return rules, nil
}

// WithRoles adds allow rules without weakening existing explicit denies.
func (p Policy) WithRoles(roles []Role) (*PolicyAuthorizer, error) {
	p.Rules = slices.Clone(p.Rules)
	for _, role := range roles {
		rules, err := role.RoleSpec.rules()
		if err != nil {
			return nil, err
		}
		p.Rules = append(p.Rules, rules...)
	}
	return NewPolicyAuthorizer(p)
}
