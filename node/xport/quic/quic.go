// Package quic is the real QUIC transport behind the xport interface.
//
// The interface and the rules were fixed in xport/quicmock before this
// implementation existed; that file is the contract and this file follows it:
//
//  1. TLS 1.3 from crypto/tls, with the same certificate and the same pinning.
//     quic-go on go1.27 hands the handshake to the standard library's own
//     tls.QUICConn (internal/handshake/tls_config_go127.go), so there is no
//     second TLS stack here: the tls.Config the caller built for the TCP
//     listener - device certificate, client CA, pinning callback - is given to
//     QUIC unchanged. The transport only adds an ALPN identifier, on a clone;
//     it never mutates the caller's config.
//
//  2. No second pairing. Streams are handed upward as net.Conn and everything
//     above - HTTP, the node protocol, identity, pairing, trust - runs exactly
//     as it does over TCP and KCP. QUIC connection IDs never leave this file.
//
//  3. 0-RTT off. The client never calls DialEarly, so there is no early-data
//     path to get wrong; the server accepts no session resumption tickets for
//     early data because none are issued. quicmock.EarlyDataPolicy stays the
//     written-down rule should that ever change.
//
// One HTTP connection is one QUIC stream. A connection whose stream ends is
// reaped by the idle timeout rather than by this package: nothing here keeps
// per-peer goroutines alive.
package quic

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"

	quicgo "github.com/quic-go/quic-go"

	"github.com/human-agent65535/nassimhub-node/proto"
)

// ALPN is the application protocol identifier of the node protocol over
// QUIC. Both ends must agree on it; it is transport-owned, not configuration.
const ALPN = "nassimhub-node/1"

// Options configures the transport.
type Options struct {
	// TLSConfig is the SAME configuration the caller serves TLS with on the
	// TCP listener: certificate, client CA and pinning callback. QUIC carries
	// TLS rather than wrapping it again, so this is required, not optional;
	// a nil config fails closed.
	TLSConfig *tls.Config
	// DialTimeout bounds Dial. Zero means ten seconds.
	DialTimeout time.Duration
}

// New builds the transport. The mode is QUIC by construction.
func New(options Options) (*Transport, error) {
	if options.TLSConfig == nil {
		return nil, errors.New("quic: a TLS configuration is required, this transport does not run without TLS")
	}
	return &Transport{options: options}, nil
}

// Transport is the QUIC xport.Transport.
type Transport struct {
	options Options

	mu        sync.Mutex
	last      *quicgo.Conn
	bytesSent atomic.Uint64
	bytesRecv atomic.Uint64
}

// Mode names it.
func (t *Transport) Mode() proto.TransportMode { return proto.TransportQUIC }

// Dial opens one connection and its first stream, and returns the stream as
// the net.Conn everything above xport expects.
func (t *Transport) Dial(ctx context.Context, address string) (net.Conn, error) {
	timeout := t.options.DialTimeout
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	dial, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, err := quicgo.DialAddr(dial, address, tlsClientConfig(t.options.TLSConfig), quicConfig())
	if err != nil {
		return nil, err
	}
	stream, err := conn.OpenStreamSync(dial)
	if err != nil {
		_ = conn.CloseWithError(0, "no stream")
		return nil, err
	}
	t.mu.Lock()
	t.last = conn
	t.mu.Unlock()
	return &streamConn{stream: stream, conn: conn, sent: &t.bytesSent, recv: &t.bytesRecv}, nil
}

// Listen accepts QUIC connections; each accepted connection's first stream
// becomes one accepted net.Conn.
func (t *Transport) Listen(address string) (ConnListener, error) {
	inner, err := quicgo.ListenAddr(address, tlsServerConfig(t.options.TLSConfig), quicConfig())
	if err != nil {
		return nil, err
	}
	return &Listener{inner: inner, transport: t}, nil
}

// Stats reports what this transport measures itself: application bytes
// through the streams. Link-level numbers (RTT, loss) belong to quic-go's
// congestion controller, which exposes none of them, so they stay zero and
// Measured stays false - a dash on the status page, not an invented 0 ms.
func (t *Transport) Stats() Stats {
	return Stats{
		BytesSent: t.bytesSent.Load(),
		BytesRecv: t.bytesRecv.Load(),
		Measured:  false,
	}
}

