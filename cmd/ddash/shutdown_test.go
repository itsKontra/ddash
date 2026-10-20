package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// components records when the Components were stopped and the core DB
// closed.
type components struct {
	mu             sync.Mutex
	stopped, close time.Time
}

func (c *components) StopComponents(context.Context) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = time.Now()
}

func (c *components) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.close = time.Now()
	return nil
}

// On SIGTERM: stop accepting HTTP, stop the Components at once (not after
// in-flight requests), and close the core DB only once HTTP is drained.
func TestShutdownStopsComponentsAtOnceAndClosesTheDBLast(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		release := make(chan struct{})
		inFlight := make(chan struct{})
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(inFlight)
			<-release
		})}
		client, server := net.Pipe()
		ln := &oneConn{conn: server, done: make(chan struct{})}
		go func() { _ = srv.Serve(ln) }()
		go func() {
			_, _ = client.Write([]byte("GET / HTTP/1.1\r\nHost: ddash\r\n\r\n"))
			_, _ = io.Copy(io.Discard, client)
		}()
		<-inFlight

		c := &components{}
		go func() {
			time.Sleep(3 * time.Second)
			close(release)
		}()
		drain(srv, c)

		if got := c.stopped.Sub(started); got != 0 {
			t.Errorf("Components stopped %v after SIGTERM, want at once", got)
		}
		// http.Server.Shutdown polls for idle connections, a little late.
		if got := c.close.Sub(started); got < 3*time.Second || got >= httpShutdownTimeout {
			t.Errorf("core DB closed %v after SIGTERM, want once the 3 s request ended", got)
		}
		client.Close()
	})
}

// oneConn is a listener handing out one in-memory connection.
type oneConn struct {
	conn net.Conn
	once sync.Once
	done chan struct{}
}

func (l *oneConn) Accept() (net.Conn, error) {
	var c net.Conn
	l.once.Do(func() { c = l.conn })
	if c != nil {
		return c, nil
	}
	<-l.done
	return nil, net.ErrClosed
}

func (l *oneConn) Close() error {
	select {
	case <-l.done:
	default:
		close(l.done)
	}
	return nil
}

func (l *oneConn) Addr() net.Addr { return &net.TCPAddr{} }
