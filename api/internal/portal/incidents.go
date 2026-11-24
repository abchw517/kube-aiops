package portal

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/abchw517/kube-aiops/api/internal/diagnosis/assetmeta"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/finding"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/incident"
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/kubernetes"
	"github.com/abchw517/kube-aiops/api/internal/permissions"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/catalog"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/sanitizer"
)

func configureIncidents(ctx context.Context, store *identitystore.Store, admin *permissions.Admin, sessions *identity.Sessions, o *onboarding, local correlation.Correlator) (*incident.Server, error) {
	if o == nil {
		return nil, nil
	}
	repository, err := incident.Open(ctx, store.DB, permissions.RecordDiagnosticOperation)
	if err != nil {
		return nil, err
	}
	environments := strings.FieldsFunc(os.Getenv("DIAGNOSTIC_INCIDENT_ENVIRONMENTS"), func(r rune) bool { return r == ',' || r == ' ' })
	allowedEnvironment := func(id string) bool { return len(environments) == 0 || slices.Contains(environments, id) }
	scopeActive := func(ctx context.Context, scope incident.Scope) (bool, error) {
		env, err := o.clusterEnvironment(ctx, scope.ClusterID)
		if err != nil || env == "" || !allowedEnvironment(env) || scope.EnvironmentID != "" && scope.EnvironmentID != env {
			return false, err
		}
		if scope.BusinessID != "" && scope.Namespace != "" {
			return o.incidentBusinessMatches(ctx, scope, env)
		}
		return true, nil
	}
	authorize := func(ctx context.Context, p identity.Principal, code string, scope incident.Scope, collection bool) (bool, error) {
		if scope.ClusterID == "" {
			return admin.AuthorizeOperation(ctx, p, code, "", authorization.GlobalScope(), collection)
		}
		resourceScope := authorization.NamespaceScope(scope.ClusterID, scope.Namespace)
		enabled, err := scopeActive(ctx, scope)
		if err != nil || !enabled {
			return false, err
		}
		var legacy authorization.Capability
		if code == incident.EvidencePermission {
			legacy = authorization.CapabilityCorrelationsRead
		}
		if collection {
			return admin.AuthorizeOperation(ctx, p, code, legacy, resourceScope, true)
		}
		return admin.AuthorizeBusinessOperation(ctx, p, code, legacy, resourceScope, false, scope.BusinessID)
	}
	automation := strings.FieldsFunc(os.Getenv("DIAGNOSTIC_POLICY_ENVIRONMENTS"), func(r rune) bool { return r == ',' || r == ' ' })
	zabbix := strings.FieldsFunc(os.Getenv("DIAGNOSTIC_ZABBIX_ENDPOINTS"), func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
	assets := assetmeta.New(os.Getenv("DIAGNOSTIC_CREDENTIALS_DIR"), zabbix)
	server := &incident.Server{
		Observability:       configureObservability(store, o),
		Zabbix:              configureZabbix(store, admin, assets, o, allowedEnvironment),
		IngressScopeAllowed: scopeActive,
		Assets:              assets,
		ValidateActor:       admin.ValidateSession,
		ResolveScope:        o.resolveConnectionScope,
		AutomationEnabled:   func(scope incident.Scope) bool { return slices.Contains(automation, scope.EnvironmentID) },
		Store:               repository, Origin: admin.Origin(), Authenticate: sessions.Authenticate, Authorize: authorize, ActiveGroup: admin.OperationGroupExists,
		Resolve: func(ctx context.Context, p identity.Principal, req incident.CreateRequest) (incident.Incident, error) {
			return o.resolveIncident(ctx, p, admin, authorize, req)
		},
	}
	server.Collect = func(ctx context.Context, i incident.Incident) (correlation.CorrelationBundle, error) {
		target, ok := o.scopedCluster(i.Scope.ClusterID)
		if !ok {
			return correlation.CorrelationBundle{}, correlation.ErrUnavailable
		}
		service := target.Correlator
		if service == nil && i.Scope.ClusterID == "local" {
			service = local
		}
		if service == nil {
			return correlation.CorrelationBundle{}, correlation.ErrUnavailable
		}
		anchor := time.Now().UTC()
		if i.FindingID != "" && anchor.Sub(i.AnchorAt) > correlation.DefaultWindow {
			anchor = i.AnchorAt
		}
		service, err := applyObservability(ctx, server, i.Scope, service)
		if err != nil {
			return correlation.CorrelationBundle{}, correlation.ErrUnavailable
		}
		return service.Correlate(ctx, correlation.Request{
			FindingID: i.ID, AnchorTime: anchor.Format(time.RFC3339),
			Scope: correlation.CorrelationScope{Cluster: i.Scope.ClusterID, Namespace: i.Scope.Namespace, Resource: i.Resource},
		})
	}
	return server, nil
}

func (o *onboarding) incidentBusinesses(ctx context.Context, scope incident.Scope, env string) ([]string, error) {
	rows, err := o.store.QueryContext(ctx, `SELECT DISTINCT n.business FROM namespace_business n
		JOIN resource_catalog_ids ids ON ids.kind='business-lines' AND ids.stable_key=n.business
		WHERE n.cluster=? AND n.namespace=? ORDER BY n.business`, scope.ClusterID, scope.Namespace)
	if err != nil {
		return nil, err
	}
	var businesses []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		businesses = append(businesses, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	if len(businesses) > 0 || env == "production" || env == "staging" {
		return businesses, nil
	}
	if target, ok := o.scopedCluster(scope.ClusterID); ok {
		for _, cluster := range target.Backend.Clusters() {
			if cluster.ID != scope.ClusterID || cluster.Business == "" {
				continue
			}
			key, err := catalogStableKey(ctx, o.store, "business-lines", cluster.Business)
			if err != nil {
				return nil, err
			}
			if key != "" {
				return []string{key}, nil
			}
		}
	}
	return nil, nil
}

func (o *onboarding) incidentBusinessMatches(ctx context.Context, scope incident.Scope, env string) (bool, error) {
	businesses, err := o.incidentBusinesses(ctx, scope, env)
	return slices.Contains(businesses, scope.BusinessID), err
}

func (o *onboarding) resolveConnectionScope(ctx context.Context, input incident.Scope) (incident.Scope, error) {
	scope, err := o.resolveDiagnosticScope(ctx, input)
	if err != nil {
		return incident.Scope{}, err
	}
	target, ok := o.scopedCluster(scope.ClusterID)
	if !ok {
		return incident.Scope{}, incident.ErrBadScope
	}
	namespaces, err := target.Backend.ListNamespaces(ctx)
	if err != nil {
		return incident.Scope{}, err
	}
	if !slices.ContainsFunc(namespaces, func(item kubernetes.Namespace) bool { return item.Name == scope.Namespace }) {
		return incident.Scope{}, incident.ErrBadScope
	}
	return scope, nil
}

func (o *onboarding) resolveDiagnosticScope(ctx context.Context, scope incident.Scope) (incident.Scope, error) {
	env, err := o.clusterEnvironment(ctx, scope.ClusterID)
	if err != nil {
		return incident.Scope{}, err
	}
	if env == "" || scope.EnvironmentID != "" && scope.EnvironmentID != env {
		return incident.Scope{}, incident.ErrBadScope
	}
	scope.EnvironmentID = env
	businesses, err := o.incidentBusinesses(ctx, scope, env)
	if err != nil {
		return incident.Scope{}, err
	}
	if scope.BusinessID == "" {
		if len(businesses) == 1 {
			scope.BusinessID = businesses[0]
		}
		if len(businesses) > 1 || len(businesses) == 0 && (env == "production" || env == "staging") {
			return incident.Scope{}, incident.ErrBadScope
		}
	} else if !slices.Contains(businesses, scope.BusinessID) {
		return incident.Scope{}, incident.ErrBadScope
	}
	return scope, nil
}

func (o *onboarding) resolveIncident(ctx context.Context, p identity.Principal, admin *permissions.Admin,
	allow func(context.Context, identity.Principal, string, incident.Scope, bool) (bool, error), req incident.CreateRequest) (incident.Incident, error) {
	scope := req.Scope
	allowed, err := allow(ctx, p, incident.CreatePermission, incident.Scope{ClusterID: scope.ClusterID, Namespace: scope.Namespace}, true)
	if err != nil {
		return incident.Incident{}, err
	}
	if !allowed {
		return incident.Incident{}, incident.ErrForbidden
	}
	target, ok := o.scopedCluster(scope.ClusterID)
	if !ok {
		return incident.Incident{}, incident.ErrBadScope
	}
	scope, err = o.resolveDiagnosticScope(ctx, scope)
	if err != nil {
		return incident.Incident{}, err
	}
	allowed, err = allow(ctx, p, incident.CreatePermission, scope, false)
	if err != nil {
		return incident.Incident{}, err
	}
	if !allowed {
		return incident.Incident{}, incident.ErrForbidden
	}
	resource := finding.ResourceRef{APIVersion: "v1", Kind: "Namespace", Namespace: scope.Namespace, Name: scope.Namespace}
	var first time.Time
	if req.FindingID != "" {
		if req.Resource != (finding.ResourceRef{}) {
			return incident.Incident{}, incident.ErrBadScope
		}
		allowed, err := admin.AuthorizeBusinessOperation(ctx, p, catalog.FindingRead, authorization.CapabilityFindingsRead,
			authorization.NamespaceScope(scope.ClusterID, scope.Namespace), false, scope.BusinessID)
		if err != nil {
			return incident.Incident{}, err
		}
		if !allowed {
			return incident.Incident{}, incident.ErrForbidden
		}
		f, err := target.Backend.GetFinding(ctx, req.FindingID)
		var apiErr *kubernetes.APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			return incident.Incident{}, incident.ErrNotFound
		}
		if err != nil {
			return incident.Incident{}, err
		}
		f, err = sanitizer.Default().Finding(f)
		if err != nil {
			return incident.Incident{}, err
		}
		if f.Cluster != scope.ClusterID || f.Namespace != scope.Namespace || f.Resource.Namespace != "" && f.Resource.Namespace != scope.Namespace || f.Resource.Kind == "" || f.Resource.Name == "" {
			return incident.Incident{}, incident.ErrNotFound
		}
		resource = f.Resource
		resource.Namespace = scope.Namespace
		first, _ = time.Parse(time.RFC3339Nano, f.CreatedAt)
		if first.After(time.Now()) {
			first = time.Time{}
		}
	} else {
		namespaces, err := target.Backend.ListNamespaces(ctx)
		if err != nil {
			return incident.Incident{}, err
		}
		if !slices.ContainsFunc(namespaces, func(n kubernetes.Namespace) bool { return n.Name == scope.Namespace }) {
			return incident.Incident{}, incident.ErrBadScope
		}
		if req.Resource != (finding.ResourceRef{}) {
			kind := strings.ToLower(req.Resource.Kind)
			if (kind != "pod" && kind != "deployment") || !validKubernetesResourceName(req.Resource.Name) ||
				req.Resource.Namespace != "" && req.Resource.Namespace != scope.Namespace {
				return incident.Incident{}, incident.ErrBadScope
			}
			found, err := target.Backend.GetResource(ctx, kind, scope.Namespace, req.Resource.Name)
			if err != nil {
				return incident.Incident{}, err
			}
			if found.Namespace != scope.Namespace || !strings.EqualFold(found.Kind, kind) || found.Name != req.Resource.Name {
				return incident.Incident{}, incident.ErrBadScope
			}
			resource = finding.ResourceRef{APIVersion: found.APIVersion, Kind: found.Kind, Namespace: found.Namespace, Name: found.Name}
		}
	}
	resourceKeys, _ := json.Marshal([]string{scope.EnvironmentID, scope.BusinessID, scope.ClusterID, scope.Namespace, apiGroup(resource), strings.ToLower(resource.Kind), resource.Name})
	identityKey := fmt.Sprintf("%x", sha256.Sum256(resourceKeys))
	if req.ResourceID != "" && req.ResourceID != identityKey {
		return incident.Incident{}, incident.ErrBadScope
	}
	return incident.Incident{Scope: scope, Resource: resource, ResourceID: identityKey, FindingID: req.FindingID, FirstSeen: first}, nil
}

func validKubernetesResourceName(value string) bool {
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, c := range label {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

func apiGroup(resource finding.ResourceRef) string {
	if group, _, ok := strings.Cut(resource.APIVersion, "/"); ok {
		return group
	}
	if resource.APIVersion == "" && strings.EqualFold(resource.Kind, "Deployment") {
		return "apps"
	}
	return ""
}
