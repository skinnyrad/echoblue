# Blue Echo (ECHOBLUE)

A Python-based Bluetooth scanner that monitors for specific devices and sends alerts when they come into range. This tool efficiently distributes workload across available Bluetooth hardware and continuously scans for devices listed in a configuration file.

## Features

- **Scans for specific Bluetooth devices** using their MAC addresses
- **Distributes scanning workload** across multiple Bluetooth adapters
- **Sends alerts** when known devices are detected
- **Configurable scan intervals** for both Classic Bluetooth and BLE
- **Simple device management** through a text file
- **Supports both Classic Bluetooth and BLE scanning**

## Prerequisites

- Python 3.7 or higher
- Linux, macOS, or Windows operating system
- Bluetooth adapter with support for Classic Bluetooth or BLE
- Required Python packages:
  - bleak
  - asyncio

## Installation

1. **Clone the repository** or download the source code.

2. **Install the required dependencies:**
```bash
pip install bleak asyncio
```

3. **Create a text file** containing the MAC addresses of devices you want to monitor, one per line:
```text
XX:XX:XX:XX:XX:XX
YY:YY:YY:YY:YY:YY
```

## Usage

Run the scanner with the following command:
```bash
python echoblue.py -f devices.txt -c --classic-interval 10
```

**Command Arguments:**
- `-f, --file`: Path to the file containing device MAC addresses (required)
- `-c, --classic`: Use Classic Bluetooth mode (l2ping)
- `-b, --btle`: Use BLE mode
- `--classic-interval`: Scan interval in seconds for Classic Bluetooth (optional, default: 30)
- `--ble-interval`: Scan interval in seconds for BLE (optional, default: 30)

## Configuration

**Device List File Format:**
- Each line should contain a single MAC address
- Addresses should be in the format `XX:XX:XX:XX:XX:XX`
- Empty lines and whitespace are automatically ignored

```
77:0E:FD:98:10:30
89:57:5F:D0:F4:06
9B:F8:76:6A:1E:FC
B1:69:D1:E4:06:FB
```
## How It Works

1. **Loads known device addresses** from the specified file
2. **Detects available Bluetooth adapters** using `hciconfig`
3. **Initializes the scanner** for either Classic Bluetooth or BLE based on user selection
4. **Scans for devices** at the specified interval:
   - For **BLE**, it uses the Bleak library to scan and alert when a known device is discovered.
   - For **Classic Bluetooth**, it uses `l2ping` to ping known devices and alert if they respond.
5. **Clears the cache** before each scan to ensure re-alerting if desired.

## Customization

To customize the alert system, modify the `send_alert` function in the code:
```python
def send_alert(device_address, device_name):
    # Add your custom alert logic here
    print(f"Alert: Device {device_name} ({device_address}) is in range!")
```

## Error Handling

The program includes basic error handling for:
- Missing or invalid device list files
- Empty device lists
- Scanner initialization failures
- Bluetooth adapter detection issues

## License

This project is open-source and available under the MIT License.
