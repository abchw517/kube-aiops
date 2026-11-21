package incident

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/abchw517/kube-aiops/api/internal/adminhttp"
	"github.com/abchw517/kube-aiops/api/internal/diagnosis/correlation"
	"github.com/abchw517/kube-aiops/api/internal/permissions/identity"
	"github.com/abchw517/kube-aiops/api/internal/sanitizer"
)

func cleanText(input string, max int) string {
	return strings.TrimSpace(sanitizer.WikiText(input, max))
}

func (s *Server) create(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	if err := s.require(ctx, p, CreatePermission, Scope{}, true); err != nil {
		return err
	}
	keys := r.Header.Values("Idempotency-Key")
	if len(keys) != 1 || !validID(keys[0]) || len(keys[0]) > 128 {
		return badInput
	}
	var input CreateRequest
	if !adminhttp.Decode(w, r, &input) || !validSeverity(input.Severity) || len(input.Title) > 240 || len(input.Description) > 4000 {
		return badInput
	}
	if err := input.Scope.Validate(); err != nil {
		return err
	}
	if input.FindingID != "" && !validID(input.FindingID) {
		return badInput
	}
	if cleanText(input.Title, 240) == "" {
		return badInput
	}
	request, err := json.Marshal(input)
	if err != nil {
		return err
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(request))
	requester := p.Provider + "\x00" + p.Subject
	repeated, err := replay(ctx, s.Store.db, requester, keys[0], digest)
	if err == nil {
		if err := s.require(ctx, p, CreatePermission, repeated.Scope, false); err != nil {
			return err
		}
		responseItem(w, repeated, http.StatusOK)
		return nil
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	var item Incident
	var anchor *assetIncidentAnchor
	if input.AssetID != "" {
		if !validID(input.AssetID) || input.FindingID != "" || input.ResourceID != "" ||
			input.Resource.Kind != "" || input.Resource.Name != "" || input.Resource.Namespace != "" || input.Resource.APIVersion != "" {
			return badInput
		}
		item, anchor, err = s.resolveAssetIncident(ctx, p, input)
	} else {
		item, err = s.Resolve(ctx, p, input)
	}
	if err != nil {
		return err
	}
	if err := s.require(ctx, p, CreatePermission, item.Scope, false); err != nil {
		return err
	}

	item.Title, item.Description = cleanText(input.Title, 240), cleanText(input.Description, 4000)
	item.Severity, item.Status, item.Source = input.Severity, Pending, "manual"
	now := nowUTC()
	item.CreatedAt, item.UpdatedAt = now, now
	if item.FirstSeen.IsZero() {
		item.FirstSeen = now
	}
	item.FirstSeen = item.FirstSeen.UTC().Truncate(time.Millisecond)
	item.LastSeen, item.AnchorAt = item.FirstSeen, item.FirstSeen
	item, created, err := s.Store.Create(ctx, item, p.Subject, requester, keys[0], digest, anchor)
	if err != nil {
		return err
	}
	// The reply may come from an earlier request using the same key.
	if err := s.require(ctx, p, CreatePermission, item.Scope, false); err != nil {
		return err
	}
	if !created {
		responseItem(w, item, http.StatusOK)
		return nil
	}
	responseItem(w, item, http.StatusCreated)
	return nil
}

func (s *Server) update(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	i, err := s.visible(ctx, p, r.PathValue("id"), UpdatePermission)
	if err != nil {
		return err
	}
	version, err := requestedVersion(r)
	if err != nil {
		return err
	}
	var input UpdateRequest
	if !adminhttp.Decode(w, r, &input) || len(input.Reason) > 1000 || cleanText(input.Reason, 1000) == "" ||
		input.Status != "" && !validStatus(input.Status) ||
		input.Severity != "" && !validSeverity(input.Severity) ||
		input.Status == "" && input.Severity == "" && input.OwnerGroup == nil {
		return badInput
	}
	if input.OwnerGroup != nil && *input.OwnerGroup != "" {
		if !validID(*input.OwnerGroup) {
			return badInput
		}
		ok, err := s.ActiveGroup(ctx, *input.OwnerGroup)
		if err != nil {
			return err
		}
		if !ok {
			return badInput
		}
	}
	input.Reason = cleanText(input.Reason, 1000)
	updated, err := s.Store.Update(ctx, i.ID, p.Subject, version, input, nowUTC())
	if err != nil {
		return err
	}
	responseItem(w, updated, http.StatusOK)
	return nil
}

func (s *Server) collect(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	i, err := s.visible(ctx, p, r.PathValue("id"), CorrelatePermission)
	if err != nil {
		return err
	}
	if err := s.require(ctx, p, EvidencePermission, i.Scope, false); err != nil {
		return err
	}
	version, err := requestedVersion(r)
	if err != nil {
		return err
	}
	if i.Version != version {
		return ErrVersion
	}
	if i.Status == Closed {
		return ErrConflict
	}
	bundle, err := s.Collect(ctx, i)
	if err != nil {
		return upstream
	}
	bundle, err = sanitizer.DefaultCorrelation().CorrelationBundle(bundle)
	if err != nil {
		return upstream
	}
	if bundle.Scope.Cluster != i.Scope.ClusterID || bundle.Scope.Namespace != i.Scope.Namespace || bundle.Scope.Resource != i.Resource {
		return upstream
	}
	signals := make([]Signal, 0, len(bundle.Signals))
	windowStart, _ := time.Parse(time.RFC3339Nano, bundle.Window.Start)
	windowEnd, _ := time.Parse(time.RFC3339Nano, bundle.Window.End)
	for _, signal := range bundle.Signals {
		if signal.Resource == nil || signal.Resource.Namespace != i.Resource.Namespace ||
			!strings.EqualFold(signal.Resource.Kind, i.Resource.Kind) || signal.Resource.Name != i.Resource.Name {
			continue
		}
		first, firstErr := time.Parse(time.RFC3339Nano, signal.FirstSeen)
		last, lastErr := time.Parse(time.RFC3339Nano, signal.LastSeen)
		if firstErr != nil || lastErr != nil || last.Before(windowStart) || first.After(windowEnd) {
			continue
		}
		key := signal.ID
		if signal.Occurrence != "" {
			key = signal.Occurrence
		}
		if signal.Source == correlation.SourcePrometheus {
			// Time series fingerprints identify the metric; each sampled window is its own observation.
			key += "@" + last.UTC().Format(time.RFC3339Nano)
		}
		signals = append(signals, Signal{Key: key, State: "observed", First: first, Last: last, Value: signal})
	}
	current, err := s.Authenticate(ctx, r)
	if err != nil || current.Subject != p.Subject || current.Provider != p.Provider {
		return httpError{401, "AUTHENTICATION_REQUIRED", "请登录后继续。"}
	}
	for _, code := range []string{ReadPermission, CorrelatePermission, EvidencePermission} {
		if err := s.require(ctx, current, code, i.Scope, false); err != nil {
			return err
		}
	}
	updated, err := s.Store.Correlate(ctx, i.ID, p.Subject, version, signals, bundle.Sources, nowUTC())
	if err != nil {
		return err
	}
	responseItem(w, updated, http.StatusOK)
	return nil
}

func (s *Server) merge(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	i, err := s.visible(ctx, p, r.PathValue("id"), CorrelatePermission)
	if err != nil {
		return err
	}
	version, err := requestedVersion(r)
	if err != nil {
		return err
	}
	var input MergeRequest
	if !adminhttp.Decode(w, r, &input) || !validID(input.SourceID) || input.SourceVersion < 1 || !validReason(input.Reason) {
		return badInput
	}
	if _, err := s.visible(ctx, p, input.SourceID, CorrelatePermission); err != nil {
		return err
	}
	if err := s.require(ctx, p, EvidencePermission, i.Scope, false); err != nil {
		return err
	}
	input.Reason = cleanText(input.Reason, 1000)
	item, err := s.Store.Merge(ctx, i.ID, p.Subject, version, input, nowUTC())
	if err != nil {
		return err
	}
	responseItem(w, item, http.StatusOK)
	return nil
}

func (s *Server) split(w http.ResponseWriter, r *http.Request, p identity.Principal) error {
	ctx := r.Context()
	i, err := s.visible(ctx, p, r.PathValue("id"), CorrelatePermission)
	if err != nil {
		return err
	}
	version, err := requestedVersion(r)
	if err != nil {
		return err
	}
	var input SplitRequest
	if !adminhttp.Decode(w, r, &input) || len(input.EvidenceIDs) == 0 || len(input.EvidenceIDs) > 100 ||
		len(input.Title) > 240 || cleanText(input.Title, 240) == "" || !validReason(input.Reason) {
		return badInput
	}
	for _, id := range input.EvidenceIDs {
		if !validID(id) {
			return badInput
		}
	}
	for _, code := range []string{CreatePermission, EvidencePermission} {
		if err := s.require(ctx, p, code, i.Scope, false); err != nil {
			return err
		}
	}
	input.Title, input.Reason = cleanText(input.Title, 240), cleanText(input.Reason, 1000)
	item, err := s.Store.Split(ctx, i.ID, p.Subject, version, input, nowUTC())
	if err != nil {
		return err
	}
	responseItem(w, item, http.StatusCreated)
	return nil
}

func validReason(s string) bool { return len(s) <= 1000 && cleanText(s, 1000) != "" }
