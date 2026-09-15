# 056_balancer_vs_ping_reply - source fixture provenance (C1 #2621)

This directory holds exact copies of the ping-reply fixtures used by the
`Test_L3b_ConfiguredRealsGateEcho` legacy packet cases in
`modules/l3b/tests/functional/l3b_test.go`, plus this record. The bytes below
are the source of truth; every adaptation the test applies happens in code at
load time and is described here. The mapping below links each source step and
packet pair either to the passing assertions it is verified by or to an
explicit remaining obligation. Durable verification data for every covered
row lives once, in the [Run record](#run-record) at the end of this file.

## Source

- Repository: `yanet-platform/yanet`
- Commit: `6b30c2f376c5148a661eadb57fd316d3b453e085` (pinned)
- Directory: `autotest/units/001_one_port/056_balancer_vs_ping_reply`
- Pinned sources: [autotest.yaml](https://github.com/yanet-platform/yanet/blob/6b30c2f376c5148a661eadb57fd316d3b453e085/autotest/units/001_one_port/056_balancer_vs_ping_reply/autotest.yaml), [controlplane.conf](https://github.com/yanet-platform/yanet/blob/6b30c2f376c5148a661eadb57fd316d3b453e085/autotest/units/001_one_port/056_balancer_vs_ping_reply/controlplane.conf), [services.conf](https://github.com/yanet-platform/yanet/blob/6b30c2f376c5148a661eadb57fd316d3b453e085/autotest/units/001_one_port/056_balancer_vs_ping_reply/services.conf), [gen.py](https://github.com/yanet-platform/yanet/blob/6b30c2f376c5148a661eadb57fd316d3b453e085/autotest/units/001_one_port/056_balancer_vs_ping_reply/gen.py).
- Fetch: read-only `gh api` commit/contents/blob lookups (`vibe/stage3-fetch.py`
  in the task worktree); every blob verified against its Git SHA1 and declared
  size. The `gen.py` generator was never executed.

The committed PCAPs are authoritative. One recorded divergence exists:
batch 002 packet 2 carries identifier/sequence 1/1 in the committed pcaps
although the `gen.py` text says 2/2. The pcap bytes win; the generator is
recorded only as provenance, and nothing here attributes intent for the
difference.

## Source fixture manifest

All ten source files, with Git blob IDs and SHA256:

| Source file | Git blob | SHA256 | Bytes | Role |
|---|---|---|---|---|
| autotest.yaml | f1339298680f18aa1029a11a71aa4f4b42fca939 | cab7857a78122ace0757e87d6ef3293a7ddc9dfc0fad045ae66972e26acc2f47 | 2724 | 10-step scenario |
| controlplane.conf | 23562586b4310555c35402651022d5533ee67a0f | 9de6ae3fd7b2c22d7055cc58ecddf9f8e90bf59659c22d1b9482f1c3e8c354bc | 1158 | module topology |
| services.conf | b68b21016934c2589a2017496b2acdb556b99775 | 6b725f7b4004df80d77c624df313a3fa35638a2128bace236b1d3e12bc2c0f16 | 913 | 5 services |
| gen.py | 26a4937d15008922c9d3ba4c50cab187c15d1153 | d93f7c74dd71660bce9a71a99f65f66ba5063af5ce475c3fda229ada22517a8c | 2591 | generator text (not run) |
| 001-send.pcap | 87809a9fd9810e5016d1e4e8205bbd0b77f47aeb | ba89034ae9c285a27e34c188f7dc0286f86b207ffe0fa109afb215ac0e02d1ce | 184 | 2 packets, VLAN 100 |
| 001-expect.pcap | f2d26a202ce383dbcc66e7994eb87f94e6ace759 | a271a431ec9f26099f18086de34c82c80b3009ddf83d1b86db3bb6f6170d9b3c | 184 | 2 packets, VLAN 200 |
| 002-send.pcap | e81820dac61b7e49099790b25399b255bacb730b | b216bdc0b291864cd4e714a7ed74b2b0fd004671e8fdf904d99b9fdce0516de3 | 224 | 2 packets, VLAN 100 |
| 002-expect.pcap | d3249dd091b6ddbefd78369170b3d618c561136f | ba3dac9c4d206895e38411b113a11fef99011942b867cc379a9398f63efc611c | 224 | 2 packets, VLAN 200 |
| 003-send.pcap | 5418fd2f3882ae14bdcf322ad2cb443c7f383e58 | d9e5883cbbfc5f147923a5e641e2e1fdf2a515e55a2ecfcbf25d4bee8b725bad | 122 | 1 packet, VLAN 100 |
| 003-expect.pcap | 1d982c49c25c3250a1fb66ce53c49d9f194cf462 | a2be6d70d08e21c642403582bff4fb793eacb7bb1a618b00b06b4e293362ddaf | 122 | 1 packet, VLAN 200 |

Packet inventory (lengths are captured/original bytes with VLAN tag, then
`adapted` after the test strips the tag; `id`/`seq` are ICMP identifier and
sequence; all packets have code 0 and TTL/hop limit 64; all IPv4 header and
ICMP/ICMPv6 checksums, including the IPv6 pseudoheader, were independently
re-verified as valid; MAC addresses, IPv4 header identification, and
identifier/sequence were re-verified directly from the committed bytes:
send frames carry source MAC `00:00:00:00:00:01` to destination
`00:11:22:33:44:55`, expect frames carry source `00:11:22:33:44:55` to
destination `00:00:00:00:00:02`, and every IPv4 header carries
identification 1):

- 001-send, packets 1-2 (source step 6): `1.1.0.1 -> 10.0.0.20`, type 8,
  id/seq 1/1 then 2/2, payload empty then `abcdefghijklmnopqrstuvwxyz0123456789`
  (36 bytes), 46/82 bytes, adapted 42/78.
- 001-expect, packets 1-2 (source step 6): `10.0.0.20 -> 1.1.0.1`, type 0,
  same id/seq and payloads as the requests, 46/82 bytes, adapted 42/78.
- 002-send, packets 1-2 (source step 7): `2000:51b::1 -> 2005:dead:beef::1`,
  type 128, id/seq 1/1 for both (pcap authority; gen.py text says 2/2 for
  packet 2), payload empty then `0123456789abcdefghijklmnopqrstuvwxyz`
  (36 bytes, byte order differs from 001), 66/102 bytes, adapted 62/98.
- 002-expect, packets 1-2 (source step 7): `2005:dead:beef::1 -> 2000:51b::1`,
  type 129, same id/seq and payloads as the requests, 66/102 bytes, adapted
  62/98.
- 003-send, packet 1 (source step 10): `1.1.0.2 -> 10.0.0.21`, type 8,
  id/seq 1/1, payload `abcdefghijklmnopqrstuvwxyz0123456789`, 82 bytes,
  adapted 78.
- 003-expect, packet 1 (source step 10): `10.0.0.21 -> 1.1.0.2`, type 0,
  id/seq 1/1, same payload, 82 bytes, adapted 78.

## Files in this directory

The six committed `*.pcap` files are byte-identical copies of the source
blobs above (same SHA256 values); no VLAN, MAC, or L3 byte is pre-modified in
the files themselves. `README.md` (this file) is documentation only and is
deliberately excluded from the code-and-fixture identity hashes.

## Adaptation rules applied by the test, not by the bytes

- VLAN removal is L2-only: the loader requires TPID 0x8100 at bytes 12:14,
  requires TCI 100 on send and 200 on expect captures, and removes exactly
  bytes 12:16 (the four tag bytes). Nothing else is stripped.
- Expected-MAC rewrite: only the first 12 bytes of an adapted expect frame
  are overwritten with the first 12 bytes of the matching request frame, so
  the harness Ethernet addressing matches while the pinned L3 oracle is kept.
- Committed L3 is never recomputed: the test asserts byte equality between
  the capture payload from byte 18 (post-tag L3 onward) and the adapted frame
  from byte 14, before any parsing. Checksums, addresses, TTL/hop limit,
  type/code, identifier, sequence, and payload are pinned by these committed
  bytes; the test never serializes, pads, or recalculates them.
- Strict loading: pinned per-file SHA256, Ethernet link type, exact per-
  packet captured/original lengths and counts, exact EOF.
- Reply comparison uses the raw segmented-packet harness path. The parsed
  result path pads short frames (42 to 60 bytes for the empty-payload first
  packet); attempt `stage3-01` failed on exactly that artifact and was
  corrected test-only to compare raw output/drop bytes. See
  [Run record](#run-record).

## Source step to target mapping

Source steps are the ten actions of `autotest.yaml`. The target is the single
`Test_L3b_ConfiguredRealsGateEcho` matrix (11 state cases, 15 packet
subcases). Covered rows verify through the [Run record](#run-record).

| Step | Source action | Target mapping | Status |
|---|---|---|---|
| 1 | ipv4Update two routes | Routed egress topology is not modeled; replies are compared as expected bytes. Remaining obligation. | not covered |
| 2 | ipv6Update two routes | Same as step 1. | not covered |
| 3 | enable 5 reals + flush | Adapted: singleton legacy reals created and enabled through the owning service API, pre-publication; no CLI parity. | covered (adapted) |
| 4 | cli_check balancer0 10.0.0.20 | Adapted: the enabled seed is established through the owning service API and proven by successful UDP seeding plus the exact seeded-session readback; the final replacement inspection asserts these reals enabled with family, address, and source network; counter deltas asserted per packet subcase. There is no preseed Inspect call and no complete CLI per-protocol accounting reproduction. | covered (adapted) |
| 5 | cli_check balancer0 10.0.0.21 | The enabled state for this VIP is established through the owning API, proven by successful UDP seeding and the exact session readback; the source's enabled CLI inspection and output remain un-replicated (remaining obligation). The final target inspection of this VIP observes the disabled state after step 8 and is not enabled-inspection evidence. | covered (adapted, CLI inspection remaining) |
| 6 | sendPackets 001 | batch001: `legacy_001_packet_1`, `legacy_001_packet_2` byte-oracle subcases. | covered |
| 7 | sendPackets 002 | batch002: `legacy_002_packet_1`, `legacy_002_packet_2` byte-oracle subcases. | covered |
| 8 | disable 10.0.0.21 tcp real + flush | Adapted: safe pre-publication replacement - the disabled state and empty ring are configured on the replacement service, which adopts the seeded session table at Publish; no live-table mutation. | covered (adapted) |
| 9 | cli_check disabled row | Inspect asserts the disabled real; the Echo asserts no real counter delta after the post-publication baseline. The source absolute-zero display is not claimed: UDP seeds ran earlier in the test. | covered (adapted) |
| 10 | sendPackets 003 | batch003: `disabled_ipv6_real_replies_to_legacy_echo` state with the single `legacy_003_packet_1` subcase replying with the IPv6 real disabled. | covered |

UDP seeding happens after the seed service publication and before the final
replacement publication; seeds insert sessions through the normal worker
path, so live-table insertion is expected and never denied. The target
claims only that replacement state and ring are configured before their
publication (no unsafe state/ring mutation) and adopted at Publish.

## Packet pair mapping

The five send/expect packet pairs map to the five legacy subcase names, given
here as full subtest paths; every covered row and pair resolves the single
[Run record](#run-record) anchor:

| Pair | Source packets | Exact target subcase path | Exercised by |
|---|---|---|---|
| 1 | 001 packet 1 | `Test_L3b_ConfiguredRealsGateEcho/IPv4/enabled_configured_reals_reply/legacy_001_packet_1` | IPv4 enabled state |
| 2 | 001 packet 2 | `Test_L3b_ConfiguredRealsGateEcho/IPv4/enabled_configured_reals_reply/legacy_001_packet_2` | IPv4 enabled state |
| 3 | 002 packet 1 | `Test_L3b_ConfiguredRealsGateEcho/IPv6/enabled_configured_reals_reply/legacy_002_packet_1` | IPv6 enabled state |
| 4 | 002 packet 2 | `Test_L3b_ConfiguredRealsGateEcho/IPv6/enabled_configured_reals_reply/legacy_002_packet_2` | IPv6 enabled state |
| 5 | 003 packet 1 | `Test_L3b_ConfiguredRealsGateEcho/IPv4/disabled_ipv6_real_replies_to_legacy_echo/legacy_003_packet_1` | IPv4 `disabled_ipv6_real_replies_to_legacy_echo` state |

Each pair asserts the expect frame byte-for-byte as the harness output, with
the incoming and replied counters checked against the same frame lengths.

## Target matrix

The original ten C6 Echo cases keep their ten generated vectors exactly, with
target-only subcase names (`generated` plus per-state names). The enabled
cases override the two-real C6 fixture with the source singleton reals
(`101.0.0.1` for IPv4 batch 001, `2010::2` for IPv6 batch 002). The source
`services.conf` defines five service tuples; the target coalesces them into
three separate legacy fixture instances. Each adds its exact VIP Echo rule
(`10.0.0.20/32`, `10.0.0.21/32`, `2005:dead:beef::1/128`) alongside the
preserved generated destination rules (`192.168.1.0/24`, `2001:db8:1::/64`).
The new IPv4-only case 003 adds the single packet for VIP `10.0.0.21/32`
with disabled IPv6 real `2010::1`. Totals: 11 state cases, 15 Echo packet
subcases (10 generated, 5 legacy).

## Session seeding contract

Order: the seed service is created with its states and rings while
unpublished, then published; every unique Echo source is then seeded with two
real UDP flows at source ports 2048/2049 (IPv4) or 32768/32769 (IPv6) - the
first port aliases ICMP type/code under Echo session lookup, the second is
the same-source sentinel. Mock time starts at T0 = 1700000000 with UDP
timeout 600 seconds and Other timeout 37 seconds. The replacement service is
then configured and published, adopting the seeded table. Sessions are read
back through page-size 1 reads to the terminal empty page - full records,
expiries, and cursor path, with the dataplane clock checked on every page -
after seeding, after adoption, and again after the one-second time advance
before every Echo. Expiries stay at T0+600 and in the future throughout.

The final source filter deliberately uses the permissive port range
0..65535 so that an Echo wrongly taking the session-lookup branch would
still reach the collision path. The correct Echo handling decides before the
source filter, so these tests do not exercise the actual filter branch for
Echo packets.

Outer seed addresses are adapted: the target reals advertise source networks
`192.0.2.0/24` and `2001:db8:3::/64` for UDP seed encapsulation, and the
source balancer's outer addresses (`100.0.0.22`, `2000:51b::1`) are not
configured. The committed Echo packets keep their original addresses, so the
pinned L3 oracle is unaffected.

## Counter contract

Counter baselines are read only after the final service and module
publication, immediately before each packet. Incoming and replied packet and
byte deltas carry the expectations; `filter_rejected`, `ring_empty`,
`real_disabled`, and every current real counter must not move. No
cross-publication inheritance is asserted and no reset is performed or
asserted; underlying counter values may persist across publications and are
simply outside every asserted contract.

## Run record

Identity:

- Target branch/worktree HEAD at this record:
  `93d82875fea109fd2470ab5efcc90e17a0356105`
  (`test/l3b-echo-provenance`).
- Frozen C6 dependency (inherited patch file, never changes):
  `fc2cf652e4052d687e53d2829703c276ee9411c93414b4fffc3fd30e734db4fc`.
- Code-and-fixtures identity, README excluded. Covered paths:
  `modules/l3b/dataplane/process.h`, `modules/l3b/tests/functional/l3b_test.go`,
  `modules/l3b/tests/functional/testdata/056_balancer_vs_ping_reply/*.pcap`
  (six pcaps). Algorithm: a temporary git index (never the real index) is
  seeded from HEAD, the inherited C6 patch is applied with `--cached`, the six
  pcaps are intent-to-add, then `git diff --binary` over exactly those paths
  gives the C1-only hash and `git diff --binary HEAD` over the same paths the
  combined hash. This README is excluded to avoid self-reference.
  - C1-only (process.h diff empty):
    `1d404cc5d0c936f34a1ef51007dae680cccbaf73794a4ea990fa331fac4e1a04`
  - Combined (includes the inherited process.h change):
    `965c2b193aa2c2be06eba60d440b252cc08acc8bf369551d3aa23cbc157f16c9`
- Full C1-only and combined patches including this README are saved for lead
  review as `vibe/2621-c1only.patch` and `vibe/2621-combined.patch` in the
  task worktree; their hashes are recorded in the worktree-local
  `vibe/2621-evidence.md` only, after this document was final.

Executable byte manifest (SHA256, local worktree == native candidate,
verified by read-only `sha256sum` over SSH):

- `modules/l3b/dataplane/process.h`
  `21f015db1b4cf798cdf5ec1ed9470794e45da7ecc18773748508374018c66317`
- `modules/l3b/tests/functional/l3b_test.go`
  `5602ae08164843a52317210f8c160cf595daadfe529187e733a9dc3c8effbfbc`
- six pcaps: identical to the source manifest SHA256 values in the table
  above.

Native platform and toolchain: host `moonug-dev.sas.yp-c.yandex.net`, Linux
5.15.0-170 x86_64; container image `yanet2-dev:latest`, immutable ID
`sha256:037dc6d09939a666229c93c157cbde372b341f82b2f83628a9ffcf808bdb3d87`
(linux/amd64); Go 1.24.13, GCC 13.3.0, Ninja 1.11.1, Meson 1.12.0 (container
venv), golangci-lint v2.12.2 (container-only). Task root
`/extra_disk_2/projects/l3b-2621-native-20260915-run01`, own candidate tree
and caches; no other root written.

Replay commands for the prepared native tree (not a verbatim attempt log).
Prepared environment per attempt - inside the container launched as
`docker run --rm -i --name <attempt> --cpuset-cpus 0-31 --cpus 6 --memory 12g
-v /extra_disk_2/projects/l3b-2621-native-20260915-run01:/extra_disk_2/projects/l3b-2621-native-20260915-run01
-e MESON_NUM_PROCESSES=2 -e GIT_CONFIG_COUNT=1 -e GIT_CONFIG_KEY_0=safe.directory
-e GIT_CONFIG_VALUE_0='*' --entrypoint /bin/sh yanet2-dev:latest -s` - the
candidate is at `/extra_disk_2/projects/l3b-2621-native-20260915-run01/candidate`
with `GOCACHE=<root>/cache-candidate`, `GOMODCACHE=<root>/mod-candidate`, and
Meson 1.12.0 from a container venv on PATH:

```sh
cd /extra_disk_2/projects/l3b-2621-native-20260915-run01/candidate
# release phase (stage3-02; baseline01 had the one-time
# `meson setup build -Dbuildtype=release`)
unset CGO_CFLAGS CGO_LDFLAGS UBSAN_OPTIONS
meson setup --reconfigure build -Dbuildtype=release -Doptimization=2 -Db_sanitize=none
meson introspect build --buildoptions
make go-cache-clean
meson compile -C build -j2
export CGO_CPPFLAGS="-DYANET_CACHE_LINE_SIZE=$(awk '$2 == "RTE_CACHE_LINE_SIZE" { print $3; exit }' build/subprojects/dpdk/rte_build_config.h)"
go test -count=1 -v ./modules/l3b/tests/functional -run '^Test_L3b_ConfiguredRealsGateEcho$'
go test -count=1 -v ./modules/l3b/tests/functional
go test -count=1 -v ./modules/l3b/controlplane -run '^Test_RingFromWeights_AllZeroIsEmpty$'
# debug race phase (stage4-debug01)
make setup-debug
make go-cache-clean
meson compile -C build -j2
go test -race -count=10 -v ./modules/l3b/tests/functional -run '^Test_L3b_ConfiguredRealsGateEcho$'
# ASan+UBSan phase (stage4-asan01)
meson configure build -Dbuildtype=debug -Doptimization=0 -Db_sanitize=address,undefined
meson introspect build --buildoptions
make go-cache-clean
meson compile -C build -j2
export CGO_CPPFLAGS="-DYANET_CACHE_LINE_SIZE=$(awk '$2 == "RTE_CACHE_LINE_SIZE" { print $3; exit }' build/subprojects/dpdk/rte_build_config.h)"
export CGO_CFLAGS=-fsanitize=address,undefined CGO_LDFLAGS=-fsanitize=address,undefined
export UBSAN_OPTIONS=halt_on_error=1:abort_on_error=1:print_summary=1:print_stacktrace=1
go test -count=1 -v ./modules/l3b/tests/functional
go test -count=1 -v ./modules/l3b/tests/functional -run '^Test_L3b_ConfiguredRealsGateEcho$'
# lint/vet/build phase (stage4-lint01)
unset CGO_CFLAGS CGO_LDFLAGS UBSAN_OPTIONS
make setup-debug
make go-cache-clean
meson compile -C build -j2
go test -count=1 ./lint/style/cmd/stylelint/
go run ./lint/style/cmd/stylelint/
make proto-go
/tmp/2621-lint/golangci-lint-2.12.2-linux-amd64/golangci-lint run ./modules/l3b/tests/functional/...
go vet ./modules/l3b/tests/functional/...
go build ./modules/l3b/tests/functional/...
```

Gates, all synchronous, container bounds cpuset 0-31 / 6 CPUs / 12 GiB,
`MESON_NUM_PROCESSES=2`, bounded `-j2` C compile, 1800 s gate limits, cacheline
`CGO_CPPFLAGS=-DYANET_CACHE_LINE_SIZE=64` extracted from the generated DPDK
config:

| Attempt | Command (from task worktree) | Result | Log (local copy, SHA256) |
|---|---|---|---|
| stage2-01 | `sh vibe/run01-sync.sh`; `ssh -T -o BatchMode=yes -o ConnectTimeout=15 moonug-dev.sas.yp-c.yandex.net 'ATTEMPT=stage2-01 /bin/sh' < vibe/run01-gates.sh` | matrix 10/10, full package, zero-weight, all 0 | `vibe/2621-native-logs-run01/stage2-01-matrix.log` `198566728f4958aadaec997b7290f498eaa5cf82a3be386a8c05d0209baa0042` |
| stage3-01 | same pattern, `ATTEMPT=stage3-01` | FAILED: `legacy_001_packet_1` expected 42 vs reported 60 | `vibe/2621-native-logs-run01/stage3-01-matrix.log` (retained) |
| stage3-02 | same pattern, `ATTEMPT=stage3-02` | 11 states + 15 packet subcases PASS, full package PASS, zero-weight PASS, oom 0 | `.../stage3-02-matrix.log` `f2616732778102b6c1dc700a035d31804aea3ff612181e027f4604a9b195c953`; `.../stage3-02-functional.log` `7bfd21c7e68a1f6711cdf76ef581b0c4c7687d537a0cba6c637f02ebd6530a48`; `.../stage3-02-zero-weight.log` `43fe7ed0a47a452e7dd47269269b8a10a4db339a381d6a9a26fb006df19e26de` |
| stage4-debug01 | `ssh ... 'ATTEMPT=stage4-debug01 PHASE=debug /bin/sh' < vibe/run01-stage4.sh` | race `-count=10`: 110 state + 150 Echo PASS executions | `.../stage4-debug01-race.log` `8f11b2a90c732ede14f58774f1e927f3adf4bb7d131ae47eaff8697afb0efdac` |
| stage4-asan01 | `ssh ... 'ATTEMPT=stage4-asan01 PHASE=asan /bin/sh' < vibe/run01-stage4.sh` | ASan+UBSan full package PASS; matrix 11+15 PASS | `.../stage4-asan01-functional-asan.log` `c713e5146ac73aebd46bbb3e4e8f77a42a812b55f4a0c83767e53e85eb7402a5`; `.../stage4-asan01-matrix-asan.log` `d9a734a829f89e4e56345a78b2363270d98c513f61c787037d164252bd3a5c04` |
| stage4-negative03 | `ssh ... 'ATTEMPT=stage4-negative03 PHASE=negative /bin/sh' < vibe/run01-stage4.sh` | intended FAIL: 15/15 packet subcases, expiry-only mismatch | `.../stage4-negative03-matrix-negative.log` `a165e1bdca1e3c8694f4be01897e22d2054a7f3e6643aaa6e4e033ffd62f7a92` |
| stage4-restored01 | `ssh ... 'ATTEMPT=stage4-restored01 PHASE=restored /bin/sh' < vibe/run01-stage4.sh` | 11 states + 15 packet subcases PASS, pristine test hash restored | `.../stage4-restored01-matrix-restored.log` `ffd6d13c5a2c4ab91e3383c43b81bc4796fefbfe90e4f386e69ba7ac782df638` |
| stage4-lint01 | `ssh ... 'ATTEMPT=stage4-lint01 PHASE=lint /bin/sh' < vibe/run01-stage4.sh` | style test+run, proto-go, golangci v2.12.2 scoped, vet, build - all 0 | `.../stage4-lint01-golangci.log` `e92606b0bf483111dff0a120c315ea165821348f31365020e2468a0059095c47`; other lint logs retained (empty on success) |

Strict sanitizer environment (stage4-asan01):
`CGO_CFLAGS=-fsanitize=address,undefined`,
`CGO_LDFLAGS=-fsanitize=address,undefined`,
`UBSAN_OPTIONS=halt_on_error=1:abort_on_error=1:print_summary=1:print_stacktrace=1`.

Log excerpts. From `stage3-02-matrix.log` (the current code; abridged - the
two PASS lines are trimmed of their leading indentation):

```text
--- PASS: Test_L3b_ConfiguredRealsGateEcho/IPv4/disabled_ipv6_real_replies_to_legacy_echo/legacy_003_packet_1 (0.00s)
ok  	github.com/yanet-platform/yanet2/modules/l3b/tests/functional	0.586s
```

Exact lines 16-17 of `stage4-negative03-matrix-negative.log` (expected
increment versus actual, decimal, verbatim):

```text
-  ExpiresAt: (uint64) 1700000600000000001
+  ExpiresAt: (uint64) 1700000600000000000
```

Historical failures, recorded truthfully and concisely: `stage3-01` failed
only `legacy_001_packet_1` because the parsed harness result pads short
frames (42 to 60 bytes); corrected test-only via the raw segmented path, no
dataplane defect. `stage4-negative01` failed at build because the runner
mistakenly selected optimization 3 (unrelated `-Werror` stringop diagnostic);
runner corrected to repository-default -O2. `stage4-negative02` failed on a
task-local assertion that did not accept Meson 1.12.0's `['none']` sanitizer
representation; only that assertion was corrected. The negative control
itself (`stage4-negative03`) failed exactly the 15 packet subcases on the
one-nanosecond expected-expiry increment and nothing else, after which the
pristine test (`5602ae08164843a52317210f8c160cf595daadfe529187e733a9dc3c8effbfbc`)
was restored and re-passed (`stage4-restored01`). A stage 2 sync once carried
a macOS AppleDouble file, removed from the candidate after the gates; the
sync now excludes metadata files. Every attempt above, starting with
`baseline01`, ran on this task's root
`/extra_disk_2/projects/l3b-2621-native-20260915-run01` at base
`93d82875fea109fd2470ab5efcc90e17a0356105`. The historical C6-era native
evidence lives in the separate older root
`/extra_disk_2/projects/l3b-2625-native-20260915-diag01` at base
`837fcb7bbe8e050b4b8d1eccd6e382fa30b8fbfd` and includes a separate,
unresolved baseline pdump instability; that older root is not part of this
task's gates.

Boundaries: this mapping does not claim complete source parity, health,
readiness, qualification, or deployment. TSan, the VM suite, and unrelated
full-repository gates are out of scope for this task. Stage 6 external review
had not been initiated when this record was written.

Remaining obligations: source steps 1-2 (routed egress topology: logical
ports, ACL handoff, route neighbors) are not exercised by the target harness;
CLI command and flush parity is not replicated; the source's enabled-state
CLI inspection output for VIP `10.0.0.21` (step 5) is not reproduced;
complete per-protocol CLI accounting columns are not reproduced;
absolute-zero counter displays are not claimed (delta-based assertions only).
