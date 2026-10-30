# Echo Blue

Echo Blue is a Bluetooth device scanner that monitors for specific devices and sends alerts when they come into range. It distributes scanning workloads across available Bluetooth adapters, supporting both Classic Bluetooth (using `l2ping`) and BLE scanning (BlueZ over D-Bus). It is written in Go and ships as a single static binary for Linux and Raspberry Pi.

See [docs/go-migration.md](docs/go-migration.md) for the migration assessment and hardware validation results.

## Features

- **Monitors specific Bluetooth devices** by reading MAC addresses from a configuration file
- **Supports both BLE and Classic Bluetooth modes**: BLE scanning via BlueZ, or Classic scanning via `l2ping`
- **Workload distribution for Classic mode**: evenly distributes device pings across available Bluetooth adapters
- **Configurable scanning intervals**: separate intervals for BLE (`--ble-interval`) and Classic (`--classic-interval`) modes
- **Adapter detection and exclusion**: detects adapters using `hciconfig` (falling back to `/sys/class/bluetooth`) and allows excluding selected interfaces
- **Persistent device tracking**: maintains a session record of all discovered devices while resetting per-cycle alerts
- **Graceful shutdown**: handles interruptions and outputs a summary of all detected devices

## Prerequisites

- Linux with BlueZ running and the adapter powered on (`bluetoothctl power on`)
- Bluetooth adapter(s) supporting Classic Bluetooth or BLE
- `hciconfig` and `l2ping` (BlueZ tools) for adapter detection and Classic/UAP modes
- Sudo privileges (passwordless, or run as root) for Classic mode (running `l2ping`)
- To build from source: Go 1.23 or newer. The released binary has no runtime dependencies.

## Installation

