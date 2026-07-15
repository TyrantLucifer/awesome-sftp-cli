package openssh

import (
	"os/exec"
	"reflect"
	"testing"
)

func TestArgumentsMatchADR0001Exactly(t *testing.T) {
	got, err := Arguments("work-alias")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"-T", "-oEscapeChar=none", "-oForwardAgent=no", "-oForwardX11=no", "-oPermitLocalCommand=no", "-oClearAllForwardings=yes", "-oRemoteCommand=none", "-oStdinNull=no", "-oForkAfterAuthentication=no", "-oTunnel=no", "-oGSSAPIDelegateCredentials=no", "-s", "work-alias", "sftp"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("arguments = %#v", got)
	}
}

func TestValidateHostAliasRejectsOptionAndControlInjection(t *testing.T) {
	for _, value := range []string{"", "-oProxyCommand=bad", "host\x00bad", "host\nbad", "host\x7fbad"} {
		if err := ValidateHostAlias(value); err == nil {
			t.Fatalf("ValidateHostAlias(%q) error = nil", value)
		}
	}
	if err := ValidateHostAlias("work-prod.example"); err != nil {
		t.Fatal(err)
	}
}

func TestExpectedExitOnlyAcceptsCancellationSignal(t *testing.T) {
	nonzero := exec.Command("/bin/sh", "-c", "exit 7")
	if err := nonzero.Run(); err == nil || isExpectedExit(err) {
		t.Fatalf("ordinary non-zero exit classified as expected: %v", err)
	}

	killed := exec.Command("/bin/sh", "-c", "exec sleep 30")
	if err := killed.Start(); err != nil {
		t.Fatal(err)
	}
	if err := killed.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := killed.Wait(); err == nil || !isExpectedExit(err) {
		t.Fatalf("cancellation signal classified as unexpected: %v", err)
	}
}
