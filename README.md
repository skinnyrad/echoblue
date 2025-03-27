# Blue Echo (ECHOBLUE)

Blue Echo is a Python-based Bluetooth device scanner that monitors for specific devices and sends alerts when they come into range. The tool efficiently distributes scanning workloads across available Bluetooth adapters, supporting both Classic Bluetooth (using l2ping) and BLE scanning (using the Bleak library).

## Features

- **Monitors specific Bluetooth devices** by reading MAC addresses from a configuration file  
- **Supports both BLE and Classic Bluetooth modes**: Use BLE scanning with Bleak or Classic scanning via l2ping  
- **Workload distribution for Classic mode**: Evenly distributes device pings across available Bluetooth adapters  
- **Configurable scanning intervals**: Set separate intervals for BLE (`--ble-interval`) and Classic (`--classic-interval`) modes  
- **Adapter detection and exclusion**: Automatically detects available Bluetooth adapters using `hciconfig` and allows excluding selected interfaces  
- **Persistent device tracking**: Maintains a session record of all discovered devices while resetting per-cycle alerts  
- **Graceful shutdown**: Handles interruptions and outputs a summary of all detected devices

## Prerequisites

- Python 3.10 or higher
- Linux or macOS operating system
- Bluetooth adapter(s) supporting Classic Bluetooth or BLE
- System utility `hciconfig` for adapter detection
- Sudo privileges for Classic mode (running `l2ping`)
- Required Python packages:
  - bleak
  - asyncio
  - aiofiles

*(Note: asyncio is included in the Python standard library)*

## Installation

1. **Clone the repository** or download the source code.

2. **Create a virtual environment:**
   ```bash
   python3 -m venv venv 
   ```
   This creates a virtual environment named `venv` in your project directory.

3. **Activate the virtual environment:**
   ```bash
   source venv/bin/activate
   ```

4. **Install the required dependency:**
   ```bash
   pip install bleak aiofiles
   ```

5. **Prepare a configuration file** with the MAC addresses of devices to monitor (one per line):
   ```text
   XX:XX:XX:XX:XX:XX
   YY:YY:YY:YY:YY:YY
   ```

**(Optional) Deactivate the virtual environment when you are finished running the tool:**
   ```bash
   deactivate
   ```

## Usage

Run the script with your desired options. For example:

```bash
python echoblue.py -f devices.txt -c --classic-interval 10 --num-packets 3
```

**Command Line Arguments:**

- `-f, --file`: Path to the configuration file containing device MAC addresses (required)  
- `-c, --classic`: Use Classic Bluetooth mode (l2ping)  
- `-b, --btle`: Use BLE mode  
- `--classic-interval`: Scan interval in seconds for Classic mode (default: 30)  
- `--ble-interval`: Scan interval in seconds for BLE mode (default: 30)  
- `-n, --num-packets`: Number of l2ping packets to send in Classic mode (default: 3)  
- `-x, --exclude`: List of Bluetooth adapter interfaces to ignore (e.g., `-x hci0 hci2`)

## Configuration

**Device List File Format:**

- Each line should contain a single MAC address in the format `XX:XX:XX:XX:XX:XX`
- Lines that are empty or consist of only whitespace are automatically ignored

Example file content:
```
77:0E:FD:98:10:30
89:57:5F:D0:F4:06
9B:F8:76:6A:1E:FC
B1:69:D1:E4:06:FB
```

## How It Works

**Device Loading:** Reads known device MAC addresses from the specified configuration file.

**Adapter Detection:** Uses the `hciconfig` utility to list available Bluetooth adapters; adapters can be excluded using the `-x/--exclude` flag.

**Scanner Initialization:**  
  - **BLE Mode:** The Bleak library is used for asynchronous scanning. The scanner is restarted in each cycle—clearing temporary state—to ensure devices are re-checked.  
  - **Classic Mode:** Known devices are distributed evenly across adapters and pinged using `l2ping`. Sudo privileges are required, and response verification triggers alerts.

**Alert & Logging:** When a known device is detected, the code prints an alert message and maintains an overall record of all discovered devices throughout the session.

