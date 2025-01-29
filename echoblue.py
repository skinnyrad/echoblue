import asyncio
import argparse
import math
import os
import subprocess
from bleak import BleakScanner

def load_known_devices(file_path):
    if not os.path.isfile(file_path):
        print(f"Error: File '{file_path}' does not exist.")
        return []
    with open(file_path, 'r') as file:
        return [line.strip() for line in file if line.strip()]

def device_found(device, advertisement_data):
    if device.address in known_devices and device.address not in discovered_devices:
        discovered_devices.add(device.address)
        print(f"Device found: {device.name} ({device.address})")
        send_alert(device.address, device.name)

def send_alert(device_address, device_name):
    print(f"Alert: Device {device_name} ({device_address}) is in range!")

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

async def scan_with_adapter(adapter, interval):
    scanner = BleakScanner(adapter=adapter)
    scanner.register_detection_callback(device_found)
    await scanner.start()
    while True:
        await asyncio.sleep(interval)
        discovered_devices.clear()
        await scanner.stop()
        await scanner.start()

async def ping_device(adapter, mac, ping_count=3):
    try:
        result = await asyncio.create_subprocess_exec(
            'sudo', 'l2ping', '-i', adapter, '-c', str(ping_count), mac,
            stdout=subprocess.PIPE, stderr=subprocess.PIPE
        )
        # Use asyncio.wait_for to enforce a timeout
        stdout, stderr = await asyncio.wait_for(result.communicate(), timeout=30)
        if any("bytes from" in line for line in stdout.decode().split('\n')):
            print(f"[{adapter}] Classic device active: {mac}")
            send_alert(mac, "Classic Device")
        else:
            print(f"[{adapter}] Classic device inactive: {mac}")
    except asyncio.TimeoutError:
        print(f"[{adapter}] Timeout occurred while pinging {mac}")
        result.terminate()  # Ensure the subprocess is terminated
    except subprocess.CalledProcessError as e:
        print(f"[{adapter}] Error pinging {mac}: {str(e)}")



async def classic_scan_with_adapter(adapter, devices, interval, ping_count=3):
    while True:
        print(f"[{adapter}] Starting scan cycle for {len(devices)} devices")
        # Use asyncio.gather to ping all devices simultaneously
        await asyncio.gather(*[ping_device(adapter, mac, ping_count=ping_count) for mac in devices])
        print(f"[{adapter}] Scan cycle completed, waiting {interval}s")
        await asyncio.sleep(interval)


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

    if args.classic:
        tasks = []
        num_adapters = len(adapters)
        # Create a list of device lists distributed across adapters
        devices_per_adapter = [[] for _ in adapters]
        for i, mac in enumerate(known_devices):
            adapter_index = i % num_adapters
            devices_per_adapter[adapter_index].append(mac)
        
        # Create tasks for each adapter with its assigned devices
        for adapter, devices in zip(adapters, devices_per_adapter):
            if devices:  # Only create task if adapter has devices to scan
                print(f"[{adapter}] Monitoring {len(devices)} devices: {', '.join(devices)}")
                tasks.append(
                    classic_scan_with_adapter(
                        adapter, devices, args.classic_interval, ping_count=args.num_packets
                    )
                )
    else:
        tasks = [scan_with_adapter(adapter, args.ble_interval) for adapter in adapters]

    await asyncio.gather(*tasks)

if __name__ == "__main__":
    parser = argparse.ArgumentParser(description="Bluetooth device scanner")
    parser.add_argument('-f', '--file', type=str, required=True,
                        help='Path to file with known device addresses')
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument('-c', '--classic', action='store_true',
                       help='Use Classic Bluetooth mode (l2ping)')
    group.add_argument('-b', '--btle', action='store_true',
                       help='Use BLE mode')

    parser.add_argument('-n', '--num-packets', type=int, default=3,
                        help='Number of l2ping packets to send (default: 3)')

    parser.add_argument('--classic-interval', type=int, default=30,
                        help='Scan interval in seconds (Classic mode)')
    parser.add_argument('--ble-interval', type=int, default=30,
                        help='Scan interval in seconds (BLE mode)')

    args = parser.parse_args()
    asyncio.run(main(args))
