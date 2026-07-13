package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// tvData stores one TV's connection and managed photos.
type tvData struct {
	Connect connection     `json:"connect"`
	Managed []managedPhoto `json:"managed,omitempty"`
}

// managedPhoto maps an absolute source path and change metadata to the TV's
// content id. sync may delete TV photos without such a record as unmanaged.
type managedPhoto struct {
	Path    string    `json:"path"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"modtime"`
	ID      string    `json:"id"`
}

// matches reports whether the local file is unchanged since upload.
func (p managedPhoto) matches(size int64, modTime time.Time) bool {
	return p.Size == size && p.ModTime.Equal(modTime)
}

// managed returns the record for the given absolute source path, if any.
func (d tvData) managed(path string) (managedPhoto, bool) {
	for _, p := range d.Managed {
		if p.Path == path {
			return p, true
		}
	}
	return managedPhoto{}, false
}

// managedNames maps content ids to relative paths when below the current dir,
// and absolute paths otherwise.
func (d tvData) managedNames() map[string]string {
	cwd, cwdErr := os.Getwd()
	m := make(map[string]string, len(d.Managed))
	for _, p := range d.Managed {
		name := p.Path
		if cwdErr == nil {
			if rel, err := filepath.Rel(cwd, p.Path); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				name = rel // a child of the current directory
			}
		}
		m[p.ID] = name
	}
	return m
}

// withoutIDs returns s without the records whose ID is in ids (s is consumed).
func withoutIDs(s []managedPhoto, ids map[string]bool) []managedPhoto {
	return slices.DeleteFunc(s, func(p managedPhoto) bool { return ids[p.ID] })
}

// withoutPath returns s without any record for the given path (s is consumed).
func withoutPath(s []managedPhoto, path string) []managedPhoto {
	return slices.DeleteFunc(s, func(p managedPhoto) bool { return p.Path == path })
}

// connection stores a token, either a literal host or rediscovered SSDP name,
// and an optional MAC for Wake-on-LAN.
type connection struct {
	Host  string `json:"host,omitempty"`
	Name  string `json:"name,omitempty"`
	MAC   string `json:"mac,omitempty"`
	Token string `json:"token"`
}

// address returns the literal host or resolves the stored SSDP name.
func (c connection) address(ctx context.Context) (string, error) {
	switch {
	case c.Host != "":
		return c.Host, nil
	case c.Name != "":
		return resolveName(ctx, c.Name)
	default:
		return "", errors.New("no host or name recorded")
	}
}

// saveTVData writes d as atomic JSON.
func saveTVData(tvFile string, d tvData) error {
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return atomicWriteFile(tvFile, append(raw, '\n'))
}

// atomicWriteFile creates parents and renames a same-directory temporary file
// over path. Mode 0600 protects stored auth tokens.
func atomicWriteFile(path string, data []byte) (err error) {
	defer func() {
		if err != nil {
			err = fmt.Errorf("write %s: %w", path, err)
		}
	}()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*") // same dir, so rename is atomic
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		if tmpName != "" {
			os.Remove(tmpName) // clean up on any failure before the rename
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	tmpName = "" // renamed into place; nothing to clean up
	return nil
}

// loadTVData reads a tv.json data file written by pair.
func loadTVData(tvFile string) (tvData, error) {
	raw, err := os.ReadFile(tvFile)
	if errors.Is(err, fs.ErrNotExist) {
		return tvData{}, fmt.Errorf("%w (create it with: framectl %s pair (-host <ip> | -name <name>))", err, tvFile)
	}
	if err != nil {
		return tvData{}, err
	}
	var d tvData
	if err := json.Unmarshal(raw, &d); err != nil {
		return tvData{}, fmt.Errorf("parse %s: %w", tvFile, err)
	}
	return d, nil
}
