//go:build linux

// SPDX-License-Identifier: AGPL-3.0-only
// Bounded runtime voice preference over the existing QMI proxy; no raw device ownership.
package main

import (
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

type transport struct {
	c  net.Conn
	tx uint16
}

func (t *transport) req(service, cid byte, id uint16, body []byte) (map[byte][]byte, error) {
	t.tx++
	tx := t.tx
	hlen := 7
	if service == 0 {
		hlen = 6
		tx = uint16(byte(tx))
	}
	p := make([]byte, 6+hlen+len(body))
	p[0] = 1
	binary.LittleEndian.PutUint16(p[1:3], uint16(len(p)-1))
	p[4] = service
	p[5] = cid
	q := p[6:]
	if service == 0 {
		q[1] = byte(tx)
	} else {
		binary.LittleEndian.PutUint16(q[1:3], tx)
	}
	binary.LittleEndian.PutUint16(q[hlen-4:hlen-2], id)
	binary.LittleEndian.PutUint16(q[hlen-2:hlen], uint16(len(body)))
	copy(q[hlen:], body)
	t.c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, e := t.c.Write(p); e != nil {
		return nil, e
	}
	for i := 0; i < 16; i++ {
		h := make([]byte, 3)
		if _, e := io.ReadFull(t.c, h); e != nil {
			return nil, e
		}
		n := int(binary.LittleEndian.Uint16(h[1:3]))
		if h[0] != 1 || n < 11 || n > 4096 {
			return nil, fmt.Errorf("bad QMUX frame")
		}
		r := make([]byte, n+1)
		copy(r, h)
		if _, e := io.ReadFull(t.c, r[3:]); e != nil {
			return nil, e
		}
		if r[4] != service || r[5] != cid {
			continue
		}
		z := r[6:]
		if len(z) < hlen {
			return nil, fmt.Errorf("short QMI frame")
		}
		rt := uint16(z[1])
		flag := byte(1)
		if service != 0 {
			rt = binary.LittleEndian.Uint16(z[1:3])
			flag = 2
		}
		if z[0] != flag || rt != tx || binary.LittleEndian.Uint16(z[hlen-4:hlen-2]) != id {
			continue
		}
		if int(binary.LittleEndian.Uint16(z[hlen-2:hlen])) != len(z)-hlen {
			return nil, fmt.Errorf("invalid QMI length")
		}
		return fields(z[hlen:])
	}
	return nil, fmt.Errorf("no matching response")
}
func fields(b []byte) (map[byte][]byte, error) {
	out := map[byte][]byte{}
	for len(b) > 0 {
		if len(b) < 3 {
			return nil, fmt.Errorf("short TLV")
		}
		n := int(binary.LittleEndian.Uint16(b[1:3]))
		if n > len(b)-3 {
			return nil, fmt.Errorf("invalid TLV size")
		}
		if _, ok := out[b[0]]; ok {
			return nil, fmt.Errorf("duplicate TLV")
		}
		out[b[0]] = b[3 : 3+n]
		b = b[3+n:]
	}
	result := out[2]
	if len(result) != 4 {
		return nil, fmt.Errorf("missing result")
	}
	if binary.LittleEndian.Uint16(result) != 0 || binary.LittleEndian.Uint16(result[2:]) != 0 {
		return nil, fmt.Errorf("QMI error %d", binary.LittleEndian.Uint16(result[2:]))
	}
	return out, nil
}

func run() error {
	if len(os.Args) != 2 {
		return fmt.Errorf("use get|ps-preferred|cs-preferred")
	}
	op := os.Args[1]
	if op != "get" && op != "ps-preferred" && op != "cs-preferred" {
		return fmt.Errorf("unsupported operation")
	}
	c, e := net.DialTimeout("unix", "\x00qmi-proxy", 5*time.Second)
	if e != nil {
		return e
	}
	defer c.Close()
	t := &transport{c: c}
	path := []byte("/dev/wwan0qmi0")
	body := append([]byte{1, byte(len(path)), 0}, path...)
	if _, e = t.req(0, 0, 0xff00, body); e != nil {
		return e
	}
	f, e := t.req(0, 0, 0x22, []byte{1, 1, 0, 3})
	if e != nil {
		return e
	}
	alloc := f[1]
	if len(alloc) != 2 || alloc[0] != 3 || alloc[1] == 0 {
		return fmt.Errorf("invalid client allocation")
	}
	cid := alloc[1]
	defer t.req(0, 0, 0x23, []byte{1, 2, 0, 3, cid})
	f, e = t.req(3, cid, 0x34, nil)
	if e != nil {
		return e
	}
	v := f[0x20]
	if len(v) != 4 {
		return fmt.Errorf("voice domain field unsupported")
	}
	current := binary.LittleEndian.Uint32(v)
	fmt.Printf("voice-domain=%d\n", current)
	if op == "get" {
		return nil
	}
	desired, expected := uint32(3), uint32(2)
	if op == "cs-preferred" {
		desired, expected = 2, 3
	}
	if current == desired {
		return nil
	}
	if current != expected {
		return fmt.Errorf("unexpected baseline; no change")
	}
	_, e = t.req(3, cid, 0x33, []byte{0x17, 1, 0, 0, 0x23, 4, 0, byte(desired), 0, 0, 0})
	if e != nil {
		return e
	}
	fmt.Printf("runtime voice domain requested=%d\n", desired)
	return nil
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
