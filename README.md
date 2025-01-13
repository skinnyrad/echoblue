# ECHOBLUE

A Python-based Bluetooth scanner that monitors for specific devices and sends alerts when they come into range. This tool efficiently distributes workload across available Bluetooth hardware and continuously scans for devices listed in a configuration file.

## Features

- Scans for specific Bluetooth devices using their MAC addresses
- Distributes scanning workload across multiple Bluetooth adapters
- Sends alerts when known devices are detected
- Configurable scan intervals
- Simple device management through a text file

## Prerequisites

- Python 3.7 or higher
- Linux, macOS, or Windows operating system
- Bluetooth adapter with BLE support
- Required Python packages:
  - bleak
  - asyncio

## Installation

1. Clone the repository or download the source code.

2. Install the required dependencies:
```bash
pip install bleak asyncio
```

3. Create a text file containing the MAC addresses of devices you want to monitor, one per line:
```text
XX:XX:XX:XX:XX:XX
YY:YY:YY:YY:YY:YY
```

## Usage

Run the scanner with the following command:
```bash
python scanner.py -f devices.txt -i 30
```

**Command Arguments:**
- `-f, --file`: Path to the file containing device MAC addresses (required)
- `-i, --interval`: Scan interval in seconds (optional, default: 30)

## Configuration

**Device List File Format:**
- Each line should contain a single MAC address
- Addresses should be in the format `XX:XX:XX:XX:XX:XX`
- Empty lines and whitespace are automatically ignored

## How It Works

1. The program loads a list of known device addresses from the specified file
2. It initializes the BLE scanner using the Bleak library
3. When a device is discovered, it checks if the device's address matches any in the known devices list
4. If a match is found, an alert is triggered
5. The scanner clears its cache and restarts at the specified interval

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

## License

This project is open-source and available under the MIT License.
