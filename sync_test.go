package main

import (
	"slices"
	"testing"
	"time"
)

func TestSourcesDiffer(t *testing.T) {
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	rec := func(path string, size int64, mod time.Time) managedPhoto {
		return managedPhoto{Path: path, Size: size, ModTime: mod, ID: "id" + path}
	}
	src := func(path string, size int64, mod time.Time) sourceImage {
		return sourceImage{path: path, size: size, modTime: mod}
	}
	tests := []struct {
		name    string
		managed []managedPhoto
		sources []sourceImage
		want    bool
	}{
		{"all unchanged", []managedPhoto{rec("/a.jpg", 1, t0), rec("/b.jpg", 2, t0)},
			[]sourceImage{src("/a.jpg", 1, t0), src("/b.jpg", 2, t0)}, false},
		{"new file", []managedPhoto{rec("/a.jpg", 1, t0)},
			[]sourceImage{src("/a.jpg", 1, t0), src("/b.jpg", 2, t0)}, true},
		{"changed size", []managedPhoto{rec("/a.jpg", 1, t0)},
			[]sourceImage{src("/a.jpg", 3, t0)}, true},
		{"changed modtime", []managedPhoto{rec("/a.jpg", 1, t0)},
			[]sourceImage{src("/a.jpg", 1, t0.Add(time.Hour))}, true},
		{"removed file", []managedPhoto{rec("/a.jpg", 1, t0), rec("/b.jpg", 2, t0)},
			[]sourceImage{src("/a.jpg", 1, t0)}, true},
		{"nothing recorded yet", nil,
			[]sourceImage{src("/a.jpg", 1, t0)}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sourcesDiffer(tvData{Managed: tt.managed}, tt.sources); got != tt.want {
				t.Errorf("sourcesDiffer = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPlanMirror(t *testing.T) {
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	rec := func(path, id string, size int64, mod time.Time) managedPhoto {
		return managedPhoto{Path: path, Size: size, ModTime: mod, ID: id}
	}
	src := func(path string, size int64, mod time.Time) sourceImage {
		return sourceImage{path: path, size: size, modTime: mod}
	}
	// item builds a My Photos content-list entry for id.
	item := func(id string) contentItem {
		return contentItem{ContentID: id, CategoryID: uploadsCategory}
	}
	tests := []struct {
		name        string
		force       bool
		managed     []managedPhoto
		sources     []sourceImage
		items       []contentItem
		wantUploads []string // source paths
		wantDeletes []string // content ids
	}{
		{
			name:    "unchanged and present on TV -> skip",
			managed: []managedPhoto{rec("/a.jpg", "ca", 1, t0)},
			sources: []sourceImage{src("/a.jpg", 1, t0)},
			items:   []contentItem{item("ca")},
		},
		{
			name:        "managed unchanged locally but missing from TV -> upload",
			managed:     []managedPhoto{rec("/a.jpg", "ca", 1, t0)},
			sources:     []sourceImage{src("/a.jpg", 1, t0)},
			items:       nil, // ca deleted at the TV
			wantUploads: []string{"/a.jpg"},
		},
		{
			name:        "changed file -> upload, stale id deleted",
			managed:     []managedPhoto{rec("/a.jpg", "ca", 1, t0)},
			sources:     []sourceImage{src("/a.jpg", 2, t0)},
			items:       []contentItem{item("ca")},
			wantUploads: []string{"/a.jpg"},
			wantDeletes: []string{"ca"},
		},
		{
			name:        "new source -> upload",
			managed:     nil,
			sources:     []sourceImage{src("/a.jpg", 1, t0)},
			items:       nil,
			wantUploads: []string{"/a.jpg"},
		},
		{
			name:        "unmanaged TV photo -> delete",
			managed:     []managedPhoto{rec("/a.jpg", "ca", 1, t0)},
			sources:     []sourceImage{src("/a.jpg", 1, t0)},
			items:       []contentItem{item("ca"), item("cx")},
			wantDeletes: []string{"cx"},
		},
		{
			name:        "force re-uploads everything",
			force:       true,
			managed:     []managedPhoto{rec("/a.jpg", "ca", 1, t0)},
			sources:     []sourceImage{src("/a.jpg", 1, t0)},
			items:       []contentItem{item("ca")},
			wantUploads: []string{"/a.jpg"},
			wantDeletes: []string{"ca"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &syncCmd{Force: tt.force}
			uploads, deletes := c.planMirror(tvData{Managed: tt.managed}, tt.sources, tt.items)
			var gotUploads []string
			for _, u := range uploads {
				gotUploads = append(gotUploads, u.path)
			}
			var gotDeletes []string
			for _, del := range deletes {
				gotDeletes = append(gotDeletes, del.id)
			}
			if !slices.Equal(gotUploads, tt.wantUploads) {
				t.Errorf("uploads = %v, want %v", gotUploads, tt.wantUploads)
			}
			if !slices.Equal(gotDeletes, tt.wantDeletes) {
				t.Errorf("deletes = %v, want %v", gotDeletes, tt.wantDeletes)
			}
		})
	}
}
