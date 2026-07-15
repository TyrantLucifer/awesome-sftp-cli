package provider

import (
	"errors"
	"strings"
	"testing"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

const (
	testEndpointID      domain.EndpointID = "ep_aaaaaaaaaaaaaaaaaaaaaaaaaa"
	testOtherEndpointID domain.EndpointID = "ep_bbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestValidateListRequestRejectsPageLimitOutsideBounds(t *testing.T) {
	tests := []struct {
		name  string
		limit uint32
	}{
		{name: "zero", limit: 0},
		{name: "above maximum", limit: 4097},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := ListRequest{
				Location: testLocation("/"),
				Limit:    tt.limit,
			}
			err := ValidateListRequest(testEndpointID, request)
			requireOpError(t, err, domain.CodeInvalidArgument, "list", request.Location)
		})
	}
}

func TestValidateListRequestAcceptsBoundaryLimits(t *testing.T) {
	for _, limit := range []uint32{1, 4096} {
		request := ListRequest{Location: testLocation("/"), Limit: limit}
		if err := ValidateListRequest(testEndpointID, request); err != nil {
			t.Fatalf("ValidateListRequest(limit=%d): %v", limit, err)
		}
	}
}

func TestValidateListRequestRejectsEndpointMismatch(t *testing.T) {
	request := ListRequest{
		Location: domain.Location{EndpointID: testOtherEndpointID, Path: "/"},
		Limit:    1,
	}

	err := ValidateListRequest(testEndpointID, request)
	requireOpError(t, err, domain.CodeInvalidArgument, "list", request.Location)
}

func TestValidateListRequestRejectsInvalidPath(t *testing.T) {
	tests := []struct {
		name string
		path domain.CanonicalPath
	}{
		{name: "empty"},
		{name: "NUL", path: domain.CanonicalPath([]byte{'/', 0, 'x'})},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := ListRequest{
				Location: domain.Location{EndpointID: testEndpointID, Path: tt.path},
				Limit:    1,
			}
			err := ValidateListRequest(testEndpointID, request)
			requireOpError(t, err, domain.CodeInvalidArgument, "list", request.Location)
		})
	}
}

func TestValidateListRequestRejectsInvalidSortDirection(t *testing.T) {
	request := ListRequest{
		Location: testLocation("/"),
		Limit:    1,
		Sort: &SortHint{
			Key:       "name",
			Direction: "sideways",
		},
	}

	err := ValidateListRequest(testEndpointID, request)
	requireOpError(t, err, domain.CodeInvalidArgument, "list", request.Location)
}

func TestValidateOpenReadRequestRejectsInvalidRange(t *testing.T) {
	negative := int64(-1)
	tests := []struct {
		name   string
		offset int64
		limit  *int64
	}{
		{name: "negative offset", offset: -1},
		{name: "negative limit", limit: &negative},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			request := OpenReadRequest{
				Location: testLocation("/file"),
				Offset:   tt.offset,
				Limit:    tt.limit,
			}
			err := ValidateOpenReadRequest(testEndpointID, request)
			requireOpError(t, err, domain.CodeInvalidArgument, "open_read", request.Location)
		})
	}
}

func TestValidateListPageRequiresDoneExactlyWhenCursorIsEmpty(t *testing.T) {
	request := ListRequest{Location: testLocation("/"), Limit: 1}
	tests := []struct {
		name string
		page ListPage
	}{
		{
			name: "done with cursor",
			page: ListPage{
				Done:        true,
				NextCursor:  "opaque-cursor",
				Consistency: ConsistencySnapshot,
			},
		},
		{
			name: "not done without cursor",
			page: ListPage{
				Done:        false,
				NextCursor:  "",
				Consistency: ConsistencySnapshot,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateListPage(request, tt.page)
			requireOpError(t, err, domain.CodeInternal, "list", request.Location)
		})
	}
}

func TestValidateListPageRejectsUnboundedOrMismatchedEntries(t *testing.T) {
	request := ListRequest{Location: testLocation("/"), Limit: 1}
	tests := []struct {
		name string
		page ListPage
	}{
		{
			name: "too many entries",
			page: ListPage{
				Entries: []domain.Entry{
					{Location: testLocation("/a")},
					{Location: testLocation("/b")},
				},
				Done:        true,
				Consistency: ConsistencySnapshot,
			},
		},
		{
			name: "entry endpoint mismatch",
			page: ListPage{
				Entries: []domain.Entry{{
					Location: domain.Location{EndpointID: testOtherEndpointID, Path: "/a"},
				}},
				Done:        true,
				Consistency: ConsistencySnapshot,
			},
		},
		{
			name: "unknown consistency",
			page: ListPage{
				Done:        true,
				Consistency: "unknown",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateListPage(request, tt.page)
			requireOpError(t, err, domain.CodeInternal, "list", request.Location)
		})
	}
}

