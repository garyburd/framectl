package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/garyburd/websocket"
)

// artClient serializes art-app operations over the wire client. Responses and
// unordered notifications share the channel, so replies match by echoed id.
// Concurrent requests are unsafe: the TV may drop one.
type artClient struct {
	cl   *client
	ctx  context.Context // the command's lifetime; bounds the wait to start a write
	stop func() bool     // tears down the ctx-cancellation binding (see openArt)
}

// send wraps inner as a JSON string in an ms.channel.emit envelope.
func (c *artClient) send(inner any) error {
	body, err := json.Marshal(inner)
	if err != nil {
		return err
	}
	msg, _ := json.Marshal(map[string]any{
		"method": "ms.channel.emit",
		"params": map[string]any{
			"event": "art_app_request",
			"to":    "host",
			"data":  string(body),
		},
	})
	return c.cl.write(c.ctx, msg)
}

// replyTimeout catches dropped requests and incompatible firmware responses.
const replyTimeout = 60 * time.Second

// waitForResponse returns the reply or ack matching id. It skips notifications
// and stale replies, and attributes matching error events through tvError.
func (c *artClient) waitForResponse(id string) (json.RawMessage, error) {
	if id == "" {
		return nil, errors.New("request has no id") // would match notification events
	}
	wctx, cancel := context.WithTimeout(c.ctx, replyTimeout)
	defer cancel()
	defer c.cl.bind(wctx)()
	for {
		raw, err := c.readEvent()
		if err != nil {
			if wctx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("no reply from the TV within %v", replyTimeout)
			}
			return nil, err
		}
		var inner map[string]json.RawMessage
		if raw == nil || json.Unmarshal(raw, &inner) != nil {
			continue // channel-level, or not a JSON object
		}
		if rawString(inner["event"]) == "error" {
			// Attribute by the id echoed in request_data; an unattributable
			// error still belongs to this serial client's in-flight request.
			if eid, _ := errorRequest(inner); eid == "" || eid == id {
				return nil, tvError(inner)
			}
			continue // stale error for an earlier, timed-out request
		}
		if echoedRequestID(inner) == id {
			return raw, nil
		}
	}
}

// echoedRequestID accepts a direct id or a JSON-encoded request containing it.
// Notifications return "".
func echoedRequestID(inner map[string]json.RawMessage) string {
	for _, k := range []string{"request_id", "id"} {
		var s string
		if json.Unmarshal(inner[k], &s) != nil {
			continue
		}
		var req struct {
			ID string `json:"id"`
		}
		if json.Unmarshal([]byte(s), &req) == nil && req.ID != "" {
			return req.ID
		}
		return s
	}
	return ""
}

// readEvent decodes d2d_service_message payloads and skips channel events.
func (c *artClient) readEvent() (json.RawMessage, error) {
	data, err := c.cl.read()
	if err != nil {
		return nil, err
	}
	var e envelope
	if json.Unmarshal(data, &e) != nil || e.Event != "d2d_service_message" {
		return nil, nil
	}
	var s string // the data field is a JSON-encoded string
	if json.Unmarshal(e.Data, &s) != nil {
		return nil, nil
	}
	return json.RawMessage(s), nil
}

// request sends req, waits for its echoed id, and optionally decodes the reply.
func (c *artClient) request(req map[string]any, result any) error {
	if err := c.send(req); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	id, _ := req["id"].(string)
	raw, err := c.waitForResponse(id)
	if err != nil || result == nil {
		return err
	}
	return json.Unmarshal(raw, result)
}

// negotiateD2D obtains a one-shot transfer endpoint from the TV.
func (c *artClient) negotiateD2D(req map[string]any) (connInfo, error) {
	var resp struct {
		ConnInfo connInfo `json:"conn_info"`
	}
	if err := c.request(req, &resp); err != nil {
		return connInfo{}, err
	}
	if resp.ConnInfo.IP == "" {
		return connInfo{}, errors.New("reply carried no conn_info")
	}
	return resp.ConnInfo, nil
}

// rawString accepts the TV's quoted or numeric encodings.
func rawString(r json.RawMessage) string {
	return strings.Trim(string(r), `"`)
}

// errorRequest decodes an error event's echoed request_data, when present.
func errorRequest(inner map[string]json.RawMessage) (id, request string) {
	var rd string
	if json.Unmarshal(inner["request_data"], &rd) != nil {
		return "", ""
	}
	var req struct {
		ID      string `json:"id"`
		Request string `json:"request"`
	}
	_ = json.Unmarshal([]byte(rd), &req)
	return req.ID, req.Request
}

// tvError names the failed request when request_data is available.
func tvError(inner map[string]json.RawMessage) error {
	if _, request := errorRequest(inner); request != "" {
		return fmt.Errorf("TV returned error for %s (code %s)", request, rawString(inner["error_code"]))
	}
	return fmt.Errorf("TV returned error (code %s)", rawString(inner["error_code"]))
}

