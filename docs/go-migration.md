# Go migration assessment

Verdict: **feasible, and the Go port is implemented.** Every feature of
the original Python implementation maps to Go on Linux with no feature
loss. The Python implementation has since been removed; the Go binary is
the only implementation. (Do not run multiple instances at once — the
README warns this degrades scanning.)

## Feature mapping

| Python feature | Go equivalent | Status |
|---|---|---|
| BLE scan (Bleak, one scanner per `hci` adapter) | `tinygo.org/x/bluetooth` `NewAdapter("hciN")` per adapter, BlueZ over D-Bus | ✅ Verified live (see below). `NewAdapter(id)` is the key API — `DefaultAdapter` alone would have lost multi-adapter BLE |
| Classic scan (`sudo l2ping` subprocess) | Same `l2ping` subprocess via `exec.CommandContext` (`sudo -n`) | ✅ Verified live. No mature native Go BR/EDR L2CAP ping library exists; keeping the subprocess is the correct call, as Python did |
| Adapter detection (`hciconfig` parse) | Same `hciconfig` parse, plus `/sys/class/bluetooth` fallback | ✅ `hciconfig` is deprecated/absent on some modern BlueZ installs, hence the fallback |
| UAP fuzzing (256 candidates/LAP, batches of 4/adapter, ordered logging) | Goroutines + batch barrier, results sorted by attempt sequence | ✅ Unit-tested with a fake pinger; live smoke run confirmed batching/order |
| Device file, per-cycle dedup + session summary, graceful Ctrl-C | Mutex-guarded tracker, `signal.NotifyContext` | ✅ |
| CLI (mutually exclusive `-c/-b/-u`, mode-dependent required flags, positive ints, `-x` nargs) | Two-stage parse mirroring argparse: mode detection, then per-mode `flag.FlagSet`; `-x a b` is rewritten to `-x=a,b` | ✅ Unit-tested incl. error cases |

## Why tinygo.org/x/bluetooth

- Pure Go on Linux (BlueZ D-Bus via `godbus/dbus`), so `CGO_ENABLED=0`
  cross-compiles static binaries for `amd64`, `arm64`, `arm/v7`, `arm/v6`
  — verified locally for all four targets (~3.1–3.2 MB stripped).
- Actively maintained, cross-platform API if macOS/Windows is ever wanted.
- Alternatives considered: `muka/go-bluetooth` (heavier dependency tree,
  also D-Bus), raw D-Bus hand-rolled (more code to maintain for the same
  result).

## Known differences / risks

1. **Linux only.** Python nominally claimed macOS, but Classic mode
   (`hciconfig`/`l2ping`) never worked there. Releases ship Linux only,
   as requested.
2. **`sudo -n`**: Go uses non-interactive sudo so a password prompt can
   never hang a scan loop. Run with passwordless sudo or as root for
   Classic/UAP modes (same practical requirement as Python).
3. **UAP NAP is fixed to `00:00`** — preserved from Python. Fuzzing only
   finds devices whose NAP really is `00:00`; this is a pre-existing
   limitation, not a migration regression.
4. **BLE needs D-Bus/BlueZ running** and the adapter powered on
   (`bluetoothctl power on`). TinyGo does not power on the adapter.
5. Cross-compiled Pi binaries are compile-verified only; run the
   hardware checklist below on an actual Pi before deploying there.

## Hardware validation (this machine, 2026-10-29)

Host: `hci0` Intel BE200, `B0:47:E9:7A:A0:39`, BlueZ powered on.
Traffic source: ESP32 on `/dev/ttyUSB0` running
[esp32_traffic.ino](https://github.com/skinnyrad/Classidyne/blob/main/dataset_tools/esp32_traffic/esp32_traffic.ino)
(serial `b` = BLE only; note opening the serial port resets the ESP32
into idle, so the port must be held open while scanning).

- Discovery probe found `CLASSIDYNE-BLE` at `04:B2:47:05:4B:82`, RSSI −56.
- **BLE mode (Go)**: `./echoblue -b -f esp_devices.txt --ble-interval 5`
  → `Device found: CLASSIDYNE-BLE (04:B2:47:05:4B:82)` + alert once per
  5 s cycle, session summary listed the device once. ✅
- **Classic mode (Go)** against a real BR/EDR device found in range
  (`C8:84:47:5C:AA:0C`, “bytes from” via `l2ping`): active + alert each
  cycle; the BLE-only ESP32 correctly reported inactive. ✅
- **UAP mode (Go)** live smoke (LAP `5C:AA:0C`): batches of 4, ordered
  `(N of 256 …)` progress, graceful interrupt, empty `found.txt`. ✅
  (No hit expected: the reference device's NAP is not `00:00`.)
- ESP32 returned to idle afterwards: `mode wifi=0 ble=0`.

## Second-adapter + synthetic 00:00-NAP target validation

A Sena UD100 (CSR, `0a12:0001`) was attached as `hci1`
(`00:01:95:C7:18:C5`) alongside the onboard Intel `hci0`:

- BLE scan via the UD100 alone (`-x hci0`) found the ESP32 advertiser;
  both adapters scanning simultaneously also worked.
- Classic scan via the UD100 alone (`-x hci0`) alerted on the in-range
  BR/EDR device every cycle.

Real `00:00`-NAP devices are rare (the NAP is the top two octets of the
burned-in BD_ADDR), so the target was synthesised: the ESP32 was flashed
with a Classic (BluetoothSerial) sketch that calls
`esp_base_mac_addr_set()` before BT init, giving it BD_ADDR
`00:00:2A:11:22:35` (NAP `00:00`, UAP `0x2A`, LAP `11:22:35`). It was
found by Classic inquiry from both adapters. Results:

- **Classic (Go, via UD100)** against `00:00:2A:11:22:35`: active +
  alert. One cycle in several reported inactive — the ESP32 target
  occasionally misses a page; the scanner is behaving correctly.
- **UAP (Go)**: fuzzing LAP `11:22:35` found `00:00:2a:11:22:35` and
  wrote it to the output file. Absent candidates cost one BlueZ page
  timeout (~5 s) each and page attempts serialise per adapter, so a
  full 256-candidate sweep takes ~10 min on two adapters / ~20 min on
  one. This matches the Python design's cost; `--uap-timeout` does not
  shorten the hardware page timeout.
- The ESP32 was reflashed with its original traffic sketch afterwards
  (it needs `PartitionScheme=huge_app`).

To reproduce the `00:00`-NAP test you need: (1) a BR/EDR target whose
BD_ADDR starts `00:00` — either a device that happens to have one, a
CSR dongle with a rewritten BD_ADDR, or a dev board (ESP32) with a
software-set base MAC running a Classic BT profile; (2) the target
powered and in range of a scanning adapter it is *not* plugged into;
(3) passwordless sudo/root for `l2ping`; (4) only the target's LAP in
the LAP file, with the UAP deliberately withheld; (5) ~10–20 min for
the sweep, or check the output file incrementally — hits are appended
as soon as they answer.

## Releases

Tag `vX.Y.Z` and push: the Release workflow tests, cross-compiles, and
publishes `echoblue-<tag>-linux-{amd64,arm64,armv7,armv6}` plus
`SHA256SUMS` to the GitHub Releases page. `linux-arm64` = 64-bit Pi OS,
`linux-armv7` = 32-bit Pi OS (Pi 2/3/4/5), `linux-armv6` = Pi Zero/1.