Download a binary from the [Releases page](https://github.com/skinnyrad/echoblue/releases):

| Asset | Platform |
|---|---|
| `echoblue-<tag>-linux-amd64` | Linux PC/server (x86-64) |
| `echoblue-<tag>-linux-arm64` | Raspberry Pi, 64-bit Pi OS |
| `echoblue-<tag>-linux-armv7` | Raspberry Pi, 32-bit Pi OS (Pi 2/3/4/5) |
| `echoblue-<tag>-linux-armv6` | Raspberry Pi Zero / Pi 1 |

```bash
chmod +x echoblue-<tag>-linux-arm64
```

Or build from source:

```bash
go build -o echoblue .
# Raspberry Pi cross-compile examples:
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -o echoblue .
GOOS=linux GOARCH=arm GOARM=7 CGO_ENABLED=0 go build -o echoblue .
```

Prepare a configuration file with the MAC addresses of devices to monitor (one per line):

```text
XX:XX:XX:XX:XX:XX
YY:YY:YY:YY:YY:YY
```

## Usage

```bash
./echoblue -f devices.txt -c --classic-interval 10 --num-packets 3
./echoblue -f devices.txt -b --ble-interval 10
./echoblue -u -l laps.txt -o found.txt
./echoblue --version
```

Discovery mode (no watchlist file) reports everything nearby:

```bash
./echoblue -b -d                     # BLE: every advertiser, once per cycle
./echoblue -c -d                     # Classic: inquiry, discoverable devices only
```

Sample watchlist/LAP files are in [examples/](examples/). The watchlist is
reloaded automatically when the file changes, so devices can be added
without restarting. Alerts fire once when a device arrives; a departure
is logged after the device has been unseen for about two scan intervals.

Common options:

- `--log-file <path>`: also append timestamped output to a file
- `--alert-hook '<command>'`: run a shell command on arrival, with
  `ECHOBLUE_ADDRESS`, `ECHOBLUE_NAME` and `ECHOBLUE_ADAPTER` in the
  environment, e.g. `--alert-hook 'echo $ECHOBLUE_ADDRESS >> /var/log/arrivals.log'`

Exactly one mode is required:

- `-c, --classic`: use Classic Bluetooth mode (`l2ping`)
- `-b, --btle`: use BLE mode
- `-u, --uap-fuzz`: enable UAP fuzzing mode

Classic/BLE arguments:

- `-f, --file`: path to the configuration file containing device MAC addresses (required)
- `--classic-interval`: scan interval in seconds for Classic mode (default: 30)
- `--ble-interval`: scan interval in seconds for BLE mode (default: 30)
- `-n, --num-packets`: number of `l2ping` packets to send in Classic mode (default: 3)
- `-x, --exclude`: Bluetooth adapter interfaces to ignore (e.g., `-x hci0 hci2`)

## Configuration

Device list file format:

- Each line should contain a single MAC address in the format `XX:XX:XX:XX:XX:XX`
- Lines that are empty or consist of only whitespace are automatically ignored

Example file content:

```text
77:0E:FD:98:10:30
89:57:5F:D0:F4:06
9B:F8:76:6A:1E:FC
B1:69:D1:E4:06:FB
```

## How It Works

**Device Loading:** Reads known device MAC addresses from the specified configuration file.

**Adapter Detection:** Uses the `hciconfig` utility to list available Bluetooth adapters (with a `/sys/class/bluetooth` fallback); adapters can be excluded using the `-x/--exclude` flag.

**Scanner Initialization:**

- **BLE Mode:** BlueZ scanning over D-Bus, one scanner per adapter. The scanner is restarted in each cycle — clearing temporary state — to ensure devices are re-checked.
- **Classic Mode:** Known devices are distributed evenly across adapters and pinged using `l2ping`. Sudo privileges are required, and response verification triggers alerts.

**Alert & Logging:** When a known device is detected, an alert is printed and an overall record of all discovered devices is maintained throughout the session. To change how alerts are delivered, modify the `sendAlert` function in `main.go`.

**Graceful Shutdown:** Handles keyboard interrupts by cancelling scanning tasks and then outputs a summary of devices seen during the run.

## UAP Fuzzing

UAP fuzzing is a method used to determine the missing Upper Address Part (UAP) of a Bluetooth device's address. A full device address (BD_ADDR) consists of the NAP (Non-significant Address Part, 2 octets), the UAP (Upper Address Part, 1 octet), and the LAP (Lower Address Part, 3 octets). When capturing packets from Bluetooth Classic piconets, it is common to only obtain the LAP. To fully target a device, the UAP must be determined — this is achieved by "fuzzing" (brute forcing) through all 256 possible UAP values.

### How It Works

- The tool reads a file of known LAP values (each represented as 3 octets) captured from Bluetooth traffic.
- For each LAP, it generates candidate full addresses by iterating through all possible UAP values (from 00 to FF) and prepending a fixed NAP ("00:00").
- It then uses `l2ping` (executed with elevated privileges) against these generated addresses to see if a valid response is returned.
- If a device replies (for example, the output contains "bytes from"), the candidate address is considered valid and is appended to the specified output file for further targeting.
- The process runs concurrently, and when multiple Bluetooth adapters are available, the workload is split so that each adapter targets unique UAP values without duplicate efforts. Specific interfaces can also be excluded from fuzzing as needed.

Note: candidates with no device behind them each cost one BlueZ page timeout (~5 seconds), and page attempts serialise per adapter, so a full 256-candidate sweep for one LAP takes roughly 10 minutes on two adapters (roughly 20 minutes on one).

### Command-Line Flags for UAP Fuzzing

- **`-u` / `--uap-fuzz`**: enables the UAP fuzzing mode.
- **`-l` / `--lap-file`**: input file containing the list of LAP values (e.g., `laps.txt`).
- **`-o` / `--output`**: output file where discovered, valid Bluetooth addresses (with the correct UAP) are saved (e.g., `found.txt`).
- **`--uap-timeout`**: (optional) timeout (in seconds) for each `l2ping` attempt (default: 30).
- **`--nap`**: (optional) the NAP to prepend, two octets (default: `00:00`). Set this when the target's NAP is known to be something else.
- **`-x` / `--exclude`**: (optional) exclude specific Bluetooth interface adapters from the fuzzing process.

### Usage Example

```bash
./echoblue -u -l laps.txt -o found.txt
./echoblue -u -l laps.txt -o found.txt -x hci0   # exclude hci0
```

## Error Handling

The tool includes error handling for:

- Missing or invalid configuration files
- Empty device lists
- Bluetooth adapter detection issues
- Subprocess failures or timeouts (especially when pinging devices in Classic mode)
- Graceful termination on user interruption

## Notes

#### Important Notice: Avoid Running Multiple Instances Simultaneously

Running multiple instances of this program has been tested and found to severely impact performance. This is particularly evident when performing tasks like UAP fuzzing while simultaneously attempting to run BLE with a reduced interval. Such configurations can lead to significant degradation in tool efficiency.

Recommendation: To ensure optimal performance and reliability, run only one instance of the program at a time, especially if using the same interfaces.

## Development

```bash
go vet ./...
go test ./...        # unit tests; hardware modes are covered by manual tests
```

Releases are built by GitHub Actions: pushing a `vX.Y.Z` tag publishes the four binaries plus `SHA256SUMS` to the Releases page. CI runs `gofmt`, `go vet`, `go test -race`, `govulncheck`, and cross-compiles all targets on every push/PR.

### Creating a release

```bash
git tag v0.1.0
git push origin v0.1.0
```

That triggers the Release workflow (Actions tab); when it finishes, the
binaries appear under Releases in the repository sidebar. To build them
without publishing, run the Release workflow manually from the Actions
tab (workflow_dispatch).

### Running as a service

An example systemd unit is in [deploy/echoblue.service](deploy/echoblue.service).
It runs unprivileged with `CAP_NET_RAW` (via `AmbientCapabilities`), which
is what `l2ping` needs — no root or passwordless sudo required. The same
capability can be granted to the binary directly:

```bash
sudo setcap cap_net_raw+ep ./echoblue
```

When neither root nor the capability is present, the tool falls back to
`sudo -n l2ping` (passwordless sudo).

## License

This project is open-source and distributed under the MIT License.
