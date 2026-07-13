package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net"
	"strconv"
	"time"
)

// connInfo is a negotiated one-shot endpoint. Port accepts numeric strings.
type connInfo struct {
	IP      string  `json:"ip"`
	Port    flexInt `json:"port"`
	Key     string  `json:"key"`
	Secured bool    `json:"secured"`
}

// UnmarshalJSON accepts conn_info as an object or JSON-encoded object string.
func (ci *connInfo) UnmarshalJSON(raw []byte) error {
	if s := ""; json.Unmarshal(raw, &s) == nil {
		raw = []byte(s)
	}
	type plain connInfo // the method-less shape; unmarshalling connInfo would recurse
	var p plain
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if p.IP == "" || p.Port == 0 {
		return fmt.Errorf("incomplete conn_info: %s", raw)
	}
	*ci = connInfo(p)
	return nil
}

// dialD2D opens the negotiated TCP or TLS socket with a 15-second cap.
func dialD2D(ctx context.Context, ci connInfo) (net.Conn, error) {
	addr := net.JoinHostPort(ci.IP, strconv.Itoa(int(ci.Port)))
	dialer := net.Dialer{Timeout: 15 * time.Second}
	if ci.Secured {
		return (&tls.Dialer{NetDialer: &dialer, Config: &tls.Config{InsecureSkipVerify: true}}).DialContext(ctx, "tcp", addr)
	}
	return dialer.DialContext(ctx, "tcp", addr)
}

// openD2D binds the socket to ctx and applies a 60-second transfer deadline.
// Callers defer both Close and the returned release function.
func openD2D(ctx context.Context, ci connInfo) (net.Conn, func(), error) {
	conn, err := dialD2D(ctx, ci)
	if err != nil {
		return nil, nil, fmt.Errorf("d2d dial: %w", err)
	}
	unbind := context.AfterFunc(ctx, func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(60 * time.Second))
	return conn, func() { unbind() }, nil
}

// sendOverD2D writes a big-endian header length, JSON header, then file bytes.
func sendOverD2D(ctx context.Context, ci connInfo, header, img []byte) error {
	conn, release, err := openD2D(ctx, ci)
	if err != nil {
		return err
	}
	defer release()
	defer conn.Close()

	var prefix [4]byte
	binary.BigEndian.PutUint32(prefix[:], uint32(len(header)))
	for _, chunk := range [][]byte{prefix[:], header, img} {
		if _, err := conn.Write(chunk); err != nil {
			return err
		}
	}
	return nil
}

// flexInt accepts numbers or quoted numbers used in D2D fields.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	s := string(bytes.Trim(b, `"`))
	if s == "" {
		*f = 0
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return err
	}
	*f = flexInt(n)
	return nil
}

// newConnID generates a D2D negotiation connection_id.
func newConnID() string {
	var b [4]byte
	rand.Read(b[:])
	return strconv.FormatUint(uint64(binary.BigEndian.Uint32(b[:])), 10)
}
