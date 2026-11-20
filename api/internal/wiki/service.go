package wiki

import (
	"github.com/abchw517/kube-aiops/api/internal/identitystore"
	"github.com/abchw517/kube-aiops/api/internal/permissions/authorization"
	"github.com/abchw517/kube-aiops/api/internal/permissions/catalog"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"net/http"
)

type Service struct {
	authorization.Authorizer
	store         *identitystore.Store
	resolve       TargetResolver
	find          FindingLookup
	origin        string
	wikiTransport http.RoundTripper
}

func New(store *identitystore.Store, authorizer authorization.Authorizer, origin string) *Service {
	return &Service{store: store, Authorizer: authorizer, origin: origin}
}
func (a *Service) SetFindingLookup(find FindingLookup) { a.find = find }

func (a *Service) SetTargetResolver(resolve TargetResolver) { a.resolve = resolve }

func (a *Service) Register(mux *http.ServeMux, s *identity.Sessions, ask AskModel) {
	read, publish, sync := authorization.CapabilityWikiRead, authorization.CapabilityWikiPublish, authorization.CapabilityWikiSync
	source := func(path, get, post, put, remove string, handler func(http.ResponseWriter, *http.Request, identity.Principal, string)) {
		mux.HandleFunc("/api/v1/wiki/"+path, a.wikiSourceHandler(s, func(w http.ResponseWriter, r *http.Request, p identity.Principal, id string) {
			handler(w, forWikiMethod(r, get, post, put, remove, sync), p, id)
		}))
	}
	resource := func(path, get, post string, legacy authorization.Capability, handler func(http.ResponseWriter, *http.Request, identity.Principal)) {
		mux.HandleFunc("/api/v1/wiki/"+path, a.wikiSessionHandler(s, func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
			handler(w, forWikiMethod(r, get, post, "", "", legacy), p)
		}))
	}
	source("sources/{id}", "knowledge.source.read", "", "knowledge.source.register", "knowledge.source.disable", a.handleWikiSource)
	source("sources/{id}/check", "", "knowledge.source.check", "", "", a.handleWikiCheck)
	source("sources/{id}/sync", "", "knowledge.sync.run", "", "", a.handleWikiSync)
	source("sources/{id}/runs", "knowledge.sync.runs", "", "", "", a.syncRuns)
	source("sources/{id}/runs/{run}", "knowledge.sync.run-read", "", "", "", a.syncRuns)
	source("sources/{id}/runs/{run}/{action}", "", "knowledge.sync.retry", "", "", a.syncRuns)
	source("sources/{id}/schedule", "knowledge.schedule.read", "", "knowledge.schedule.save", "", a.handleWikiSchedule)
	source("sources/{id}/credential", "knowledge.credential.status", "", "", "knowledge.credential.unbind", a.handleWikiCredential)

	resource("finding", "", "", "", a.findingKnowledge)
	resource("governance", "knowledge.governance.list", "", publish, a.governanceList)
	resource("candidates", "knowledge.candidate.list", "knowledge.candidate.create", publish, a.candidates)
	resource("candidates/{id}", "knowledge.candidate.read", "", publish, a.candidate)
	resource("knowledge/{id}", catalog.DocumentRead, "", read, a.handleWikiDocument)
	resource("knowledge/{id}/feedback", "knowledge.feedback.list", "", publish, a.wikiFeedback)
	resource("knowledge/{id}/governance", "knowledge.governance.read", "", publish, a.governDocument)
	resource("knowledge", "knowledge.document.search", "", read, a.handleWikiDocuments)
	resource("knowledge/{id}/revisions/{revision}", catalog.VersionRead, "", read, a.handleWikiDocument)
	resource("knowledge/{id}/revisions", "", "", publish, a.handleWikiRevision)
	resource("imports/files", "", "knowledge.candidate.import", publish, a.handleWikiFiles)
	resource("imports", "", "", publish, a.handleWikiImport)
	resource("ask", "", catalog.WikiAsk, "", func(w http.ResponseWriter, r *http.Request, p identity.Principal) {
		a.handleWikiAsk(w, r, p, ask)
	})
}
