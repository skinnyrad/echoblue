import asyncio
import argparse
import os
import subprocess
from bleak import BleakScanner

# Function to load known device addresses from a file
def load_known_devices(file_path):
    if not os.path.isfile(file_path):
        print(f"Error: File '{file_path}' does not exist.")
        return []
    with open(file_path, 'r') as file:
        return [line.strip() for line in file if line.strip()]

# Callback function to be called when a device is discovered
def device_found(device, advertisement_data):
    if device.address in known_devices and device.address not in discovered_devices:
        discovered_devices.add(device.address)
        print(f"Device found: {device.name} ({device.address})")
        send_alert(device.address, device.name)

def send_alert(device_address, device_name):
    # Implement your alerting logic here (e.g., email, SMS, etc.)
    print(f"Alert: Device {device_name} ({device_address}) is in range!")

# Function to get list of valid Bluetooth adapters using hciconfig
def get_bluetooth_adapters():
    try:
        result = subprocess.run(['hciconfig'], capture_output=True, text=True, check=True)
        adapters = []
        for line in result.stdout.splitlines():
            if line and '\t' in line and not line.startswith('\t'):  # Adapter lines contain tabs and do not start with a tab
                adapter = line.split(':')[0]
                adapters.append(adapter)
        print(f"Valid Bluetooth adapters found: {adapters}")
        return adapters
    except subprocess.CalledProcessError:
        print("Error: Failed to execute hciconfig.")
        return []

async def scan_with_adapter(adapter, interval):
    scanner = BleakScanner(adapter=adapter)
    scanner.register_detection_callback(device_found)
    await scanner.start()
    while True:
        await asyncio.sleep(interval)  # Use the specified interval
        discovered_devices.clear()  # Clear the discovered devices set for the next scan
        await scanner.stop()
        await scanner.start()

async def main(args):
    global known_devices, discovered_devices
    known_devices = load_known_devices(args.file)
    discovered_devices = set()
    if not known_devices:
        print("No known devices loaded. Exiting.")
        return

    adapters = get_bluetooth_adapters()
    if not adapters:
        print("No Bluetooth adapters found. Exiting.")
        return

    tasks = [scan_with_adapter(adapter, args.interval) for adapter in adapters]
    await asyncio.gather(*tasks)

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Bluetooth device scanner")
    parser.add_argument('-f', '--file', type=str, required=True, help='Path to the file with known device addresses')
    parser.add_argument('-i', '--interval', type=int, default=30, help='Sleep interval between scans (in seconds)')
    args = parser.parse_args()

    asyncio.run(main(args))

