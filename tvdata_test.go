package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSaveDataAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tv.json")

	want := tvData{
		Connect: connection{Name: "The Frame 50", Token: "abc123"},
		Managed: []managedPhoto{
			{Path: "/Users/me/photos/photo1.jpg", ModTime: time.Now().Round(time.Second), ID: "MY_F0012"},
		},
	}
	if err := saveTVData(path, want); err != nil {
		t.Fatalf("saveTVData: %v", err)
	}

	// Round-trips.
	got, err := loadTVData(path)
	if err != nil {
		t.Fatalf("loadTVData: %v", err)
	}
	if got.Connect != want.Connect {
		t.Errorf("connect: got %+v, want %+v", got.Connect, want.Connect)
	}
	if len(got.Managed) != 1 || got.Managed[0].ID != "MY_F0012" || !got.Managed[0].ModTime.Equal(want.Managed[0].ModTime) {
		t.Errorf("managed: got %+v, want %+v", got.Managed, want.Managed)
	}

	// Secret-file perms.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}

	// Overwrite, then confirm no temp residue is left next to the file.
	want.Connect.Token = "def456"
	if err := saveTVData(path, want); err != nil {
		t.Fatalf("saveTVData (overwrite): %v", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "tv.json" {
			t.Errorf("unexpected leftover file %q (temp not cleaned up?)", e.Name())
		}
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %q", e.Name())
		}
	}
	if got, _ := loadTVData(path); got.Connect.Token != "def456" {
		t.Errorf("overwrite not applied: token = %q", got.Connect.Token)
	}
}

// TestManagedNames checks the display names: a path under the current directory
// shows relative to it; any other path shows in full.
func TestManagedNames(t *testing.T) {
	t.Chdir(t.TempDir())
	cwd, err := os.Getwd() // the form the implementation's os.Getwd will also return
	if err != nil {
		t.Fatal(err)
	}
	const outside = "/somewhere/else/far.jpg"

	d := tvData{Managed: []managedPhoto{
		{Path: filepath.Join(cwd, "a.jpg"), ID: "A"},
		{Path: filepath.Join(cwd, "sub", "b.jpg"), ID: "B"},
		{Path: outside, ID: "C"},
	}}
	n := d.managedNames()
	if n["A"] != "a.jpg" {
		t.Errorf("under cwd: got %q, want a.jpg", n["A"])
	}
	if want := filepath.Join("sub", "b.jpg"); n["B"] != want {
		t.Errorf("under cwd subdir: got %q, want %q", n["B"], want)
	}
	if n["C"] != outside {
		t.Errorf("outside cwd: got %q, want full path %q", n["C"], outside)
	}
}
