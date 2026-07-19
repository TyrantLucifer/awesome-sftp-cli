package app

import (
	"errors"
	"fmt"
	"strings"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/workspace"
)

type clientInvocation struct {
	Workspace string
	Locations []string
	Pick      bool
}

func parseClientInvocation(args []string) (clientInvocation, error) {
	if len(args) == 0 {
		return clientInvocation{Pick: true}, nil
	}
	if args[0] == "--workspace" {
		if len(args) != 2 {
			return clientInvocation{}, errors.New("--workspace requires exactly one name")
		}
		if err := workspace.ValidateName(args[1]); err != nil {
			return clientInvocation{}, fmt.Errorf("--workspace: %w", err)
		}
		return clientInvocation{Workspace: args[1]}, nil
	}
	if len(args) > 2 {
		return clientInvocation{}, errors.New("client accepts at most two locations")
	}
	for _, argument := range args {
		if strings.HasPrefix(argument, "-") {
			return clientInvocation{}, fmt.Errorf("unknown client option %q", argument)
		}
	}
	return clientInvocation{Locations: append([]string(nil), args...)}, nil
}
