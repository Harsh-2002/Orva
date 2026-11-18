//go:build linux

package builder

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strconv"
	"unicode/utf8"

	"github.com/Harsh-2002/Orva/backend/internal/database"
)

const MaxLifecycleRequestBytes = 1024

var ErrLifecycleInput = errors.New("exact function and generation UUIDs, reviewed decimal revision and epoch strings, and confirm=true required")

var ErrLifecycleConflict = errors.New("workload state or authority is ineligible or changed; inspect again")

type LifecycleAcknowledgement struct {
	FunctionID           string `json:"function_id"`
	FunctionStatus       string `json:"function_status"`
	OperatorPaused       bool   `json:"operator_paused"`
	InstallationRequired bool   `json:"installation_required,omitempty"`
	ReviewRequired       bool   `json:"review_required,omitempty"`
}

// ExecuteLifecycle is the shared transport acknowledgement for a completed mutation.
func (s *PublicationQueue) ExecuteLifecycle(ctx context.Context, req database.WorkloadPauseRequest, resume bool) (LifecycleAcknowledgement, error) {
	var err error
	if resume {
		err = s.Resume(ctx, req)
	} else {
		err = s.Pause(ctx, req)
	}
	switch {
	case err == nil:
		return LifecycleAcknowledgement{FunctionID: req.OwnerID, FunctionStatus: "inactive",
			OperatorPaused: !resume, InstallationRequired: resume, ReviewRequired: resume}, nil
	case errors.Is(err, database.ErrOperatorDenied):
		return LifecycleAcknowledgement{}, database.ErrOperatorDenied
	case errors.Is(err, database.ErrIAMConflict), errors.Is(err, database.ErrWorkloadArtifactConflict),
		errors.Is(err, database.ErrFunctionDeleting), errors.Is(err, database.ErrIAMFunctionNotFound),
		errors.Is(err, database.ErrIAMPolicyNotFound), errors.Is(err, database.ErrWorkloadArtifactNotFound),
		errors.Is(err, database.ErrWorkloadResumeAssociation):
		return LifecycleAcknowledgement{}, ErrLifecycleConflict
	default:
		return LifecycleAcknowledgement{}, ErrPublicationUnavailable
	}
}

// DecodeLifecycleInput uses the path owner for REST and a required function_id for tools.
func DecodeLifecycleInput(raw []byte, pathOwner string, tool bool) (database.WorkloadPauseRequest, error) {
	var zero database.WorkloadPauseRequest
	if len(raw) > MaxLifecycleRequestBytes || !utf8.Valid(raw) || !publicationEscapes(raw) {
		return zero, ErrLifecycleInput
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return zero, ErrLifecycleInput
	}
	values := make(map[string]string, 4)
	confirmed := false
	for d.More() {
		key, err := d.Token()
		if err != nil {
			return zero, ErrLifecycleInput
		}
		value, err := d.Token()
		if err != nil {
			return zero, ErrLifecycleInput
		}
		switch key {
		case "confirm":
			if confirmed || value != true {
				return zero, ErrLifecycleInput
			}
			confirmed = true
			continue
		case "function_id":
			if !tool {
				return zero, ErrLifecycleInput
			}
		case "generation", "policy_revision", "authority_epoch":
		default:
			return zero, ErrLifecycleInput
		}
		field := key.(string)
		text, ok := value.(string)
		if !ok || text == "" || values[field] != "" {
			return zero, ErrLifecycleInput
		}
		values[field] = text
	}
	if token, err := d.Token(); err != nil || token != json.Delim('}') {
		return zero, ErrLifecycleInput
	}
	if tool {
		pathOwner = values["function_id"]
	}
	generation := values["generation"]
	if _, err := d.Token(); err != io.EOF || !confirmed || !validRollbackID(pathOwner) || !validRollbackID(generation) {
		return zero, ErrLifecycleInput
	}
	number := func(field string) (uint64, bool) {
		text := values[field]
		n, err := strconv.ParseUint(text, 10, 63)
		return n, err == nil && n != 0 && strconv.FormatUint(n, 10) == text
	}
	revision, okRevision := number("policy_revision")
	epoch, okEpoch := number("authority_epoch")
	if !okRevision || !okEpoch {
		return zero, ErrLifecycleInput
	}
	return database.WorkloadPauseRequest{OwnerID: pathOwner, ExpectedGeneration: generation,
		ExpectedPolicyRevision: revision, ExpectedAuthorityEpoch: epoch}, nil
}
