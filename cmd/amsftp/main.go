package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/app"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	os.Exit(app.Run(
		ctx,
		app.InternalRoleArgs(os.Args[1:], os.Getenv),
		os.Stdout,
		os.Stderr,
		app.DefaultHandlers(),
	))
}
