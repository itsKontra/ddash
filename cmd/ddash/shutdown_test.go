package main

import (
	"context"
	"io"
	"net"
	"net/http"
	"slices"
	"sync"
	"testing"
	"testing/synctest"
	"time"
)

// steps records the shutdown's steps in the order they happened, and when.
type steps struct {
	mu   sync.Mutex
	done []string
	at   []time.Duration
	from time.Time
}

func (s *steps) add(step string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.done = append(s.done, step)
	s.at = append(s.at, time.Since(s.from))
}

func (s *steps) StopComponents(context.Context) { s.add("stop Components") }

func (s *steps) Close() error {
	s.add("close the core DB")
	return nil
}

// serving serves srv on one in-memory connection, or on none when conn is
// nil.
func serving(srv *http.Server, conn net.Conn) {
	go func() { _ = srv.Serve(&oneConn{conn: conn, done: make(chan struct{})}) }()
}

// On SIGTERM: stop accepting HTTP and let in-flight requests (and the
// Actions they run) finish, then stop the Components, and close the core
// DB last.
func TestShutdownLetsInFlightRequestsFinishBeforeStoppingComponents(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &steps{from: time.Now()}
		inFlight := make(chan struct{})
		srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(inFlight)
			time.Sleep(30 * time.Second)
			s.add("end the request")
		})}
		client, server := net.Pipe()
		serving(srv, server)
		go func() {
			_, _ = client.Write([]byte("GET / HTTP/1.1\r\nHost: ddash\r\n\r\n"))
			_, _ = io.Copy(io.Discard, client)
		}()
		<-inFlight

		_ = drain(srv, s)

		if want := []string{"end the request", "stop Components", "close the core DB"}; !slices.Equal(s.done, want) {
			t.Errorf("steps = %v, want %v", s.done, want)
		}
		// http.Server.Shutdown polls for idle connections, a little late.
		if got := s.at[len(s.at)-1]; got < 30*time.Second || got >= 31*time.Second {
			t.Errorf("drained %v after SIGTERM, want once the 30 s request ended", got)
		}
		client.Close()
	})
}

func TestAnIdleShutdownIsImmediate(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s := &steps{from: time.Now()}
		srv := &http.Server{Handler: http.NotFoundHandler()}
		serving(srv, nil)

		_ = drain(srv, s)

		if want := []string{"stop Components", "close the core DB"}; !slices.Equal(s.done, want) {
			t.Errorf("steps = %v, want %v", s.done, want)
		}
		if got := time.Since(s.from); got != 0 {
			t.Errorf("drained %v after SIGTERM, want at once", got)
		}
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
