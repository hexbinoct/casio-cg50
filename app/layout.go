package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
)

// The workspace layout (which windows are open, where they are docked, their sizes and
// each window's own view state) is the page's business: it hands Go an opaque JSON
// document and gets it back on the next start. It lives next to settings.json as
// layout.json, so it is per user and survives updates of the app.

const maxLayout = 1 << 20 // a layout is a few KB; refuse anything absurd

func (s *store) layoutPath() string { return filepath.Join(s.dir, "layout.json") }

// LoadLayout returns the saved workspace layout, or "" when there is none (first start,
// or after Reset layout).
func (a *App) LoadLayout() string {
	b, err := os.ReadFile(a.store.layoutPath())
	if err != nil || !json.Valid(b) {
		return ""
	}
	return string(b)
}

// SaveLayout stores the workspace layout ("" removes it: the next start uses the default).
func (a *App) SaveLayout(layout string) error {
	p := a.store.layoutPath()
	if layout == "" {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	}
	if len(layout) > maxLayout || !json.Valid([]byte(layout)) {
		return errors.New("not a layout")
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(layout), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}
