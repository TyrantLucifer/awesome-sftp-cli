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
  amsftp config <validate|print-effective> [<path>]
  amsftp [client|daemon|askpass|helper] [arguments...]
  amsftp [--help|--version]
`

type Handler func(context.Context, []string, io.Writer, io.Writer) error

type Handlers struct {
	Client  Handler
	Daemon  Handler
	Askpass Handler
	Helper  Handler
	Config  Handler
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
		return int(ExitUsage)
	}

	if invocation.ShowHelp {
		fmt.Fprint(stdout, usage)
		return int(ExitSuccess)
	}
	if invocation.ShowVersion {
		fmt.Fprintln(stdout, buildinfo.Current())
		return int(ExitSuccess)
	}

	handler := handlers.handler(invocation.Role)
	if handler == nil {
		fmt.Fprintf(stderr, "amsftp: %s handler is not configured\n", invocation.Role)
		return int(ExitInternal)
	}
	if err := handler(ctx, roleArgs(args, invocation.Role), stdout, stderr); err != nil {
		fmt.Fprintf(stderr, "amsftp: %s handler: %v\n", invocation.Role, err)
		return int(exitCode(err))
	}

	return int(ExitSuccess)
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
	case RoleConfig:
		return h.Config
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
