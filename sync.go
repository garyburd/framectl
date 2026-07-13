package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// syncCmd holds one sync invocation's flags.
type syncCmd struct {
	Exact  bool `flag:"exact" usage:"delete even the currently selected photo (knocks the TV out of slideshow mode)"`
	DryRun bool `flag:"dry-run" usage:"print the upload/delete plan without changing the TV or the data file (never wakes the TV)"`
	Force  bool `flag:"force" usage:"re-upload every image, even ones unchanged since the last run"`
}

var syncCommand = &command{
	name: "sync", needTVFile: true, minArgs: 1, args: "<path>...",
	summary: "mirror My Photos to these images (or a dir, or - for stdin); never touches the slideshow",
	cmd:     new(syncCmd),
}

// sourceImage identifies a local image by absolute path, size, and mtime.
type sourceImage struct {
	path    string
	size    int64
	modTime time.Time
}

// deletion pairs a content id with a user-facing label.
type deletion struct{ id, label string }

// run mirrors the given images to My Photos.
func (c *syncCmd) run(ctx context.Context, g *globals, paths []string) error {
	sources, err := gatherSources(paths)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return fmt.Errorf("no images to show (give files or directories, or - to read paths from stdin)")
	}
	// One read serves both the wake decision and the mirror.
	d, err := loadTVData(g.tvFile)
	if err != nil {
		return err
	}
	// Wake only for locally visible work; dry runs never wake. TV-side drift is
	// reconciled only when the TV is already on.
	switch {
	case !g.wake: // global -no-wake already forces the single attempt
	case c.DryRun:
		g.wake = false
	case c.Force: // TV work regardless of the records
	default:
		g.wake = sourcesDiffer(d, sources)
	}
	return g.withTVData(ctx, d, func(d tvData, cl *artClient) error {
		return c.mirror(ctx, g, cl, &d, sources)
	})
}

// mirror applies uploads before deletes so My Photos is never briefly empty.
func (c *syncCmd) mirror(ctx context.Context, g *globals, cl *artClient, d *tvData, sources []sourceImage) error {
	items, err := cl.ContentList()
	if err != nil {
		return err
	}
	uploads, deletes := c.planMirror(*d, sources, items)

	// Current-image lookup is best-effort during dry runs.
	var currentID string
	if !c.Exact {
		currentID, err = cl.CurrentImage()
		if err != nil && !c.DryRun {
			return fmt.Errorf("reading the current image: %w", err)
		}
	}

	// Keep the current photo until selection moves; -exact overrides this guard.
	kept := false
	if !c.Exact && currentID != "" {
		if reduced, found := dropDeletion(deletes, currentID); found {
			deletes, kept = reduced, true
		}
	}

	if c.DryRun {
		for _, u := range uploads {
			fmt.Printf("would upload %s\n", filepath.Base(u.path))
		}
		if kept {
			fmt.Printf("would keep current image %s (deleting it would knock the TV out of slideshow mode; -exact deletes it)\n", currentID)
		}
		for _, del := range deletes {
			fmt.Printf("would delete %s (%s)\n", del.label, del.id)
		}
		fmt.Printf("dry run: %d to upload, %d to delete; no changes made\n", len(uploads), len(deletes))
		return nil
	}

	if err := applyUploads(ctx, cl, d, g.tvFile, uploads); err != nil {
		return err
	}

	if err := applyDeletes(cl, d, g.tvFile, deletes); err != nil {
		return err
	}

	if kept {
		log.Printf("sync: %d uploaded, %d deleted; kept current image %s (it'll be removed once the selection moves off it)", len(uploads), len(deletes), currentID)
	} else {
		log.Printf("sync: %d uploaded, %d deleted", len(uploads), len(deletes))
	}
	return nil
}

