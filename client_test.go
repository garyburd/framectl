package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func rawMap(t *testing.T, s string) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("bad test payload %q: %v", s, err)
	}
	return m
}

func TestEchoedRequestID(t *testing.T) {
	tests := []struct {
		name  string
		inner string
		want  string
	}{
		{
			name:  "request_id holds the whole request as a JSON string (4.3.4.0)",
			inner: `{"event":"select_image","request_id":"{\"id\":\"abc\",\"request\":\"select_image\",\"show\":false}"}`,
			want:  "abc",
		},
		{
			name:  "plain uuid echo",
			inner: `{"event":"get_artmode_status","request_id":"abc"}`,
			want:  "abc",
		},
		{
			name:  "id fallback",
			inner: `{"event":"get_artmode_status","id":"abc"}`,
			want:  "abc",
		},
		{
			name:  "notification echoes nothing",
			inner: `{"event":"image_selected","content_id":"MY_F0084"}`,
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := echoedRequestID(rawMap(t, tt.inner)); got != tt.want {
				t.Errorf("echoedRequestID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestContentItemUnmarshal(t *testing.T) {
	var items []contentItem
	blob := `[{"content_id":"MY_F0084","category_id":"MY-C0002","content_type":"mobile","image_date":"2026:06:02 09:47:24"},
	          {"content_id":"MY_F0090","category_id":"MY-C0002","image_date":"not a date"},
	          {"content_id":"MY_F0091","category_id":"MY-C0008"}]`
	if err := json.Unmarshal([]byte(blob), &items); err != nil {
		t.Fatal(err)
	}
	if got := items[0].ImageDate.String(); got != "2026:06:02 09:47:24" {
		t.Errorf("ImageDate round-trip = %q", got)
	}
	if !items[1].ImageDate.IsZero() || items[1].ImageDate.String() != "" {
		t.Errorf("malformed date: got %v, want zero rendering empty", items[1].ImageDate)
	}
	if !items[2].ImageDate.IsZero() {
		t.Errorf("absent date: got %v, want zero", items[2].ImageDate)
	}
	if got := photoEntries(items); len(got) != 2 || got[0].ContentID != "MY_F0084" {
		t.Errorf("photoEntries = %v, want MY_F0084 (dated) first, MY-C0008 filtered", got)
	}
}

// waitForResponse uses errorRequest's id to skip errors echoed for an earlier,
// timed-out request instead of misattributing them to the in-flight one.
func TestErrorRequest(t *testing.T) {
	attributed := rawMap(t, `{"event":"error","error_code":"-10","request_data":"{\"id\":\"x\",\"request\":\"select_image\"}"}`)
	if id, request := errorRequest(attributed); id != "x" || request != "select_image" {
		t.Errorf("errorRequest = %q, %q, want \"x\", \"select_image\"", id, request)
	}
	bare := rawMap(t, `{"event":"error","error_code":"-7"}`)
	if id, request := errorRequest(bare); id != "" || request != "" {
		t.Errorf("errorRequest(bare) = %q, %q, want empty (unattributable)", id, request)
	}
}

func TestTVError(t *testing.T) {
	attributed := rawMap(t, `{"event":"error","error_code":"-10","request_data":"{\"id\":\"x\",\"request\":\"select_image\"}"}`)
	if got := tvError(attributed).Error(); !strings.Contains(got, "select_image") || !strings.Contains(got, "-10") {
		t.Errorf("attributed error = %q, want request name and code", got)
	}
	bare := rawMap(t, `{"event":"error","error_code":"-7"}`)
	if got := tvError(bare).Error(); strings.Contains(got, "for ") || !strings.Contains(got, "-7") {
		t.Errorf("bare error = %q, want code only", got)
	}
}
