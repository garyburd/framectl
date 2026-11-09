package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"iter"
	"log"
	"net"
	"net/http"
	"net/textproto"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type discoverCmd struct {
	Timeout time.Duration `flag:"timeout" usage:"how long to scan for SSDP replies; 0 = until interrupted"`
}

var discoverCommand = &command{
	name:    "discover",
	summary: "stream TVs found on the LAN (SSDP/UPnP) until interrupted",
	cmd:     new(discoverCmd),
}

func (c *discoverCmd) run(ctx context.Context, _ *globals, _ []string) error {
	dctx := ctx
	if c.Timeout > 0 {
		var cancel context.CancelFunc
		dctx, cancel = context.WithTimeout(ctx, c.Timeout)
		defer cancel()
	}
	return discover(dctx)
}

// SSDP discovery multicasts M-SEARCH, then reads names and manufacturers from
// the XML description linked by each reply's LOCATION header.

// tvInfo is a discovered Samsung TV.
type tvInfo struct {
	ID   string // stable id: MAC without colons, else IP
	Name string
	IP   string
	MAC  string
}

// tvID uses the MAC without colons, falling back to IP when unavailable.
func tvID(mac, ip string) string {
	if mac != "" {
		return strings.ReplaceAll(mac, ":", "")
	}
	return ip
}

const ssdpAddr = "239.255.255.250:1900"

// The Frame may ignore narrow targets, so probe ssdp:all and filter later.
const mSearch = "M-SEARCH * HTTP/1.1\r\n" +
	"HOST: 239.255.255.250:1900\r\n" +
	"MAN: \"ssdp:discover\"\r\n" +
	"MX: 2\r\n" +
	"ST: ssdp:all\r\n" +
	"\r\n"

// ssdpWindow is the listen duration between probes.
const ssdpWindow = 3 * time.Second

// ssdpScan repeatedly probes and yields each distinct Samsung TV once. It stops
// when ctx is canceled or the consumer breaks. A socket failure ends the scan
// with one final error.
func ssdpScan(ctx context.Context) iter.Seq2[tvInfo, error] {
	return func(yield func(tvInfo, error) bool) {
		group, err := net.ResolveUDPAddr("udp4", ssdpAddr)
		if err != nil {
			yield(tvInfo{}, fmt.Errorf("ssdp: %w", err))
			return
		}
		conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
		if err != nil {
			yield(tvInfo{}, fmt.Errorf("ssdp: %w", err))
			return
		}
		defer conn.Close()
		// Unblock ReadFromUDP on cancel.
		defer context.AfterFunc(ctx, func() { conn.Close() })()

		hc := &http.Client{Timeout: 3 * time.Second}
		described := map[string]tvInfo{} // LOCATION -> info (cache the HTTP describe)
		yielded := map[string]bool{}     // tv id -> already yielded
		buf := make([]byte, 4096)

		for ctx.Err() == nil {
			for i := 0; i < 3; i++ {
				conn.WriteToUDP([]byte(mSearch), group) // probes are best-effort
			}
			conn.SetReadDeadline(time.Now().Add(ssdpWindow))
			for {
				n, src, err := conn.ReadFromUDP(buf)
				if err != nil {
					var ne net.Error
					if ctx.Err() == nil && !(errors.As(err, &ne) && ne.Timeout()) {
						yield(tvInfo{}, fmt.Errorf("ssdp: %w", err))
						return
					}
					break // window elapsed, or socket closed by cancel
				}
				loc := ssdpHeader(buf[:n], "LOCATION")
				if loc == "" {
					continue
				}
				info, ok := described[loc]
				if !ok {
					info = describeTV(ctx, hc, loc, src.IP.String())
					described[loc] = info
					// The HTTP describe consumed read time; restart the window
					// so other replies aren't squeezed out.
					conn.SetReadDeadline(time.Now().Add(ssdpWindow))
				}
				if info.IP == "" || yielded[info.ID] {
					continue // not a Samsung TV, or already delivered
				}
				yielded[info.ID] = true
				if !yield(info, nil) {
					return
				}
			}
		}
	}
}

// describeTV returns Samsung device details and an ARP-derived MAC.
func describeTV(ctx context.Context, hc *http.Client, location, ip string) tvInfo {
	name, mfr := describe(ctx, hc, location)
	if !strings.Contains(strings.ToLower(mfr), "samsung") {
		return tvInfo{}
	}
	mac := macForIP(ctx, ip)
	return tvInfo{ID: tvID(mac, ip), Name: name, IP: ip, MAC: mac}
}

