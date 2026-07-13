package main

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/garyburd/websocket"
)

// Samsung TV WebSocket transport, shared by art operations and pairing.

// The remote channel issues tokens; the art channel handles photo requests.
const (
	artChannel    = "com.samsung.art-app"
	remoteChannel = "samsung.remote.control"
)

// client owns one channel and raw I/O; artClient owns art-app semantics.
type client struct {
	conn  *websocket.Conn
	debug bool
}

// dialChannel opens a channel using the TV's self-signed TLS certificate. The
// handshake remains separate because pairing may wait indefinitely for approval.
// Read keepalives detect dead peers; D2D transfers use their own deadline.
func dialChannel(ctx context.Context, host, channel, token string, debug bool) (*client, error) {
	u := url.URL{
		Scheme: "wss",
		Host:   host + ":8002",
		Path:   "/api/v2/channels/" + channel,
	}
	q := u.Query()
	q.Set("name", base64.StdEncoding.EncodeToString([]byte("frame")))
	if token != "" {
		q.Set("token", token)
	}
	u.RawQuery = q.Encode()

	opts := &websocket.DialOptions{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
		ShutdownTimeout: shutdownTimeout,
	}
	c, _, err := websocket.Dial(ctx, u.String(), opts, nil)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	return &client{conn: c, debug: debug}, nil
}

// tvReadOptions pings after 10 seconds and allows 10 seconds for a pong.
var tvReadOptions = &websocket.ReadOptions{
	KeepaliveInterval: 10 * time.Second,
	KeepaliveTimeout:  10 * time.Second,
}

// bind closes the connection when ctx ends; the returned function removes it.
func (c *client) bind(ctx context.Context) (stop func() bool) {
	return context.AfterFunc(ctx, func() { c.conn.Close() })
}

// read receives one message, with keepalive and context cancellation supplied
// by the connection and bind.
func (c *client) read() ([]byte, error) {
	data, _, err := c.conn.ReadMessage(tvReadOptions)
	if c.debug && err == nil {
		dumpMessage("<-", data)
	}
	return data, err
}

// write sends one text message; bind unblocks a wedged write.
func (c *client) write(ctx context.Context, msg []byte) error {
	if c.debug {
		dumpMessage("->", msg)
	}
	return c.conn.WriteMessage(ctx, websocket.MessageText, nil, msg)
}

// dumpMessage logs raw data plus decoded nested JSON layers when present.
func dumpMessage(dir string, msg []byte) {
	log.Printf("%s %s", dir, msg)
	data := doubleEncodedData(msg)
	if data == nil {
		return
	}
	log.Printf("%s data: %s", dir, data)
	if list := encodedStringField(data, "content_list"); list != nil {
		log.Printf("%s content_list: %s", dir, list)
	}
}

// encodedStringField decodes a named JSON-string field.
func encodedStringField(obj []byte, name string) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(obj, &m) != nil {
		return nil
	}
	var s string
	if json.Unmarshal(m[name], &s) != nil || !json.Valid([]byte(s)) {
		return nil
	}
	return []byte(s)
}

// doubleEncodedData finds and decodes an event or emit request's data field.
func doubleEncodedData(msg []byte) []byte {
	var m struct {
		Data   json.RawMessage `json:"data"`
		Params struct {
			Data json.RawMessage `json:"data"`
		} `json:"params"`
	}
	if json.Unmarshal(msg, &m) != nil {
		return nil
	}
	raw := m.Data
	if raw == nil {
		raw = m.Params.Data
	}
	var s string
	if json.Unmarshal(raw, &s) != nil || !json.Valid([]byte(s)) {
		return nil
	}
	return []byte(s)
}

// waitConnect waits for the remote channel's connect event and returns its token.
func (c *client) waitConnect() (string, error) {
	for {
		data, err := c.read()
		if err != nil {
			return "", fmt.Errorf("read: %w", err)
		}
		var e envelope
		if err := json.Unmarshal(data, &e); err != nil {
			continue // not JSON; ignore
		}
		switch e.Event {
		case "ms.channel.connect":
			var d struct {
				Token string `json:"token"`
			}
			_ = json.Unmarshal(e.Data, &d)
			return d.Token, nil
		case "ms.channel.unauthorized":
			return "", fmt.Errorf("unauthorized — accept the pairing prompt on the TV and retry")
		case "ms.channel.timeOut":
			return "", fmt.Errorf("timed out waiting for TV-side approval")
		}
	}
}

// waitReady waits for the art app, not merely channel connection. Requests sent
// between connect and ready may be dropped (observed on API 4.3.4.0).
func (c *client) waitReady() error {
	for {
		data, err := c.read()
		if err != nil {
			return fmt.Errorf("read: %w", err)
		}
		var e envelope
		if json.Unmarshal(data, &e) != nil {
			continue // not JSON; ignore
		}
		switch e.Event {
		case "ms.channel.ready":
			return nil
		case "ms.channel.unauthorized":
			return fmt.Errorf("unauthorized — accept the pairing prompt on the TV and retry")
		case "ms.channel.timeOut":
			return fmt.Errorf("timed out waiting for TV-side approval")
		}
	}
}

// shutdownTimeout bounds the close write and peer echo.
const shutdownTimeout = 10 * time.Second

// shutdown starts graceful close; callers drain and then close the transport.
func (c *client) shutdown() error {
	return c.conn.Shutdown(websocket.CloseNormalClosure, "")
}

// close tears the connection down abruptly, discarding anything unread.
func (c *client) close() error {
	return c.conn.Close()
}

// envelope is the outer JSON shape used by the Samsung TV API.
type envelope struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// newUUID generates the id used to match requests with replies and acks.
func newUUID() string {
	var b [16]byte
	rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
