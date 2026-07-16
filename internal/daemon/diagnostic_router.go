package daemon

import (
	"encoding/json"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

type DiagnosticSource interface {
	Query(diagnostic.Query) diagnostic.Page
}

type DiagnosticListRequest struct {
	AfterSequence uint64            `json:"after_sequence,omitempty"`
	Limit         int               `json:"limit,omitempty"`
	EndpointID    domain.EndpointID `json:"endpoint_id,omitempty"`
	JobID         domain.JobID      `json:"job_id,omitempty"`
}

type DiagnosticListResponse struct {
	Records []diagnostic.Record `json:"records"`
	More    bool                `json:"more"`
}

func (session *providerSession) listDiagnostics(payload json.RawMessage) (DiagnosticListResponse, error) {
	if session.diagnostics == nil {
		return DiagnosticListResponse{}, &domain.OpError{Code: domain.CodeUnsupported, Message: "diagnostic snapshot is unavailable", Retry: domain.RetryAdvice{Kind: domain.RetryNever}, Effect: domain.EffectNone}
	}
	var request DiagnosticListRequest
	if err := decodePayload(payload, &request); err != nil {
		return DiagnosticListResponse{}, invalidArgument("decode diagnostic list request", err)
	}
	if request.Limit < 0 {
		return DiagnosticListResponse{}, invalidArgument("diagnostic list limit must not be negative", nil)
	}
	if request.EndpointID != "" {
		if _, err := domain.ParseEndpointID(string(request.EndpointID)); err != nil {
			return DiagnosticListResponse{}, invalidArgument("diagnostic endpoint ID is invalid", err)
		}
	}
	if request.JobID != "" {
		if _, err := domain.ParseJobID(string(request.JobID)); err != nil {
			return DiagnosticListResponse{}, invalidArgument("diagnostic Job ID is invalid", err)
		}
	}
	page := session.diagnostics.Query(diagnostic.Query{
		AfterSequence: request.AfterSequence,
		Limit:         request.Limit,
		EndpointID:    request.EndpointID,
		JobID:         request.JobID,
	})
	return DiagnosticListResponse{Records: page.Records, More: page.More}, nil
}