// shutdown closes gracefully and drains straggling errors.
func (c *artClient) shutdown() error {
	defer c.stop()
	defer c.cl.close() // shutdown doesn't release the transport; the drain's caller must
	_ = c.cl.shutdown()
	for {
		raw, err := c.readEvent()
		if err != nil {
			// Expected close paths end the drain; transport failures do not.
			var ce *websocket.CloseError
			if errors.As(err, &ce) || errors.Is(err, websocket.ErrClosed) || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		var inner map[string]json.RawMessage
		if raw == nil || json.Unmarshal(raw, &inner) != nil {
			continue
		}
		if rawString(inner["event"]) == "error" {
			return tvError(inner)
		}
	}
}

// close tears down immediately without draining.
func (c *artClient) close() error {
	defer c.stop()
	return c.cl.close()
}

// Finish drains after successful work and closes immediately after failure.
func (c *artClient) Finish(workErr error) error {
	if workErr != nil {
		c.close()
		return workErr
	}
	return c.shutdown()
}

// TV operations keep wire details out of command code.

// contentItem is one entry; content ids may repeat across categories.
type contentItem struct {
	ContentID   string    `json:"content_id"`
	CategoryID  string    `json:"category_id"`
	ContentType string    `json:"content_type"`
	ImageDate   imageDate `json:"image_date"`
}

// imageDateLayout is the image_date wire format.
const imageDateLayout = "2006:01:02 15:04:05"

// imageDate decodes upload time; invalid values become zero.
type imageDate struct {
	time.Time
}

func (d *imageDate) UnmarshalJSON(b []byte) error {
	var s string
	if json.Unmarshal(b, &s) == nil {
		d.Time, _ = time.ParseInLocation(imageDateLayout, s, time.Local)
	}
	return nil
}

// String renders the wire format, or empty for zero.
func (d imageDate) String() string {
	if d.IsZero() {
		return ""
	}
	return d.Format(imageDateLayout)
}

// ContentList fetches the JSON-string-encoded content list.
func (c *artClient) ContentList() ([]contentItem, error) {
	var resp struct {
		ContentList string `json:"content_list"` // JSON-encoded, decoded below
	}
	if err := c.request(map[string]any{"request": "get_content_list", "id": newUUID()}, &resp); err != nil {
		return nil, err
	}
	var items []contentItem
	if err := json.Unmarshal([]byte(resp.ContentList), &items); err != nil {
		return nil, fmt.Errorf("parse content_list: %w", err)
	}
	return items, nil
}

// photoEntries returns distinct My Photos items, newest first.
func photoEntries(items []contentItem) []contentItem {
	seen := map[string]bool{}
	var out []contentItem
	for _, it := range items {
		if it.CategoryID != uploadsCategory || it.ContentID == "" || seen[it.ContentID] {
			continue
		}
		seen[it.ContentID] = true
		out = append(out, it)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].ImageDate.Equal(out[j].ImageDate.Time) {
			return out[i].ImageDate.After(out[j].ImageDate.Time)
		}
		return out[i].ContentID < out[j].ContentID // equal (often zero) dates stay deterministic
	})
	return out
}

// CurrentImage returns the selected content id, even outside Art Mode.
func (c *artClient) CurrentImage() (string, error) {
	var resp struct {
		ContentID string `json:"content_id"`
	}
	err := c.request(map[string]any{"request": "get_current_artwork", "id": newUUID()}, &resp)
	return resp.ContentID, err
}

// ArtmodeStatus queries Art Mode and returns "on" or "off".
func (c *artClient) ArtmodeStatus() (string, error) {
	var resp struct {
		Value string `json:"value"`
	}
	err := c.request(map[string]any{"request": "get_artmode_status", "id": newUUID()}, &resp)
	return resp.Value, err
}

// SlideshowStatus returns "off" or the interval in minutes.
func (c *artClient) SlideshowStatus() (string, error) {
	var resp struct {
		Value string `json:"value"`
	}
	err := c.request(map[string]any{"request": "get_slideshow_status", "id": newUUID()}, &resp)
	return resp.Value, err
}

// SetArtmode reads first because the TV never acknowledges a no-op change.
func (c *artClient) SetArtmode(on bool) error {
	value := "off"
	if on {
		value = "on"
	}
	current, err := c.ArtmodeStatus()
	if err != nil {
		return err
	}
	if current == value {
		return nil
	}
	return c.request(map[string]any{
		"request": "set_artmode_status",
		"id":      newUUID(),
		"value":   value,
	}, nil)
}

// SelectImage selects contentID and waits for its ack. show displays it now;
// false only sets the selection.
func (c *artClient) SelectImage(contentID, category string, show bool) error {
	req := map[string]any{
		"request":    "select_image",
		"id":         newUUID(),
		"content_id": contentID,
		"show":       show,
	}
	if category != "" {
		req["category_id"] = category
	}
	return c.request(req, nil)
}

// SendSlideshow updates the slideshow and waits for its ack.
func (c *artClient) SendSlideshow(ss slideshow) error {
	return c.request(map[string]any{
		"request":     "set_slideshow_status",
		"id":          newUUID(),
		"value":       ss.Value,
		"category_id": ss.CategoryID,
		"type":        ss.Type,
	}, nil)
}

