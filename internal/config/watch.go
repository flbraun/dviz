package config

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	watchDebounce = 200 * time.Millisecond
	// dirRescan is how often candidate directories that did not exist yet are re-checked.
	dirRescan = 2 * time.Second
)

// Watch observes the candidate directories and calls apply with every config that differs
// from current. Invalid configs are logged and skipped, keeping the last good one. Watch
// blocks until ctx is done.
//
// Directories rather than files are watched, so atomic rename-saves, newly created files and
// deletions are all noticed and re-resolved against the full search order.
func Watch(ctx context.Context, candidates []string, current *Config, apply func(*Config)) error {
	w, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer w.Close()

	watched := map[string]bool{}
	addDirs := func() (added bool) {
		for _, c := range candidates {
			dir := filepath.Dir(c)
			if watched[dir] {
				continue
			}
			if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
				continue
			}
			if err := w.Add(dir); err != nil {
				slog.Warn("config: cannot watch directory", "dir", dir, "err", err)
				continue
			}
			watched[dir] = true
			added = true
		}
		return added
	}
	addDirs()

	reload := func() {
		next, err := Load(candidates)
		if err != nil {
			slog.Error("config: reload failed, keeping previous config", "err", err)
			return
		}
		if reflect.DeepEqual(next, current) {
			return
		}
		slog.Info("config: applying changes", "source", next.Source)
		current = next
		apply(next)
	}

	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	rescan := time.NewTicker(dirRescan)
	defer rescan.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case ev, ok := <-w.Events:
			if !ok {
				return nil
			}
			if watched[ev.Name] && ev.Has(fsnotify.Remove|fsnotify.Rename) {
				// The watched directory itself is gone; rescan re-adds it if it reappears.
				delete(watched, ev.Name)
				debounce.Reset(watchDebounce)
				continue
			}
			name := filepath.Base(ev.Name)
			// "..data" style names cover symlink-swapped configs (e.g. Kubernetes ConfigMaps).
			if name == FileName || strings.HasPrefix(name, "..") {
				debounce.Reset(watchDebounce)
			}
		case err, ok := <-w.Errors:
			if !ok {
				return nil
			}
			slog.Warn("config: watcher error", "err", err)
		case <-rescan.C:
			if addDirs() {
				debounce.Reset(watchDebounce)
			}
		case <-debounce.C:
			reload()
		}
	}
}
