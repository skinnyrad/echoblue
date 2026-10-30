package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseWatchlistValidation(t *testing.T) {
	devices, warnings := parseWatchlist([]string{
		"77:0E:FD:98:10:30",
		"",
		"not-a-mac",
		"77:0e:fd:98:10:30", // duplicate after normalisation
		"89:57:5F:D0:F4:06",
		"12345",
	})
	if len(devices) != 2 || devices[0] != "77:0E:FD:98:10:30" {
		t.Fatalf("devices %v", devices)
	}
	if len(warnings) != 3 {
		t.Fatalf("want 3 warnings (invalid, duplicate, invalid), got %v", warnings)
	}
	if !strings.Contains(warnings[0], "line 3") {
		t.Fatalf("warning should carry line number: %q", warnings[0])
	}
}

func TestWatchlistReload(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "devices.txt")
	os.WriteFile(p, []byte("AA:BB:CC:DD:EE:01\n"), 0o644)
	wl, err := loadWatchlist(p)
	if err != nil {
		t.Fatal(err)
	}
	if wl.len() != 1 || !wl.contains("AA:BB:CC:DD:EE:01") {
		t.Fatalf("initial load: %v", wl.snapshot())
	}
	// Edit the file: a running scanner must pick this up without restart.
	time.Sleep(10 * time.Millisecond)
	os.WriteFile(p, []byte("AA:BB:CC:DD:EE:01\nAA:BB:CC:DD:EE:02\n"), 0o644)
	wl.reloadIfChanged()
	if wl.len() != 2 || !wl.contains("AA:BB:CC:DD:EE:02") {
		t.Fatalf("after reload: %v", wl.snapshot())
	}
}

func TestPresenceTransitions(t *testing.T) {
	tr := newTracker()
	now := time.Now()
	addr := "AA:BB:CC:DD:EE:FF"
	if !tr.observeSeen(addr, now) {
		t.Fatal("first sighting should be an arrival")
	}
	if tr.observeSeen(addr, now.Add(time.Second)) {
		t.Fatal("second sighting while present must not re-alert")
	}
	// One missed window must not mark departure; two must.
	if gone := tr.sweep(now.Add(10*time.Second), 30*time.Second); len(gone) != 0 {
		t.Fatalf("departed too early: %v", gone)
	}
	if gone := tr.sweep(now.Add(time.Minute), 30*time.Second); len(gone) != 1 {
		t.Fatalf("should depart after window: %v", gone)
	}
	if !tr.observeSeen(addr, now.Add(2*time.Minute)) {
		t.Fatal("sighting after departure should alert again")
	}
	if seen := tr.seen(); len(seen) != 1 {
		t.Fatalf("session set: %v", seen)
	}
}

func TestGenerateUAPWithNAP(t *testing.T) {
	addrs, err := generateUAPCombinations("A1:B2:C3", "00:00")
	if err != nil {
		t.Fatal(err)
	}
	if addrs[0] != "00:00:00:a1:b2:c3" || addrs[255] != "00:00:ff:a1:b2:c3" {
		t.Fatalf("bounds: %s .. %s", addrs[0], addrs[255])
	}
	custom, err := generateUAPCombinations("a1b2c3", "C0:FF")
	if err != nil {
		t.Fatal(err)
	}
	if custom[0x2A] != "c0:ff:2a:a1:b2:c3" {
		t.Fatalf("custom NAP: %s", custom[0x2A])
	}
	if _, err := generateUAPCombinations("a1b2c3", "00"); err == nil {
		t.Fatal("expected error for one-octet NAP")
	}
	if _, err := parseNAP("zz:00"); err == nil {
		t.Fatal("expected error for non-hex NAP")
	}
}

