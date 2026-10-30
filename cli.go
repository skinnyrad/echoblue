package main

import (
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
)

type config struct {
	mode            string // "classic", "ble" or "uap"
	discover        bool   // with classic/ble: report everything, no watchlist
	file            string
	lapFile         string
	output          string
	nap             string
	classicInterval int
	bleInterval     int
	numPackets      int
	uapTimeout      int
	exclude         []string
	logFile         string
	alertHook       string
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

// expandExclude rewrites `-x a b c` into `-x=a,b,c` so the stdlib flag
// package can consume it.
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
  echoblue -u -l laps.txt -o found.txt [--uap-timeout 30] [--nap 00:00] [-x hci0 ...]
  echoblue -b -d [--ble-interval 30] [-x hci0 ...]     Discovery (BLE)
  echoblue -c -d [--classic-interval 30] [-x hci0 ...] Discovery (Classic inquiry)

Modes (exactly one, required):
  -c, --classic    Use Classic Bluetooth mode (l2ping)
  -b, --btle       Use BLE mode
  -u, --uap-fuzz   Enable UAP fuzzing mode

Common:
  -d, --discover   With -b/-c: report every device seen (no -f watchlist)
  -x, --exclude    Interface(s) to exclude (e.g. -x hci0 hci2)
      --log-file   Also append output to this file
      --alert-hook Shell command run on device arrival (env: ECHOBLUE_ADDRESS,
                   ECHOBLUE_NAME, ECHOBLUE_ADAPTER)
      --version    Print version and exit
`

// parseNAP validates a NAP such as "00:00" and returns it lower-cased.
func parseNAP(s string) (string, error) {
	parts := strings.Split(s, ":")
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid NAP %q: want two octets, e.g. 00:00", s)
	}
	for _, p := range parts {
		if len(p) != 2 {
			return "", fmt.Errorf("invalid NAP %q: want two octets, e.g. 00:00", s)
		}
		if _, err := strconv.ParseUint(p, 16, 8); err != nil {
			return "", fmt.Errorf("invalid NAP %q: %v", s, err)
		}
	}
	return strings.ToLower(s), nil
}

func parseConfig(args []string) (config, error) {
	var cfg config
	cfg.nap = "00:00"
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
	fs.BoolVar(&cfg.discover, "d", false, "")
	fs.BoolVar(&cfg.discover, "discover", false, "")
	var exclude stringList
	fs.Var(&exclude, "x", "")
	fs.Var(&exclude, "exclude", "")
	fs.StringVar(&cfg.logFile, "log-file", "", "")
	fs.StringVar(&cfg.alertHook, "alert-hook", "", "")
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
		fs.StringVar(&cfg.nap, "nap", "00:00", "")
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
	if cfg.discover && mode == "uap" {
		return cfg, fmt.Errorf("--discover only applies to Classic/BLE modes")
	}
	if mode == "uap" {
		if cfg.lapFile == "" {
			return cfg, fmt.Errorf("UAP fuzzing requires -l/--lap-file")
		}
		if cfg.output == "" {
			return cfg, fmt.Errorf("UAP fuzzing requires -o/--output")
		}
		if _, err := parseNAP(cfg.nap); err != nil {
			return cfg, err
		}
	} else if !cfg.discover && cfg.file == "" {
		return cfg, fmt.Errorf("-f/--file is required in Classic/BLE mode (or use -d/--discover)")
	}
	return cfg, nil
}
