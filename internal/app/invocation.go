package app

import "github.com/TyrantLucifer/awesome-mac-sftp/internal/auth"

type Role string

const (
	RoleClient  Role = "client"
	RoleDaemon  Role = "daemon"
	RoleAskpass Role = "askpass"
	RoleHelper  Role = "helper"
	RoleConfig  Role = "config"
)

type Invocation struct {
	Role        Role
	ShowHelp    bool
	ShowVersion bool
}

func InternalRoleArgs(args []string, getenv func(string) string) []string {
	if getenv != nil && getenv(auth.EnvInternalRole) == string(auth.InternalRoleAskpass) {
		return append([]string{string(RoleAskpass)}, args...)
	}
	return args
}

func ParseInvocation(args []string) (Invocation, error) {
	if len(args) == 0 {
		return Invocation{Role: RoleClient}, nil
	}

	switch args[0] {
	case string(RoleClient):
		return Invocation{Role: RoleClient}, nil
	case string(RoleDaemon):
		return Invocation{Role: RoleDaemon}, nil
	case string(RoleAskpass):
		return Invocation{Role: RoleAskpass}, nil
	case string(RoleHelper):
		return Invocation{Role: RoleHelper}, nil
	case string(RoleConfig):
		return Invocation{Role: RoleConfig}, nil
	case "--help":
		return Invocation{Role: RoleClient, ShowHelp: true}, nil
	case "--version":
		return Invocation{Role: RoleClient, ShowVersion: true}, nil
	default:
		return Invocation{Role: RoleClient}, nil
	}
}
