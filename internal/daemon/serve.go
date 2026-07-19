package daemon

import (
	"context"
	"errors"
	"net"
	"sync"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/diagnostic"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
)

type ConnectionListener interface {
	Accept() (*net.UnixConn, error)
	Close() error
}

func Serve(ctx context.Context, listener ConnectionListener, server *Server) error {
	if listener == nil || server == nil {
		return errors.New("serve daemon: listener and server are required")
	}
	var connections sync.WaitGroup
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = listener.Close()
		case <-done:
		}
	}()
	defer close(done)
	defer connections.Wait()
	for {
		connection, err := listener.Accept()
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		connections.Add(1)
		go func() {
			defer connections.Done()
			if err := server.ServeConn(ctx, connection); err != nil {
				server.logger.Error("daemon connection failed", diagnostic.Component("daemon"), diagnostic.Event("connection_failed"), diagnostic.ErrorCode(domain.CodeTransportInterrupted))
			}
		}()
	}
}