func TestValidateRequestsAcceptValidValues(t *testing.T) {
	zero := int64(0)
	source := testLocation("/source")
	destination := testLocation("/destination")

	tests := []struct {
		name     string
		validate func() error
	}{
		{
			name: "list",
			validate: func() error {
				return ValidateListRequest(testEndpointID, ListRequest{
					Location: source,
					Limit:    1,
					Sort: &SortHint{
						Key:       "name",
						Direction: SortAscending,
					},
				})
			},
		},
		{
			name: "list page",
			validate: func() error {
				return ValidateListPage(
					ListRequest{Location: source, Limit: 1},
					ListPage{Done: true, Consistency: ConsistencyBestEffort},
				)
			},
		},
		{
			name: "stat",
			validate: func() error {
				return ValidateStatRequest(testEndpointID, StatRequest{Location: source})
			},
		},
		{
			name: "open read",
			validate: func() error {
				return ValidateOpenReadRequest(testEndpointID, OpenReadRequest{
					Location: source,
					Limit:    &zero,
				})
			},
		},
		{
			name: "open write",
			validate: func() error {
				return ValidateOpenWriteRequest(testEndpointID, OpenWriteRequest{
					Location:    source,
					Disposition: WriteCreateNew,
				})
			},
		},
		{
			name: "mkdir",
			validate: func() error {
				return ValidateMkdirRequest(testEndpointID, MkdirRequest{Location: source})
			},
		},
		{
			name: "rename",
			validate: func() error {
				return ValidateRenameRequest(testEndpointID, RenameRequest{
					Source:      source,
					Destination: destination,
				})
			},
		},
		{
			name: "remove",
			validate: func() error {
				return ValidateRemoveRequest(testEndpointID, RemoveRequest{Location: source})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.validate(); err != nil {
				t.Fatalf("validation failed: %v", err)
			}
		})
	}
}

func TestValidateMutableRequestsRejectInvalidValues(t *testing.T) {
	tests := []struct {
		name      string
		operation string
		location  domain.Location
		validate  func() error
	}{
		{
			name:      "negative write offset",
			operation: "open_write",
			location:  testLocation("/file"),
			validate: func() error {
				return ValidateOpenWriteRequest(testEndpointID, OpenWriteRequest{
					Location:    testLocation("/file"),
					Offset:      -1,
					Disposition: WriteResumeExisting,
				})
			},
		},
		{
			name:      "unknown write disposition",
			operation: "open_write",
			location:  testLocation("/file"),
			validate: func() error {
				return ValidateOpenWriteRequest(testEndpointID, OpenWriteRequest{
					Location:    testLocation("/file"),
					Disposition: "unknown",
				})
			},
		},
		{
			name:      "rename destination endpoint mismatch",
			operation: "rename",
			location:  domain.Location{EndpointID: testOtherEndpointID, Path: "/destination"},
			validate: func() error {
				return ValidateRenameRequest(testEndpointID, RenameRequest{
					Source:      testLocation("/source"),
					Destination: domain.Location{EndpointID: testOtherEndpointID, Path: "/destination"},
				})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.validate()
			requireOpError(t, err, domain.CodeInvalidArgument, tt.operation, tt.location)
		})
	}
}

func testLocation(path domain.CanonicalPath) domain.Location {
	return domain.Location{EndpointID: testEndpointID, Path: path}
}

func requireOpError(
	t *testing.T,
	err error,
	wantCode domain.Code,
	wantOperation string,
	wantLocation domain.Location,
) {
	t.Helper()
	if err == nil {
		t.Fatal("validation error = nil")
	}

	var opError *domain.OpError
	if !errors.As(err, &opError) {
		t.Fatalf("validation error type = %T, want *domain.OpError", err)
	}
	if opError.Code != wantCode {
		t.Errorf("Code = %q, want %q", opError.Code, wantCode)
	}
	if opError.Operation != wantOperation {
		t.Errorf("Operation = %q, want %q", opError.Operation, wantOperation)
	}
	if opError.EndpointID != testEndpointID {
		t.Errorf("EndpointID = %q, want %q", opError.EndpointID, testEndpointID)
	}
	if opError.Location == nil || *opError.Location != wantLocation {
		t.Errorf("Location = %#v, want %#v", opError.Location, wantLocation)
	}
	if opError.Retry.Kind != domain.RetryNever {
		t.Errorf("Retry.Kind = %q, want %q", opError.Retry.Kind, domain.RetryNever)
	}
	if opError.Effect != domain.EffectNone {
		t.Errorf("Effect = %q, want %q", opError.Effect, domain.EffectNone)
	}
	if wantLocation.Path != "" && strings.Contains(opError.Error(), string(wantLocation.Path)) {
		t.Errorf("Error() = %q, must not include raw path", opError.Error())
	}
}
