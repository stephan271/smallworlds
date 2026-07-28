package console

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/stephan271/smallworlds/operator-console/internal/consoleworkflow"
	"github.com/stephan271/smallworlds/operator-console/internal/releaseupdate"
)

const releasePlanTTL = time.Hour

var (
	ErrClusterProfileUnavailable  = errors.New("console: cluster profile unavailable")
	ErrReleaseAdoptionUnavailable = errors.New("console: release adoption evidence unavailable")
)

type ClusterProfileReporter interface {
	Profile(context.Context) (releaseupdate.ClusterProfile, error)
}

type ReleaseAdoptionReporter interface {
	Adoption(context.Context, string) (releaseupdate.AdoptionEvidence, error)
}

type unavailableClusterProfileReporter struct{}

func (unavailableClusterProfileReporter) Profile(context.Context) (releaseupdate.ClusterProfile, error) {
	return releaseupdate.ClusterProfile{}, ErrClusterProfileUnavailable
}

type unavailableReleaseAdoptionReporter struct{}

func (unavailableReleaseAdoptionReporter) Adoption(context.Context, string) (releaseupdate.AdoptionEvidence, error) {
	return releaseupdate.AdoptionEvidence{}, ErrReleaseAdoptionUnavailable
}

func (server *Server) currentClusterProfile(ctx context.Context) (releaseupdate.ClusterProfile, error) {
	profile, err := server.clusterProfile.Profile(ctx)
	if err != nil {
		return releaseupdate.ClusterProfile{}, err
	}
	// Keep profile exports stable and avoid leaking caller-owned mutable slices.
	profile.Capabilities = append([]string(nil), profile.Capabilities...)
	profile.Images = copyStringMap(profile.Images)
	profile.Tools = copyStringMap(profile.Tools)
	return profile, nil
}

func copyStringMap(source map[string]string) map[string]string {
	if source == nil {
		return nil
	}
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}

func (server *Server) handleUpdateProfile(response http.ResponseWriter, request *http.Request) {
	profile, err := server.currentClusterProfile(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "cluster_profile_unavailable")
		return
	}
	writeJSON(response, http.StatusOK, profile)
}

// Export is intentionally available at Observe permission, including when the
// launcher is incompatible. It contains only the same non-secret profile as the
// inspection endpoint and is marked as a download.
func (server *Server) handleUpdateProfileExport(response http.ResponseWriter, request *http.Request) {
	profile, err := server.currentClusterProfile(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "cluster_profile_unavailable")
		return
	}
	response.Header().Set("Content-Disposition", `attachment; filename="smallworlds-cluster-profile.json"`)
	writeJSON(response, http.StatusOK, profile)
}

func (server *Server) handleUpdateAvailable(response http.ResponseWriter, request *http.Request) {
	profile, err := server.currentClusterProfile(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "cluster_profile_unavailable")
		return
	}
	available, err := server.releaseCatalog.Available(profile)
	switch {
	case errors.Is(err, releaseupdate.ErrNoUpdate):
		writeJSON(response, http.StatusOK, map[string]any{"available": nil})
	case errors.Is(err, releaseupdate.ErrInvalidMetadata):
		writeError(response, http.StatusBadGateway, "release_metadata_invalid")
	case err != nil:
		writeError(response, http.StatusServiceUnavailable, "release_catalog_unavailable")
	default:
		writeJSON(response, http.StatusOK, map[string]any{"available": available})
	}
}

