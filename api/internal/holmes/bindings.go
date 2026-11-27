package holmes

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
)

const pilotEndpoint = "http://holmes-pilot-holmes.holmes-pilot.svc"

var (
	ErrModelNotFound = errors.New("Holmes 未返回所选模型别名")
	ErrAlreadyBound  = errors.New("该数据范围已绑定，请在列表中编辑")
)

// Only deployment configuration can select a network destination or supply credentials.
func portalService(source string) binding {
	if source == "wiki" {
		endpoint := strings.TrimRight(strings.TrimSpace(os.Getenv("HOLMES_WIKI_ENDPOINT")), "/")
		if endpoint == strings.TrimRight(pilotService().Endpoint, "/") {
			return binding{}
		}
		return binding{Endpoint: endpoint, APIKey: os.Getenv("HOLMES_WIKI_API_KEY")}
	}
	return pilotService()
}

func pilotService() binding {
	endpoint := strings.TrimSpace(os.Getenv("HOLMES_PILOT_ENDPOINT"))
	if endpoint == "" {
		endpoint = pilotEndpoint
	}
	return binding{Endpoint: endpoint, APIKey: os.Getenv("HOLMES_PILOT_API_KEY")}
}

type scopeBinding struct {
	Scope Scope  `json:"scope"`
	Model string `json:"model"`
}

func (b scopeBinding) resolve() binding {
	service := portalService(b.Scope.Source)
	service.Scope, service.Model, service.Managed = b.Scope, b.Model, true
	return service
}

type PortalService struct {
	Address string `json:"address,omitempty"`
	Status  string `json:"status"`
}

func PilotStatus(ctx context.Context) PortalService {
	service := pilotService()
	u, err := endpointFor(service, "")
	if err != nil {
		return PortalService{Status: "invalid"}
	}
	status := "unreachable"
	if checkService(ctx, service) == nil {
		status = "ready"
	}
	return PortalService{Address: u.String(), Status: status}
}

func DiscoverPortalModels(ctx context.Context, source string) ([]string, error) {
	service := portalService(source)
	if service.Endpoint == "" {
		return nil, ErrUnconfigured
	}
	return discoverModels(ctx, service)
}

func BindPortalScope(ctx context.Context, scope Scope, model string) error {
	w := WorkspaceFrom(ctx)
	if w == nil {
		return ErrUnavailable
	}
	// A legacy scope keeps its existing routing and credentials.
	bindings, err := readBindings()
	if err == nil && slices.ContainsFunc(bindings, func(b binding) bool { return b.Scope == scope }) {
		return ErrAlreadyBound
	}
	aliases, err := DiscoverPortalModels(ctx, scope.Source)
	if err != nil {
		return err
	}
	if !slices.Contains(aliases, model) {
		return ErrModelNotFound
	}
	err = SaveEntry(ctx, w, "binding", Entry[scopeBinding]{
		ID: ScopeID(scope), Data: scopeBinding{Scope: scope, Model: model},
	})
	if errors.Is(err, ErrConflict) {
		return ErrAlreadyBound
	}
	return err
}
