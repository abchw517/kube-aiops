package wiki

import (
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"net/http"
	"sync"
)

type Service struct {
	authorization.Authorizer
	store          *identitystore.Store
	resolve        TargetResolver
	find           FindingLookup
	origin         string
	wikiSyncMu     sync.Mutex
	wikiSyncActive map[string]bool
	wikiTransport  http.RoundTripper
}

func New(store *identitystore.Store, authorizer authorization.Authorizer, origin string) *Service {
	return &Service{store: store, Authorizer: authorizer, origin: origin}
}
func (a *Service) SetFindingLookup(find FindingLookup) { a.find = find }

func (a *Service) SetTargetResolver(resolve TargetResolver) { a.resolve = resolve }

func (a *Service) Register(mux *http.ServeMux, s *identity.Sessions, ask AskModel) {
	mux.HandleFunc("/api/v1/wiki/sources/{id}", a.wikiSourceHandler(s, a.handleWikiSource))
	mux.HandleFunc("/api/v1/wiki/sources/{id}/check", a.wikiSourceHandler(s, a.handleWikiCheck))
	mux.HandleFunc("/api/v1/wiki/sources/{id}/sync", a.wikiSourceHandler(s, a.handleWikiSync))
	mux.HandleFunc("/api/v1/wiki/sources/{id}/schedule", a.wikiSourceHandler(s, a.handleWikiSchedule))
	mux.HandleFunc("/api/v1/wiki/sources/{id}/credential", a.wikiSourceHandler(s, a.handleWikiCredential))
	mux.HandleFunc("/api/v1/wiki/finding", a.wikiSessionHandler(s, a.findingKnowledge))
	mux.HandleFunc("/api/v1/wiki/governance", a.wikiSessionHandler(s, a.governanceList))
	mux.HandleFunc("/api/v1/wiki/candidates", a.wikiSessionHandler(s, a.candidates))
	mux.HandleFunc("/api/v1/wiki/candidates/{id}", a.wikiSessionHandler(s, a.candidate))
	mux.HandleFunc("/api/v1/wiki/knowledge/{id}", a.wikiSessionHandler(s, a.handleWikiDocument))
	mux.HandleFunc("/api/v1/wiki/knowledge/{id}/governance", a.wikiSessionHandler(s, a.governDocument))
	mux.HandleFunc("/api/v1/wiki/knowledge", a.wikiSessionHandler(s, a.handleWikiDocuments))
	mux.HandleFunc("/api/v1/wiki/knowledge/{id}/revisions/{revision}", a.wikiSessionHandler(s, a.handleWikiDocument))
	mux.HandleFunc("/api/v1/wiki/knowledge/{id}/revisions", a.wikiSessionHandler(s, a.handleWikiRevision))
	mux.HandleFunc("/api/v1/wiki/imports/files", a.wikiSessionHandler(s, a.handleWikiFiles))
	mux.HandleFunc("/api/v1/wiki/imports", a.wikiSessionHandler(s, a.handleWikiImport))
	mux.HandleFunc("/api/v1/wiki/ask", a.wikiSessionHandler(s, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
		a.handleWikiAsk(w, r, p, ask)
	}))
}