**Graceful Shutdown:** Handles keyboard interrupts by canceling scanning tasks and then outputs a summary of devices seen during the run.

## Customization

To alter how alerts are provided, modify the `send_alert` function in the source code:
```python
def send_alert(device_address, device_name):
    # Customize alert logic here (e.g., email, SMS, or custom logging)
    print(f"Alert: Device {device_name} ({device_address}) is in range!")
```

Below is an example section you can include in your README that explains UAP fuzzing, describes how the feature works in BlueEcho, and details the available command-line flags:


## UAP Fuzzing

UAP fuzzing is a method used to determine the missing Upper Address Part (UAP) of a Bluetooth device’s address. In Bluetooth, a full device address (BD_ADDR) consists of three parts: the NAP (Non-significant Address Part, 2 octets), the UAP (Upper Address Part, 1 octet), and the LAP (Lower Address Part, 3 octets). When capturing packets from Bluetooth Classic piconets, it is common to only obtain the LAP. To fully target a device, the UAP must be determined—this is achieved by "fuzzing" (brute forcing) through all 256 possible UAP values.

### How It Works

- The script reads a file of known LAP values (each represented as 3 octets) captured from Bluetooth traffic.
- For each LAP, the code generates candidate full addresses by iterating through all possible UAP values (from 00 to FF) and prepending a fixed NAP (in our case, “00:00”).
- It then uses the `l2ping` command (executed with elevated privileges) against these generated addresses to see if a valid response is returned.
- If a device replies (for example, the output contains “bytes from”), the candidate address is considered valid and is appended to the specified output file for further targeting.
- The process runs asynchronously, and when multiple Bluetooth adapters are available, the workload is split so that each adapter targets unique UAP values without duplicate efforts. Specific interfaces can also be excluded from fuzzing as needed.

This approach effectively automates the discovery and validation of the UAP for each LAP, making it possible to robustly target Bluetooth devices that were initially partially identified.

### Command-Line Flags for UAP Fuzzing

- **`-u` / `--uap-fuzz`**: Enables the UAP fuzzing mode.
- **`-l` / `--lap-file`**: Specifies the input file containing the list of LAP values (e.g., `laps.txt`).
- **`-o` / `--output`**: Sets the output file where discovered, valid Bluetooth addresses (with the correct UAP) are saved (e.g., `found.txt`).
- **`--uap-timeout`**: (Optional) Sets the timeout (in seconds) for each `l2ping` attempt. The default is set to 30 seconds.
- **`-x` / `--exclude`**: (Optional) Allows exclusion of specific Bluetooth interface adapters from the fuzzing process.

### Usage Example

To run BlueEcho in UAP fuzzing mode using a file containing LAPs with the output stored in `found.txt`, use the following command:

```bash
python3 echoblue.py -u -l laps.txt -o found.txt
```

This command will:
- Enable UAP fuzzing mode with the `-u` flag.
- Read the captured LAP values from `laps.txt` using the `-l` flag.
- Append any discovered full addresses (with a responsive UAP) to `found.txt` using the `-o` flag.

If you need to exclude a particular adapter (for example, `hci0`), you can add the `-x` flag:

```bash
python3 echoblue.py -u -l laps.txt -o found.txt -x hci0
```


## Error Handling

The tool includes error handling for the following:
- Missing or invalid configuration files
- Empty device lists
- Bluetooth adapter detection issues (using output from `hciconfig`)
- Subprocess failures or timeouts (especially when pinging devices in Classic mode)
- Graceful termination on user interruption

## Notes

#### Important Notice: Avoid Running Multiple Instances Simultaneously

Running multiple instances of this program has been tested and found to severely impact performance. This is particularly evident when performing tasks like UAP fuzzing while simultaneously attempting to run BLE with a reduced interval. Such configurations can lead to significant degradation in tool efficiency.

Recommendation: To ensure optimal performance and reliability, it is strongly advised to run only one instance of the program at a time, especially if using the same interfaces.

## License

This project is open-source and distributed under the MIT License.
