package acp

// Task 790: resident ACP listener. The stdio command (`reasonix acp`) stays
// the client-spawned form; this adds the host-resident form — one process
// listens on a loopback TCP port and every accepted connection becomes an
// independent ACP client surface (its own JSON-RPC Conn and session registry),
// exactly what Serve builds per stdio pipe.
//
// Transport reuse: Conn is built over io.Reader/io.Writer pairs, and net.Conn
// satisfies both, so the NDJSON JSON-RPC framing needs no adaptation at all —
// Serve(conn, conn, ...) is the whole story. Session lifecycle is likewise
// unchanged: each connection's service assembles controllers through the same
// Factory (boot.Build in production) and persists transcripts to the shared
// session dir, so a client that reconnects (to this or any other surface)
// resumes via session/load.

import (
	"context"
	"errors"
	"net"
	"sync"
)

// errListenerClosed is the accept-loop sentinel: after Close the loop must
// return quietly instead of reporting the use-of-closed-listener error.
var errListenerClosed = errors.New("acp: listener closed")

// Listener serves ACP over a TCP listener. The zero value is not usable; call
// Listen. Concurrent Serve + Close are safe.
type Listener struct {
	ln      net.Listener
	factory Factory
	info    AgentInfo

	mu    sync.Mutex
	conns map[net.Conn]struct{}
	wg    sync.WaitGroup // one count per live connection

	closeOnce sync.Once
	served    chan struct{} // closed when the accept loop has returned
}

// Listen binds addr (production passes a 127.0.0.1 host:port; the host owns
// the loopback-only policy) and returns a ready Listener. Nothing is accepted
// until Serve is called.
func Listen(addr string, factory Factory, info AgentInfo) (*Listener, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	return &Listener{
		ln:      ln,
		factory: factory,
		info:    info,
		conns:   make(map[net.Conn]struct{}),
		served:  make(chan struct{}),
	}, nil
}

// Addr reports the bound address; with a :0 port this is how callers learn
// the resolved port.
func (l *Listener) Addr() net.Addr { return l.ln.Addr() }

// Serve accepts connections until Close is called or ctx is cancelled. Each
// accepted connection runs acp.Serve on its own goroutine with a connection-
// scoped context: the client surfaces are independent — one dropping or
// misbehaving never touches another, and every one assembles sessions through
// the same Factory as the stdio command does.
func (l *Listener) Serve(ctx context.Context) error {
	defer close(l.served)
	for {
		conn, err := l.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return errListenerClosed
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			// Transient accept errors (e.g. EMFILE) must not kill the
			// listener for every future client; back off via the next
			// Accept's own blocking rather than spinning.
			continue
		}
		l.mu.Lock()
		if l.conns == nil { // Close raced the accept
			l.mu.Unlock()
			_ = conn.Close()
			return errListenerClosed
		}
		l.conns[conn] = struct{}{}
		l.mu.Unlock()
		l.wg.Add(1)
		go l.serveConn(ctx, conn)
	}
}

// serveConn drives one client surface until the peer disconnects, ctx ends, or
// Close tears the connection down. Serve's own teardown (abort in-flight
// prompts, close all session controllers) runs before the slot is released.
func (l *Listener) serveConn(ctx context.Context, conn net.Conn) {
	defer l.wg.Done()
	connCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	_ = Serve(connCtx, conn, conn, l.factory, l.info)
	l.mu.Lock()
	delete(l.conns, conn)
	l.mu.Unlock()
	_ = conn.Close()
}

// Close stops accepting, tears down every live connection (their Serve calls
// unwind and flush), and waits for them to finish. Safe to call from another
// goroutine and safe against double-close.
func (l *Listener) Close() error {
	var err error
	l.closeOnce.Do(func() {
		err = l.ln.Close()
		l.mu.Lock()
		conns := l.conns
		l.conns = nil
		l.mu.Unlock()
		for conn := range conns {
			_ = conn.Close()
		}
	})
	<-l.served
	l.wg.Wait()
	return err
}
