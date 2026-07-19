package daemon

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	helperruntime "github.com/TyrantLucifer/awesome-sftp-cli/internal/helper"
	providerapi "github.com/TyrantLucifer/awesome-sftp-cli/internal/provider"
)

type recordingHelperLifecycle struct {
	calls    int
	request  helperruntime.LifecycleRequest
	response helperruntime.LifecycleResult
	err      error
}

func (service *recordingHelperLifecycle) Execute(_ context.Context, request helperruntime.LifecycleRequest) (helperruntime.LifecycleResult, error) {
	service.calls++
	service.request = request
	return service.response, service.err
}

func TestHelperLifecycleRPCDispatchesOnlyFrozenManagementFields(t *testing.T) {
	factory := newHelperLifecycleFactory(t)
	service := &recordingHelperLifecycle{response: helperruntime.LifecycleResult{State: helperruntime.LifecycleStateLevel0}}
	factory.SetHelperLifecycle(service)
	session := factory.NewSession()
	t.Cleanup(func() { _ = session.Close() })

	response := handlePayload[HelperLifecycleResponse](t, session, HelperLifecycle, HelperLifecycleRequest{
		Command: HelperLifecycleStatus, HostAlias: "production-sftp",
	})
	if response.State != "level0" || service.calls != 1 {
		t.Fatalf("response/calls = %#v/%d", response, service.calls)
	}
	if service.request.Command != helperruntime.LifecycleStatus || service.request.HostAlias != "production-sftp" || service.request.AcceptSharedSessionStableHome {
		t.Fatalf("dispatched request = %#v", service.request)
	}
}

func TestHelperLifecycleRPCRejectsArtifactPathAndCommandInjectionBeforeService(t *testing.T) {
	factory := newHelperLifecycleFactory(t)
	service := &recordingHelperLifecycle{}
	factory.SetHelperLifecycle(service)
	session := factory.NewSession()
	t.Cleanup(func() { _ = session.Close() })

	for _, payload := range []json.RawMessage{
		[]byte(`{"command":"status","host_alias":"work","manifest":"raw"}`),
		[]byte(`{"command":"status","host_alias":"work","artifact_path":"/tmp/helper"}`),
		[]byte(`{"command":"status","host_alias":"work","command_string":"exec evil"}`),
	} {
		if _, err := session.Handle(context.Background(), HelperLifecycle, payload); !domain.IsCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("payload %s error = %v, want invalid_argument", payload, err)
		}
	}
	if service.calls != 0 {
		t.Fatalf("invalid payload reached lifecycle service %d times", service.calls)
	}
}

func TestHelperLifecycleRPCEnforcesCommandSpecificConsent(t *testing.T) {
	factory := newHelperLifecycleFactory(t)
	service := &recordingHelperLifecycle{}
	factory.SetHelperLifecycle(service)
	session := factory.NewSession()
	t.Cleanup(func() { _ = session.Close() })

	for _, request := range []HelperLifecycleRequest{
		{Command: HelperLifecycleInstall, HostAlias: "work"},
		{Command: HelperLifecycleUpgrade, HostAlias: "work"},
		{Command: HelperLifecycleRemove, HostAlias: "work"},
		{Command: HelperLifecycleDisable, HostAlias: "work", AcceptSharedSessionStableHome: true},
		{Command: HelperLifecycleStatus, HostAlias: "work", AcceptSharedSessionStableHome: true},
	} {
		payload, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := session.Handle(context.Background(), HelperLifecycle, payload); !domain.IsCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("request %#v error = %v, want invalid_argument", request, err)
		}
	}
	if service.calls != 0 {
		t.Fatalf("invalid consent reached lifecycle service %d times", service.calls)
	}
	for _, command := range []HelperLifecycleCommand{HelperLifecycleInstall, HelperLifecycleUpgrade, HelperLifecycleRemove} {
		handlePayload[HelperLifecycleResponse](t, session, HelperLifecycle, HelperLifecycleRequest{
			Command: command, HostAlias: "work", AcceptSharedSessionStableHome: true,
		})
	}
	if service.calls != 3 {
		t.Fatalf("valid consent calls = %d, want 3", service.calls)
	}
}

func TestHelperLifecycleRPCFailsClosedWithoutOwnedService(t *testing.T) {
	factory := newHelperLifecycleFactory(t)
	session := factory.NewSession()
	t.Cleanup(func() { _ = session.Close() })
	payload, err := json.Marshal(HelperLifecycleRequest{Command: HelperLifecycleStatus, HostAlias: "work"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.Handle(context.Background(), HelperLifecycle, payload); !domain.IsCode(err, domain.CodeUnsupported) {
		t.Fatalf("error = %v, want unsupported", err)
	}
}

func newHelperLifecycleFactory(t *testing.T) *ProviderSessions {
	t.Helper()
	implementation := testLocalProvider(t)
	factory, err := NewProviderSessions([]providerapi.Provider{implementation}, 4)
	if err != nil {
		t.Fatal(err)
	}
	return factory
}
