import asyncio
import argparse
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
    global discovered_devices, all_seen_devices
    if device.address in known_devices and device.address not in discovered_devices:
        discovered_devices.add(device.address)
        all_seen_devices.add(device.address)
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
    try:
        while True:
            await asyncio.sleep(interval)
            # Clear discovered devices for this cycle while keeping the overall record
            discovered_devices.clear()
            await scanner.stop()
            await scanner.start()
    except asyncio.CancelledError:
        await scanner.stop()
        raise

async def ping_device(adapter, mac, ping_count=3):
    global all_seen_devices
    try:
        process = await asyncio.create_subprocess_exec(
            'sudo', 'l2ping', '-i', adapter, '-c', str(ping_count), mac,
            stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE
        )
        stdout, stderr = await asyncio.wait_for(process.communicate(), timeout=30)
        if any("bytes from" in line for line in stdout.decode().split('\n')):
            print(f"[{adapter}] Classic device active: {mac}")
            send_alert(mac, "Classic Device")
            all_seen_devices.add(mac)
        else:
            print(f"[{adapter}] Classic device inactive: {mac}")
    except asyncio.TimeoutError:
        print(f"[{adapter}] Timeout occurred while pinging {mac}")
        process.terminate()
    except subprocess.CalledProcessError as e:
        print(f"[{adapter}] Error pinging {mac}: {str(e)}")

async def classic_scan_with_adapter(adapter, devices, interval, ping_count=3):
    try:
        while True:
            print(f"[{adapter}] Starting scan cycle for {len(devices)} devices")
            await asyncio.gather(*[ping_device(adapter, mac, ping_count=ping_count) for mac in devices])
            print(f"[{adapter}] Scan cycle completed, waiting {interval}s")
            await asyncio.sleep(interval)
    except asyncio.CancelledError:
        raise

async def main(args):
    global known_devices, discovered_devices, all_seen_devices
    known_devices = load_known_devices(args.file)
    discovered_devices = set()  # This set is cleared every scan cycle.
    all_seen_devices = set()    # This set persists throughout the session.

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
        # Distribute the known devices evenly across the adapters.
        devices_per_adapter = [[] for _ in adapters]
        for i, mac in enumerate(known_devices):
            adapter_index = i % num_adapters
            devices_per_adapter[adapter_index].append(mac)
        
        # Create scanning tasks for each adapter with assigned devices.
        for adapter, devices in zip(adapters, devices_per_adapter):
            if devices:
                print(f"[{adapter}] Monitoring {len(devices)} devices: {', '.join(devices)}")
                tasks.append(
                    classic_scan_with_adapter(
                        adapter, devices, args.classic_interval, ping_count=args.num_packets
                    )
                )
    else:
        # BLE mode: one task per adapter.
        tasks = [scan_with_adapter(adapter, args.ble_interval) for adapter in adapters]

    try:
        await asyncio.gather(*tasks)
    except KeyboardInterrupt:
        print("\nReceived keyboard interrupt. Cancelling tasks...")
        for task in tasks:
            task.cancel()
        await asyncio.gather(*tasks, return_exceptions=True)
    finally:
        print("Shutting down gracefully...\n\n")
        if all_seen_devices:
            print("Devices seen from target file:")
            for device in all_seen_devices:
                print(device)
        else:
            print("No devices from target file were seen.")

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
    try:
        asyncio.run(main(args))
    except KeyboardInterrupt:
        print("Program interrupted. Exiting gracefully.")