// discover prints devices until its timeout or Ctrl-C.
func discover(ctx context.Context) error {
	found := false
	for tv, err := range ssdpScan(ctx) {
		if err != nil {
			return err
		}
		fmt.Printf("%s\t%s\t%s\n", tv.IP, tv.Name, tv.MAC)
		found = true
	}
	if !found && ctx.Err() == context.DeadlineExceeded {
		log.Println("no Samsung devices found (is the TV powered on and on this subnet?)")
	}
	return nil
}

var macRE = regexp.MustCompile(`[0-9a-fA-F]{1,2}(?::[0-9a-fA-F]{1,2}){5}`)

// macForIP reads and normalizes a same-subnet MAC from the system ARP cache.
// It returns "" when no entry exists; Go exposes no ARP-table API.
func macForIP(ctx context.Context, ip string) string {
	out, err := exec.CommandContext(ctx, "arp", "-n", ip).Output()
	if err != nil {
		return "" // no entry: arp exits non-zero
	}
	return normalizeMAC(macRE.FindString(string(out)))
}

func normalizeMAC(s string) string {
	parts := strings.Split(s, ":")
	if len(parts) != 6 {
		return ""
	}
	for i, p := range parts {
		v, err := strconv.ParseUint(p, 16, 8)
		if err != nil {
			return ""
		}
		parts[i] = fmt.Sprintf("%02x", v)
	}
	return strings.Join(parts, ":")
}

// ambiguityGrace is how long a substring match waits for rival matches. It
// covers the MX reply delay requested in mSearch.
const ambiguityGrace = 2 * time.Second

// resolveName finds a TV by case-insensitive friendly name. An exact match
// returns at once. A substring match waits ambiguityGrace and fails if a
// second TV also matches. ctx bounds the scan; a deadline becomes "not
// found," while cancellation is preserved.
func resolveName(ctx context.Context, name string) (tvInfo, error) {
	sctx, cancel := context.WithCancel(ctx)
	defer cancel()
	low := strings.ToLower(name)
	var matches []tvInfo
	for tv, err := range ssdpScan(sctx) {
		if err != nil {
			return tvInfo{}, err
		}
		n := strings.ToLower(tv.Name)
		if n == low {
			return tv, nil
		}
		if !strings.Contains(n, low) {
			continue
		}
		if len(matches) == 0 {
			defer time.AfterFunc(ambiguityGrace, cancel).Stop()
		}
		matches = append(matches, tv)
	}
	if ctx.Err() == context.Canceled {
		return tvInfo{}, ctx.Err()
	}
	switch len(matches) {
	case 0:
		return tvInfo{}, fmt.Errorf("no Samsung device matching %q (try: framectl discover)", name)
	case 1:
		return matches[0], nil
	}
	names := make([]string, len(matches))
	for i, tv := range matches {
		names[i] = fmt.Sprintf("%q (%s)", tv.Name, tv.IP)
	}
	return tvInfo{}, fmt.Errorf("%q matches several TVs: %s; give more of the name", name, strings.Join(names, ", "))
}

// ssdpHeader returns one header value from an SSDP (HTTP-shaped) reply.
func ssdpHeader(resp []byte, key string) string {
	r := textproto.NewReader(bufio.NewReader(bytes.NewReader(resp)))
	if _, err := r.ReadLine(); err != nil {
		return "" // no status line
	}
	// A missing trailing blank line yields io.EOF after successful parsing.
	h, err := r.ReadMIMEHeader()
	if err != nil && len(h) == 0 {
		return ""
	}
	return h.Get(key)
}

// describe returns friendly name and manufacturer, or empty strings on failure.
func describe(ctx context.Context, hc *http.Client, location string) (name, manufacturer string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return "", ""
	}
	resp, err := hc.Do(req)
	if err != nil {
		return "", ""
	}
	defer resp.Body.Close()

	var doc struct {
		Device struct {
			FriendlyName string `xml:"friendlyName"`
			Manufacturer string `xml:"manufacturer"`
			ModelName    string `xml:"modelName"`
		} `xml:"device"`
	}
	if err := xml.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return "", ""
	}
	name = doc.Device.FriendlyName
	if name == "" {
		name = doc.Device.ModelName // friendlyName is occasionally empty
	}
	return name, doc.Device.Manufacturer
}
