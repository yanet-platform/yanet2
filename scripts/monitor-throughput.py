import argparse
import subprocess
import json
import time
import sys

def get_args():
    parser = argparse.ArgumentParser(description="Yanet Throughput Monitor")
    parser.add_argument("--device", required=True, help="Device identifier")
    parser.add_argument("--pipeline", required=True, help="Pipeline identifier")
    parser.add_argument("--function", required=True, help="Function identifier")
    parser.add_argument("--chain", required=True, help="Chain identifier")
    parser.add_argument("--module", required=True, help="Module identifier")
    parser.add_argument("--interval", type=float, default=2.0, help="Polling interval in seconds (default: 2.0)")
    return parser.parse_args()

def run_yanet_command(args):
    """
    Constructs and runs the shell command, returning the parsed JSON.
    """
    cmd = [
        "yanet-cli-counters", "perf",
        "--device", args.device,
        "--pipeline", args.pipeline,
        "--function", args.function,
        "--chain", args.chain,
        "--module", args.module,
        "--json"
    ]

    try:
        # Capture stdout, decode bytes to string
        result = subprocess.run(cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, check=True)
        return json.loads(result.stdout.decode('utf-8'))
    except subprocess.CalledProcessError as e:
        print(f"Error executing command: {' '.join(cmd)}", file=sys.stderr)
        print(f"Stderr: {e.stderr.decode('utf-8')}", file=sys.stderr)
        sys.exit(1)
    except json.JSONDecodeError as e:
        print(f"Error parsing JSON output: {e}", file=sys.stderr)
        sys.exit(1)
    except FileNotFoundError:
        print("Error: 'yanet-cli-counters' executable not found in PATH.", file=sys.stderr)
        sys.exit(1)

def format_unit(value, unit_type='k', is_bytes=False):
    """
    Helper to format large numbers (e.g., 1000000 -> 1.00 M).
    is_bytes=True calculates bits per second (value * 8) usually used for bandwidth.
    """
    suffix = ""
    
    if is_bytes:
        # Convert bytes to bits for bandwidth display
        value = value * 8
        units = ['bps', 'Kbps', 'Mbps', 'Gbps', 'Tbps']
    else:
        # Packets
        units = [' ', 'K', 'M', 'G', 'T']

    idx = 0
    while value >= 1000 and idx < len(units) - 1:
        value /= 1000.0
        idx += 1
    
    return f"{value:.2f} {units[idx]}"

def main():
    args = get_args()
    
    # State variables for the previous poll
    prev_time = None
    prev_stats = None

    print(f"Starting monitor for Device: {args.device}, Module: {args.module}...")
    print(f"Polling every {args.interval} seconds.")
    print("-" * 95)
    print(f"{'Timestamp':<20} | {'TX PPS':<15} | {'RX PPS':<15} | {'TX Bandwidth':<15} | {'RX Bandwidth':<15}")
    print("-" * 95)

    try:
        while True:
            # 1. Capture current time accurately before/at execution
            # We use monotonic for accurate delta calculation, time() for display
            now_monotonic = time.monotonic() 
            display_time = time.strftime("%H:%M:%S")
            
            # 2. Execute Command
            data = run_yanet_command(args)

            # 3. Extract relevant fields
            curr_stats = {
                'tx': data.get('tx', 0),
                'rx': data.get('rx', 0),
                'tx_bytes': data.get('tx_bytes', 0),
                'rx_bytes': data.get('rx_bytes', 0)
            }

            # 4. Calculate Throughput
            if prev_stats is not None:
                # Time delta between the two data captures
                delta_time = now_monotonic - prev_time

                # Prevent division by zero if script runs too fast
                if delta_time > 0:
                    delta_tx = curr_stats['tx'] - prev_stats['tx']
                    delta_rx = curr_stats['rx'] - prev_stats['rx']
                    delta_tx_bytes = curr_stats['tx_bytes'] - prev_stats['tx_bytes']
                    delta_rx_bytes = curr_stats['rx_bytes'] - prev_stats['rx_bytes']

                    # Calculate rates
                    tx_pps = delta_tx / delta_time
                    rx_pps = delta_rx / delta_time
                    tx_bw = delta_tx_bytes / delta_time # Bytes per second
                    rx_bw = delta_rx_bytes / delta_time # Bytes per second

                    print(f"{display_time:<20} | "
                          f"{format_unit(tx_pps):<15} | "
                          f"{format_unit(rx_pps):<15} | "
                          f"{format_unit(tx_bw, is_bytes=True):<15} | "
                          f"{format_unit(rx_bw, is_bytes=True):<15}")
                else:
                    print(f"{display_time:<20} | Calc skipped (dt=0)")
            else:
                print(f"{display_time:<20} | Initializing baseline data...")

            # 5. Update state
            prev_time = now_monotonic
            prev_stats = curr_stats

            # 6. Wait for next interval
            # Note: We sleep for the interval. The actual loop time will be 
            # interval + command_execution_time. This is handled correctly 
            # because we measure actual time difference (delta_time) every loop.
            time.sleep(args.interval)

    except KeyboardInterrupt:
        print("\nStopping monitor.")
        sys.exit(0)

if __name__ == "__main__":
    main()
