package main

import (
	"context"
	"flag"
	"testing"
	"time"
)

type testFlags struct {
	Host    string        `flag:"host" default:"127.0.0.1" usage:"server {hostname}"`
	Mode    string        `flag:"mode" usage:"no default tag: the field's value is the default"`
	Verbose bool          `flag:"verbose" usage:"chatty"`
	Show    bool          `flag:"show" default:"true" usage:"display it"`
	Wait    time.Duration `flag:"wait" default:"5s" usage:"how long"`
	skipped string        // untagged: not a flag
}

func (*testFlags) run(context.Context, *globals, []string) error { return nil }

func TestRegisterFlags(t *testing.T) {
	c := &testFlags{Mode: "auto", skipped: "x"}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	registerFlags(fs, c)

	// Defaults land in the fields: from the default tag, or the field's value.
	if c.Host != "127.0.0.1" || c.Mode != "auto" || c.Verbose || !c.Show || c.Wait != 5*time.Second {
		t.Fatalf("defaults not applied: %+v", c)
	}
	if got := fs.Lookup("mode").DefValue; got != "auto" {
		t.Errorf("mode DefValue = %q, want auto", got)
	}

	// {word} renders as the backquoted placeholder.
	ph, usage := flag.UnquoteUsage(fs.Lookup("host"))
	if ph != "hostname" || usage != "server hostname" {
		t.Errorf("UnquoteUsage(host) = %q, %q", ph, usage)
	}

	// Parsing writes through to the fields.
	if err := fs.Parse([]string{"-host", "tv.local", "-verbose", "-show=false", "-wait", "1m"}); err != nil {
		t.Fatal(err)
	}
	if c.Host != "tv.local" || !c.Verbose || c.Show || c.Wait != time.Minute {
		t.Errorf("parsed values not applied: %+v", c)
	}

	if fs.Lookup("skipped") != nil {
		t.Error("untagged field was registered")
	}
}
