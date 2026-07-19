package app

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	helperruntime "github.com/TyrantLucifer/awesome-sftp-cli/internal/helper"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/ipc"
)

func TestRestrictedHelperServeRoleUsesOnlyFramedStdio(t *testing.T) {
	hello := helperruntime.ClientHello{
		MinimumProtocol: 1, MaximumProtocol: 1,
		MaximumFrame: helperruntime.MaxHelperFrameBytes, MaximumConcurrent: 1,
		ClientVersion: helperruntime.Version{Major: 4},
		Capabilities:  []helperruntime.CapabilityRequest{{Name: helperruntime.CapabilityStrongHash, MaximumVersion: 1}},
	}
	payload, err := json.Marshal(hello)
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := helperruntime.EncodeHelperEnvelope(helperruntime.Envelope{Version: 1, Type: helperruntime.FrameClientHello, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	var input bytes.Buffer
	frameWriter, err := ipc.NewWriter(&input, helperruntime.MaxHelperFrameBytes)
	if err != nil {
		t.Fatal(err)
	}
	if err := frameWriter.WriteFrame(envelope); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := runHelperWithIdentity(context.Background(), []string{"serve"}, &input, &output, 1001); err != nil {
		t.Fatal(err)
	}
	if !bytes.HasPrefix(output.Bytes(), []byte(helperruntime.HelperPreface)) {
		t.Fatalf("helper stdout = %q", output.Bytes())
	}
	if err := runHelperWithIdentity(context.Background(), []string{"serve", "extra"}, bytes.NewReader(nil), &bytes.Buffer{}, 1001); err == nil {
		t.Fatal("helper accepted an additional command argument")
	}
	if err := runHelperWithIdentity(context.Background(), []string{"serve"}, bytes.NewReader(nil), &bytes.Buffer{}, 0); err == nil {
		t.Fatal("helper accepted effective uid 0")
	}
}