// Stats is this package's measurement. xport converts it into its own Stats
// type: xport imports this package, so this one cannot import xport back, and
// the conversion lives in the one place that sees both.
type Stats struct {
	BytesSent uint64
	BytesRecv uint64
	Measured  bool
}

// ConnListener is the shape xport.Listener has. It is declared here because
// xport imports this package and this package cannot import xport back; the
// concrete *Listener below satisfies both by structure, not by declaration.
type ConnListener interface {
	Accept() (net.Conn, error)
	Close() error
	Addr() net.Addr
}

// Listener accepts QUIC streams as net.Conn.
type Listener struct {
	inner     *quicgo.Listener
	transport *Transport
}

// Accept returns the next connection's first stream.
func (l *Listener) Accept() (net.Conn, error) {
	conn, err := l.inner.Accept(context.Background())
	if err != nil {
		return nil, err
	}
	stream, err := conn.AcceptStream(context.Background())
	if err != nil {
		_ = conn.CloseWithError(0, "no stream")
		return nil, err
	}
	return &streamConn{stream: stream, conn: conn, sent: &l.transport.bytesSent, recv: &l.transport.bytesRecv}, nil
}

// Close stops accepting.
func (l *Listener) Close() error { return l.inner.Close() }

// Addr reports the bound UDP address.
func (l *Listener) Addr() net.Addr { return l.inner.Addr() }

// TLSTerminated reports that the connection this listener hands out has
// already run its TLS handshake inside QUIC. The server layer asks this
// instead of knowing which transport it configured, and must not wrap the
// connection in tls.Server a second time.
func (l *Listener) TLSTerminated() bool { return true }

// streamConn adapts one QUIC stream to net.Conn, counting application bytes
// on the way through.
type streamConn struct {
	stream *quicgo.Stream
	conn   *quicgo.Conn
	sent   *atomic.Uint64
	recv   *atomic.Uint64
}

func (c *streamConn) Read(buffer []byte) (int, error) {
	n, err := c.stream.Read(buffer)
	c.recv.Add(uint64(n))
	return n, err
}

func (c *streamConn) Write(buffer []byte) (int, error) {
	n, err := c.stream.Write(buffer)
	c.sent.Add(uint64(n))
	return n, err
}

// Close ends the stream and, because one HTTP connection is one stream on
// one connection here, the connection with it: nothing else will use it, and
// waiting for the idle timeout to reap it would hold state for a minute.
func (c *streamConn) Close() error {
	err := c.stream.Close()
	_ = c.conn.CloseWithError(0, "stream closed")
	return err
}

func (c *streamConn) LocalAddr() net.Addr  { return c.conn.LocalAddr() }
func (c *streamConn) RemoteAddr() net.Addr { return c.conn.RemoteAddr() }

func (c *streamConn) SetDeadline(t time.Time) error      { return c.stream.SetDeadline(t) }
func (c *streamConn) SetReadDeadline(t time.Time) error  { return c.stream.SetReadDeadline(t) }
func (c *streamConn) SetWriteDeadline(t time.Time) error { return c.stream.SetWriteDeadline(t) }

// tlsCommon clones the caller's config and adds what QUIC mandates. The
// caller's object is never modified: the TCP listener keeps using its own.
func tlsCommon(config *tls.Config) *tls.Config {
	cloned := config.Clone()
	cloned.MinVersion = tls.VersionTLS13
	if len(cloned.NextProtos) == 0 {
		cloned.NextProtos = []string{ALPN}
	}
	return cloned
}

func tlsClientConfig(config *tls.Config) *tls.Config { return tlsCommon(config) }
func tlsServerConfig(config *tls.Config) *tls.Config { return tlsCommon(config) }

// quicConfig sizes the protocol for one owner on a small device: short
// handshake budget, modest stream ceilings, keepalives to hold a USB or Wi-Fi
// link through silent periods, and an idle timeout that reaps a closed peer.
func quicConfig() *quicgo.Config {
	return &quicgo.Config{
		HandshakeIdleTimeout:  10 * time.Second,
		MaxIdleTimeout:        60 * time.Second,
		KeepAlivePeriod:       20 * time.Second,
		MaxIncomingStreams:    4,
		MaxIncomingUniStreams: 0,
	}
}

// Compile-time proof that the adapter is a net.Conn.
var _ net.Conn = (*streamConn)(nil)