// ChangeFavorite sets contentID's favorite status ("on" or "off") and waits
// for the ack. Verified July 2026: store art works (ack plus a
// favorite_changed notification; the item gains category MY-C0004), but My
// Photos uploads are rejected with error -7.
func (c *artClient) ChangeFavorite(contentID, status string) error {
	return c.request(map[string]any{
		"request":    "change_favorite",
		"id":         newUUID(),
		"content_id": contentID,
		"status":     status,
	}, nil)
}

// DeletePhotos removes ids and waits for the ack.
func (c *artClient) DeletePhotos(ids []string) error {
	list := make([]map[string]string, len(ids))
	for i, id := range ids {
		list[i] = map[string]string{"content_id": id}
	}
	return c.request(map[string]any{
		"request":         "delete_image_list",
		"id":              newUUID(),
		"content_id_list": list,
	}, nil)
}

// UploadPhoto prepares an image, sends it through a negotiated D2D socket, and
// returns the content id from the ack. image_date records upload time, matching
// the Samsung app (verified June 2026).
func (c *artClient) UploadPhoto(ctx context.Context, path string) (string, error) {
	img, err := prepareImage(path)
	if err != nil {
		return "", err
	}
	const ft = "jpg" // prepareImage always emits JPEG
	id := newUUID()

	req := map[string]any{
		"request":           "send_image",
		"id":                id,
		"request_id":        id,
		"file_type":         ft,
		"file_size":         len(img),
		"image_date":        time.Now().Format(imageDateLayout),
		"matte_id":          "none",
		"portrait_matte_id": "none",
		"conn_info": map[string]any{
			"d2d_mode":      "socket",
			"connection_id": newConnID(),
			"id":            id,
		},
	}
	ci, err := c.negotiateD2D(req)
	if err != nil {
		return "", err
	}

	header, _ := json.Marshal(map[string]any{
		"num":        0,
		"total":      1,
		"fileLength": len(img),
		"fileName":   "image",
		"fileType":   ft,
		"secKey":     ci.Key,
		"version":    "0.0.1",
	})
	if err := sendOverD2D(ctx, ci, header, img); err != nil {
		return "", err
	}

	// image_added has no id echo; the ack carries the assigned content id.
	raw, err := c.waitForResponse(id)
	if err != nil {
		return "", err
	}
	var ack struct {
		ContentID string `json:"content_id"`
	}
	_ = json.Unmarshal(raw, &ack)
	if ack.ContentID == "" {
		return "", fmt.Errorf("send_image ack carried no content_id")
	}
	return ack.ContentID, nil
}

// openArt resolves and connects within connectTimeout, broadcasting Wake-on-LAN
// while retrying when enabled. ctx governs the later operation.
func openArt(ctx context.Context, conn connection, connectTimeout time.Duration, wake, debug bool) (*artClient, error) {
	cctx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	// Start waking before name resolution or dialing.
	if wake && conn.MAC != "" {
		endWoL := make(chan struct{})
		defer close(endWoL)
		go wakeLoop(conn.MAC, endWoL)
	}

	addr, err := conn.address(cctx)
	if err != nil {
		return nil, err
	}

	// A resolved name already answered; only waking a literal host needs retries.
	retry := conn.Host != "" && wake && conn.MAC != ""
	for waited := false; ; waited = true {
		cl, err := connectArt(cctx, addr, conn.Token, debug)
		if err == nil {
			if waited {
				log.Printf("%s is up", addr)
			}
			return newArtClient(ctx, cl), nil
		}
		if !retry {
			return nil, err
		}
		if !waited {
			log.Printf("%s didn't answer; sending Wake-on-LAN and waiting for it to come up…", addr)
		}
		select {
		case <-cctx.Done():
			return nil, connectErr(cctx, addr, err)
		case <-time.After(connectRetryInterval):
		}
	}
}

// newArtClient binds a connected channel to the operation context.
func newArtClient(ctx context.Context, cl *client) *artClient {
	stop := cl.bind(ctx)
	return &artClient{cl: cl, ctx: ctx, stop: stop}
}

// connectArt bounds one dial and art handshake. Pairing uses a separate,
// unbounded approval wait.
func connectArt(ctx context.Context, addr, token string, debug bool) (*client, error) {
	actx, cancel := context.WithTimeout(ctx, connectAttemptTimeout)
	defer cancel()
	cl, err := dialChannel(actx, addr, artChannel, token, debug)
	if err != nil {
		return nil, connectErr(actx, addr, err)
	}
	unbind := cl.bind(actx) // bound the handshake
	err = cl.waitReady()
	unbind()
	if err != nil {
		cl.close()
		return nil, connectErr(actx, addr, err)
	}
	return cl, nil
}

// connectErr translates deadline-triggered socket failures into a clear timeout.
func connectErr(ctx context.Context, addr string, err error) error {
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("timed out connecting to %s (is the TV awake and reachable? ensure Wake-on-LAN / 'Power On with Mobile' is enabled on it)", addr)
	}
	return err
}
