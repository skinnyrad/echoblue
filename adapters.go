package main

import (
	"os"
	"os/exec"
	"sort"
	"strings"
)

// parseAdapters extracts adapter names from hciconfig output.
// Rule: a non-indented line containing a tab, name before ':'.
func parseAdapters(hciconfigOutput string) []string {
	var out []string
	for _, line := range strings.Split(hciconfigOutput, "\n") {
		if line == "" || strings.HasPrefix(line, "\t") || !strings.Contains(line, "\t") {
			continue
		}
		if i := strings.Index(line, ":"); i > 0 {
			out = append(out, line[:i])
		}
	}
	return out
}

// getBluetoothAdapters lists adapters via hciconfig, falling back to
// /sys/class/bluetooth when hciconfig is missing or fails (it is
// deprecated and absent on some modern BlueZ installs).
func getBluetoothAdapters() []string {
	out, err := exec.Command("hciconfig").Output()
	if err == nil {
		if adapters := parseAdapters(string(out)); len(adapters) > 0 {
			logf("Valid Bluetooth adapters found: %v", adapters)
			return adapters
		}
	}
	entries, err := os.ReadDir("/sys/class/bluetooth")
	if err != nil {
		logf("Error: Failed to execute hciconfig.")
		return nil
	}
	var adapters []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "hci") {
			adapters = append(adapters, e.Name())
		}
	}
	sort.Strings(adapters)
	logf("Valid Bluetooth adapters found: %v", adapters)
	return adapters
}

func filterAdapters(adapters, exclude []string) []string {
	for _, ex := range exclude {
		found := false
		for _, a := range adapters {
			if a == ex {
				found = true
			}
		}
		if !found {
			logf("Warning: Interface %s specified to ignore but not found.", ex)
		}
	}
	var out []string
	for _, a := range adapters {
		skip := false
		for _, ex := range exclude {
			if a == ex {
				skip = true
			}
		}
		if !skip {
			out = append(out, a)
		}
	}
	return out
}

// distribute splits devices round-robin across adapters.
func distribute(devices []string, n int) [][]string {
	buckets := make([][]string, n)
	for i, mac := range devices {
		buckets[i%n] = append(buckets[i%n], mac)
	}
	return buckets
}

// parseInquiry parses `hcitool scan` output into address -> name pairs.
// The first line ("Scanning ...") and malformed lines are skipped.
func parseInquiry(output string) map[string]string {
	found := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || !validMAC(fields[0]) {
			continue
		}
		name := ""
		if len(fields) > 1 {
			name = strings.Join(fields[1:], " ")
		}
		found[normalizeMAC(fields[0])] = name
	}
	return found
}
