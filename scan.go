package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"tinygo.org/x/bluetooth"
)

// pinger runs one l2ping attempt and returns its stdout. Tests
// substitute a fake without Bluetooth hardware or sudo.
type pinger func(ctx context.Context, adapter, mac string, count int) (string, error)

const pingTimeout = 30 * time.Second // per-attempt cap for Classic pings

// runPrivileged runs a BlueZ tool directly when root / capability
// permitted, otherwise via passwordless sudo. The whole process group
// is killed on timeout: killing sudo alone would orphan the tool,
// which keeps the output pipe open and blocks the caller.
func runPrivileged(ctx context.Context, name string, args ...string) (string, error) {
	var cmd *exec.Cmd
	if _, err := exec.LookPath("sudo"); err == nil && !privilegedDirectly() {
		cmd = exec.CommandContext(ctx, "sudo", append([]string{"-n", name}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, name, args...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.WaitDelay = 2 * time.Second
	out, err := cmd.Output()
	return string(out), err
}

// privilegedDirectly reports whether l2ping/hcitool can run without
// sudo: the process is root, or it holds CAP_NET_RAW (bit 13 of the
// effective capability set, granted e.g. by
// `setcap cap_net_raw+ep echoblue` or systemd AmbientCapabilities).
func privilegedDirectly() bool {
	if os.Geteuid() == 0 {
		return true
	}
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "CapEff:") {
			var caps uint64
			if _, err := fmt.Sscanf(strings.TrimSpace(line[len("CapEff:"):]), "%x", &caps); err == nil {
				return caps&(1<<13) != 0
			}
		}
	}
	return false
}

func defaultPinger(ctx context.Context, adapter, mac string, count int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	return runPrivileged(ctx, "l2ping", "-i", adapter, "-c", fmt.Sprint(count), mac)
}

func pingSucceeded(stdout string) bool { return strings.Contains(stdout, "bytes from") }

// pingDevice pings one Classic device and reports whether it replied.
func pingDevice(ctx context.Context, adapter, mac string, count int, ping pinger) bool {
	stdout, _ := ping(ctx, adapter, mac, count)
	if ctx.Err() != nil {
		return false
	}
	// l2ping exits non-zero / times out for absent devices; success is
	// decided purely on stdout.
	return pingSucceeded(stdout)
}

// classicScan pings this adapter's share of the watchlist each cycle.
// The share is recomputed per cycle, so watchlist reloads and device
// additions take effect without restarting. Alerts fire only on the
// absent -> present transition (see tracker).
func classicScan(ctx context.Context, adapter string, idx, numAdapters int, interval time.Duration, count int, wl *watchlist, tr *tracker, ping pinger) {
	for {
		if ctx.Err() != nil {
			return
		}
		wl.reloadIfChanged()
		devices := distribute(wl.snapshot(), numAdapters)[idx]
		logf("[%s] Starting scan cycle for %d devices", adapter, len(devices))
		for _, mac := range devices {
			if ctx.Err() != nil {
				return
			}
			if pingDevice(ctx, adapter, mac, count, ping) {
				if tr.observeSeen(mac, time.Now()) {
					logf("[%s] Classic device active: %s", adapter, mac)
					sendAlert(mac, "Classic Device", adapter)
				} else {
					logf("[%s] Classic device active: %s", adapter, mac)
				}
			} else if ctx.Err() == nil {
				logf("[%s] Classic device inactive: %s", adapter, mac)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond): // brief pause between pings
			}
		}
		logf("[%s] Scan cycle completed, waiting %s", adapter, interval)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// handleAdvertisement decides what one BLE advertisement causes. In
// discovery mode every address is reported once per cycle; otherwise
// only watchlist devices are tracked, alerting on arrival.
func handleAdvertisement(wl *watchlist, tr *tracker, discover bool, cycleSeen map[string]bool, adapter, addr, name string, rssi int16) {
	if discover {
		if !cycleSeen[addr] {
			cycleSeen[addr] = true
			logf("[%s] BLE %s name=%q RSSI %d dBm", adapter, addr, name, rssi)
		}
		return
	}
	if wl == nil || !wl.contains(addr) {
		return
	}
	if tr.observeSeen(addr, time.Now()) {
		logf("Device found: %s (%s) [%s]", name, addr, adapter)
		sendAlert(addr, name, adapter)
	}
}

// scanBLE scans one adapter with BlueZ (via tinygo.org/x/bluetooth,
// which talks to BlueZ over D-Bus). NewAdapter(id) provides one scanner
// per hci adapter, but the Linux backend is not safe with several
// scans running at once (shared D-Bus connection; a stop on one
// adapter also surfaces as a spurious "scan stopped" on the others),
// so scans are serialised: with several adapters each one scans for
// one interval in rotation, and with a single adapter the scan runs
// continuously. Presence/departure is handled by the tracker; in
// discovery mode an address is re-reported at most once per interval.
var bleScanMu sync.Mutex

func scanBLE(ctx context.Context, adapterName string, idx, numAdapters int, interval time.Duration, wl *watchlist, tr *tracker, discover bool) error {
	adapter := bluetooth.NewAdapter(adapterName)
	if err := adapter.Enable(); err != nil {
		return err
	}
	logf("[%s] BLE Scan Started", adapterName)
	lastReported := map[string]time.Time{}
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if wl != nil {
					wl.reloadIfChanged()
				}
			}
		}
	}()
	_ = idx
	for {
		if ctx.Err() != nil {
			return nil
		}
		bleScanMu.Lock()
		scanDone := make(chan error, 1)
		go func() {
			scanDone <- adapter.Scan(func(_ *bluetooth.Adapter, result bluetooth.ScanResult) {
				addr := normalizeMAC(result.Address.String())
				if discover {
					now := time.Now()
					if last, ok := lastReported[addr]; !ok || now.Sub(last) >= interval {
						lastReported[addr] = now
						logf("[%s] BLE %s name=%q RSSI %d dBm", adapterName, addr, result.LocalName(), result.RSSI)
					}
					return
				}
				handleAdvertisement(wl, tr, false, nil, adapterName, addr, result.LocalName(), result.RSSI)
			})
		}()
		if numAdapters <= 1 {
			select {
			case <-ctx.Done():
				_ = adapter.StopScan()
				<-scanDone
				bleScanMu.Unlock()
				return nil
			case err := <-scanDone:
				bleScanMu.Unlock()
				if ctx.Err() != nil {
					return nil
				}
				logf("[%s] BLE scan ended: %v (retrying in 3s)", adapterName, err)
			}
		} else {
			select {
			case <-ctx.Done():
				_ = adapter.StopScan()
				<-scanDone
				bleScanMu.Unlock()
				return nil
			case <-time.After(interval):
				_ = adapter.StopScan()
				<-scanDone
				bleScanMu.Unlock()
			case err := <-scanDone:
				bleScanMu.Unlock()
				if ctx.Err() != nil {
					return nil
				}
				logf("[%s] BLE scan ended: %v (retrying in 3s)", adapterName, err)
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(200 * time.Millisecond):
		}
	}
}

// discoverClassic repeatedly runs a Classic inquiry on one adapter and
// reports every discoverable BR/EDR device once per round. Only
// devices in discoverable mode answer inquiry; that is a Bluetooth
// limitation, not a filter.
func discoverClassic(ctx context.Context, adapter string, interval time.Duration) {
	for {
		if ctx.Err() != nil {
			return
		}
		logf("[%s] Classic inquiry started", adapter)
		inquiryCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		out, err := runPrivileged(inquiryCtx, "hcitool", "-i", adapter, "scan")
		cancel()
		if err != nil && ctx.Err() != nil {
			return
		}
		if err != nil {
			logf("[%s] Classic inquiry failed: %v", adapter, err)
		} else {
			found := parseInquiry(out)
			addrs := make([]string, 0, len(found))
			for a := range found {
				addrs = append(addrs, a)
			}
			sort.Strings(addrs)
			for _, a := range addrs {
				logf("[%s] Classic %s name=%q", adapter, a, found[a])
			}
			if len(addrs) == 0 {
				logf("[%s] Classic inquiry: no discoverable devices", adapter)
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}
