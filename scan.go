package main

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"tinygo.org/x/bluetooth"
)

// pinger runs one l2ping attempt and returns its stdout. It is a variable
// so tests can substitute a fake without Bluetooth hardware or sudo.
type pinger func(ctx context.Context, adapter, mac string, count int) (string, error)

const pingTimeout = 30 * time.Second // as in the Python ping_device

func defaultPinger(ctx context.Context, adapter, mac string, count int) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	args := []string{"l2ping", "-i", adapter, "-c", fmt.Sprint(count), mac}
	var cmd *exec.Cmd
	if _, err := exec.LookPath("sudo"); err == nil {
		cmd = exec.CommandContext(ctx, "sudo", append([]string{"-n"}, args...)...)
	} else {
		cmd = exec.CommandContext(ctx, args[0], args[1:]...)
	}
	// Kill the whole process group on timeout/cancel: killing sudo alone
	// would orphan l2ping, which keeps the output pipe open and blocks
	// the caller until it gives up on its own.
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

func pingSucceeded(stdout string) bool { return strings.Contains(stdout, "bytes from") }

// pingDevice pings one Classic device; on a reply it alerts and records it.
func pingDevice(ctx context.Context, adapter, mac string, count int, tr *tracker, ping pinger) {
	stdout, err := ping(ctx, adapter, mac, count)
	if err != nil && ctx.Err() != nil {
		return // shutting down
	}
	if err != nil && stdout == "" {
		// l2ping exits non-zero / times out for absent devices; the Python
		// version decides purely on stdout, so fall through to the check.
		_ = err
	}
	if pingSucceeded(stdout) {
		fmt.Printf("[%s] Classic device active: %s\n", adapter, mac)
		sendAlert(mac, "Classic Device")
		tr.mark(mac)
	} else {
		fmt.Printf("[%s] Classic device inactive: %s\n", adapter, mac)
	}
}

// classicScan pings each assigned device in turn, then waits interval.
func classicScan(ctx context.Context, adapter string, devices []string, interval time.Duration, count int, tr *tracker, ping pinger) {
	for {
		fmt.Printf("[%s] Starting scan cycle for %d devices\n", adapter, len(devices))
		for _, mac := range devices {
			if ctx.Err() != nil {
				return
			}
			pingDevice(ctx, adapter, mac, count, tr, ping)
			select {
			case <-ctx.Done():
				return
			case <-time.After(10 * time.Millisecond): // Python: 0.01s between pings
			}
		}
		fmt.Printf("[%s] Scan cycle completed, waiting %s\n", adapter, interval)
		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// scanBLE scans one adapter with BlueZ (via tinygo.org/x/bluetooth, which
// talks to BlueZ over D-Bus). The scan restarts every interval so the
// per-cycle dedup set resets, exactly as the Python Bleak version does.
// NewAdapter(id) is what preserves the per-hci behaviour: the Python code
// starts one BleakScanner per adapter, so we start one Go adapter per hci.
func scanBLE(ctx context.Context, adapterName string, interval time.Duration, known map[string]struct{}, tr *tracker) error {
	adapter := bluetooth.NewAdapter(adapterName)
	if err := adapter.Enable(); err != nil {
		return err
	}
	fmt.Printf("[%s] BLE Scan Started\n", adapterName)
	for {
		if ctx.Err() != nil {
			return nil
		}
		scanDone := make(chan error, 1)
		go func() {
			scanDone <- adapter.Scan(func(_ *bluetooth.Adapter, result bluetooth.ScanResult) {
				addr := normalizeMAC(result.Address.String())
				if _, ok := known[addr]; !ok {
					return
				}
				if tr.mark(addr) {
					name := result.LocalName()
					fmt.Printf("Device found: %s (%s)\n", name, addr)
					sendAlert(addr, name)
				}
			})
		}()
		select {
		case <-ctx.Done():
			_ = adapter.StopScan()
			<-scanDone
			return nil
		case <-time.After(interval):
			_ = adapter.StopScan()
			if err := <-scanDone; err != nil && ctx.Err() == nil {
				fmt.Printf("[%s] BLE scan cycle ended: %v\n", adapterName, err)
			}
			tr.resetCycle()
		case err := <-scanDone:
			if err != nil && ctx.Err() == nil {
				fmt.Printf("[%s] BLE scan ended early: %v\n", adapterName, err)
			}
			tr.resetCycle()
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(time.Second):
			}
		}
	}
}