// gatherSources expands files, non-recursive directories, and stdin. Absolute
// paths are the managed identity and deduplication key.
func gatherSources(paths []string) ([]sourceImage, error) {
	paths, err := expandStdin(paths)
	if err != nil {
		return nil, err
	}
	byPath := map[string]sourceImage{}
	for _, p := range paths {
		info, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		if info.IsDir() {
			imgs, err := scanDir(p)
			if err != nil {
				return nil, err
			}
			for _, s := range imgs {
				byPath[s.path] = s
			}
			continue
		}
		if !isImage(p) {
			return nil, fmt.Errorf("%s is not a supported image (.jpg/.jpeg/.png)", p)
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return nil, err
		}
		byPath[abs] = sourceImage{path: abs, size: info.Size(), modTime: info.ModTime()}
	}
	out := make([]sourceImage, 0, len(byPath))
	for _, s := range byPath {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out, nil
}

// scanDir returns image files directly in dir, with absolute paths.
func scanDir(dir string) ([]sourceImage, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []sourceImage
	for _, e := range entries {
		if e.IsDir() || !isImage(e.Name()) {
			continue
		}
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		abs, err := filepath.Abs(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		out = append(out, sourceImage{path: abs, size: info.Size(), modTime: info.ModTime()})
	}
	return out, nil
}

func isImage(name string) bool {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".jpg", ".jpeg", ".png":
		return true
	}
	return false
}

// planMirror uploads new, changed, forced, or TV-deleted sources. It deletes
// every My Photos item not backed by an unchanged source.
func (c *syncCmd) planMirror(d tvData, sources []sourceImage, items []contentItem) (uploads []sourceImage, deletes []deletion) {
	bySource := make(map[string]sourceImage, len(sources))
	for _, s := range sources {
		bySource[s.path] = s
	}
	// A managed source absent from My Photos must be restored.
	onTV := map[string]bool{}
	for _, e := range photoEntries(items) { // My Photos only
		onTV[e.ContentID] = true
	}
	for _, s := range sources {
		if !c.Force {
			if p, ok := d.managed(s.path); ok && p.matches(s.size, s.modTime) && onTV[p.ID] {
				continue // unchanged and still on the TV
			}
		}
		uploads = append(uploads, s)
	}
	sort.Slice(uploads, func(i, j int) bool { return uploads[i].path < uploads[j].path })

	// Keep only unchanged sources still present on the TV.
	keptIDs := map[string]bool{}
	for _, p := range d.Managed {
		if s, ok := bySource[p.Path]; ok && !c.Force && p.matches(s.size, s.modTime) && onTV[p.ID] {
			keptIDs[p.ID] = true
		}
	}
	names := d.managedNames()
	for _, e := range photoEntries(items) { // My Photos only
		if keptIDs[e.ContentID] {
			continue
		}
		label := names[e.ContentID]
		if label == "" {
			label = "(unmanaged)"
		}
		deletes = append(deletes, deletion{e.ContentID, label})
	}
	sort.Slice(deletes, func(i, j int) bool { return deletes[i].label < deletes[j].label })
	return uploads, deletes
}

// sourcesDiffer detects local work before dialing. It cannot see TV-side drift.
func sourcesDiffer(d tvData, sources []sourceImage) bool {
	inSources := make(map[string]bool, len(sources))
	for _, s := range sources {
		inSources[s.path] = true
		if p, ok := d.managed(s.path); !ok || !p.matches(s.size, s.modTime) {
			return true
		}
	}
	for _, p := range d.Managed {
		if !inSources[p.Path] {
			return true
		}
	}
	return false
}

// applyUploads saves after each upload so interruption leaves accurate records.
func applyUploads(ctx context.Context, cl *artClient, d *tvData, tvFile string, uploads []sourceImage) error {
	for _, u := range uploads {
		id, err := cl.UploadPhoto(ctx, u.path)
		if err != nil {
			return fmt.Errorf("upload %s: %w", filepath.Base(u.path), err)
		}
		d.Managed = append(withoutPath(d.Managed, u.path), managedPhoto{Path: u.path, Size: u.size, ModTime: u.modTime, ID: id})
		if err := saveTVData(tvFile, *d); err != nil {
			return err
		}
		fmt.Printf("uploaded %s -> %s\n", filepath.Base(u.path), id)
	}
	return nil
}

// applyDeletes removes photos and their managed records, then saves.
func applyDeletes(cl *artClient, d *tvData, tvFile string, deletes []deletion) error {
	if len(deletes) == 0 {
		return nil
	}
	ids := make([]string, len(deletes))
	gone := make(map[string]bool, len(deletes))
	for i, del := range deletes {
		ids[i], gone[del.id] = del.id, true
	}
	if err := cl.DeletePhotos(ids); err != nil {
		return fmt.Errorf("delete: %w", err)
	}
	d.Managed = withoutIDs(d.Managed, gone) // unmanaged ids aren't records; no-op for them
	if err := saveTVData(tvFile, *d); err != nil {
		return err
	}
	for _, del := range deletes {
		fmt.Printf("deleted %s (%s)\n", del.label, del.id)
	}
	return nil
}

// dropDeletion removes id without mutating the caller's slice.
func dropDeletion(deletes []deletion, id string) ([]deletion, bool) {
	for i, del := range deletes {
		if del.id == id {
			return append(deletes[:i:i], deletes[i+1:]...), true
		}
	}
	return deletes, false
}
