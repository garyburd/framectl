package main

import (
	"log"
	"net"
	"syscall"
	"time"
)

// wakeOnLAN best-effort broadcasts a magic packet on standard WoL ports.
func wakeOnLAN(mac string) error {
	hw, err := net.ParseMAC(mac)
	if err != nil {
		return err
	}
	pkt := make([]byte, 0, 6+16*len(hw))
	pkt = append(pkt, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff)
	for i := 0; i < 16; i++ {
		pkt = append(pkt, hw...)
	}
	// SO_BROADCAST is required to send to a broadcast address.
	dialer := net.Dialer{Control: func(_, _ string, c syscall.RawConn) error {
		var serr error
		c.Control(func(fd uintptr) {
			serr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_BROADCAST, 1)
		})
		return serr
	}}
	var firstErr error
	for _, addr := range []string{"255.255.255.255:9", "255.255.255.255:7"} {
		conn, err := dialer.Dial("udp4", addr)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		_, err = conn.Write(pkt)
		conn.Close()
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// wakeLoop broadcasts immediately and every wakeInterval until done closes.
func wakeLoop(mac string, done <-chan struct{}) {
	send := func() {
		if err := wakeOnLAN(mac); err != nil {
			log.Printf("sending Wake-on-LAN to %s failed: %v", mac, err)
		}
	}
	send()
	t := time.NewTicker(wakeInterval)
	defer t.Stop()
	for {
		select {
		case <-done:
			return
		case <-t.C:
			send()
		}
	}
}
