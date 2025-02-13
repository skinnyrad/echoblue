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
- Required Python package:
  - bleak  

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
   pip install bleak
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

## Error Handling

The tool includes error handling for the following:
- Missing or invalid configuration files
- Empty device lists
- Bluetooth adapter detection issues (using output from `hciconfig`)
- Subprocess failures or timeouts (especially when pinging devices in Classic mode)
- Graceful termination on user interruption

## License

This project is open-source and distributed under the MIT License.
