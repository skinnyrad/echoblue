"""
Blue Echo: A Bluetooth Device Scanner

This module provides functionality to scan for Bluetooth devices using either BLE (via Bleak)
or Classic Bluetooth (via l2ping). It reads a configuration file for known device MAC addresses,
detects available Bluetooth adapters using the `hciconfig` command, and continuously scans for
devices in range. When a known device is found, an alert is printed.
"""

import asyncio
import argparse
import os
import subprocess
from bleak import BleakScanner


def load_known_devices(file_path):
    """
    Load known Bluetooth device addresses from a text file.

    Args:
        file_path (str): Path to the file containing device MAC addresses, one per line.

    Returns:
        list: A list of device MAC addresses (str). If the file does not exist, returns an empty list.
    """
    if not os.path.isfile(file_path):
        print(f"Error: File '{file_path}' does not exist.")
        return []
    with open(file_path, 'r') as file:
        return [line.strip() for line in file if line.strip()]


def device_found(device, advertisement_data):
    """
    Callback function to handle Bluetooth device detection during BLE scanning.

    This function is invoked each time the BleakScanner detects a device. If the device's MAC address
    is in the list of known devices and hasn't been alerted in the current scan cycle, it adds the device
    to the discovered list, prints a message, and sends an alert.

    Args:
        device: A Bleak device object, which includes attributes such as 'address' and 'name'.
        advertisement_data: Advertising data for the device (unused in this implementation).
    """
    global discovered_devices, all_seen_devices
    if device.address in known_devices and device.address not in discovered_devices:
        discovered_devices.add(device.address)
        all_seen_devices.add(device.address)
        print(f"Device found: {device.name} ({device.address})")
        send_alert(device.address, device.name)


def send_alert(device_address, device_name):
    """
    Send an alert when a known device is detected.

    Currently, this function simply prints an alert message. It can be modified to integrate with
    external notification systems such as email, SMS, or logging services.

    Args:
        device_address (str): The MAC address of the detected device.
        device_name (str): The human-readable name of the detected device.
    """
    print(f"Alert: Device {device_name} ({device_address}) is in range!")


def get_bluetooth_adapters():
    """
    Detect available Bluetooth adapters using the `hciconfig` system command.

    Returns:
        list: A list of detected Bluetooth adapter names (e.g., 'hci0', 'hci1').
        If the command fails, an empty list is returned.
    """
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
    """
    Asynchronously scan for BLE devices using a specific Bluetooth adapter.

    This function creates a BleakScanner for the given adapter, registers the detection callback,
    and continuously scans for devices. At the end of each interval, it clears the temporary record
    of discovered devices while preserving the overall list of devices detected in the session.

    Args:
        adapter (str): The Bluetooth adapter to use (e.g., 'hci0').
        interval (int): The time interval in seconds between scanning cycles.

    Raises:
        asyncio.CancelledError: If the scanning task is cancelled.
    """
    scanner = BleakScanner(adapter=adapter)
    scanner.register_detection_callback(device_found)
    await scanner.start()
    try:
        while True:
            await asyncio.sleep(interval)
            # Clear discovered devices for this cycle while keeping the overall record.
            discovered_devices.clear()
            await scanner.stop()
            await scanner.start()
    except asyncio.CancelledError:
        await scanner.stop()
        raise


async def ping_device(adapter, mac, ping_count=3):
    """
    Asynchronously ping a Classic Bluetooth device using a specified adapter.

    Uses the 'l2ping' command (with sudo) to ping the specified MAC address. If a valid response
    is detected in the command output, an alert is triggered and the device is recorded; otherwise,
    the device is reported inactive.

    Args:
        adapter (str): The Bluetooth adapter to use for pinging (e.g., 'hci0').
        mac (str): MAC address of the device to be pinged.
        ping_count (int, optional): Number of ping packets to send. Defaults to 3.

    Notes:
        Requires sudo privileges and the availability of the 'l2ping' command.
    """
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
    """
    Asynchronously scan for Classic Bluetooth devices by pinging known devices on a provided adapter.

    The function continuously pings each device in the list and waits for a specified interval between
    each scan cycle.

    Args:
        adapter (str): The Bluetooth adapter to use (e.g., 'hci0').
        devices (list): A list of device MAC addresses to ping.
        interval (int): Time in seconds to wait between scan cycles.
        ping_count (int, optional): Number of ping packets to send for each device. Defaults to 3.

    Raises:
        asyncio.CancelledError: If the scanning task is cancelled.
    """
    try:
        while True:
            print(f"[{adapter}] Starting scan cycle for {len(devices)} devices")
            await asyncio.gather(*[ping_device(adapter, mac, ping_count=ping_count) for mac in devices])
            print(f"[{adapter}] Scan cycle completed, waiting {interval}s")
            await asyncio.sleep(interval)
    except asyncio.CancelledError:
        raise


async def main(args):
    """
    Main asynchronous function to initialize and run the Bluetooth scanning process.

    Loads known device addresses from the provided configuration file, detects available Bluetooth adapters,
    and starts asynchronous scanning tasks based on the selected mode (Classic or BLE). In Classic mode,
    devices are distributed evenly across adapters and pinged, while in BLE mode, scanning is handled by Bleak.
    On keyboard interrupt, scanning tasks are cancelled gracefully and a summary of detected devices is output.

    Args:
        args: Command-line arguments parsed by argparse, including options for file path, scan intervals,
              mode selection, packet count, and adapter exclusion.
    """
    global known_devices, discovered_devices, all_seen_devices
    known_devices = load_known_devices(args.file)
    discovered_devices = set()  # This set is cleared every scan cycle.
    all_seen_devices = set()    # This set persists throughout the session.

    if not known_devices:
        print("No known devices loaded. Exiting.")
        return

    adapters = get_bluetooth_adapters()

    # Filter out excluded adapters if any were specified.
    if args.exclude:
        for ex in args.exclude:
            if ex not in adapters:
                print(f"Warning: Interface {ex} specified to ignore but not found.")
        adapters = [adapter for adapter in adapters if adapter not in args.exclude]

    if not adapters:
        print("No Bluetooth adapters available after filtering. Exiting.")
        return

    if args.classic:
        tasks = []
        num_adapters = len(adapters)
        devices_per_adapter = [[] for _ in adapters]
        # Distribute known devices evenly across available adapters
        for i, mac in enumerate(known_devices):
            adapter_index = i % num_adapters
            devices_per_adapter[adapter_index].append(mac)
        
        for adapter, devices in zip(adapters, devices_per_adapter):
            if devices:
                print(f"[{adapter}] Monitoring {len(devices)} devices: {', '.join(devices)}")
                tasks.append(
                    classic_scan_with_adapter(
                        adapter, devices, args.classic_interval, ping_count=args.num_packets
                    )
                )
    else:
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
    parser.add_argument('-x', '--exclude', type=str, nargs='*', default=[],
                        help='Interface(s) to exclude (e.g., -x hci0 hci2)')
    
    args = parser.parse_args()
    try:
        asyncio.run(main(args))
    except KeyboardInterrupt:
        print("Program interrupted. Exiting gracefully.")
