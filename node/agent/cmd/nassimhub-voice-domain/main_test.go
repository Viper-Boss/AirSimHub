//go:build linux

package main

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
)

func TestResultParserRejectsMalformedData(t *testing.T) {
	for _, b := range [][]byte{nil, {2, 4, 0, 0}, {2, 4, 0, 0, 0, 1, 0}, {2, 4, 0, 0, 0, 0, 0, 2, 4, 0, 0, 0, 0, 0}, {2, 4, 0, 0, 0, 0, 0, 32, 9, 0, 2}} {
		if _, e := fields(b); e == nil {
			t.Fatalf("accepted malformed response: %x", b)
		}
	}
}
func TestProxyRequestIgnoresWrongTransactionAndAcceptsMatchedReply(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	done := make(chan error, 1)
	go func() {
		h := make([]byte, 3)
		_, e := io.ReadFull(b, h)
		if e != nil {
			done <- e
			return
		}
		n := int(binary.LittleEndian.Uint16(h[1:3]))
		r := make([]byte, n+1)
		copy(r, h)
		_, e = io.ReadFull(b, r[3:])
		if e != nil {
			done <- e
			return
		}
		for _, tx := range []uint16{17, binary.LittleEndian.Uint16(r[7:9])} {
			body := []byte{2, 4, 0, 0, 0, 0, 0, 0x20, 4, 0, 2, 0, 0, 0}
			out := make([]byte, 13+len(body))
			out[0] = 1
			binary.LittleEndian.PutUint16(out[1:3], uint16(len(out)-1))
			out[3] = 0x80
			out[4] = 3
			out[5] = 4
			out[6] = 2
			binary.LittleEndian.PutUint16(out[7:9], tx)
			binary.LittleEndian.PutUint16(out[9:11], 0x34)
			binary.LittleEndian.PutUint16(out[11:13], uint16(len(body)))
			copy(out[13:], body)
			if _, e = b.Write(out); e != nil {
				done <- e
				return
			}
		}
		done <- nil
	}()
	tr := &transport{c: a}
	f, e := tr.req(3, 4, 0x34, nil)
	if e != nil {
		t.Fatal(e)
	}
	if binary.LittleEndian.Uint32(f[0x20]) != 2 {
		t.Fatal("incorrect domain")
	}
	if e = <-done; e != nil {
		t.Fatal(e)
	}
}
