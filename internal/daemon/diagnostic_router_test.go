package daemon

import (
	"context"
	"log/slog"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
	providerapi "github.com/TyrantLucifer/awesome-mac-sftp/internal/provider"
)

func TestProviderSessionsExposeBoundedRedactedDiagnosticRecords(t *testing.T) {
	implementation := testLocalProvider(t)
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	ring := diagnostic.NewRing(1000)
	factory.SetDiagnosticSource(ring)
	logger := slog.New(diagnostic.NewRingHandler(ring, nil))
	jobID := domain.JobID("job_aaaaaaaaaaaaaaaaaaaaaaaaaa")
	endpointID := implementation.Descriptor().ID
	logger.Info("secret body", diagnostic.Component("transfer"), diagnostic.Event("progress"), diagnostic.JobID(jobID), diagnostic.EndpointID(endpointID), slog.String("path", "/secret"))

	session := factory.NewSession()
	t.Cleanup(func() { _ = session.Close() })
	response := handlePayload[DiagnosticListResponse](t, session, DiagnosticList, DiagnosticListRequest{JobID: jobID, EndpointID: endpointID, Limit: 256})
	if len(response.Records) != 1 || response.Records[0].Message != "diagnostic" || response.Records[0].JobID != jobID || response.Records[0].EndpointID != endpointID {
		t.Fatalf("response = %#v", response)
	}
}

func TestDiagnosticListRejectsInvalidCorrelationID(t *testing.T) {
	implementation := testLocalProvider(t)
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	factory.SetDiagnosticSource(diagnostic.NewRing(10))
	session := factory.NewSession()
	t.Cleanup(func() { _ = session.Close() })
	payload := []byte(`{"job_id":"not-a-job-id"}`)
	if _, err := session.Handle(context.Background(), DiagnosticList, payload); !domain.IsCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("error = %v, want invalid_argument", err)
	}
}
