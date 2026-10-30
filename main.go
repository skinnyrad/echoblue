// Command echoblue is a Bluetooth device scanner for defensive monitoring
// of secure spaces. It watches for known devices (by MAC address) over BLE
// or Classic Bluetooth and alerts when one comes into range. It also
// provides a UAP-fuzzing mode that recovers the full BD_ADDR of a Classic
// device from a captured LAP. Linux only (BlueZ, hciconfig, l2ping).
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// tracker records devices seen this cycle and for the whole session.
// A cycle entry is cleared each scan interval; the session set is not.
type tracker struct {
	mu      sync.Mutex
	cycle   map[string]struct{}
	session map[string]struct{}
}

func newTracker() *tracker {
	return &tracker{cycle: map[string]struct{}{}, session: map[string]struct{}{}}
}

// mark records addr. It reports false if addr was already seen this cycle.
func (t *tracker) mark(addr string) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.cycle[addr]; ok {
		return false
	}
	t.cycle[addr] = struct{}{}
	t.session[addr] = struct{}{}
	return true
}

func (t *tracker) resetCycle() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.cycle = map[string]struct{}{}
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

func normalizeMAC(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// sendAlert is the customisation point for alert delivery.
func sendAlert(address, name string) {
	fmt.Printf("Alert: Device %s (%s) is in range!\n", name, address)
}

// loadKnownDevices reads one MAC per line; blank lines are ignored.
// A missing file yields an empty list.
func loadKnownDevices(path string) []string {
	f, err := os.Open(path)
	if err != nil {
		fmt.Printf("Error: File '%s' does not exist.\n", path)
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" {
			out = append(out, normalizeMAC(line))
		}
	}
	return out
}

func loadLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(string(data), "\n") {
		if s := strings.TrimSpace(line); s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

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
			fmt.Printf("Valid Bluetooth adapters found: %v\n", adapters)
			return adapters
		}
	}
	entries, err := os.ReadDir("/sys/class/bluetooth")
	if err != nil {
		fmt.Println("Error: Failed to execute hciconfig.")
		return nil
	}
	var adapters []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "hci") {
			adapters = append(adapters, e.Name())
		}
	}
	sort.Strings(adapters)
	fmt.Printf("Valid Bluetooth adapters found: %v\n", adapters)
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
			fmt.Printf("Warning: Interface %s specified to ignore but not found.\n", ex)
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

type config struct {
	mode            string // "classic", "ble" or "uap"
	file            string
	lapFile         string
	output          string
	classicInterval int
	bleInterval     int
	numPackets      int
	uapTimeout      int
	exclude         []string
}

type positiveInt int

func (p *positiveInt) String() string {
	if p == nil {
		return "0"
	}
	return strconv.Itoa(int(*p))
}

func (p *positiveInt) Set(s string) error {
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return fmt.Errorf("%s is not a positive integer greater than zero", s)
	}
	*p = positiveInt(v)
	return nil
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if part != "" {
			*s = append(*s, part)
		}
	}
	return nil
}

// expandExclude rewrites `-x a b c` (argparse nargs='*') into `-x=a,b,c`
// so the stdlib flag package can consume it.
func expandExclude(args []string) []string {
	var out []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "-x" || a == "--exclude" {
			var vals []string
			for i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				vals = append(vals, args[i])
			}
			out = append(out, "-x="+strings.Join(vals, ","))
			continue
		}
		out = append(out, a)
	}
	return out
}

func detectMode(args []string) (string, error) {
	var modes []string
	for _, a := range args {
		switch a {
		case "-c", "--classic":
			modes = append(modes, "classic")
		case "-b", "--btle":
			modes = append(modes, "ble")
		case "-u", "--uap-fuzz":
			modes = append(modes, "uap")
		default:
			// Bundled short flags, e.g. -cb, as argparse would parse them.
			if len(a) > 2 && a[0] == '-' && a[1] != '-' {
				for _, ch := range a[1:] {
					switch ch {
					case 'c':
						modes = append(modes, "classic")
					case 'b':
						modes = append(modes, "ble")
					case 'u':
						modes = append(modes, "uap")
					}
				}
			}
		}
	}
	if len(modes) == 0 {
		return "", fmt.Errorf("one mode is required: -c/--classic, -b/--btle or -u/--uap-fuzz")
	}
	if len(modes) > 1 {
		return "", fmt.Errorf("modes -c, -b and -u are mutually exclusive")
	}
	return modes[0], nil
}

const usageText = `Bluetooth device scanner

Usage:
  echoblue -c -f devices.txt [--classic-interval 30] [-n 3] [-x hci0 ...]
  echoblue -b -f devices.txt [--ble-interval 30] [-x hci0 ...]
  echoblue -u -l laps.txt -o found.txt [--uap-timeout 30] [-x hci0 ...]

Modes (exactly one, required):
  -c, --classic    Use Classic Bluetooth mode (l2ping)
  -b, --btle       Use BLE mode
  -u, --uap-fuzz   Enable UAP fuzzing mode
`

