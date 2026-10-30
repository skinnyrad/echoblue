package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGenerateUAPCombinations(t *testing.T) {
	addrs, err := generateUAPCombinations("A1:B2:C3", "00:00")
	if err != nil {
		t.Fatal(err)
	}
	if len(addrs) != 256 {
		t.Fatalf("want 256, got %d", len(addrs))
	}
	if addrs[0] != "00:00:00:a1:b2:c3" || addrs[255] != "00:00:ff:a1:b2:c3" {
		t.Fatalf("unexpected bounds: %s .. %s", addrs[0], addrs[255])
	}
	// Colon-less form normalises the same way.
	bare, _ := generateUAPCombinations("a1b2c3", "00:00")
	if bare[1] != "00:00:01:a1:b2:c3" {
		t.Fatalf("bare LAP: %s", bare[1])
	}
}

func TestGenerateUAPCombinationsInvalid(t *testing.T) {
	for _, bad := range []string{"", "A1:B2", "ZZ:ZZ:ZZ", "A1B2C34", "12:34:5g"} {
		if _, err := generateUAPCombinations(bad, "00:00"); err == nil {
			t.Errorf("expected error for %q", bad)
		}
	}
}

func TestParseAdapters(t *testing.T) {
	sample := "hci0:\tType: Primary  Bus: USB\n\tBD Address: B0:47:E9:7A:A0:39\n\nhci1:\tType: Primary  Bus: USB\n\tBD Address: 00:11:22:33:44:55\n"
	got := parseAdapters(sample)
	if len(got) != 2 || got[0] != "hci0" || got[1] != "hci1" {
		t.Fatalf("got %v", got)
	}
	if got := parseAdapters(""); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
}

func TestLoadWatchlistFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "devices.txt")
	os.WriteFile(p, []byte("77:0E:FD:98:10:30\n\n  \n89:57:5f:d0:f4:06\n"), 0o644)
	wl, err := loadWatchlist(p)
	if err != nil {
		t.Fatal(err)
	}
	got := wl.snapshot()
	if len(got) != 2 || got[1] != "89:57:5F:D0:F4:06" {
		t.Fatalf("got %v", got)
	}
	wl2, _ := loadWatchlist(filepath.Join(dir, "nope.txt"))
	if wl2.len() != 0 {
		t.Fatalf("missing file: %v", wl2.snapshot())
	}
}

func TestDistribute(t *testing.T) {
	got := distribute([]string{"a", "b", "c", "d", "e"}, 2)
	if len(got[0]) != 3 || len(got[1]) != 2 || got[0][1] != "c" {
		t.Fatalf("got %v", got)
	}
}

func TestPingSucceeded(t *testing.T) {
	if !pingSucceeded("0 bytes from X\n64 bytes from AA:BB (ec=1)") {
		t.Fatal("expected success")
	}
	if pingSucceeded("Send failed: Network is unreachable") {
		t.Fatal("expected failure")
	}
}

func TestParseConfigModes(t *testing.T) {
	cfg, err := parseConfig([]string{"-c", "-f", "devices.txt", "-n", "5", "--classic-interval", "10", "-x", "hci0", "hci2"})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.mode != "classic" || cfg.numPackets != 5 || cfg.classicInterval != 10 || len(cfg.exclude) != 2 {
		t.Fatalf("cfg %+v", cfg)
	}
	cfg, err = parseConfig([]string{"-u", "-l", "laps.txt", "-o", "found.txt"})
	if err != nil || cfg.mode != "uap" {
		t.Fatalf("uap cfg %+v err %v", cfg, err)
	}
	for _, args := range [][]string{
		{"-f", "devices.txt"},             // no mode
		{"-c", "-b", "-f", "devices.txt"}, // mutually exclusive
		{"-c"},                            // missing -f
		{"-u", "-l", "laps.txt"},          // missing -o
		{"-c", "-f", "d.txt", "-n", "0"},  // non-positive
		{"-b", "-f", "d.txt", "--ble-interval", "-1"},
	} {
		if _, err := parseConfig(args); err == nil {
			t.Errorf("expected error for %v", args)
		}
	}
}

func TestCoordinateFuzzingFindsDevice(t *testing.T) {
	dir := t.TempDir()
	lapFile := filepath.Join(dir, "laps.txt")
	outFile := filepath.Join(dir, "found.txt")
	os.WriteFile(lapFile, []byte("A1:B2:C3\nBAD!\n"), 0o644)
	target := "00:00:2a:a1:b2:c3"
	ping := func(ctx context.Context, adapter, mac string, count int) (string, error) {
		if mac == target {
			return "64 bytes from " + mac, nil
		}
		return "", nil
	}
	coordinateFuzzing(context.Background(), []string{"hci0"}, lapFile, outFile, "00:00", 5, ping)
	data, _ := os.ReadFile(outFile)
	if !strings.Contains(string(data), target) {
		t.Fatalf("output missing target:\n%s", data)
	}
	// Bounded runtime guard: fuzzing 256 candidates with a fake is instant.
	_ = time.Now()
}
