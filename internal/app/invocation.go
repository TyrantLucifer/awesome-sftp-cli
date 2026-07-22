package app

import "github.com/TyrantLucifer/awesome-sftp-cli/internal/auth"

type Role string

const (
	RoleClient        Role = "client"
	RoleDaemon        Role = "daemon"
	RoleAskpass       Role = "askpass"
	RoleHelper        Role = "helper"
	RoleJob           Role = "job"
	RoleConfig        Role = "config"
	RoleDoctor        Role = "doctor"
	RoleUpgrade       Role = "upgrade"
	RoleSupportBundle Role = "support-bundle"
	RoleCompletion    Role = "completion"
	RoleInstall       Role = "__install"
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
	case string(RoleJob):
		return Invocation{Role: RoleJob}, nil
	case string(RoleConfig):
		return Invocation{Role: RoleConfig}, nil
	case string(RoleDoctor):
		return Invocation{Role: RoleDoctor}, nil
	case string(RoleUpgrade):
		return Invocation{Role: RoleUpgrade}, nil
	case string(RoleSupportBundle):
		return Invocation{Role: RoleSupportBundle}, nil
	case string(RoleCompletion):
		return Invocation{Role: RoleCompletion}, nil
	case string(RoleInstall):
		return Invocation{Role: RoleInstall}, nil
	case "--help":
		return Invocation{Role: RoleClient, ShowHelp: true}, nil
	case "--version":
		return Invocation{Role: RoleClient, ShowVersion: true}, nil
	default:
		return Invocation{Role: RoleClient}, nil
	}
}
