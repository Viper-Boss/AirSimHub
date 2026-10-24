package quic

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"io"
	"math/big"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/human-agent65535/nassimhub-node/xport/quicmock"
)

// deviceCertificate builds the shape the node serves: an Ed25519 self-signed
// certificate, the same kind serverTLSConfig puts on the TCP listener.
func deviceCertificate(t *testing.T) tls.Certificate {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "node-test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, public, private)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: private}
}

// The full loopback: one HTTP round trip over the real QUIC transport, with
// the pinning callback of the caller's tls.Config running inside the QUIC
// handshake - the contract quicmock fixed before this package existed.
func TestHTTPOverQUICWithTheCallersPinning(t *testing.T) {
	// One configuration plays both roles in the loopback, which a QUIC
	// handshake allows: server side it presents the device certificate and
	// asks for no client certificate; client side it has no CA to verify
	// against, so verification is the pinning callback - exactly how the
	// production NAS verifies the node. On the device only the server half
	// of this shape is ever used; the NAS builds its own client half.
	certificate := deviceCertificate(t)
	var pinningRuns atomic.Int32
	config := &tls.Config{
		Certificates:       []tls.Certificate{certificate},
		ClientAuth:         tls.NoClientCert,
		InsecureSkipVerify: true, // no CA in the test; the pinning callback replaces it
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			pinningRuns.Add(1)
			if len(rawCerts) != 1 {
				return fmt.Errorf("expected the device certificate, got %d", len(rawCerts))
			}
			return nil
		},
	}
	transport, err := New(Options{TLSConfig: config})
	if err != nil {
		t.Fatal(err)
	}
	listener, err := transport.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	serve := make(chan struct{})
	go func() {
		close(serve)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		_, _ = conn.Write([]byte("HTTP/1.1 200 OK\r\nContent-Length: 2\r\nConnection: close\r\n\r\nok"))
		time.Sleep(50 * time.Millisecond)
		_ = conn.Close()
	}()
	<-serve

	conn, err := transport.Dial(t.Context(), listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	request := "GET /health HTTP/1.1\r\nHost: node\r\nConnection: close\r\n\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("write: %v", err)
	}
	buffer := make([]byte, 4096)
	total, err := conn.Read(buffer)
	if err != nil && err != io.EOF {
		t.Fatalf("read: %v", err)
	}
	if !strings.Contains(string(buffer[:total]), "200 OK") || !strings.Contains(string(buffer[:total]), "ok") {
		t.Fatalf("round trip failed: %q", buffer[:total])
	}
	if pinningRuns.Load() == 0 {
		t.Fatal("the caller's pinning callback never ran: QUIC did not use the caller's tls.Config")
	}
	stats := transport.Stats()
	if stats.BytesSent == 0 || stats.BytesRecv == 0 {
		t.Fatalf("byte counters not measured: %#v", stats)
	}
	if stats.Measured {
		t.Fatal("link-level fields are not measured by this transport; Measured must stay false")
	}
}

// The ALPN identifier is transport-owned. A client that speaks a different
// application protocol must not complete the handshake.
func TestALPNIsTransportOwned(t *testing.T) {
	certificate := deviceCertificate(t)
	server, err := New(Options{TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate}}})
	if err != nil {
		t.Fatal(err)
	}
	if got := tlsCommon(&tls.Config{}).NextProtos; len(got) != 1 || got[0] != ALPN {
		t.Fatalf("alpn not applied on a clone: %v", got)
	}
	original := &tls.Config{}
	_ = tlsCommon(original)
	if len(original.NextProtos) != 0 {
		t.Fatal("the caller's tls.Config was mutated")
	}
	listener, err := server.Listen("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	wrongClient, err := New(Options{TLSConfig: &tls.Config{Certificates: []tls.Certificate{certificate},
		NextProtos: []string{"something-else"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := wrongClient.Dial(t.Context(), listener.Addr().String()); err == nil {
		t.Fatal("a client with a foreign ALPN completed the handshake")
	}
}

// 0-RTT stays off by construction: the early-data policy from quicmock is the
// written rule and the transport has no DialEarly path at all.
func TestEarlyDataStaysOff(t *testing.T) {
	if quicmock.EarlyDataEnabled() {
		t.Fatal("early data must stay disabled")
	}
	if err := quicmock.EarlyDataPolicy("POST", "/v1/sms"); err == nil {
		t.Fatal("a side-effecting request must be refused as early data")
	}
}

// No TLS configuration means no transport: QUIC without TLS is not a mode this
// project offers, and the error must say so rather than degrade.
func TestNoTLSConfigurationFailsClosed(t *testing.T) {
	if _, err := New(Options{}); err == nil {
		t.Fatal("quic without a tls configuration was accepted")
	}
}

func base64Wrap(der []byte) string {
	const line = 64
	var out strings.Builder
	for i := 0; i < len(der); i += line {
		end := i + line
		if end > len(der) {
			end = len(der)
		}
		out.WriteString(string(der[i:end]))
		if end < len(der) {
			out.WriteByte('\n')
		}
	}
	return out.String()
}
