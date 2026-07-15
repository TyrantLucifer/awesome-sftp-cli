package provider

import (
	"fmt"
	"strings"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/domain"
)

const maxListPageLimit uint32 = 4096

// ValidateListRequest checks provider identity, location, page bound, and sort
// direction before an implementation consumes an opaque cursor.
func ValidateListRequest(endpointID domain.EndpointID, request ListRequest) error {
	if err := validateLocation(endpointID, request.Location, "list"); err != nil {
		return err
	}
	if request.Limit == 0 || request.Limit > maxListPageLimit {
		return invalidRequest(
			"list",
			endpointID,
			request.Location,
			fmt.Sprintf("page limit must be between 1 and %d", maxListPageLimit),
		)
	}
	if request.Sort != nil &&
		request.Sort.Direction != SortAscending &&
		request.Sort.Direction != SortDescending {
		return invalidRequest(
			"list",
			endpointID,
			request.Location,
			"sort direction is invalid",
		)
	}
	return nil
}

// ValidateListPage checks response invariants shared by all implementations.
func ValidateListPage(request ListRequest, page ListPage) error {
	if err := ValidateListRequest(request.Location.EndpointID, request); err != nil {
		return err
	}
	if page.Done != (page.NextCursor == "") {
		return invalidResponse(request.Location, "Done and NextCursor are inconsistent")
	}
	if len(page.Entries) > int(request.Limit) {
		return invalidResponse(request.Location, "page exceeds requested limit")
	}
	if page.Consistency != ConsistencySnapshot && page.Consistency != ConsistencyBestEffort {
		return invalidResponse(request.Location, "list consistency is invalid")
	}
	for _, entry := range page.Entries {
		if err := validateResponseLocation(request.Location.EndpointID, entry.Location); err != nil {
			return invalidResponse(request.Location, err.Error())
		}
	}
	return nil
}

// ValidateStatRequest checks provider identity and location.
func ValidateStatRequest(endpointID domain.EndpointID, request StatRequest) error {
	return validateLocation(endpointID, request.Location, "stat")
}

// ValidateOpenReadRequest checks provider identity, location, and byte range.
func ValidateOpenReadRequest(endpointID domain.EndpointID, request OpenReadRequest) error {
	if err := validateLocation(endpointID, request.Location, "open_read"); err != nil {
		return err
	}
	if request.Offset < 0 {
		return invalidRequest("open_read", endpointID, request.Location, "offset must not be negative")
	}
	if request.Limit != nil && *request.Limit < 0 {
		return invalidRequest("open_read", endpointID, request.Location, "limit must not be negative")
	}
	return nil
}

// ValidateOpenWriteRequest checks identity, location, offset, and disposition.
func ValidateOpenWriteRequest(endpointID domain.EndpointID, request OpenWriteRequest) error {
	if err := validateLocation(endpointID, request.Location, "open_write"); err != nil {
		return err
	}
	if request.Offset < 0 {
		return invalidRequest("open_write", endpointID, request.Location, "offset must not be negative")
	}
	switch request.Disposition {
	case WriteCreateNew, WriteResumeExisting, WriteTruncate:
		return nil
	default:
		return invalidRequest("open_write", endpointID, request.Location, "write disposition is invalid")
	}
}

// ValidateMkdirRequest checks provider identity and location.
func ValidateMkdirRequest(endpointID domain.EndpointID, request MkdirRequest) error {
	return validateLocation(endpointID, request.Location, "mkdir")
}

// ValidateRenameRequest requires source and destination on this provider.
func ValidateRenameRequest(endpointID domain.EndpointID, request RenameRequest) error {
	if err := validateLocation(endpointID, request.Source, "rename"); err != nil {
		return err
	}
	return validateLocation(endpointID, request.Destination, "rename")
}

// ValidateRemoveRequest checks provider identity and location.
func ValidateRemoveRequest(endpointID domain.EndpointID, request RemoveRequest) error {
	return validateLocation(endpointID, request.Location, "remove")
}

func validateLocation(
	endpointID domain.EndpointID,
	location domain.Location,
	operation string,
) error {
	if endpointID == "" {
		return invalidRequest(operation, endpointID, location, "provider endpoint ID is empty")
	}
	if location.EndpointID != endpointID {
		return invalidRequest(operation, endpointID, location, "location endpoint does not match provider")
	}
	if location.Path == "" {
		return invalidRequest(operation, endpointID, location, "location path is empty")
	}
	if strings.IndexByte(string(location.Path), 0) >= 0 {
		return invalidRequest(operation, endpointID, location, "location path contains NUL")
	}
	return nil
}

func validateResponseLocation(endpointID domain.EndpointID, location domain.Location) error {
	if location.EndpointID != endpointID {
		return fmt.Errorf("entry endpoint does not match request")
	}
	if location.Path == "" {
		return fmt.Errorf("entry path is empty")
	}
	if strings.IndexByte(string(location.Path), 0) >= 0 {
		return fmt.Errorf("entry path contains NUL")
	}
	return nil
}

func invalidRequest(
	operation string,
	endpointID domain.EndpointID,
	location domain.Location,
	message string,
) error {
	ownedLocation := location
	return &domain.OpError{
		Code:       domain.CodeInvalidArgument,
		Message:    message,
		Operation:  operation,
		EndpointID: endpointID,
		Location:   &ownedLocation,
		Retry:      domain.RetryAdvice{Kind: domain.RetryNever},
		Effect:     domain.EffectNone,
	}
}

func invalidResponse(location domain.Location, message string) error {
	ownedLocation := location
	return &domain.OpError{
		Code:       domain.CodeInternal,
		Message:    message,
		Operation:  "list",
		EndpointID: location.EndpointID,
		Location:   &ownedLocation,
		Retry:      domain.RetryAdvice{Kind: domain.RetryNever},
		Effect:     domain.EffectNone,
	}
}
