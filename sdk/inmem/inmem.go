// Package inmem serves gRPC services in memory, over net.Pipe connections.
// The host's in-process launcher and sdktest run Components with it.
//
// Unlike a socket or bufconn, a pipe blocks only on channels, which
// testing/synctest understands, so a server and its client work inside a
// synctest bubble.
package inmem

import (
	"context"
	"net"
	"sync"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Serve starts a gRPC server with the services register adds, and returns a
// client connection to it. stop closes the connection and the server.
func Serve(name string, register func(grpc.ServiceRegistrar)) (conn *grpc.ClientConn, stop func(), err error) {
	lis := newPipeListener()
	srv := grpc.NewServer()
	register(srv)
	go srv.Serve(lis)

	conn, err = grpc.NewClient("passthrough:///"+name,
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.dial(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		srv.Stop()
		return nil, nil, err
	}
	return conn, func() {
		conn.Close()
		srv.Stop()
	}, nil
}

// pipeListener hands out net.Pipe connections.
type pipeListener struct {
	conns     chan net.Conn
	done      chan struct{}
	closeOnce sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), done: make(chan struct{})}
}

func (l *pipeListener) dial(ctx context.Context) (net.Conn, error) {
	client, server := net.Pipe()
	select {
	case l.conns <- server:
		return client, nil
	case <-l.done:
		return nil, net.ErrClosed
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.closeOnce.Do(func() { close(l.done) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return pipeAddr{} }

type pipeAddr struct{}

func (pipeAddr) Network() string { return "pipe" }
func (pipeAddr) String() string  { return "pipe" }
