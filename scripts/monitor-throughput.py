#!/usr/bin/env python3
"""Monitor YANET throughput via the Counters gRPC service.

Polls CountersService/ByTags for the tx, rx, tx_bytes and rx_bytes counters
matching a set of tags, every N seconds, and prints per-second rates (packets
per second and bits per second). Tags are accepted the same way the
`yanet-cli counters` command accepts them.

A "position" is a set of tags identifying one counter group. If the supplied
tags match more than one position, the first is used and a warning is printed.
"""

import argparse
import os
import sys
import time
import tempfile

RATE_NAMES = ["tx", "rx", "tx_bytes", "rx_bytes"]

DEFAULT_ENDPOINT = os.environ.get("YANET_ENDPOINT", "grpc://[::1]:8080")


def fail(message):
    print(message, file=sys.stderr)
    sys.exit(1)


def import_grpc():
    try:
        import grpc
    except ImportError:
        fail(
            "the 'grpcio' package is required.\n"
            "install it with: pip install grpcio grpcio-tools"
        )
    return grpc


def generate_stubs(proto_path):
    """Compile the counters proto at runtime and import the generated stubs.

    Returns a tuple of the message module and the gRPC stub module.
    """
    try:
        from grpc_tools import protoc
    except ImportError:
        fail(
            "the 'grpcio-tools' package is required to generate gRPC stubs.\n"
            "install it with: pip install grpcio grpcio-tools"
        )

    proto_path = os.path.abspath(proto_path)
    if not os.path.isfile(proto_path):
        fail(f"counters proto not found at {proto_path}; pass --proto to override")

    proto_dir = os.path.dirname(proto_path)
    proto_name = os.path.basename(proto_path)
    out_dir = tempfile.mkdtemp(prefix="yanet-counters-stubs-")

    rc = protoc.main([
        "protoc",
        f"--proto_path={proto_dir}",
        f"--python_out={out_dir}",
        f"--grpc_python_out={out_dir}",
        proto_name,
    ])
    if rc != 0:
        fail(f"failed to compile {proto_name} (protoc exit code {rc})")

    sys.path.insert(0, out_dir)
    stem = os.path.splitext(proto_name)[0]
    pb2 = __import__(f"{stem}_pb2")
    pb2_grpc = __import__(f"{stem}_pb2_grpc")
    return pb2, pb2_grpc


def parse_args():
    parser = argparse.ArgumentParser(
        description="Monitor YANET throughput via the Counters gRPC service.",
    )
    parser.add_argument("-d", "--device", help="Device tag.")
    parser.add_argument("-p", "--pipeline", help="Pipeline tag.")
    parser.add_argument("-f", "--function", help="Function tag.")
    parser.add_argument("-c", "--chain", help="Chain tag.")
    parser.add_argument("-t", "--module-type", help="Module type tag.")
    parser.add_argument("-m", "--module-name", help="Module name tag.")
    parser.add_argument(
        "-n",
        "--name",
        action="append",
        default=[],
        help="Counter name to query (repeatable). The tx/rx/tx_bytes/rx_bytes "
        "counters are always queried so rates can be computed.",
    )
    parser.add_argument(
        "--tag",
        action="append",
        default=[],
        metavar="KEY=VALUE",
        help="Arbitrary tag predicate (repeatable).",
    )
    parser.add_argument(
        "-i",
        "--interval",
        type=float,
        default=1.0,
        help="Polling interval in seconds (default: 1.0).",
    )
    parser.add_argument(
        "--endpoint",
        default=DEFAULT_ENDPOINT,
        help=f"Gateway endpoint (default: {DEFAULT_ENDPOINT}).",
    )
    parser.add_argument(
        "--proto",
        default=os.path.join(
            os.path.dirname(os.path.abspath(__file__)),
            "..",
            "controlplane",
            "ynpb",
            "v1",
            "counters.proto",
        ),
        help="Path to counters.proto for stub generation.",
    )
    return parser.parse_args()


def build_tags(args):
    """Build the (key, value) tag predicates from CLI arguments."""
    tags = []
    named = {
        "device": args.device,
        "pipeline": args.pipeline,
        "function": args.function,
        "chain": args.chain,
        "module_type": args.module_type,
        "module_name": args.module_name,
    }
    for key, value in named.items():
        if value is not None:
            tags.append((key, value))

    for item in args.tag:
        if "=" not in item:
            fail(f"invalid --tag '{item}', expected KEY=VALUE")
        key, value = item.split("=", 1)
        if not key:
            fail(f"invalid --tag '{item}', empty key")
        tags.append((key, value))

    return tags


