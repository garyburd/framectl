package main

import (
	"context"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var prepareCommand = &command{
	name: "prepare", hidden: true, minArgs: 1, args: "<path>...",
	summary: "render Frame previews of these images (or a dir, or - for stdin) to a browser page (no TV)",
	cmd:     runnerFunc(runPrepare),
}

// runPrepare renders each source onto the Frame canvas and opens an index
// page over the results. One stable directory is replaced per run so
// previews never accumulate, and the page URL stays reload-able.
func runPrepare(ctx context.Context, _ *globals, args []string) error {
	sources, err := gatherSources(args)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fmt.Errorf("no images to preview (give files or directories, or - to read paths from stdin)")
	}

	dir := filepath.Join(os.TempDir(), "framectl-preview")
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	var page strings.Builder
	page.WriteString(previewHeader)
	used := map[string]bool{}
	for _, s := range sources {
		if err := ctx.Err(); err != nil {
			return err
		}
		data, err := prepareImage(s.path)
		if err != nil {
			return fmt.Errorf("prepare %s: %w", s.path, err)
		}
		name := previewName(used, s.path)
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
			return err
		}
		fmt.Printf("prepared %s -> %s\n", s.path, name)
		fmt.Fprintf(&page, "<figure><img src=\"%s\" alt=\"\"><figcaption>%s</figcaption></figure>\n",
			html.EscapeString(name), html.EscapeString(s.path))
	}

	index := filepath.Join(dir, "index.html")
	if err := os.WriteFile(index, []byte(page.String()), 0o600); err != nil {
		return err
	}
	fmt.Printf("preview: %s\n", index)
	// The preview is complete either way; a failed launch is only a note.
	if err := openBrowser(index); err != nil {
		fmt.Printf("open it manually (auto-open failed: %v)\n", err)
	}
	return nil
}

const previewHeader = `<!doctype html>
<meta charset="utf-8">
<title>framectl preview</title>
<style>
body { background: #111; color: #999; font: 14px/1.4 system-ui, sans-serif; margin: 2rem auto; max-width: 75rem; padding: 0 1rem; }
figure { margin: 0 0 2.5rem; }
img { display: block; width: 100%; }
figcaption { margin-top: .5rem; word-break: break-all; }
</style>
<h1>framectl preview</h1>
`

// previewName maps a source path to an output name that is unique within
// this run and always ends in .jpg (prepareImage always emits JPEG).
func previewName(used map[string]bool, path string) string {
	base := filepath.Base(path)
	stem := strings.TrimSuffix(base, filepath.Ext(base))
	name := stem + ".jpg"
	for i := 2; used[name]; i++ {
		name = fmt.Sprintf("%s-%d.jpg", stem, i)
	}
	used[name] = true
	return name
}

// openBrowser hands the file to the platform's default opener without
// waiting for the browser to exit.
func openBrowser(path string) error {
	switch runtime.GOOS {
	case "darwin":
		return exec.Command("open", path).Start()
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", path).Start()
	default:
		return exec.Command("xdg-open", path).Start()
	}
}
