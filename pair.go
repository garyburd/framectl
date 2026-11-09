package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
)

type pairCmd struct {
	Host string `flag:"host" usage:"Frame TV {hostname} or IP"`
	Name string `flag:"name" usage:"SSDP friendly {name} of the TV (case-insensitive substring; runs discovery)"`
}

var pairCommand = &command{
	name: "pair", needTVFile: true,
	summary: "authorize and record the connection (give exactly one of -host / -name)",
	cmd:     new(pairCmd),
}

func (c *pairCmd) run(ctx context.Context, g *globals, _ []string) error {
	// Re-pairing preserves managed photos. Load before the TV-side approval so
	// an unreadable file doesn't discard a granted token.
	d, err := loadTVData(g.tvFile)
	if err != nil {
		if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		d = tvData{} // no file yet: start fresh
	}

	// Preserve a host verbatim. Record a name in full, so later rediscovery
	// matches it exactly and stays unambiguous. One -timeout budget covers
	// both resolution and the dial.
	cctx, cancel := context.WithTimeout(ctx, g.timeout)
	defer cancel()
	addr, fullName, err := targetHost(cctx, c.Host, c.Name)
	if err != nil {
		return err
	}
	return pair(ctx, cctx, addr, d, connection{Host: c.Host, Name: fullName}, g)
}

// fetchToken uses the remote channel because the art channel issues no token.
// connectCtx bounds the dial; the on-screen approval wait lasts until Ctrl-C
// or a TV timeout. If a trusted TV returns no token, unpair it and retry.
func fetchToken(ctx, connectCtx context.Context, addr string, debug bool) (string, error) {
	cl, err := dialChannel(connectCtx, addr, remoteChannel, "", debug)
	if err != nil {
		return "", err
	}
	defer cl.close()
	stop := cl.bind(ctx)
	defer stop()
	token, err := cl.waitConnect()
	if err != nil {
		return "", err
	}
	if token == "" {
		return "", fmt.Errorf("connected to %s but it returned no token; unpair this device on the TV "+
			"(General > External Device Manager > Device Connection Manager), then try again", addr)
	}
	return token, nil
}

// pair saves d with the token and host or name in g.tvFile.
func pair(ctx, connectCtx context.Context, addr string, d tvData, conn connection, g *globals) error {
	token, err := fetchToken(ctx, connectCtx, addr, g.debug)
	if err != nil {
		return err
	}
	conn.Token = token

	// Record an ARP-derived MAC for Wake-on-LAN; failure only disables waking.
	ip := addr
	if net.ParseIP(addr) == nil {
		if r, err := net.ResolveIPAddr("ip4", addr); err == nil {
			ip = r.String()
		}
	}
	if conn.MAC = macForIP(ctx, ip); conn.MAC == "" {
		log.Printf("pair: could not determine MAC for %s; commands won't be able to wake the TV with Wake-on-LAN", addr)
	}

	d.Connect = conn
	if err := saveTVData(g.tvFile, d); err != nil {
		return err
	}
	log.Printf("paired with %s; wrote data file %s", addr, g.tvFile)
	return nil
}
