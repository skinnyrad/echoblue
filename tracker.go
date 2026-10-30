package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var macPattern = regexp.MustCompile(`^([0-9A-Fa-f]{2}:){5}[0-9A-Fa-f]{2}$`)

func normalizeMAC(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

func validMAC(s string) bool { return macPattern.MatchString(strings.TrimSpace(s)) }

// parseWatchlist validates one MAC per line. Blank lines are ignored,
// invalid lines and duplicates are reported as warnings (with line
// numbers) instead of silently becoming targets that never match.
func parseWatchlist(lines []string) (devices []string, warnings []string) {
	seen := map[string]struct{}{}
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" {
			continue
		}
		if !validMAC(line) {
			warnings = append(warnings, fmt.Sprintf("line %d: ignoring invalid MAC address %q", i+1, line))
			continue
		}
		mac := normalizeMAC(line)
		if _, dup := seen[mac]; dup {
			warnings = append(warnings, fmt.Sprintf("line %d: ignoring duplicate %s", i+1, mac))
			continue
		}
		seen[mac] = struct{}{}
		devices = append(devices, mac)
	}
	return devices, warnings
}

// watchlist is the known-device list. It can be reloaded in place when
// the file changes, so devices can be added without restarting.
type watchlist struct {
	path string

	mu      sync.RWMutex
	devices []string
	set     map[string]struct{}
	modTime time.Time
	size    int64
}

func readLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return strings.Split(string(data), "\n"), nil
}

func loadWatchlist(path string) (*watchlist, error) {
	w := &watchlist{path: path}
	if err := w.reload(true); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *watchlist) reload(force bool) error {
	fi, err := os.Stat(w.path)
	if err != nil {
		if force {
			logf("Error: File '%s' does not exist.", w.path)
			w.mu.Lock()
			w.set = map[string]struct{}{}
			w.mu.Unlock()
			return nil
		}
		return err
	}
	w.mu.RLock()
	changed := force || !fi.ModTime().Equal(w.modTime) || fi.Size() != w.size
	w.mu.RUnlock()
	if !changed {
		return nil
	}
	lines, err := readLines(w.path)
	if err != nil {
		return err
	}
	devices, warnings := parseWatchlist(lines)
	for _, warn := range warnings {
		logf("Warning: %s: %s", w.path, warn)
	}
	set := make(map[string]struct{}, len(devices))
	for _, d := range devices {
		set[d] = struct{}{}
	}
	w.mu.Lock()
	w.devices, w.set, w.modTime, w.size = devices, set, fi.ModTime(), fi.Size()
	w.mu.Unlock()
	if !force {
		logf("Reloaded watchlist %s: %d devices", w.path, len(devices))
	}
	return nil
}

// reloadIfChanged reloads the file if it changed on disk.
func (w *watchlist) reloadIfChanged() {
	if err := w.reload(false); err != nil {
		logf("Warning: watchlist reload failed: %v", err)
	}
}

func (w *watchlist) snapshot() []string {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return append([]string(nil), w.devices...)
}

func (w *watchlist) contains(addr string) bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	_, ok := w.set[addr]
	return ok
}

func (w *watchlist) len() int {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return len(w.devices)
}

// tracker records presence per device. A device alerts once when it
// arrives; it is only considered gone after it has been unseen for
// longer than the departure window, so one missed cycle (a flaky page
// or advertisement) does not flap the state.
type tracker struct {
	mu       sync.Mutex
	present  map[string]bool
	lastSeen map[string]time.Time
	session  map[string]struct{}
}

func newTracker() *tracker {
	return &tracker{
		present:  map[string]bool{},
		lastSeen: map[string]time.Time{},
		session:  map[string]struct{}{},
	}
}

// observeSeen records a sighting. It returns true only on the
// absent -> present transition (i.e. when an alert should fire).
func (t *tracker) observeSeen(addr string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.session[addr] = struct{}{}
	t.lastSeen[addr] = now
	if t.present[addr] {
		return false
	}
	t.present[addr] = true
	return true
}

// sweep returns devices that were present but have been unseen for
// longer than `after`, marking them absent.
func (t *tracker) sweep(now time.Time, after time.Duration) []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	var gone []string
	for addr, isPresent := range t.present {
		if isPresent && now.Sub(t.lastSeen[addr]) > after {
			t.present[addr] = false
			gone = append(gone, addr)
		}
	}
	sort.Strings(gone)
	return gone
}

func (t *tracker) isPresent(addr string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.present[addr]
}

func (t *tracker) seen() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]string, 0, len(t.session))
	for a := range t.session {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