func TestParseInquiry(t *testing.T) {
	out := "Scanning ...\n\t00:00:2A:11:22:35\tECHOBLUE-TEST\n\tC8:84:47:5C:AA:0C\tATS-1080 Yamaha\ngarbage line\n"
	found := parseInquiry(out)
	if len(found) != 2 || found["00:00:2A:11:22:35"] != "ECHOBLUE-TEST" {
		t.Fatalf("found %v", found)
	}
}

func TestHandleAdvertisement(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "devices.txt")
	os.WriteFile(p, []byte("AA:BB:CC:DD:EE:01\n"), 0o644)
	wl, _ := loadWatchlist(p)
	tr := newTracker()

	// Watchlist mode: unknown ignored, known tracked once as present.
	cycle := map[string]bool{}
	handleAdvertisement(wl, tr, false, cycle, "hci0", "11:22:33:44:55:66", "Other", -60)
	if len(tr.seen()) != 0 {
		t.Fatal("unknown device tracked in watchlist mode")
	}
	handleAdvertisement(wl, tr, false, cycle, "hci0", "AA:BB:CC:DD:EE:01", "Mine", -50)
	if !tr.isPresent("AA:BB:CC:DD:EE:01") {
		t.Fatal("known device not present")
	}

	// Discovery mode: everything is reported, deduped per cycle.
	cycle2 := map[string]bool{}
	handleAdvertisement(wl, tr, true, cycle2, "hci1", "11:22:33:44:55:66", "Other", -60)
	handleAdvertisement(wl, tr, true, cycle2, "hci1", "11:22:33:44:55:66", "Other", -55)
	if len(cycle2) != 1 {
		t.Fatalf("discovery dedup per cycle: %v", cycle2)
	}
}

func TestParseConfigDiscoveryAndOptions(t *testing.T) {
	cfg, err := parseConfig([]string{"-b", "-d"})
	if err != nil || !cfg.discover || cfg.mode != "ble" {
		t.Fatalf("ble discover: %+v %v", cfg, err)
	}
	cfg, err = parseConfig([]string{"--classic", "--discover", "--classic-interval", "5"})
	if err != nil || !cfg.discover {
		t.Fatalf("classic discover: %+v %v", cfg, err)
	}
	if _, err := parseConfig([]string{"-b"}); err == nil {
		t.Fatal("watchlist mode without -f must fail")
	}
	if _, err := parseConfig([]string{"-u", "-d", "-l", "laps.txt", "-o", "out.txt"}); err == nil {
		t.Fatal("discover with UAP must fail")
	}
	cfg, err = parseConfig([]string{"-u", "-l", "laps.txt", "-o", "out.txt", "--nap", "C0:FF", "--log-file", "x.log", "--alert-hook", "echo hi"})
	if err != nil || cfg.nap != "C0:FF" {
		t.Fatalf("uap options: %+v %v", cfg, err)
	}
	if _, err := parseConfig([]string{"-u", "-l", "laps.txt", "-o", "out.txt", "--nap", "00"}); err == nil {
		t.Fatal("invalid NAP must fail")
	}
}

func TestPingDeviceResult(t *testing.T) {
	ping := func(ctx context.Context, adapter, mac string, count int) (string, error) {
		return "64 bytes from " + mac, nil
	}
	if !pingDevice(context.Background(), "hci0", "AA:BB:CC:DD:EE:FF", 1, ping) {
		t.Fatal("expected active")
	}
	miss := func(ctx context.Context, adapter, mac string, count int) (string, error) {
		return "", nil
	}
	if pingDevice(context.Background(), "hci0", "AA:BB:CC:DD:EE:FF", 1, miss) {
		t.Fatal("expected inactive")
	}
}

func TestWaitScanDone(t *testing.T) {
	done := make(chan error, 1)
	done <- nil
	if !waitScanDone(done, time.Second) {
		t.Fatal("finished scan should report true")
	}
	never := make(chan error)
	start := time.Now()
	if waitScanDone(never, 30*time.Millisecond) {
		t.Fatal("wedged scan should report false on timeout")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("wait was not bounded: %s", elapsed)
	}
}
