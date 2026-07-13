// Command framectl manages art on a Samsung "The Frame" TV through its
// undocumented art-app WebSocket channel. Run "framectl help" for details.
package main

import (
	"bufio"
	"context"
	_ "embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"
)

// guideText supplements the generated command and flag reference.
//
//go:embed guide.txt
var guideText string

// Regenerate COMMANDS.txt after changing commands, flags, or guide.txt.
//
//go:generate sh -c "go run . help > COMMANDS.txt"

func main() {
	// Keep interactive diagnostics short and identifiable in pipelines.
	log.SetFlags(0)
	log.SetPrefix("framectl: ")

	// Only Ctrl-C, not the connection timeout, cancels an operation.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Global flags precede the data file, keeping command flags before command
	// positionals as required by flag parsing:
	//
	//	framectl [-timeout d] [-debug] [-no-wake] <tv.json> <command> [command flags] <args>
	//
	// help, discover, and prepare need no data file.
	gf := flag.NewFlagSet("framectl", flag.ExitOnError)
	gf.Usage = usage
	timeout, debug, noWake := globalFlags(gf)
	gf.Parse(os.Args[1:])
	g := globals{timeout: *timeout, wake: !*noWake, debug: *debug}

	args := gf.Args()
	if len(args) == 0 {
		usage()
	}

	// Commands without a data file are command-first; all others are file-first.
	var name string
	var rest []string
	if c := lookup(args[0]); c != nil && !c.needTVFile {
		name, rest = args[0], args[1:]
	} else {
		if len(args) < 2 {
			usage()
		}
		g.tvFile, name, rest = args[0], args[1], args[2:]
	}
	c := lookup(name)
	if c == nil {
		usage()
	}

	fs := flag.NewFlagSet(c.name, flag.ExitOnError)
	fs.Usage = func() { fmt.Fprintf(os.Stderr, "usage: framectl %s\n", c.synopsis()) }
	registerFlags(fs, c.cmd)
	fs.Parse(rest)
	if len(fs.Args()) < c.minArgs {
		fs.Usage()
		os.Exit(2)
	}
	if err := c.cmd.run(ctx, &g, fs.Args()); err != nil {
		if errors.Is(err, errUsage) {
			fs.Usage()
			os.Exit(2)
		}
		fail(ctx, err)
	}
}

// globals holds the data file and global flags for one invocation.
type globals struct {
	tvFile  string // the tv.json data file; "" for the command-first commands
	timeout time.Duration
	wake    bool
	debug   bool
}

// withTV loads the data file, connects, runs fn, and finishes the client.
// Callers validate arguments first to avoid dialing on usage errors.
func (g *globals) withTV(ctx context.Context, fn func(tvData, *artClient) error) error {
	d, err := loadTVData(g.tvFile)
	if err != nil {
		return err
	}
	return g.withTVData(ctx, d, fn)
}

// withTVData is withTV for callers that already loaded the data file.
func (g *globals) withTVData(ctx context.Context, d tvData, fn func(tvData, *artClient) error) error {
	cl, err := openArt(ctx, d.Connect, g.timeout, g.wake, g.debug)
	if err != nil {
		return err
	}
	return cl.Finish(fn(d, cl))
}

// globalFlags registers the flags shared by TV commands.
func globalFlags(fs *flag.FlagSet) (timeout *time.Duration, debug, noWake *bool) {
	timeout = fs.Duration("timeout", defaultConnectTimeout, "total time to get a connection to the TV (includes sending Wake-on-LAN and retrying if it's asleep)")
	debug = fs.Bool("debug", false, "dump every raw message exchanged with the TV to stderr")
	noWake = fs.Bool("no-wake", false, "don't send Wake-on-LAN if the TV doesn't answer")
	return
}

// printTV builds a command that connects, calls get, and prints its result.
func printTV(get func(*artClient) (string, error)) runner {
	return runnerFunc(func(ctx context.Context, g *globals, _ []string) error {
		return g.withTV(ctx, func(_ tvData, cl *artClient) error {
			s, err := get(cl)
			if err != nil {
				return err
			}
			fmt.Println(s)
			return nil
		})
	})
}

