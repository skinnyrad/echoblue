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

# Callback function to be called when a BLE device is discovered
def device_found(device, advertisement_data):
    # Only alert once per discovered device for BLE
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
            if line and '\t' in line and not line.startswith('\t'):
                adapter = line.split(':')[0]
                adapters.append(adapter)
        print(f"Valid Bluetooth adapters found: {adapters}")
        return adapters
    except subprocess.CalledProcessError:
        print("Error: Failed to execute hciconfig.")
        return []

# Asynchronous BLE scanning function
async def scan_with_adapter(adapter, interval):
    scanner = BleakScanner(adapter=adapter)
    scanner.register_detection_callback(device_found)
    await scanner.start()
    while True:
        await asyncio.sleep(interval)
        # Clear discovered set before the next BLE scan, so we can re-alert if desired
        discovered_devices.clear()
        await scanner.stop()
        await scanner.start()

# Helper function to ping a device via Classic Bluetooth
def ping_device(adapter, mac, ping_count=3):
    """
    Pings the specified MAC address using l2ping. This does not rely on
    discovered_devices, so the device is pinged every time.
    """
    try:
        result = subprocess.run(
            ['sudo', 'l2ping', '-i', adapter, '-c', str(ping_count), mac],
            capture_output=True,
            text=True,
            timeout=10
        )
        # If "bytes from" is in the output, the device responded
        if any("bytes from" in line for line in result.stdout.split('\n')):
            print(f"Classic device active: {mac}")
            send_alert(mac, "Classic Device")
        else:
            print(f"Classic device inactive: {mac}")
    except subprocess.CalledProcessError as e:
        print(f"Error pinging {mac} on {adapter}: {str(e)}")

# Asynchronous Classic Bluetooth scanning function
async def classic_scan_with_adapter(adapter, devices, interval, ping_count=3):
    """
    Continuously pings each known device in a loop, sleeping the specified
    interval between full scans.
    """
    while True:
        for mac in devices:
            ping_device(adapter, mac, ping_count=ping_count)
        await asyncio.sleep(interval)

async def main(args):
    global known_devices, discovered_devices
    known_devices = load_known_devices(args.file)
    discovered_devices = set()  # used for BLE only

    if not known_devices:
        print("No known devices loaded. Exiting.")
        return

    adapters = get_bluetooth_adapters()
    if not adapters:
        print("No Bluetooth adapters found. Exiting.")
        return

    # Run Classic or BLE depending on user selection
    if args.classic:
        # Classic mode
        tasks = [
            classic_scan_with_adapter(adapter, known_devices, args.classic_interval)
            for adapter in adapters
        ]
    else:
        # BLE mode
        tasks = [scan_with_adapter(adapter, args.ble_interval) for adapter in adapters]

    await asyncio.gather(*tasks)

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Bluetooth device scanner")
    parser.add_argument('-f', '--file', type=str, required=True,
                        help='Path to file with known device addresses')

    # Mutually exclusive group for selecting mode
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument('-c', '--classic', action='store_true',
                       help='Use Classic Bluetooth mode (l2ping)')
    group.add_argument('-b', '--btle', action='store_true',
                       help='Use BLE mode')

    # Intervals
    parser.add_argument('--classic-interval', type=int, default=30,
                        help='Scan interval in seconds (Classic mode)')
    parser.add_argument('--ble-interval', type=int, default=30,
                        help='Scan interval in seconds (BLE mode)')

    args = parser.parse_args()
    asyncio.run(main(args))