func parseConfig(args []string) (config, error) {
	var cfg config
	mode, err := detectMode(args)
	if err != nil {
		return cfg, err
	}
	cfg.mode = mode
	fs := flag.NewFlagSet("echoblue", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var classic, btle, uap bool
	fs.BoolVar(&classic, "c", false, "")
	fs.BoolVar(&classic, "classic", false, "")
	fs.BoolVar(&btle, "b", false, "")
	fs.BoolVar(&btle, "btle", false, "")
	fs.BoolVar(&uap, "u", false, "")
	fs.BoolVar(&uap, "uap-fuzz", false, "")
	var exclude stringList
	fs.Var(&exclude, "x", "Interface(s) to exclude")
	fs.Var(&exclude, "exclude", "Interface(s) to exclude")
	numPackets := positiveInt(3)
	classicInterval := positiveInt(30)
	bleInterval := positiveInt(30)
	uapTimeout := positiveInt(30)
	switch mode {
	case "uap":
		fs.StringVar(&cfg.lapFile, "l", "", "")
		fs.StringVar(&cfg.lapFile, "lap-file", "", "")
		fs.StringVar(&cfg.output, "o", "", "")
		fs.StringVar(&cfg.output, "output", "", "")
		fs.Var(&uapTimeout, "uap-timeout", "")
	case "classic":
		fs.StringVar(&cfg.file, "f", "", "")
		fs.StringVar(&cfg.file, "file", "", "")
		fs.Var(&numPackets, "n", "")
		fs.Var(&numPackets, "num-packets", "")
		fs.Var(&classicInterval, "classic-interval", "")
	case "ble":
		fs.StringVar(&cfg.file, "f", "", "")
		fs.StringVar(&cfg.file, "file", "", "")
		fs.Var(&bleInterval, "ble-interval", "")
	}
	if err := fs.Parse(expandExclude(args)); err != nil {
		return cfg, err
	}
	cfg.exclude = exclude
	cfg.numPackets = int(numPackets)
	cfg.classicInterval = int(classicInterval)
	cfg.bleInterval = int(bleInterval)
	cfg.uapTimeout = int(uapTimeout)
	if mode == "uap" {
		if cfg.lapFile == "" {
			return cfg, fmt.Errorf("UAP fuzzing requires -l/--lap-file")
		}
		if cfg.output == "" {
			return cfg, fmt.Errorf("UAP fuzzing requires -o/--output")
		}
	} else if cfg.file == "" {
		return cfg, fmt.Errorf("-f/--file is required in Classic/BLE mode")
	}
	return cfg, nil
}

func run(ctx context.Context, cfg config) error {
	tr := newTracker()
	var known []string
	knownSet := map[string]struct{}{}
	if cfg.mode != "uap" {
		known = loadKnownDevices(cfg.file)
		if len(known) == 0 {
			fmt.Println("No known devices loaded. Exiting.")
			return nil
		}
		for _, m := range known {
			knownSet[m] = struct{}{}
		}
	}

	adapters := filterAdapters(getBluetoothAdapters(), cfg.exclude)
	if len(adapters) == 0 {
		fmt.Println("No Bluetooth adapters available after filtering. Exiting.")
		return nil
	}

	var wg sync.WaitGroup
	switch cfg.mode {
	case "classic":
		buckets := distribute(known, len(adapters))
		for i, adapter := range adapters {
			if len(buckets[i]) == 0 {
				continue
			}
			fmt.Printf("[%s] Monitoring %d devices: %s\n", adapter, len(buckets[i]), strings.Join(buckets[i], ", "))
			wg.Add(1)
			go func(adapter string, devices []string) {
				defer wg.Done()
				classicScan(ctx, adapter, devices, time.Duration(cfg.classicInterval)*time.Second, cfg.numPackets, tr, defaultPinger)
			}(adapter, buckets[i])
		}
	case "ble":
		for _, adapter := range adapters {
			wg.Add(1)
			go func(adapter string) {
				defer wg.Done()
				if err := scanBLE(ctx, adapter, time.Duration(cfg.bleInterval)*time.Second, knownSet, tr); err != nil && ctx.Err() == nil {
					fmt.Printf("[%s] BLE scan error: %v\n", adapter, err)
				}
			}(adapter)
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
			coordinateFuzzing(ctx, adapters, cfg.lapFile, cfg.output, cfg.uapTimeout, defaultPinger)
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
	fmt.Println("Shutting down gracefully...")
	fmt.Println()
	if cfg.mode == "uap" {
		found, _ := loadLines(cfg.output)
		if len(found) > 0 {
			fmt.Printf("Total valid addresses found (including previous runs): %d\n", len(found))
		} else {
			fmt.Println("No valid addresses discovered")
		}
	} else if seen := tr.seen(); len(seen) > 0 {
		fmt.Println("Devices seen from target file:")
		for _, d := range seen {
			fmt.Println(d)
		}
	} else {
		fmt.Println("No devices from target file were seen.")
	}
	return nil
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usageText)
		os.Exit(2)
	}
	for _, a := range os.Args[1:] {
		if a == "-h" || a == "--help" {
			fmt.Print(usageText)
			return
		}
	}
	cfg, err := parseConfig(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "echoblue: %v\n%s", err, usageText)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, cfg); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