// errUsage causes main to print command usage and exit 2.
var errUsage = errors.New("missing or invalid arguments")

// command defines a subcommand for both dispatch and generated help. cmd is a
// tagged flag struct or runnerFunc; hidden commands remain dispatchable.
type command struct {
	name       string
	needTVFile bool
	hidden     bool
	minArgs    int    // fewest positional args; main enforces it before running
	args       string // positional-arg synopsis shown after the flags
	summary    string // one-line description for the listings
	cmd        runner
}

// commands sets dispatch and help order. Non-trivial definitions live by file.
var commands = []*command{
	pairCommand,
	syncCommand,
	getArtmodeStatusCommand,
	setArtmodeStatusCommand,
	getContentListCommand,
	getCurrentArtworkCommand,
	selectImageCommand,
	getSlideshowStatusCommand,
	setSlideshowStatusCommand,
	sendImageCommand,
	deleteImageListCommand,
	changeFavoriteCommand,
	discoverCommand,
	prepareCommand,
	helpCommand,
}

// lookup returns the command with the given name, or nil.
func lookup(name string) *command {
	for _, c := range commands {
		if c.name == name {
			return c
		}
	}
	return nil
}

// Small commands live inline here.

var getArtmodeStatusCommand = &command{
	name: "get-artmode-status", needTVFile: true,
	summary: "print the TV's Art Mode state (on or off)",
	cmd:     printTV((*artClient).ArtmodeStatus),
}

var setArtmodeStatusCommand = &command{
	name: "set-artmode-status", needTVFile: true, minArgs: 1, args: "<on|off>",
	summary: "switch Art Mode on or off",
	cmd: runnerFunc(func(ctx context.Context, g *globals, args []string) error {
		if args[0] != "on" && args[0] != "off" {
			return errUsage
		}
		return g.withTV(ctx, func(_ tvData, cl *artClient) error {
			if err := cl.SetArtmode(args[0] == "on"); err != nil {
				return err
			}
			log.Printf("art mode: %s", args[0])
			return nil
		})
	}),
}

// Treat "no artwork selected" as an error instead of printing a blank line.
var getCurrentArtworkCommand = &command{
	name: "get-current-artwork", needTVFile: true,
	summary: "print the content id of the artwork the TV currently has selected",
	cmd: printTV(func(cl *artClient) (string, error) {
		id, err := cl.CurrentImage()
		if err == nil && id == "" {
			err = fmt.Errorf("the TV reported no current artwork")
		}
		return id, err
	}),
}

var getSlideshowStatusCommand = &command{
	name: "get-slideshow-status", needTVFile: true,
	summary: "print the slideshow state: off, or the interval in minutes",
	cmd:     printTV((*artClient).SlideshowStatus),
}

// Assign the body in init to avoid a fullHelp/commands initialization cycle.
var helpCommand = &command{name: "help", summary: "print this manual"}

func init() {
	helpCommand.cmd = runnerFunc(func(context.Context, *globals, []string) error {
		fmt.Print(fullHelp())
		return nil
	})
}

// flagSet returns a fresh FlagSet for rendering help. Re-registration resets
// values, but callers no longer need the parsed command state.
func (c *command) flagSet() *flag.FlagSet {
	fs := flag.NewFlagSet(c.name, flag.ContinueOnError)
	registerFlags(fs, c.cmd)
	return fs
}

// synopsis renders a command's one-line usage.
func (c *command) synopsis() string {
	var b strings.Builder
	if c.needTVFile {
		b.WriteString("<tv.json> ")
	}
	b.WriteString(c.name)
	c.flagSet().VisitAll(func(f *flag.Flag) {
		if isBoolFlag(f) {
			fmt.Fprintf(&b, " [-%s]", f.Name)
			return
		}
		ph, _ := flag.UnquoteUsage(f)
		fmt.Fprintf(&b, " [-%s %s]", f.Name, ph)
	})
	if c.args != "" {
		b.WriteString(" " + c.args)
	}
	return b.String()
}

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

