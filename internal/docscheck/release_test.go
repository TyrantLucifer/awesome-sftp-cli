package docscheck

import (
	"reflect"
	"testing"
)

func TestReleaseAuditUsesTheSameHumanDocumentationContract(t *testing.T) {
	root := prepareFixture(t, "valid")
	if got, want := CheckRelease(root), Check(root); !reflect.DeepEqual(got, want) {
		t.Fatalf("CheckRelease() = %#v, want Check() = %#v", got, want)
	}
}