def query_names(args):
    names = list(args.name)
    for name in RATE_NAMES:
        if name not in names:
            names.append(name)
    return names


def strip_scheme(endpoint):
    for scheme in ("grpc://", "grpcs://", "http://", "https://"):
        if endpoint.startswith(scheme):
            return endpoint[len(scheme):]
    return endpoint


def format_tags(tags):
    return ", ".join(f"{tag.key}={tag.value}" for tag in tags) or "(no tags)"


def fold_counter(counter):
    """Sum a counter across all instances and value slots into one scalar."""
    total = 0
    for instance in counter.instances:
        total += sum(instance.values)
    return total


def extract_values(group):
    values = {name: 0 for name in RATE_NAMES}
    for counter in group.counters:
        if counter.name in values:
            values[counter.name] = fold_counter(counter)
    return values


def format_pps(value):
    units = [" ", "K", "M", "G", "T"]
    idx = 0
    while value >= 1000.0 and idx < len(units) - 1:
        value /= 1000.0
        idx += 1
    return f"{value:8.2f} {units[idx]}pps"


def format_bps(value):
    units = ["bps", "Kbps", "Mbps", "Gbps", "Tbps"]
    idx = 0
    while value >= 1000.0 and idx < len(units) - 1:
        value /= 1000.0
        idx += 1
    return f"{value:8.2f} {units[idx]}"


def main():
    args = parse_args()

    if args.interval <= 0:
        fail("--interval must be positive")

    tags_kv = build_tags(args)
    if not tags_kv:
        fail("at least one tag is required; pass e.g. --device or --tag key=value")

    grpc = import_grpc()
    pb2, pb2_grpc = generate_stubs(args.proto)

    names = query_names(args)
    request = pb2.CountersByTagsRequest(
        tags=[pb2.CounterTag(key=key, value=value) for key, value in tags_kv],
        query=names,
    )

    target = strip_scheme(args.endpoint)
    channel = grpc.insecure_channel(target)
    stub = pb2_grpc.CountersServiceStub(channel)

    print(f"Monitoring throughput via {args.endpoint}")
    print(f"Tags: {', '.join(f'{k}={v}' for k, v in tags_kv)}")
    print(f"Polling every {args.interval}s. Press Ctrl-C to stop.")

    warned_multiple = False
    prev_values = None
    prev_time = None
    header_printed = False

    try:
        while True:
            now = time.monotonic()
            stamp = time.strftime("%H:%M:%S")

            try:
                response = stub.ByTags(request)
            except grpc.RpcError as err:
                fail(f"ByTags call failed: {err.details()}")

            groups = response.groups
            if not groups:
                fail("no counters match the given tags")

            if len(groups) > 1 and not warned_multiple:
                warned_multiple = True
                print(
                    f"WARNING: {len(groups)} positions matched the given tags; "
                    "using the first one.",
                    file=sys.stderr,
                )
                for idx, group in enumerate(groups):
                    marker = "->" if idx == 0 else "  "
                    print(f"  {marker} [{idx}] {format_tags(group.tags)}", file=sys.stderr)

            group = groups[0]
            if not header_printed:
                print(f"Position: {format_tags(group.tags)}")
                print(
                    f"{'time':<10} {'tx':>16} {'rx':>16} "
                    f"{'tx_bw':>16} {'rx_bw':>16}"
                )
                header_printed = True

            values = extract_values(group)

            if prev_values is not None:
                delta_time = now - prev_time
                if delta_time > 0:
                    def rate(name):
                        delta = values[name] - prev_values[name]
                        return max(delta, 0) / delta_time

                    tx_pps = rate("tx")
                    rx_pps = rate("rx")
                    tx_bps = rate("tx_bytes") * 8
                    rx_bps = rate("rx_bytes") * 8

                    print(
                        f"{stamp:<10} {format_pps(tx_pps):>16} {format_pps(rx_pps):>16} "
                        f"{format_bps(tx_bps):>16} {format_bps(rx_bps):>16}"
                    )
            else:
                print(f"{stamp:<10} establishing baseline...")

            prev_values = values
            prev_time = now
            time.sleep(args.interval)
    except KeyboardInterrupt:
        print("\nStopping.", file=sys.stderr)
        sys.exit(0)


if __name__ == "__main__":
    main()