// flagLines renders indented flag names, descriptions, and defaults.
func flagLines(fs *flag.FlagSet, indent string) string {
	var b strings.Builder
	fs.VisitAll(func(f *flag.Flag) {
		ph, usage := flag.UnquoteUsage(f)
		label := "-" + f.Name
		if ph != "" {
			label += " " + ph
		}
		if f.DefValue != "" && f.DefValue != "false" {
			usage += fmt.Sprintf(" (default %s)", f.DefValue)
		}
		fmt.Fprintf(&b, "%s%s\n%s    %s\n", indent, label, indent, usage)
	})
	return b.String()
}

// commandsHelp renders visible commands, including flags when full.
func commandsHelp(full bool) string {
	var b strings.Builder
	for _, c := range commands {
		if c.hidden {
			continue
		}
		fmt.Fprintf(&b, "  %s\n", c.synopsis())
		if c.summary != "" {
			fmt.Fprintf(&b, "      %s\n", c.summary)
		}
		if full {
			b.WriteString(flagLines(c.flagSet(), "      "))
		}
	}
	return b.String()
}

const usageSynopsis = `usage:
  framectl [-timeout d] [-debug] [-no-wake] <tv.json> <command> [command flags] [args]
  framectl discover [-timeout d]
  framectl help
`

const helpHeader = `framectl — manage art on a Samsung "The Frame" TV over its undocumented
art-app WebSocket channel.

`

// usage prints short help and exits.
func usage() {
	var b strings.Builder
	b.WriteString(usageSynopsis)
	b.WriteString("\ncommands (the data file <tv.json> comes first; \"framectl help\" for flags and detail):\n")
	b.WriteString(commandsHelp(false))
	fmt.Fprint(os.Stderr, b.String())
	os.Exit(2)
}

// fullHelp combines generated reference text with guide.txt.
func fullHelp() string {
	var b strings.Builder
	b.WriteString(helpHeader)
	b.WriteString(usageSynopsis)
	b.WriteString("\nCommands (the data file <tv.json> comes first, before the command):\n")
	b.WriteString(commandsHelp(true))
	b.WriteString("\nGlobal flags (before <tv.json>):\n")
	gfs := flag.NewFlagSet("framectl", flag.ContinueOnError)
	globalFlags(gfs)
	b.WriteString(flagLines(gfs, "  "))
	b.WriteString("\n")
	b.WriteString(guideText)
	return b.String()
}

// expandStdin replaces one "-" with trimmed, non-empty stdin lines.
func expandStdin(args []string) ([]string, error) {
	var out []string
	expanded := false
	for _, a := range args {
		if a != "-" {
			out = append(out, a)
			continue
		}
		if expanded {
			return nil, errors.New(`"-" (read from stdin) may be given only once`)
		}
		expanded = true
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			if line := strings.TrimSpace(sc.Text()); line != "" {
				out = append(out, line)
			}
		}
		if err := sc.Err(); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// targetHost validates -host/-name and resolves names through SSDP.
func targetHost(ctx context.Context, host, name string) (string, error) {
	switch {
	case host != "" && name != "":
		return "", errors.New("specify only one of -host or -name")
	case host != "":
		return host, nil
	case name != "":
		return resolveName(ctx, name)
	default:
		return "", errors.New("specify -host <ip> or -name <ssdp-name>")
	}
}

// fail exits quietly with 130 on Ctrl-C and reports other errors.
func fail(ctx context.Context, err error) {
	if err == nil {
		return
	}
	if errors.Is(err, context.Canceled) || ctx.Err() != nil {
		os.Exit(130)
	}
	log.Fatal(err)
}

// uploadsCategory is My Photos; MY-C0008 is store art.
const uploadsCategory = "MY-C0002"

// These timeouts bound connection and wake attempts, never the operation.
const (
	defaultConnectTimeout = 60 * time.Second
	connectAttemptTimeout = 15 * time.Second
	connectRetryInterval  = 1 * time.Second
	wakeInterval          = 2 * time.Second
)