func (server *Server) handleUpdatePlan(response http.ResponseWriter, request *http.Request) {
	current, ok := server.readSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	var input struct {
		Release string `json:"release"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(response, request.Body, 8*1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil || input.Release == "" {
		writeError(response, http.StatusBadRequest, "invalid_update_request")
		return
	}
	profile, err := server.currentClusterProfile(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "cluster_profile_unavailable")
		return
	}
	metadata, err := server.releaseCatalog.Resolve(input.Release)
	if errors.Is(err, releaseupdate.ErrInvalidMetadata) {
		writeError(response, http.StatusBadGateway, "release_metadata_invalid")
		return
	}
	if err != nil {
		writeError(response, http.StatusNotFound, "release_not_available")
		return
	}
	plan, err := releaseupdate.BuildPlan(profile, metadata, server.richCatalog, server.releaseOverlay())
	if errors.Is(err, releaseupdate.ErrIncompatible) {
		writeError(response, http.StatusConflict, "release_incompatible")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, "update_plan_failed")
		return
	}

	planID := server.newID()
	summary := releaseSummary(plan, diffDigest(plan.GitDiff))
	risks := []consoleworkflow.RiskLabel{consoleworkflow.RiskReversible}
	if len(plan.Risks.Downtime) > 0 {
		risks = append(risks, consoleworkflow.RiskDowntime)
	}
	record := consoleworkflow.ChangePlan{
		ID: planID, Intent: consoleworkflow.IntentUpdateRelease, Actor: current.Username,
		Summary: summary, Risks: risks, CreatedAt: server.now(), ExpiresAt: server.now().Add(releasePlanTTL),
	}
	record.Digest = consoleworkflow.ComputeDigest(record.Intent, record.Actor, record.Summary, record.Risks)
	if err := server.workflow.PutPlan(request.Context(), record); err != nil {
		writeError(response, http.StatusInternalServerError, "update_plan_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"planId": planID, "digest": record.Digest, "summary": summary, "plan": plan,
	})
}

func (server *Server) handleUpdateApprove(response http.ResponseWriter, request *http.Request) {
	current, ok := server.readSession(request)
	if !ok {
		writeError(response, http.StatusUnauthorized, "authentication_required")
		return
	}
	plan, ok := server.loadReleasePlan(response, request, request.PathValue("id"))
	if !ok {
		return
	}
	approved, err := plan.Approve(current.Username, server.now())
	if err != nil {
		writeError(response, http.StatusConflict, "update_plan_invalid")
		return
	}
	if err := server.workflow.PutPlan(request.Context(), approved); err != nil {
		writeError(response, http.StatusInternalServerError, "update_approve_failed")
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"planId": approved.ID, "approvedBy": current.Username})
}

func (server *Server) handleUpdatePropose(response http.ResponseWriter, request *http.Request) {
	planRecord, ok := server.loadReleasePlan(response, request, request.PathValue("id"))
	if !ok {
		return
	}
	if err := planRecord.ValidateApproval(server.now()); err != nil {
		writeError(response, http.StatusConflict, "update_not_approved")
		return
	}
	release, approvedDiff, ok := parseReleaseSummary(planRecord.Summary)
	if !ok {
		writeError(response, http.StatusConflict, "update_plan_invalid")
		return
	}
	profile, err := server.currentClusterProfile(request.Context())
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "cluster_profile_unavailable")
		return
	}
	metadata, err := server.releaseCatalog.Resolve(release)
	if err != nil {
		writeError(response, http.StatusConflict, "release_metadata_changed")
		return
	}
	rebuilt, err := releaseupdate.BuildPlan(profile, metadata, server.richCatalog, server.releaseOverlay())
	if err != nil || diffDigest(rebuilt.GitDiff) != approvedDiff {
		writeError(response, http.StatusConflict, "update_plan_mismatch")
		return
	}
	opened, err := server.proposals.OpenProposal(
		request.Context(),
		fmt.Sprintf("Update SmallWorlds to %s", rebuilt.ToBaseTag),
		releaseProposalBody(rebuilt),
		rebuilt.Files,
	)
	if errors.Is(err, ErrProposalUnavailable) {
		writeError(response, http.StatusServiceUnavailable, "proposal_unavailable")
		return
	}
	if err != nil {
		writeError(response, http.StatusBadGateway, "proposal_failed")
		return
	}
	run, err := planRecord.Start(server.newID(), server.now())
	if err != nil {
		writeError(response, http.StatusInternalServerError, "update_record_failed")
		return
	}
	run = run.Checkpoint("proposal-opened-awaiting-operator-merge", server.now())
	run.EvidenceSummary = consoleworkflow.RedactDetail(commitEvidence(opened), consoleworkflow.MaxEvidenceSummaryLength)
	if err := server.workflow.PutRun(request.Context(), run); err != nil {
		writeError(response, http.StatusInternalServerError, "update_record_failed")
		return
	}
	writeJSON(response, http.StatusCreated, map[string]any{
		"runId": run.ID, "provider": opened.Provider, "branch": opened.Branch,
		"commit": opened.Commit, "url": opened.URL,
		"automaticMerge": false, "liveClusterMutated": false,
		"mergeInstructionKey": "overlay_manual_merge",
	})
}

func (server *Server) handleUpdateAdoption(response http.ResponseWriter, request *http.Request) {
	release := request.PathValue("release")
	if _, err := server.releaseCatalog.Resolve(release); err != nil {
		writeError(response, http.StatusNotFound, "release_not_available")
		return
	}
	evidence, err := server.releaseAdoption.Adoption(request.Context(), release)
	if err != nil {
		writeError(response, http.StatusServiceUnavailable, "release_adoption_unavailable")
		return
	}
	evidence.TargetRelease = release
	writeJSON(response, http.StatusOK, releaseupdate.AssessAdoption(evidence))
}

func (server *Server) loadReleasePlan(response http.ResponseWriter, request *http.Request, id string) (consoleworkflow.ChangePlan, bool) {
	plan, err := server.workflow.GetPlan(request.Context(), id)
	if errors.Is(err, consoleworkflow.ErrNotFound) {
		writeError(response, http.StatusNotFound, "update_plan_not_found")
		return consoleworkflow.ChangePlan{}, false
	}
	if err != nil || plan.Intent != consoleworkflow.IntentUpdateRelease {
		writeError(response, http.StatusConflict, "update_plan_invalid")
		return consoleworkflow.ChangePlan{}, false
	}
	return plan, true
}

var releaseSummaryTrailer = regexp.MustCompile(`\[release-update:(\S+) sha256:([0-9a-f]{64})\]`)

func releaseSummary(plan releaseupdate.Plan, digest string) string {
	return fmt.Sprintf("Update SmallWorlds from %s to %s. [release-update:%s sha256:%s]",
		plan.FromBaseTag, plan.ToBaseTag, plan.ToBaseTag, digest)
}

func parseReleaseSummary(summary string) (release, digest string, ok bool) {
	match := releaseSummaryTrailer.FindStringSubmatch(summary)
	if match == nil {
		return "", "", false
	}
	return match[1], match[2], true
}

func releaseProposalBody(plan releaseupdate.Plan) string {
	return fmt.Sprintf(
		"Update the SmallWorlds GitOps release pins from %s to %s.\n\nCatalog: %d\nRelease notes:\n- %s\n\nDowntime risks:\n- %s\nData risks:\n- %s\nExposure risks:\n- %s\n\nRecovery expectation: %s\n\nThis proposal does not merge itself or mutate the live cluster.",
		plan.FromBaseTag, plan.ToBaseTag, plan.CatalogVersion,
		strings.Join(plan.ReleaseNotes, "\n- "),
		strings.Join(plan.Risks.Downtime, "\n- "),
		strings.Join(plan.Risks.Data, "\n- "),
		strings.Join(plan.Risks.Exposure, "\n- "),
		plan.Recovery.Expected,
	)
}

// releaseOverlay is the same overlay the add-capability proposals are written
// against. Both flows change the same files in the same repository, so they
// read the operator's overlay from one place.
func (server *Server) releaseOverlay() releaseupdate.Overlay {
	return releaseupdate.Overlay{
		RepositoryURL:        server.overlayTarget.RepositoryURL,
		Domain:               server.overlayTarget.Domain,
		EnvironmentExtension: server.overlayTarget.EnvironmentExtension,
	}
}
