package app

type Role string

const (
	RoleClient  Role = "client"
	RoleDaemon  Role = "daemon"
	RoleAskpass Role = "askpass"
	RoleHelper  Role = "helper"
)

type Invocation struct {
	Role        Role
	ShowHelp    bool
	ShowVersion bool
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
	case "--help":
		return Invocation{Role: RoleClient, ShowHelp: true}, nil
	case "--version":
		return Invocation{Role: RoleClient, ShowVersion: true}, nil
	default:
		return Invocation{Role: RoleClient}, nil
	}
}
