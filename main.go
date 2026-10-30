// Command echoblue is a Bluetooth device scanner.
// It watches for known devices (by MAC address) over BLE
// or Classic Bluetooth (via l2ping) and alerts when one comes into
// range. It also provides a UAP-fuzzing mode that recovers the full
// BD_ADDR of a Classic device from a captured LAP, and a discovery
// mode that reports everything nearby. Linux only (BlueZ, l2ping).
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// version is set at release time with -ldflags "-X main.version=vX.Y.Z".
var version = "dev"

var alertHook string

// sendAlert reports a device arrival: timestamped log line plus the
// optional --alert-hook command. This is the customisation point for
// alert delivery.
func sendAlert(address, name, adapter string) {
	logf("Alert: Device %s (%s) is in range! [%s]", name, address, adapter)
	if alertHook == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "sh", "-c", alertHook)
		cmd.Env = append(os.Environ(),
			"ECHOBLUE_ADDRESS="+address,
			"ECHOBLUE_NAME="+name,
			"ECHOBLUE_ADAPTER="+adapter,
		)
		if out, err := cmd.CombinedOutput(); err != nil {
			logf("Warning: alert hook failed for %s: %v: %s", address, err, strings.TrimSpace(string(out)))
		}
	}()
}

// sweepLoop turns devices that have been unseen past their departure
// window into "departed" log lines.
func sweepLoop(ctx context.Context, tr *tracker, departureAfter time.Duration) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			for _, addr := range tr.sweep(now, departureAfter) {
				logf("Device departed: %s", addr)
			}
		}
	}
}

func run(ctx context.Context, cfg config) error {
	if err := setupLogFile(cfg.logFile); err != nil {
		return fmt.Errorf("log file: %w", err)
	}
	alertHook = cfg.alertHook
	tr := newTracker()

	var wl *watchlist
	if cfg.mode != "uap" && !cfg.discover {
		var err error
		wl, err = loadWatchlist(cfg.file)
		if err != nil {
			return err
		}
		if wl.len() == 0 {
			return fmt.Errorf("no known devices loaded from %s. Exiting", cfg.file)
		}
	}

	adapters := filterAdapters(getBluetoothAdapters(), cfg.exclude)
	if len(adapters) == 0 {
		return fmt.Errorf("no Bluetooth adapters available after filtering. Exiting")
	}

	var wg sync.WaitGroup
	switch cfg.mode {
	case "classic":
		if cfg.discover {
			for _, adapter := range adapters {
				wg.Add(1)
				go func(adapter string) {
					defer wg.Done()
					discoverClassic(ctx, adapter, time.Duration(cfg.classicInterval)*time.Second)
				}(adapter)
			}
			break
		}
		wg.Add(1)
		go sweepLoop(ctx, tr, 2*time.Duration(cfg.classicInterval)*time.Second+pingTimeout)
		for i, adapter := range adapters {
			wg.Add(1)
			go func(adapter string, idx int) {
				defer wg.Done()
				classicScan(ctx, adapter, idx, len(adapters), time.Duration(cfg.classicInterval)*time.Second, cfg.numPackets, wl, tr, defaultPinger)
			}(adapter, i)
		}
	case "ble":
		if !cfg.discover {
			wg.Add(1)
			go sweepLoop(ctx, tr, time.Duration(len(adapters))*2*time.Duration(cfg.bleInterval)*time.Second+5*time.Second)
		}
		for i, adapter := range adapters {
			wg.Add(1)
			go func(adapter string, idx int) {
				defer wg.Done()
				if err := scanBLE(ctx, adapter, idx, len(adapters), time.Duration(cfg.bleInterval)*time.Second, wl, tr, cfg.discover); err != nil && ctx.Err() == nil {
					logf("[%s] BLE scan error: %v (is the adapter powered on? try: bluetoothctl power on)", adapter, err)
				}
			}(adapter, i)
		}
	case "uap":
		if _, err := os.Stat(cfg.output); os.IsNotExist(err) {
			if f, err := os.Create(cfg.output); err == nil {
				f.Close()
			}
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			coordinateFuzzing(ctx, adapters, cfg.lapFile, cfg.output, cfg.nap, cfg.uapTimeout, defaultPinger)
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
		<-done
	}

	// Session summary on shutdown.
	logf("Shutting down gracefully...")
	if cfg.mode == "uap" {
		lines, _ := readLines(cfg.output)
		found := 0
		for _, l := range lines {
			if strings.TrimSpace(l) != "" {
				found++
			}
		}
		if found > 0 {
			logf("Total valid addresses found (including previous runs): %d", found)
		} else {
			logf("No valid addresses discovered")
		}
	} else if cfg.discover {
		logf("Discovery finished.")
	} else if seen := tr.seen(); len(seen) > 0 {
		logf("Devices seen from target file:")
		for _, d := range seen {
			fmt.Println(d)
		}
	} else {
		logf("No devices from target file were seen.")
	}
	return nil
}

func main() {
	args := os.Args[1:]
	for _, a := range args {
		if a == "--version" || a == "-version" {
			fmt.Printf("echoblue %s\n", version)
			return
		}
		if a == "-h" || a == "--help" {
			fmt.Print(usageText)
			return
		}
	}
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	cfg, err := parseConfig(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "echoblue: %v\n%s", err, usageText)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	// SIGHUP also reloads watchlists: the scan loops poll the file, but
	// handle the signal so it does not terminate the process.
	hup := make(chan os.Signal, 1)
	signal.Notify(hup, syscall.SIGHUP)
	go func() {
		for range hup {
			logf("SIGHUP received (watchlist reload happens automatically on change)")
		}
	}()
	if err := run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, "echoblue:", err)
		os.Exit(1)
	}
}
