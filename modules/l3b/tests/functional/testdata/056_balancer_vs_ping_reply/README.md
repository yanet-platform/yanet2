# YANET1 VIP Echo fixtures

These six PCAPs are byte-identical copies from `yanet-platform/yanet` at
revision `6b30c2f376c5148a661eadb57fd316d3b453e085`, directory
[`autotest/units/001_one_port/056_balancer_vs_ping_reply`][source].
The source directory includes `autotest.yaml`, `controlplane.conf`,
`services.conf`, and `gen.py`. The committed captures, not regenerated
packets, are authoritative: batch 002 packet 2 has ICMP identifier/sequence
1/1 in both captures, although `gen.py` specifies 2/2.

The entrypoint is `Test_L3b_ConfiguredRealsGateEcho` in
[`l3b_test.go`](../../l3b_test.go). Its `legacy_*` subcase names refer to
YANET1 source captures, not a compatibility layer.

## Capture contract

`loadEchoPCAPPairs` pins each file's SHA256, Ethernet link type, packet count,
captured/original frame lengths, and exact EOF. Files remain unmodified;
adaptation happens only in memory:

- Require TPID `0x8100` and VLAN TCI 100 (send) or 200 (expect), then remove
  exactly bytes 12:16.
- Copy the matching request's first 12 bytes over the expected frame's MACs.
- Assert source bytes 18 onward equal adapted bytes 14 onward. No L3
  reserialization, checksum recalculation, or padding is allowed.
- Compare raw harness output, including the 42-byte empty-payload IPv4
  frame; the parsed result path pads it to 60 bytes.

## Source mapping

The ten source actions map as follows for [#2621][issue]. Subcase paths below
are relative to `Test_L3b_ConfiguredRealsGateEcho`; each `packet_1/2` entry
denotes two subcases, one per send/expect pair.

| Step | Source action | Target assertion or remaining boundary |
|---|---|---|
| 1 | Update IPv4 routes | Not covered: routed egress, logical ports, ACL handoff, and neighbors. |
| 2 | Update IPv6 routes | Not covered: same topology boundary as step 1. |
| 3 | Enable five reals and flush | Adapted: owning API enables singleton source reals before seed-service publication; five protocol service tuples become three independent fixtures. No CLI/flush parity. |
| 4 | Inspect enabled VIP `10.0.0.20` | API setup, successful UDP seeding, exact session readback, and final enabled-real inspection; packet counter deltas replace CLI accounting. No preseed inspection. |
| 5 | Inspect enabled VIP `10.0.0.21` | API setup and UDP/session assertions prove the seed enabled; enabled CLI inspection remains untested. Final inspection observes disabled state, not this step. |
| 6 | Send batch 001 | `IPv4/enabled_configured_reals_reply/legacy_001_packet_1/2`: two exact IPv4 Echo replies, VIP `10.0.0.20`, real `101.0.0.1`. |
| 7 | Send batch 002 | `IPv6/enabled_configured_reals_reply/legacy_002_packet_1/2`: two exact IPv6 Echo replies, VIP `2005:dead:beef::1`, real `2010::2`. |
| 8 | Disable the TCP real for `10.0.0.21` and flush | Adapted: replacement state and empty ring are configured before publication; seed and replacement services borrow the same independent session table. No live state/ring mutation or CLI/flush parity. |
| 9 | Inspect disabled real | Final inspection asserts disabled state; Echo leaves real counters unchanged. No absolute-zero display assertion after UDP seeding. |
| 10 | Send batch 003 | `IPv4/disabled_ipv6_real_replies_to_legacy_echo/legacy_003_packet_1`: exact IPv4 Echo reply from `10.0.0.21` to `1.1.0.2` with IPv6 real `2010::1` disabled. This is not IPv6 Echo coverage. |

The sixteen generated cases are target-only vectors: IPv4/IPv6 empty,
enabled, disabled, zero-weight, unmatched-destination, restrictive-source-filter,
empty-session-table, and missing-session-key states. The six added reply
cases retain nonempty configured reals and do not use source captures.
Together with the five source packet pairs, the matrix has 17 state cases
and 21 packet subcases; the original 11 states and 15 packets remain.
Exact VIP rules supplement the generated destination
rules. UDP seed encapsulation uses source networks `192.0.2.0/24` and
`2001:db8:3::/64`, not the source balancer's outer addresses; Echo L3 bytes
remain unchanged.

## State invariants

By default, each unique Echo source seeds two UDP sessions through the normal worker
path after seed-service publication: ports 2048/2049 for IPv4 or 32768/32769
for IPv6. The first aliases Echo type/code if session lookup is incorrectly
reached; the second is a same-source sentinel. Mock time starts at
1700000000, with UDP timeout 600 seconds and Other timeout 37 seconds.
Page-size-one reads check all records, expiries, cursor progression, the
empty terminal page, and dataplane time after seeding, after replacement, and
before/after each Echo. Time advances one second before each Echo; seeded
expiries remain unchanged and in the future.

The empty-session-table cases send no UDP seeds and explicitly assert zero
records and the sole terminal cursor `0` after seeding and before/after Echo.
The missing-session-key cases instead seed both ports from `10.0.0.2` or
`2001:db8::2`, leaving the original Echo source unchanged. Exact readback
proves those unrelated records exist and the Echo key is absent. Nonempty
tables retain the minimum-two-record and complete unordered-record checks.
All modes publish the seed service first, then seed UDP sessions if needed,
then publish the replacement service over the same independent session table.

Counter baselines are taken after final publication for each packet.
Incoming/replied packet and byte deltas are checked; filter rejection,
empty-ring, disabled-real, and all real counters remain unchanged. Counter
reset or inheritance across publication is not asserted. The restrictive
source-filter cases restrict the final source networks to `192.0.2.0/24` and
`2001:db8:ffff::/64`, excluding generated Echo sources `10.0.0.1` and
`2001:db8::1`. The retained destination port 80 independently excludes Echo's
filter port 0. Both collision/sentinel UDP sessions are seeded before these
network restrictions: Echo must still reply without filter rejection.
All other cases use a permissive final destination-port range, isolating the
empty-table and missing-key contracts from source filtering. Correct Echo
handling bypasses both source filtering and session lookup.

This is bounded Echo coverage, not complete source-scenario parity: routing,
CLI/flush behavior, full per-protocol accounting, and health/readiness remain
outside these assertions.

## Verification

From the repository root in a prepared Linux Go/CGO environment with a built
dataplane:

```sh
go test -count=1 -v ./modules/l3b/tests/functional -run '^Test_L3b_ConfiguredRealsGateEcho$'
```

[Passing CI for revision `7c1b548e9224477bdc8af12e7fbee761da5200c0`][ci]
records `make test-asan-only` on Ubuntu 24.04 and a passing
`modules/l3b/tests/functional` package result. That log is package-level,
not a verbose per-subcase transcript. It does not establish coverage of the
six new restrictive-filter, empty-table, and missing-key cases.

[source]: https://github.com/yanet-platform/yanet/tree/6b30c2f376c5148a661eadb57fd316d3b453e085/autotest/units/001_one_port/056_balancer_vs_ping_reply
[issue]: https://github.com/yanet-platform/yanet2/issues/2621
[ci]: https://github.com/yanet-platform/yanet2/actions/runs/35081058745/job/104749137591
