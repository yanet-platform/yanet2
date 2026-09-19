#!/usr/bin/env python3
"""Dump an acl module config export for the acl benchmarks.

Every rule is written with its v4 and v6 networks, protocols, ports,
devices, vlans and fragment kind, so the benchmarks can build all five
acl module projections. Usage:

    dump_acl_ruleset.py <config.json> <rules.bin>

The config format follows the acl module control plane export: a name
and a rules array with srcs, dsts, proto_ranges, src_port_ranges,
dst_port_ranges, devices, vlan_ranges and fragment fields.
"""
import ipaddress
import json
import struct
import sys

if len(sys.argv) < 3:
    raise SystemExit(f"usage: {sys.argv[0]} <config.json> <rules.bin>")
SRC = sys.argv[1]
DST = sys.argv[2]


def parse_v6(cidr):
    addr, _, m = cidr.partition("/")
    a = int(ipaddress.IPv6Address(addr))
    if m:
        mask = (
            int.from_bytes(ipaddress.IPv6Address(m).packed, "big")
            if ":" in m
            else ((1 << 128) - (1 << (128 - int(m))) if int(m) else 0)
        )
    else:
        mask = (1 << 128) - 1
    return a & mask, mask


def parse_v4(cidr):
    net = ipaddress.ip_network(cidr, strict=False)
    a = int(net.network_address)
    m = int(net.netmask)
    return a, m


def main():
    data = json.load(open(SRC))
    rules = data["rules"]

    device_ids = {}
    out = bytearray()
    out += struct.pack("<I", len(rules))
    for r in rules:
        s6 = [parse_v6(c) for c in r["srcs"] if ":" in c]
        d6 = [parse_v6(c) for c in r["dsts"] if ":" in c]
        s4 = [parse_v4(c) for c in r["srcs"] if ":" not in c]
        d4 = [parse_v4(c) for c in r["dsts"] if ":" not in c]
        frag = r.get("fragment", {}).get("kind", 0)

        out += b"\x01"
        for nets in (s6, d6):
            out += struct.pack("<I", len(nets))
            for a, m in nets:
                out += a.to_bytes(16, "big") + m.to_bytes(16, "big")
        for nets in (s4, d4):
            out += struct.pack("<I", len(nets))
            for a, m in nets:
                out += a.to_bytes(4, "big") + m.to_bytes(4, "big")
        for key in ("proto_ranges", "src_port_ranges", "dst_port_ranges"):
            ranges = r[key]
            out += struct.pack("<I", len(ranges))
            for rg in ranges:
                out += struct.pack("<HH", rg["from"], rg["to"])
        out += struct.pack("<B", frag)
        out += struct.pack("<I", len(r["devices"]))
        for dev in r["devices"]:
            name = dev["name"]
            if name not in device_ids:
                device_ids[name] = len(device_ids)
            out += struct.pack("<Q", device_ids[name])
        out += struct.pack("<I", len(r["vlan_ranges"]))
        for rg in r["vlan_ranges"]:
            out += struct.pack("<HH", rg["from"], rg["to"])

    open(DST, "wb").write(out)
    print(f"rules={len(rules)} devices={len(device_ids)} bytes={len(out)}")


main()
