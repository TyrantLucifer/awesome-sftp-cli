package app

import (
	"context"
	"fmt"
	"io"

	"github.com/TyrantLucifer/awesome-mac-sftp/internal/buildinfo"
)

const usage = `Usage:
  amsftp [<location> [<location>]]
  amsftp --workspace <name>
  amsftp [client|daemon|askpass|helper] [arguments...]
  amsftp [--help|--version]
`

type Handler func(context.Context, []string, io.Writer, io.Writer) error

type Handlers struct {
	Client  Handler
	Daemon  Handler
	Askpass Handler
	Helper  Handler
}

func Run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	handlers Handlers,
) int {
	invocation, err := ParseInvocation(args)
	if err != nil {
		fmt.Fprintf(stderr, "amsftp: %v\n", err)
		return 2
	}

	if invocation.ShowHelp {
		fmt.Fprint(stdout, usage)
		return 0
	}
	if invocation.ShowVersion {
		fmt.Fprintln(stdout, buildinfo.Current())
		return 0
	}

	handler := handlers.handler(invocation.Role)
	if handler == nil {
		fmt.Fprintf(stderr, "amsftp: %s handler is not configured\n", invocation.Role)
		return 1
	}
	if err := handler(ctx, roleArgs(args, invocation.Role), stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "amsftp: %s handler: %v\n", invocation.Role, err)
		return 1
	}

	return 0
}

func (h Handlers) handler(role Role) Handler {
	switch role {
	case RoleClient:
		return h.Client
	case RoleDaemon:
		return h.Daemon
	case RoleAskpass:
		return h.Askpass
	case RoleHelper:
		return h.Helper
	default:
		return nil
	}
}

func roleArgs(args []string, role Role) []string {
	if len(args) > 0 && args[0] == string(role) {
		return args[1:]
	}
	return args
}
