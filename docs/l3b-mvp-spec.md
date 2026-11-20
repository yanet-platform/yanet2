# l3b Full MVP Specification

Status: **candidate; accountable approval outstanding; runtime evidence NOT RUN**.
This is the integration spine, not a smaller fixed-topology MVP or a production
pilot. It does not close an issue, select production grants or authorize a run.

## Document authority

The [representation contract](l3b-contract.md) owns F01–F41, O01–O23 and C01–C08;
the [packet contract](l3b-packet-contract.md) owns packet admission/disposition;
the [observation contract](l3b-observations-contract.md) owns external health.
Those requirements remain binding target semantics. Engineering proposals below
do not silently override them. A conflict must be resolved in the owning contract
and corresponding language version before dependent implementation.

Four separate outcomes are tracked: semantic approval, verified implementation,
integration admission, and complete issue acceptance. A delivered slice may
support one outcome without satisfying the others. All test vectors here are
obligations, not passing execution evidence.

## Baselines and reproducibility

| Input | Revision and use |
| --- | --- |
| Target representation/packet text | PR [#3007](https://github.com/yanet-platform/yanet2/pull/3007), `917f23e7ffb153bb2a46dea72e1673c3906a0ed5`; candidate, not implementation. |
| YANET2 implementation | `fc194e3b7e7bdd7170d4a3317b20f99a9e12c357`; source inspection only. |
| YANET1 historical comparison | `6b30c2f376c5148a661eadb57fd316d3b453e085`; do not call this current YANET1. |
| YANET1 available newer tree | `af3531a88a8c5b0ab140353686ab23d13cd9e200`; scheduler/controlplane/worker differences and the bounded fixture denominator are reconciled below, not universal behavioral equivalence. |
| Linux/IPVS comparison | Linux v6.12, `adc218676eef25575469234709c2d87185ca223a`; a fixed comparison baseline, not a claim about the newest kernel. |

Source inspection is not a traffic test. A cited implementation is not a
normative specification for l3b. Examples compare fixed inputs, not universal
traffic-share, progress or churn guarantees.

## Complete delivery inventory

| ID | Outcome and acceptance boundary | Existing public work |
| --- | --- | --- |
| M01 | Representation, presence, full-key identities, permits/CAS, errors and cancellation agree across direct clients and CLI. Given a stale token and equal replacement, the server returns Aborted without mutation. | #2718–#2722, #2719, #2770/#2772/#2773 |
| M02 | Atomic structural publication, last-valid state, recoverable ownership and reader-safe retirement. Given failure before acceptance, no candidate becomes authoritative; after acceptance, recovery settles that same outcome without a second publication. | #2723–#2727, #2729/#2730, #3243 |
| M03 | VS-owned sessions,complete transport-aware keys,concurrent authority,grow-only front/back,no resurrection and bounded inspection. l3b operator initiates autogrow through O14 with CAS/admission/back limits; parameters remain open. Front supersession followed by expiry/reuse never restores old back authority. | #2724/#2728,#2743–#2747,#2623 |
| M04 | WRR/HASH/WLC, zero-weight and health eligibility, complete all-worker WLC samples and guarded publication/confirmation. Given one missing required worker, no partial sample is published. | #2762–#2765; reuse calculator #3125 |
| M05 | External common ACL and one internal ACL per first-class Service grouping VSes; one immutable parent per VS. ACL operator owns Service/ACL. Whole-policy replacement,native COUNT/terminal semantics,compiler ownership and retained identity. Unavailable runtime policy fails closed; no independent member-VS ACL updates. | #2736–#2740 |
| M06 | Ordinary packets,sources,IPIP/GRE,Echo/ICMP,safe buffers,lengths,metadata and explicit RFC deviations. Separate set_mss before l3b clamps an existing TCP/IPv6 MSS(default1220),never inserts. Preparation failure writes no session. | #2742/#2748/#2749/#2750–#2755/#2760/#2761 |
| M07 | Read-only ownership query, unique ingress/service association and bounded unrdup fanout. Given indeterminate ownership, no clone or token spend occurs. | #2756–#2759; shared #2011 |
| M08 | Accepted readback versus live status,all-worker adoption,counter identity/arithmetic and cleanup visibility. An idle lagging relevant worker prevents ADOPTED. | #2731–#2734/#2746/#3121 |
| M09 | Actual-hop authorization,health fencing/freshness,operator lifecycle and announcement/withdrawal. Both operators consume l3b adapter desired snapshots; ACL operator publishes a separate result. First/repeated announcement requires confirmed required ACL for current Service/cohort. Delayed A→B→A input cannot certify later A. | #2735/#2775/#2776 |
| M10 | Installed CLI and versioned incompatible migration, not merely a workspace build. Given a packaged installation, the l3b command is discoverable and rejects unsupported legacy input. | #2719/#2720/#2771–#2773 |
| M11 | Reusable Lab reports, protected scenario, reset/cleanup and authorized stand/load automation. Given failed preparation, cleanup releases only resources owned by that run and produces a failure artifact. | #2716/#2766–#2769/#2717 |
| M12 | Full compatibility/source-assertion mapping, predeclared qualification and accountable launch verdict. Given missing evidence or an unapproved metric, launch remains unqualified. | #2770–#2774/#2717 |

Cross-cutting work is path-specific: #2791 head-segment reads, #2792 logical IP
lengths, #2787 post-encapsulation metadata, #1887/#2004 publication/layout and
#2913/#2839 recovery applicability require actual call-path evidence. Full
repository-wide closure of those issues is not a blanket l3b gate. #3120 remains
research into the accepted DF/MTU/reassembly risk, not a newly invented launch gate.

## Source comparisons

### Schedulers and sessions

YANET1 references below use the historical revision above. Linux links pin the
comparison revision, not a moving branch.

| ID | Evidence actually inspected | l3b disposition and required vector |
| --- | --- | --- |
| X01 WRR/HASH | YANET1 `dataplane/globalbase.cpp:1851–1884` emits contiguous weight runs, truncating when reservation is exhausted. `dataplane/worker.cpp:4220–4278` chooses hash or the worker's roundRobinCounter according to PURE_ROUND_ROBIN. | HASH preserves contiguous authored runs; WRR uses per-worker/per-VS interleaving. Admission rejects overflow rather than silently skipping reals. Verify [4,3,2] and [6,4], reorder, failed preparation and cursor preservation. |
| X02 Interleaved WRR | Linux [`ip_vs_wrr.c`](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/net/netfilter/ipvs/ip_vs_wrr.c), `ip_vs_wrr_init_svc`, `ip_vs_wrr_schedule`: service mark, GCD decrement, initial max−(g−1), service sched_lock and overload filtering. | Fixed eligible positive weights have the contract's threshold ordering. l3b does not inherit IPVS's shared service lock, live mark adjustment or overload flag. Its expanded length remains sum(weights), not sum/g. |
| X03 Adaptive WLC | YANET1 `controlplane/balancer.cpp:980–1129`, `reconfigure_wlc` and `calculate_wlc_weight`, samples connection totals then adjusts hash-selector weights; out-of-range power resolves to default. | l3b retains the adaptive formula, not machine-overflow behavior. Public power >100 rejects. Requested 0 is retained and effective 10. Exact half-up and cap precede narrowing. |
| X04 IPVS WLC | Linux [`ip_vs_wlc.c`](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/net/netfilter/ipvs/ip_vs_wlc.c), `ip_vs_wlc_schedule`; [`include/net/ip_vs.h:1845–1855`](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/include/net/ip_vs.h#L1845-L1855) uses active*256+inactive overhead and cross-multiplied normalized comparisons. | l3b WLC is not IPVS WLC: a complete sampled logical-session count feeds an adaptive hash selector, not per-new-connection least overhead. Test definition of counted authority, not equality to IPVS choices. |
| X05 Invalid pins and failed insertion | YANET1 `dataplane/worker.cpp:4170–4278`: enabled hits,UDP/TCP SYN&&!RST reschedule,insertion failure accounting then forwarding. | Initial SYN&&!ACK&&!RST,including repeat,selects without lookup and requires a new pin write. Other stateful packets keep reschedule permissions. Required-write failure drops; full incarnation and late commit are deliberate safety changes. |
| X06 TCP lifetime | Linux [`ip_vs_proto_tcp.c:365–406`](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/net/netfilter/ipvs/ip_vs_proto_tcp.c#L365-L406) defines state timeouts including CLOSE=10*HZ and separate active-state classification. | Three TCP classes syn/established/time_wait plus udp/other are affinity classification,not endpoint/IPVS state. Any SYN wins; otherwise FIN/RST chooses time_wait,else established. Per-VS class→per-VS tcp→global class→global tcp→60000ms; tcp is fallback,not a class. Absent/null inherits,explicit0 remains. Check WLC until authoritative expiry. |
| X07 UDP OPS | Linux [`ip_vs_core.c:537–551`](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/net/netfilter/ipvs/ip_vs_core.c#L537-L551) makes UDP ONE_PACKET connection objects; [`ip_vs_conn.c:164–183`](https://github.com/torvalds/linux/blob/adc218676eef25575469234709c2d87185ca223a/net/netfilter/ipvs/ip_vs_conn.c#L164-L183) avoids hashing; lines468–485 expire on put/timer. | Every ordinary UDP OPS selects without lookup,insert,refresh,rebind or migration. No old pin is used; switching does not clear tables. No persistent affinity does not mean IPVS allocates no connection object. |
| X08 Newer YANET1 is different | At `af3531a88a8c5b0ab140353686ab23d13cd9e200`, `common/scheduler.h` contains rr/wrr/wlc, not chash; `controlplane/balancer.cpp:925–933` only flushes in its WLC thread. `dataplane/globalbase.cpp:1589–1702`, `wlc_ratio`, `count_real_connections`, `evaluate_service_ring` computes adaptive weights in generation processing, casts the scaled weight and emits contiguous capped runs. | Do not call the historical hash-adaptive construction the current universal YANET1 implementation. l3b's exact Go calculator and selected Make/Adjust/Update behavior are explicit target choices; the newer counter-derived totals do not prove the target logical-session sampler. |

X01–X08 alone do not establish Echo, source-template, MSS/tunnel or unrdup parity.
The following bounded source reconciliation adds the configuration/script/packet
denominator; inspection is not executed compatibility acceptance.

### Legacy assertion denominator and dispositions (D07)

H and N denote the two exact YANET1 revisions in the baseline table. Enumerate
`autotest` text matches for balancer/metabalancer and unit paths bearing those names,
then include every file in each matching unit and every referenced configuration,
generator and PCAP. Also inspect parent dataplane.conf and autotest.cpp setup/
comparison semantics. This yields 17 H units and 14 N units, not just named YAMLs.
All unit paths below are under `autotest/units/001_one_port/`; file counts are
text/PCAP, stored records are send/expect, not runtime results or assertion counts.

| Unit | H files; records | N files; records | PCAP batches and semantic disposition |
| --- | --- | --- | --- |
| 048_acl_balancer_route | 6/16; 96/64 | Same | 001 single enabled real, 002 eligible expansion, 003 exact ports/pools, 005 live pins, 006 no eligible target, 007 surviving pins after disable/enable, 008 all real fragments drop: retained. 004 unconfigured-port routing bypass is external; l3b no-match itself drops. |
| 049_balancer_chash | 4/4; 256/256 | Absent | 001–002 standalone CHASH placement deferred, not the whole fixture: retain UDP state, weights/admin readback, counters, IPIP/source/inner preservation. Exact distributions [5,5,117,1] then [38,36,28,26] are historical, not HASH/WLC targets. |
| 049_balancer_pure_l3 | 4/8; 48/48 | Same | 001–004 retain portless key identity across changed ports, separate actual TCP/UDP and explicit port0. Eligible stateful lookup hits preserve pins. Initial SYN may replace the shared pin and UDP OPS does not use it. Derive portless from absence and reject the legacy flag. Exact scheduler placements require target hash vectors. |
| 049_balancer_real_ipv4 | 4/2; 24/24 | Same | 001 retains v4/v4, v4/v6, v6/v4 IPIP, transport-separated pools/sessions. Seven CLI blocks distinguish session expiry from cumulative counters; no exact 60-second GC promise. |
| 049_balancer_reload | 4/14; 72/72 | Same | 001–007 retain manual-state/session/counter continuity during replacement. GB_* latches/flush are source machinery, replaced by target acceptance/adoption protocol. Exact HASH destinations are source vectors, not target cursor-WRR. |
| 049_balancer_tcp_udp | 4/2; 8/8 | Same | 001 retains separate TCP/UDP VSes at same VIP/port with independent pools; it does not test target mixed-protocol VS shape. |
| 049_balancer_version | 5/0; 0/0 | Same | Four CLI blocks preserve labels "10", "0", arbitrary text and reload changes as F39 metadata, not tokens. Legacy automatic real-label inheritance is not retained; populate independent real metadata explicitly. |
| 049_balancer_wrr | 4/2; 32/32 | Same | 001 is weighted HASH, weights[4,3,2,1], destination histogram[15,11,2,4], not target cursor-WRR. Target threshold sequence is A,A,B,A,B,C,A,B,C,D, modulo initial cursor. |
| 049_metabalancer_decap | 6/14; 23/22 | Same | 001–002 external admitted early-decap followed by retained l3b forwarding. 003/005 nonlocal intact routing; 004/006 local/KNI delivery; 007 one early-decap plus standalone ACL drop/counter: external composition assertions, not l3b ACL or no-match forwarding. |
| 055_balancer_wlc | 5/6; 224/224 | 4/6; 96/96 | 001–003 retain eligible transition/adaptive intent, not interchangeable H/N placement. H batches128/64/32, N32/32/32; H-only check.py computes histograms without an additional pass/fail assertion. Target exact rounded formula, logical sampler and selected chash construction supersede N truncation/contiguous runs. |
| 056_balancer_icmp_rate_limit | 5/2; 10500/10500 | Same | 001 entirely commented in YAML: inactive fixture, not rate-limit evidence. Quote association/wire clone shape retained; external distributor owns query/fanout/original disposition. Explicit budget/all-or-none/trusted association require new vectors; legacy ring saturation has no selected rate threshold. |
| 056_balancer_ops | 4/5; 8/12 | Same | Retain001 TCP state,002 UDP stateless selection and replay002→003-expect after pool change. TCP ops:true migrates to false/absent because target rejects. Add old UDP pins,unavailable lookup,full tables and OPS transitions; exercise SYN under the new no-lookup rule. |
| 056_balancer_vs_ping_reply | 4/6; 5/5 | Same | 001 IPv4 empty/payload Echo, 002 IPv6, 003 all reals disabled: retain one VIP reply, identity/sequence/payload, independent real state. Egress hop64 is initial65 minus external route hop, not initial64. |
| 065_balancer_gre | 4/8; 16/16 | Same | 001–004 all inner/outer family pairs, GRE protocol47, zero flags/version, EtherType0800/86dd, inner preservation/source derivation retained. Optional GRE rejection, headroom/length failures and distinguishing ID/TC vectors remain target tests. |
| 068_balancer_outer_source_network | 4/22; 22/22 | Same | 001–011 retain literal-v4 and byte-ceiling-v6 splice cases: /64,/80,/66,/128,/126,/119,/103,/95,/77. Legacy v4 CIDR input migrates to bare host, including 123.0.12.0; do not normalize it as a network or invent rejection of a .0 host. |
| 080_balancer_pure_round_robin | 4/2; 12/12 | Absent | 001 retains equal-weight cyclic UDP OPS versus repeated hash choice; PURR intent→WRR, weighted-hash intent→HASH. Per-worker/per-VS cursor deliberately replaces worker-global/cross-VS coupling and exact initial real. |
| 083_balancer_dscp | 4/8; 64/64 | Absent | 001 omitted inheritance, 002 always, 003 onlyDefault, 004 never across all family pairs. Preserve inner/ECN; explicit external post-l3b dscp supplies marking. Reject old l3b marking fields, do not silently synthesize conflicting shared-real policy. |

Totals: H 75 text +121 PCAP, 11410 stored send/11381 expect records; N62+107,
10950/10921. The inactive ICMP family contributes 10500/10500 to each total and
must not count as an active test. OPS reuses one send file against two expectations;
stored-file counts and scheduled sends differ. Independent offline header/record
enumeration reproduced these totals; no packet was sent. Only 048 YAML allocator
assertions, WLC contents and three removed units differ in these bounded trees.

Configuration migration is assertion-specific: legacy rr means HASH/unit weights;
legacy wrr means weighted HASH; PURR means target WRR. Never translate by spelling.
Every retained wire assertion additionally preserves inner bytes/ports, family-correct
tunnel type, source derivation and successful-output byte count. VLAN/MAC/next-hop
and later TTL decrement belong to route/pipeline setup. Ordinary expected outer hop63
follows handoff hop64 and one route hop; Echo uses the separate initial65 rule.
The legacy ordinary fixture preserves inner hop64 throughout. See the [packet
contract's forwarding deviation](l3b-packet-contract.md#l3b-to-real-outer-headers)
for why the outer route decrement is not proof of inner-hop RFC conformance.
Fixtures with zero TC/flow label or IPv4 ID1 do not distinguish general header rules.
Exact source selection sequences are not proof of current target placement or shares.

Important assertion-level qualifications:

- 048 has 21 CLI blocks and 27 allocator scalar assertions at nine checkpoints.
  Preserve semantic admin/state/output counters; numeric ID capacity and 90-second
  old allocator retirement are not target guarantees. Empty legacy service-list
  removal is not target ModuleConfig unlink, empty Service ACL or VS replacement.
- 049_metabalancer 003's comment claims both source and destination disallowed,
  but sources match the allowed prefixes; it tests destination nonmatch. 007 asserts
  one external ACL drop, not shared-Service policy publication.
- Stored ping 002's second IPv6 request/reply uses ID/sequence1/1; gen.py specifies2/2.
  Both are source artifacts; never silently regenerate one and claim identical evidence.
- Source-template /126 and /128 splice zero bytes; /119's unchanged result is due
  to this input byte, not a general invariant. /103 truncates the fourth R byte;
  /95 retains four; /77 additionally overwrites the last two with M6. Host-bit and
  dormant-family/presence cases still need distinguishing target vectors.
- DSCP40 input TC[00,04,FC,FF] yields always[A0,A0,A0,A3],
  onlyDefault[A0,04,FC,FF], never[00,04,FC,FF]. This is an external marking mapping,
  not l3b-owned policy or proof that conflicting per-VS marks can be migrated.
- No configured NAT scenario or inner address/port rewrite assertion occurs in
  these 17/14 units. Cross-family encapsulation is not NAT. This establishes a zero
  NAT-assertion denominator for this corpus only, not blanket reconciliation of
  arbitrary NAT tests/IPVS paths. Target NAT-mode rejection remains independently
  required; the earlier IPVS recomputation comparison remains distinct.

The harness `autotest.cpp:477–540` compares expected packet length and all bytes,
not just selected fields; truncated captures are zero-filled to original length.
`step_sendPackets` binds expectations per port, and per-unit setup clears legacy
balancer state. CLI prose/sorting, exact legacy connection presentation, numeric
allocator IDs and reload latches migrate to the target bounded readback/identity
interfaces, not golden target strings. Neither these fixtures nor standalone ACL
topology proves shared-Service ACL identity/retention/atomic publication. This is
source reconciliation of the stated denominator, not passing new target tests or
a claim to have exhaustively reviewed every RFC or every unrelated NAT subsystem.

### Checkable arithmetic examples

For WRR [4,3,2], thresholds 4,3,2,1 yield A / AB / ABC / ABC,
or `A,A,B,A,B,C,A,B,C`. For [6,4], g=2 and thresholds 6,4,2 yield
`A,A,B,A,B`; repeat twice for `A,A,B,A,B,A,A,B,A,B` and length 10.
These examples describe the ring, not the initial cursor position.

For WLC weights [10,10], counts [0,30], p=10, W=20,C=30:
the first raw weight is 100 and the second is 10. The exact result is [100,10].
These computations do not benchmark the sampler or establish worker consistency.

## Engineering integration and proof boundary

### RFC review and selected checksum policy

This table records reviewed protocol boundaries; it is not a blanket conformance
certificate. An unknown/update status must not be converted into a passing claim.

| ID | Governing sections and force | Selected disposition |
| --- | --- | --- |
| R01 IPv6 options | RFC 8200 sections 4.1/4.2 and its update RFC 9673 sections 5.1/5.2/5.2.2 distinguish HBH router skip SHOULD/configured-discard MAY and operator-configuration SHOULD from Destination Options. | Keep bounded fixed admission; correct the packet companion's obsolete blanket error-signalling qualification. No new knob or option execution. |
| R02 ECN | RFC 6040 sections 4.1–4.3; RFC 9601 sections 3–4/6.1.2 explicitly includes GRE and requires safe configuration for unknown/non-ECN egresses. | Keep normal-mode inheritance only with evidenced compatible real decapsulation. The normal-only implementation does not acquire a compatibility exception merely by citing legacy behavior. |
| R03 IPv4 ID | RFC 6864 sections 4/4.1/4.3/5.3.1: non-atomic sources including tunnel ingresses MUST NOT repeat IDs for the same tuple within one MDL. DF0 is non-atomic without requiring observed fragmentation. | Preserve accepted ID/DF policy and research scope, but describe conditional nonconformance of colliding copied IDs as well as constant ID1; no automatic DF1, allocator or new launch gate. |
| R04 Echo | RFC 1122 section 3.2.2.6 source-route reversal MUST and RR/Timestamp update SHOULD; RFC 4443 section 4.2 IPv6 multicast Echo SHOULD/unicast reply-source MUST. | Explicit endpoint limitations remain; opaque preservation is not execution of these obligations. |
| R05 Transport checksums | RFC 9293 section 3.1 TCP endpoint checksum requirement; RFC 1122 section 4.1.3.4 UDP endpoint rules; RFC 8200 section 8.1 and RFC 6936 section 5 (requirements 8–10) include qualified zero-checksum tunnel/middlebox considerations. | Inner transport is not automatically terminated by IPIP/GRE ingress. The selected policy leaves full transport verification to the endpoint; IPv4 header validation is a separate router obligation under RFC 1812 sections 4.2.2.5/5.2.2. |

RFC 8200 verified erratum 5945 corrects fragmentation text; reported, held and
rejected errata are not adopted standards changes. RFC 9673 verified erratum 8164
is editorial; RFC 6935 verified erratum 4006 corrects the middlebox cross-reference
to requirements 8,9,10. The RFC review is bounded: the full RFC inventory, updates
and every applicable source assertion remain D07, not an asserted complete audit.

**Selected ordinary checksum policy (D03; implementation and qualification NOT RUN):**
separate bounded IPv4-header validation from whole-message TCP/UDP validation.
RFC 1812 sections 4.2.2.5/5.2.2 require received IPv4 header verification in the
router role; that is not the same as an inner transport endpoint requirement.
Check the validated IPv4 header, including options, before ordinary
lookup/state effects, but leave ordinary TCP/UDP integrity verification to the
destination endpoint. Structural admission, safe reads, ICMP checks and correct
checksum adjustment on supported mutations remain mandatory either way.

The inspected historical YANET1 `balancer_tunnel` (`worker.cpp:4290–4440`) builds
outer headers without an inner TCP/UDP validation step. Linux v6.12
`ip_vs_proto_tcp.c` and `ip_vs_proto_udp.c`, `*_snat_handler`/`*_dnat_handler`,
guard their `*_csum_check` calls by `cp->app != NULL`; this is not universal
ordinary-path transport validation. Distinguish verification from generation:
UDP `udp_csum_check` skips a zero checksum, but the NAT handler's non-partial
zero-checksum path falls through to full recomputation, not byte preservation
(`ip_vs_proto_udp.c:183–209`, corresponding DNAT branch). These bounded findings
do not prove that every possible Linux hook or ingress configuration skips checks.

For encapsulated ingress, an outer checksum result is not proof of inner
transport integrity. Hardware may expose an inner result, but its exact domain
and provenance must be proved; absent that evidence, mandatory verification needs
software work proportional to the covered bytes. No measured performance cost
is asserted. Omitting transport verification admits residual state pollution:
a corrupt packet may create/refresh affinity, consume a selection and affect WLC
counts before the endpoint rejects it. Bounds on session resources still apply;
do not claim this exposure is harmless or automatically self-healing.

UDP zero-checksum and trailing-byte admission were selected separately:
do not reject solely for a zero UDP
checksum (including IPv6 tunnel traffic), nor solely for `UDP length < validated
IP transport extent`, while requiring `8 <= UDP length <= that extent` in the
non-jumbogram MVP. Preserve the admitted IP packet rather than trimming to UDP
length; Ethernet padding remains outside that IP extent. This does not permit an
ordinary IPv6 UDP endpoint to ignore its checksum obligations or promise support
for every zero-checksum tunnel deployment. No NAT-scope approval follows from it.

The earlier strict whole-TCP/UDP alternative is not selected. The
[packet contract](l3b-packet-contract.md#packet-drop-precedence) owns the selected
S5 order, UDP bounds, counter attribution and conformance vectors. The semantic
choice is resolved; actual header-check implementation, ingress trust proof and
cost measurements remain unverified. No such product tests have been run.

The following is a design work ledger, not a claim that source inspection has
proved cross-process correctness. Each proof must name actual shared objects,
primitive semantics, memory ordering and crash cut-points before implementation.

### Publisher, sessions and recovery

Reuse the instance registry and C/shared-memory ownership boundary. Do not add a
second Go-map authority or a second health publisher. A VS handle survives
same-incarnation replacement and points at a coherent descriptor; accepted inputs,
tokens, policy and publication provenance must be recoverable with that descriptor.

The proposed implementation sequence is:

1. Safely enroll mutation ownership and acquire the current protected handle and
   descriptor. Validate recovery, structure, existence, CAS and target predicates
   in the contract's order; normalized equality exits without preparation.
2. Privately prepare descriptor/selector/policy and reserve all retained resources,
   including session suppression, staging, guard/cursor and retirement obligations.
   No active resource is rollback scratch.
3. Publish recoverable ready intent, then under the fenced final ordering recheck
   owner, predecessor allocation identity, input/sample context and permits/CAS.
   The root switch is the sole irreversible acceptance for a live handle.
4. Settle or transfer obligations before acknowledgement/next interfering mutation.
   Do not wait for worker adoption or a hazard scan in the authoritative commit.
5. Detach retired roots, scan stable participant-incarnation guard slots with the
   required cutoff, and reclaim only after every structural and reader right ends.
   A zero hazard count alone does not release a structurally reachable object.
6. On takeover, fence all conflicting admitted effects, including helpers and
   allocator/retirement work. Reconcile ready intent/root/settlement before reopening
   management. Inconsistent evidence stays unavailable; do not clear a dead owner's
   lock and infer that interrupted writes are repaired.

Given a crash before ready intent, the candidate cannot accept; given a current
candidate root without settlement, acceptance already occurred and recovery settles
it without republishing. Given ambiguous protected evidence, management is
unavailable. This must be proved against the actual allocator and parent registries,
not just the VS root. Existing `lib/controlplane/config/zone.c` supplies configuration
locking/generation infrastructure, not this whole proof by itself.

### Concrete authority and recovery construction (D01/D05)

Accept each related change once, by switching its owning protected, immutable
CP root. Failure before that switch preserves the previous state. Unprovable
recovery fails closed for affected writes and must not substitute an empty
registry for authoritative state. Sessions belong to the VS incarnation.

This construction specifies required new seams, not implemented primitives.
At the pinned baseline, `lib/controlplane/config/zone.c:273–316` publishes a
generation, waits for every worker, then releases its predecessor. It is not the
target no-adoption-wait live-handle commit or a health settlement receipt.
The PID-valued lock (`:24–100`), `reclaim.h` single-live-holder assumption and
`cp_object.h` generation references do not provide incarnation-fenced recovery
or protection of independently replaced descriptors. Preserve these existing
rights and add a typed-handle publication seam.

The construction survives process interruption in surviving shared memory, not
power loss or destruction of that memory. Attach checks ABI, instance incarnation,
aligned lock-free shared atomics and offset-pointer domain before exposing handles.
All gate, guard, root and ownership-state operations below initially use SC atomics;
immutable payload initialization precedes publication and acquisition precedes use.

| Shared record | Required contents and authority |
| --- | --- |
| Instance header | ABI/incarnation, fixed participant/guard rosters, pool geometry, non-reused identity high-watermarks and admission epoch. No process-local map is authoritative. |
| Typed handle | Kind/name/incarnation, immutable VS parent reference, atomic descriptor locator, domain gate and fixed mutation-slot roster. Deletion freezes rather than reuses a reachable handle. |
| Descriptor | Allocation/predecessor/operation identities, accepted configuration/token, runtime input/sample/result context, protected resource references and outcome provenance. Service descriptor owns compiled ACL/catalogue; VS descriptor owns matching session/cursor/counter references. |
| Mutation slot | Pre-associated participant incarnation, domain/owner epoch, operation/predecessor, reserved block set, sealed READY intent and settlement obligations. No reuse while any former claimant/helper can write it. |
| Observation authority | Exact activation/stream, sequence high-watermark, checker history/deadlines, pending-disable serials and protected publication receipts, independently of outcome cache. |
| Pool block | Fixed pool/block identity and sole atomic claim token, FREE or a non-reused immutable claim-record locator. RESERVED/OWNED/RETIRED are derived from protected operation/root evidence, not independently written authority. |

Use bounded preallocated block pools for the new seam, not an assertion of crash
repair for general `memory_balloc`. Attach-time checked geometry supplies descriptor,
selector, ACL, session-growth and retirement capacity; this is not a new public VS
knob. Publish an immutable claim record binding participant/slot incarnation,
allocation identity and operation, then CAS the block token from FREE to that
record before writing the block. The slot's block list is reconstructible from
a fixed token scan, not a second authority. Claim records use pre-associated slot
cells and cannot be reused until all tokens and readers release their incarnation.
A pre-CAS crash leaves no claim; post-CAS leaves complete owner context reachable.
Ownership transfer retains that token and derives state from committed intent/root
evidence. Release CASes the exact token to FREE only after every right ends; a
stale release cannot free a newer token. Burn identities from a protected reserved range;
advance its high-watermark before issuing any member and never return unused IDs.
Reserve every candidate/retirement dependency before acceptance. Recovery scans the
fixed metadata, quarantines uncertain claims, and releases unused reservations only
after non-resumption and proven nonacceptance. OWNED is not permission to reclaim:
structural references and reader protection still govern release.

Enrollment: claim a pre-associated participant slot; read OPEN epoch; publish HELD
with that epoch; recheck OPEN/epoch; only then perform shared effects. Gate closure
precedes a complete SC slot scan. A HELD publication after the scan must fail the
final gate check; an enrollee that passed that check is visible to the scan. An
owner seals and hands off its slot as its final shared action after all helpers
finish. Takeover requires handoff or reliable non-resumption of the exact participant
and helpers, never timeout/PID reuse. Interrupted state is reconciled before reopening;
unknown ownership keeps the domain unavailable without stopping unrelated ready VSes.

One bounded VS final section serializes observation acceptance, publisher settlement
and destructive O03/O04. Allocate/compile outside it; inside, recheck owner epoch,
exact protected predecessor allocation, CAS/targets, sample/observation applicability
and every removal obligation. No network call, worker wait or hazard scan occurs
inside. Cross-domain creation/deletion acquires Service before VS, with immutable
kind/incarnation ordering for multiple handles; no inverse acquisition is permitted.
A catalogue final section precedes Service and VS sections in lock order and is
used only for catalogue mutations. Revalidate its protected predecessor allocation
inside it; a competing predecessor requires private repreparation, never overwrites
an accepted creation. It has its own enrolled slot, READY intent, single root
acceptance and settlement protocol. Stalled catalogue recovery does not retain
Service/VS locks; unrelated live-root/health operations never acquire this final
lock and use protected catalogue acquisition plus their own domains instead.
A single immutable catalogue root contains live typed references and member-set
revisions. Its switch accepts create/delete/parent enrollment; a prepared handle is
not a created VS. Membership remains the projection of live VS parent references,
not an independently authoritative list. O19 switches only the Service policy root.

Policy-tuple arbitration is instance-wide, not Service-local. Provision a bounded
protected tuple/content index in fixed pools with its own enrolled recovery domain
and final section. Lock order is tuple arbitration, catalogue (when needed),
Service, then VS; final retained-entry release takes the same tuple section.
Final release must acquire tuple arbitration without holding catalogue, Service or
VS sections; defer release until those sections are dropped, never acquire in reverse.
Compilation and index-space reservation precede final sections. Creation and O19
recheck section 5's permits/targets/tokens, then compare exact tuple bytes and
normalized content against every retained occurrence under this section. Hold it
through the actual catalogue-create or Service-root acceptance CAS and settlement;
competing acceptance/final release cannot pass between comparison and CAS.
Unaccepted reservations establish no tuple association. The index is a recoverable
projection of accepted bindings/retained rights, not a second policy acceptance
root. Sealed intent names its reserved entry and exact candidate/predecessor:
before acceptance release only temporary claims; after acceptance finish indexing
and retention before reopening arbitration. Uncertain intent/root/index evidence
means Unavailable, never an empty comparison set. Current bindings, protected old
descriptors/readers, preparations referencing accepted content, unsettled operations
and reusable entries retain the occurrence. Remove it only after all these rights
end, ordered against new acceptance; no permanent historical ledger is required.
Capacity/scans are bounded by admitted retained-policy geometry; exhaustion rejects
before acceptance without changing state/token/permit. Required vector: concurrent
replacements of two existing Services using one cold tuple and different normalized
content cannot both accept; the loser retains state/token. Interrupt comparison→CAS
and CAS→index settlement; recovery preserves the outcome and old-reader retention.
These obligations are NOT RUN.

Live update: reserve resources; publish sealed READY intent; enter the final section
and recheck; SC-CAS the exact protected predecessor locator to the candidate root.
This successful switch is the sole acceptance
point and selects configuration/token and runtime publication provenance together.
Keep intent and predecessor/candidate evidence until idempotent settlement transfers
ownership and enqueues the pre-reserved retirement record. No next interfering
transition or success acknowledgement may erase unsettled evidence. Reused locator
equality without allocation/predecessor checks cannot accept an old continuation.

D05 acceptance reserves a retained obligation slot before accepting a disabling
transition. Observation state is an immutable history record selected by its own
atomic root under the VS final section. READY intent binds exact message/content,
predecessor history allocation, new sequence/history/deadlines and obligation
reference. Its root switch is observation acceptance, distinct from later effective
publication. Before switch, recover old history and release only proven unaccepted
reservations; after switch, settle that same message/obligation once. Complete
views and heartbeats use this protocol too; heartbeat changes stream liveness only,
not probe history/deadlines. Duplicate/conflict checks use current history, so a
lost reply cannot reaccept it. No interfering observation/publication proceeds
before settlement. Later disables for the same identity/activation may join pending coverage,
but recovery-enable stays unpublished. A READY publication intent names covered
disable serials and accepted-history boundary, not just disabled bytes; recheck that
boundary in the final section and rebuild if a new disable is uncovered. A disabled
root switch is the publication fact; settlement installs its protected receipt before
allowing removal or interfering publication. Equal disabled bytes cover a new obligation
only through a new proven publication outcome, never a predating receipt. This does
not introduce a publication barrier for every observation delta. Removal checks every
removed incarnation in the same section: pending rejects the whole request, uncertain
authority/receipt returns Unavailable. Failure retains obligation and publication
target indefinitely; bounded exhaustion rejects new admission before acceptance.

| Crash cut-point | Recovery after closure and non-resumption proof |
| --- | --- |
| Before READY | No authority to switch. Recover claims from fixed slot/block metadata; release only proven unused reservations. |
| READY, protected predecessor current | Not accepted. Discard candidate; do not invent token or publication receipt. |
| Candidate root current, settlement incomplete | Already accepted. Match operation/allocation/predecessor; complete the same ownership/receipt transfers once, without republishing or reapplying observation history. |
| Settled, reply lost | Preserve result/provenance. Outcome-cache eviction cannot change sequence fencing or pending obligations. |
| Inconsistent or inaccessible root/intent/block evidence | Quarantine dependent allocations, return Unavailable; neither rollback nor guessed removal is permitted. |
| Catalogue switch interrupted | Apply the same predecessor/candidate test to that one root; settle parent rights before reopening, never repair membership and existence independently. |

Reader acquisition starts with the instance-resident catalogue root: protect its
allocation, resolve a typed handle while that protection remains held, then protect
the handle before dropping the catalogue guard. A generation-held handle reference
is an alternative structural starting right, not an unprotected pointer. Retain the
handle guard through root access and overlap VS-descriptor acquisition with it.
The immutable parent reference holds a structural Service-handle right; overlap its
guard with Service-descriptor acquisition. Reserve all simultaneous guards before
entry. For each replaceable allocation: load root locator; publish it in an exclusively owned stable
guard; reread root; dereference only if equal. Mismatch clears the guard and consumes
an attempt. Read allocation identity only after protection. Location ABA may acquire
the current allocation there, not a remembered predecessor. Reclamation detaches
candidates before a complete SC roster scan and frees only absent locations with
all structural/dependency rights ended; validate allocation identity before freeing
reused blocks. A validated old reader is visible to the scan; a later reader must
observe root change or acquire a newer allocation. Reuse/abnormal guard cleanup needs
non-resumption proof. Acquire the parent Service handle/root the same way while
protecting VS; keep its guard through ACL evaluation and matched-VS counter use.
No cached old VS descriptor freezes policy; mutable VS indices are not shared ACL
counter storage. Catalogue and frozen-handle references are structural rights too.

Bootstrap uses a fixed control record at an ABI-defined mapping offset outside
all allocators. Exclusive instance creation initializes it and fixed pool/roster
extents before making the mapping attachable. Its atomic phase is INITIALIZING,
READY or CLOSED; immutable context binds creator incarnation and layout digest.
Only READY permits attachment; legacy attachers cannot access the new ABI. A crash
in INITIALIZING is detected as incomplete bootstrap and refuses ordinary attach.
After proving creator/participants cannot resume, an explicitly authorized fresh
instance restart may replace the mapping; automatic in-place salvage is not promised.
READY-to-CLOSED follows the roster protocol. No registry mutator enters before fixed
anchors exist. For later parent integration, close affected attach/registry admission, drain or fence
parent mutators, reserve fixed anchors in surviving instance storage, and install
one recoverable catalogue intent before exposing handles. A crash in general parent
allocator/registry work isolates that domain until outcome is established; local-pool
recovery does not repair it. The existing PID lock is insufficient. ABI migration
creates a new instance with coordinated detach/restart, never reinterprets old live
objects. Missing bootstrap evidence refuses attach. This integration is required,
not claimed present in the baseline.

Finite backing capacity and recycling apply to non-block records too: frozen
handles retire after catalogue/generation links, prepared operations, cursors and
guards all end; settled intents retire after receipt/retirement transfers and the
next operation no longer needs their evidence; disable receipts retire only after
current history summarizes coverage and no pending removal/reconciliation references
them. Compact current activation/stream/high-watermarks and pending coverage survive
old-history eviction. Old history retires after validators/guards end. Non-reused
issuance avoids historical name/stream tombstone lists. Quarantine may exhaust
admission; normal settled churn must recycle once these predicates hold.

Qualification injects interruption at every table row, enrollment check and block
claim/transfer; pauses readers at every acquisition step; races disable/remove/settle
in both orders; and reuses locations with distinct identities. Assert one accepted
result, no freed reachable storage, no skipped participant or lost obligation, and
bounded retained resources. These proof/test obligations are **NOT RUN**, not a
cross-process correctness certificate from source inspection.

Session updates reuse the current session-table boundary but must realize the
contract's exact-key front authority/back suppression. Preparation precedes each
bounded final validation/commit. A competing winner is reclassified, never blindly
overwritten; reprepare if full real key or incarnation changes. A front write and
suppression become authoritative together. Suppression outlives front expiry/reuse
until the affected back retires safely. Resize has one capacity/chain/token outcome,
one front and at most one physical back; an empty observation is not a proof that
no crossing writer can populate it. The exact suppression representation and atomic
commit primitive are not selected by these outcome statements. The following
construction selects them for the engineering proposal.

Use a fixed exact-key stripe function across both table generations and resize;
all forwarding, inspection, ownership queries, WLC reads and writers acquire the
same stripe before resolving authority. Stripe identity includes VS incarnation
and the complete canonical key, never a physical bucket or worker index. Bounded
try-acquisition failure is unavailable/counted-drop, not a fabricated miss. The
baseline `lib/statemap/fwtable.h` supplies layered storage and bucket locking but
does not supply this cross-layer authority transaction; its generation-barrier
reclamation is not replaced by a local empty scan.
Physical storage additionally retains bucket/slot locks: acquire exact-key stripe
first, then all touched physical buckets in immutable allocation/bucket order.
Reserve a front slot exclusively through COMMITTED and settlement; another key
cannot recycle it under a different stripe. Enumeration copies key/slot-version
under the physical lock, releases it, then takes stripe followed by physical lock
and revalidates that identity before reading authority. A mismatch retries within
the same finite scan budget, otherwise reports unavailable/incomplete. Never take
a stripe while retaining a physical lock from discovery. Interrupted physical-lock
holders use the same exact-participant fencing/intent repair as stripe holders.

Once a layer becomes back, its exact-key slot identities are immutable; back hits
refresh/copy into front rather than write back. Reserve one suppression bit per
physical back slot before growth. Store each bit in its own atomic slot cell,
not a shared non-atomic bitmap word. Rollback restores only that slot's prior bit,
never an entire word containing other stripes' commits. The bit is scoped to back allocation identity
and slot, not front slot/key lifetime. Never clear it when front expires or reuses
storage. Retire the whole bitmap only with that exact back after every reader and
crossing writer right ends. This bounds suppression by admitted back capacity,
not traffic history. A genuine absent-back key needs no suppression entry.

Every stripe has a fixed recoverable write-intent cell with atomic states
IDLE/PREPARING/READY/COMMITTED/SETTLED. Reset to PREPARING before building the next
payload; stale payload cannot remain visible under an old COMMITTED marker.
Stripe locks and physical locks/reservations atomically name the non-reused worker/journal incarnation
on acquisition, so a fixed physical-metadata scan discovers pre-READY claims.
PREPARING changes no existing record contents; after exact non-resumption release
its owner-tagged claims and leave authority unchanged. Under its lock, after all
fallible packet preparation, sample session time once, reclassify current authority,
and reserve front storage before modifying anything visible. Seal old/new front
references, affected back bit and prior bit, key/allocation identities and operation
serial in READY intent. Apply the prepared front and suppression changes while
locked, then atomically mark COMMITTED as the linearization point before unlocking.
Complete storage/watermark effects and publish SETTLED before releasing physical
reservations/locks. SETTLED recovery only cleans journal/lock ownership, never
replays storage writes another key may already have superseded. Reuse a journal
incarnation only after every physical owner tag and helper right ends; a new
incarnation prevents delayed cleanup from touching a subsequent transaction.
Readers cannot observe a half transaction because they use the same stripe lock.
No allocation, packet preparation or failing write remains after READY; if that
cannot be ensured the operation must reject before READY. A dead/interrupted holder
leaves the stripe unavailable, not automatically unlocked. After exact worker
non-resumption proof, READY without COMMITTED restores the recorded old references
and bit; COMMITTED finishes the new references/bit. Old resources remain reserved
until intent settlement, so rollback never reads reused storage. A live preempted
holder may resume; management must not repair its intent. Independent control-plane
owner takeover does not terminate lawful worker transactions.

The stable VS handle's current descriptor root is also the sole authoritative
session-chain/capacity/token root. Even a packet retaining old configuration
acquires that current root under a separate guard to resolve session storage;
it never writes through an old descriptor-cached front. Every transaction enrolls
against the current chain admission/generation, then rechecks it after stripe
acquisition before reading/writing. Control-plane preparation never persistently
closes worker admission: a crash there must leave session traffic usable. Reserve
all resources and a complete helpable resize intent first. Under final VS ordering,
install an immutable READY envelope retaining the predecessor's accepted snapshot
and naming the prepared candidate and abort envelope. This is protocol state, not
acceptance of new capacity/token. Its published locator is the sole conditional
decision word; no control-plane-owned mutex is needed by helpers after READY.
The initiating owner performs no unguarded root write after publication. Ordinary
publishers also use exact-predecessor root CAS, so an obsolete prepared store cannot
overwrite a READY envelope or its winning result. A surviving owner resumes only
by the same idempotent helper operations, not a second path to commit. Its retained
management lock may delay other management work but cannot prevent worker helping.
Its published locator is the sole conditional
decision word: workers may CAS that exact READY envelope to either the candidate
or the prepared abort envelope (same accepted snapshot as predecessor). Both are
distinct allocations; no allocation is republished. Other VS mutations help or
return unavailable until this operation settles, never bypass its final ordering.

Surviving workers service this finite helping protocol outside RX, even if the
control plane disappears. READY closes new old-chain transaction enrollment; a
full stable enrolled-writer scan must establish that old-chain transactions have
settled before candidate CAS. A finite scan that cannot establish quiescence may
CAS to abort instead, reopening the old chain without modifying its accepted
capacity/token. Candidate and abort race on the same exact root value; only one
wins. A delayed helper's CAS cannot change a later root. All helper effects are
idempotent atomics against this immutable operation/allocation identity; settlement
records are pre-reserved, never packet-path allocation or identity issuance.
Helpers are enrolled in the fixed worker-incarnation roster; takeover must fence
their rights too. A stalled worker may prevent growth, not force a dead control
plane to hold session admission closed. Missing quiescence never authorizes
clearing a live writer. Candidate CAS selects one prepared VS descriptor containing new front,
old-front-as-back, suppression cells, capacity and accepted token together. A
chain index is derived only, never a second acceptance root. Crash recovery and
readback use the VS root/intent test above. Recheck no crossing old-front writer
before this switch; helpers reopen admission for the chosen generation after
idempotent settlement. The READY/abort envelopes retain the prior accepted snapshot
and result identity; only candidate CAS is irreversible resize acceptance. This
requires a new helpable root-envelope variant, not use of the baseline PID mutex.
Qualification must kill the control plane before/after READY and at each helper
step while workers continue lookup/insert/refresh, including competing abort/commit.
Worker packet execution, sampler servicing and control-loop helping are explicitly
non-overlapping execution lanes on each worker: finish the complete packet guard/
enrollment before control work, and finish that work before another packet. They
cannot overwrite/clear a still-live transaction's record. A delegated concurrent
helper needs a different pre-associated exclusive slot and guards. A transaction
publishes HELD with the exact chain/root identity, rechecks that root before shared
effects, and finally seals/hands off its record after its last write. The resize
scan uses these individual activity records, not worker membership or aggregate0.

Enumerate helper writes strictly: before the decision, only operation-private
scratch/decision evidence may change. Candidate/abort CAS is the sole shared-chain
decision. Admission is derived from the protected current root kind and that
root's operation-settlement flag, not a separately reopened gate. Settlement flags
and ownership releases target the exact winning allocation/operation with atomic
conditional transitions; a prior root check followed by an unconditional write is
forbidden. Losing helpers perform no chain/admission/watermark writes. The back's
historical maximum is logically frozen by ending all old-front writers, not by a
late helper rewriting its storage. All later retirement references that exact back.
Private scratch is retained until every admitted helper ends; abort cannot free it
while a losing helper might resume. Thus delayed helpers cannot reopen/freeze a
later generation. No raw enqueue/free is permitted outside the guarded identity-
conditional settlement/reclamation protocol.

Decode envelope kind explicitly during recovery/readback:

| Current protected root | Meaning and permitted recovery |
| --- | --- |
| READY | Undecided resize; expose predecessor's accepted snapshot/provenance. A live enrolled helper may complete the same decision or abort; no new token/capacity or publication receipt exists yet. |
| Candidate, not settled | Resize accepted at successful CAS. Finish only that operation's ownership/retirement transfers; never republish, recheck as a fresh request or undo it. |
| Abort, not settled | Definite nonacceptance of resize, predecessor accepted snapshot/token/provenance retained. Finish reservation cleanup and derived admission, no successful-publication receipt. |
| Settled candidate/abort, reply lost | Preserve the corresponding definite accepted/rejected result; caller uncertainty is not permission to repeat. |

READY is published only after final authority/CAS/applicability checks and reserves
the exclusive VS ordering position until decision/settlement. Authority transfer,
configuration changes and assignment mutations cannot bypass it: they help abort/
settle first or keep the domain unavailable while fencing every admitted helper.
Candidate CAS additionally needs current enrolled helper rights and the complete
old-writer quiescence proof. Locator equality without those predicates is not
authority. READY/abort never count as disabled-state publication receipts in D05.
Only one physical back is allowed; a second growth
must fail resource/precondition admission until it can retire, never add a third
layer. Each committed session deadline updates a checked historical maximum via
atomic max after COMMITTED and before settlement/unlock; the intent retains that
deadline so interrupted settlement can complete the same monotone update. Growth
settles crossing transactions before freezing the back's retire_not_before watermark.
No retirement starts before now >= that historical maximum, even when all records
are suppressed/expired/replaced sooner. A proved-never-committed back is separate;
an empty scan is not such proof. Back retirement then closes acquisition and fences
crossing writers before checking remaining authority and detaching; guard drainage follows asynchronously.
No empty scan alone authorizes detachment. Stripe/intent/bitmap memory is charged
alongside tables, including retained old descriptors. Conformance must interrupt
each READY/write/COMMITTED/unlock boundary, collide same key across workers, expire
and reuse front, and race resize/retirement with back-hit copy. All are **NOT RUN**.

### API, ABI, ACL and installed clients

Keep module `l3b`, CLI `yanet-cli-l3b` and one new incompatible Gateway service.
Coordinate CLI, CP and dataplane migration. Use a separate protobuf package
and service version. Do not reuse tags or promise rolling ABI compatibility. Convert old input explicitly without a
second writer or hidden aliases. Attach state only after identity and ABI version
checks. If migration requires draining and recreation, document session loss.
The exact schema and migration matrix remain technical work.

The implementation inventory includes `modules/l3b/controlplane/l3bpb/v1/l3b.proto`,
`service.go`, `backend.go`, `modules/l3b/api/`, `objects/l3b/api/`, bindings and CLI.
Final versioned package/tags and shared layout require an explicit reviewed schema
and migration matrix; no implicit reuse of old tags, old mutation bypass, rolling
upgrade or transparent fallback is allowed. Record field presence, full-key and
incarnation representation, allocation/cursor widths and errors before codegen.

Preserve imported common filter types. Trace native compiler ownership, retained
policy content and invocation before selecting the shared-Service adapter with
matched-VS counter context. External common ACL precedes l3b; internal service
policy belongs to the Service root, not independent VS revision bindings. DSCP after l3b
has separate configuration and no shared atomic update. Shared-real conflicting
marks require a migration decision, not a silent prefix-policy conversion.

Every CLI change reaches the workspace membership, root Makefile selection and
Debian install manifest as well as the Rust command. Validate the installed package,
not just `cargo build`. Direct and CLI clients send one complete atomic vector;
uncertain mutation results use readback, never implicit retry or destructive recreate.

### Service groups and shared ACL publication

Service is a public API object distinct from VS and ModuleConfig. One Service
owns one complete ACL and groups explicitly referenced member VSes, for example
TCP `192.0.2.1:80`, TCP `192.0.2.1:443` and TCP `192.0.2.2:80`. A VS belongs
to exactly one Service for its entire incarnation. Moving requires unlink,
delete and create, not an in-place parent change; sessions do not migrate.
Member schedulers, real pools, health and sessions remain independent.

F40/F41 and O18–O23 in the representation contract define identity, policy CAS,
whole-policy replacement, readback, deletion and observation. O02–O05/O09–O12/O14/O16
operate on VSes, not Service groups. The Service policy token versions policy,
not the separately paginated membership inventory. A policy change switches
one shared root, not several per-VS roots; post-switch acquisition through an
old VS descriptor cannot freeze the old ACL. Already validated old-policy readers
may finish under that complete revision. No simultaneous wall-time TX cutoff
or all-worker acknowledgement barrier is inferred.

D01 must prove safe Service-root acquisition alongside VS protection, independent
VS counter contexts for changed policy layouts, create/delete enrollment ordering,
bounded retained memory and crash recovery of the one policy/token decision.
The existing per-VS root sketches do not supply these additional proofs. Empty
Services and old readers after last-member deletion must be included. This is a
selected target model, not evidence of an implemented Service object or compiler
entry point. The ordinary packet policy path remains separate from Echo, ICMP
delivery and the external distributor's projected-service budget identity.

<a id="operator-acl-integration"></a>
#### Operator integration of Service and ACL

The selected local integration has one writer: l3b adapter atomically publishes
a complete,durable,versioned desired snapshot containing Service→`policyRef`,
VS/reals and balancing settings for two independent readers. policyRef is source
namespace/key, not native ACL payload,chosen rule revision or CP incarnation.
ACL operator chooses the revision; CP assigns incarnation on CreateService.
Both readers consume whole snapshots but apply independently,without a shared
transaction.

ACL operator continuously receives,validates and translates the separate ACL
Adapter rule publication and owns O18/O19/O20(Service creation,ACL replacement,
deletion). Rule sources retain the historical integration's scope. l3b operator
obtains the created parent incarnation and owns VS/reals/ModuleConfig,health,
table growth and final readiness. It unlinks/deletes VSes before ACL operator
deletes an unwanted empty Service. O20's live-member prohibition does not solve
every new-intent race.

Both operators reread desired snapshots on startup and reconcile CP readback.
O21/O22 are accepted state,not desired inventory. Source read failure is not an
empty set or deletion; cold start without publication waits. No mandatory mutual
operator RPC is introduced.

ACL operator atomically publishes a durable local result
snapshot containing processed intent revision,Service/policyRef,rule revision,
Service incarnation/policy token,and required-policy versus protective-deny
outcome. l3b operator matches current intent and independently confirms that ACL
through CP for the current worker cohort. A file,successful RPC or old Ready is
not readiness. NOT_APPLICABLE for an empty Service cannot authorize announcement
after VS creation; ADOPTED protective deny is not required-policy readiness.

Rule delivery/validation failure preserves last confirmed ACL and does not alone
remove readiness if other gates hold. No age-only rule TTL is introduced.
Confirmed disappearance of a required section from a valid full publication
installs protective deny without deleting Service/VS; this is not readiness of
the required policy. Intentional valid denying policy is distinct. ACL operator
outage alone does not require withdrawal while previously agreed active ACL is
independently confirmed; old results cannot confirm a different/unknown policy
or incarnation. First and repeated announcements require current-membership ACL
readiness,including idle relevant workers.

l3b operator owns Q/H,hysteresis state,contributing ports/VS and final VIP readiness.
Monalive supplies check results,failure history and freshness; a legacy quorum
flag cannot replace operator calculation. Announcer/BIRD execute,not recalculate.
Formula,W composition and serving/ACL gates remain unchanged; scheduling weight
does not substitute for quorum weight. CP keeps authoritative health expiry and
effective publication; no operator health timer/active-state cache is added.

TCP `tcp` is common fallback for three classes,not a fourth state. At VS create/
full replacement resolve per-VS class → per-VS tcp → global class → global tcp
→60000ms. Global fields are in `l3b.policy.session_timeouts`,uint32 milliseconds;
absent/null inherits,explicit0 ends inheritance. Classes do not inherit each
other. Materialize defaults before candidate equality; keep existing deadlines.
Global-policy changes alone do not rewrite accepted VSes. Classifier,initial SYN
and udp/other rules remain as defined in the representation contract. Tests are
**NOT RUN**.

Automatic grow-only resizing is MVP scope: l3b operator observes VS tables and
initiates O14 through Gateway/CP with CAS/admission,no shrink,no growth while a
physical back exists. Each actual growth is at least ×2; initial default65536 is
configurable and each VS needs a finite configurable ceiling. Automatic growth
requests exactly ×2 and admits the peak with both tables present. The trigger,
thresholds, exact memory accounting and ceiling wire schema remain technical
work. O02 requires an explicit ceiling or deployment default. O03 requires the field.
This is not implicit dataplane growth or overflow-free service;
required-write failure remains a counted drop.

When a valid full `services.yaml` snapshot confirms VIP exclusion, block traffic
processing for that VIP and withdraw its route. The block covers ordinary traffic,
Echo replies and ICMP error delivery and fanout. Preserve other VIPs of the same
Service. A failed read or validation does not change the last decision. CP
publishes VIP admission covering every path, including external ICMP distribution.
Do not use ordinary Service ACL as that gate. Publish a planned block before
withdrawing the route. Emergency withdrawal does not wait for CP. Before activation, approve the maximum reader lag, record it in the verification
manifest together with its start event, and verify the cutoff on each affected path. Detection and propagation still need design.
Independent ACL destination predicates remain. Full historical BGP-agent-failure behavior is
not established here.

Follow the [operator overview](l3b-operator-overview.md) for reinstatement:
confirm local prerequisites before unblocking and confirm worker admission
before Ready and announcement.

Bind intent acceptance and result publication to the same generation. In
A → B → A, the two A intents have distinct generations, including across restart. Delayed
results cannot supersede current intent or grant readiness. Verify recovery
between CP apply and result publication. Rereading inventory or checking only
the current object token is not enough. Snapshot formats, scope, generation
encoding and the recovery mechanism remain technical work. No age-only snapshot
TTL is selected. See the [overview and four scenarios](l3b-operator-overview.md).
These are target requirements, not evidence of delivered integration.

### WLC and counter arithmetic

Reuse `modules/l3b/internal/wlc/wlc.go` (#3125) for exact arithmetic after public
domain validation. At the implementation baseline Calculate maps both 0 and >100
to effective 10; it is not evidence of target F09 rejection. The public service and
CLI must reject >100 before it is used; direct helper callers need an explicit
precondition or checked wrapper decision. No product fix is included here.

The formula's max(1,ratio) is the historical MIN_RATIO=1.0 floor: before the
selector cap, adaptation never reduces the configured weight. At H the constant
100 is a selector/output cap, not a proved configured-input bound: configparser.cpp
accepts the supplied weight with a TODO maximum check, balancer.cpp sums full
effective/configured weights, and globalbase.cpp clamps selector state afterward.
l3b explicitly validates up to 16777215 and sums full eligible configured weights
before output capping. Restrict historical arithmetic comparisons to an explicitly
checked input domain (for example weights <=100); do not infer historical admission
from the cap or claim universal equivalence above it. Inputs >100 affect adaptive
scale and may change the C>=W branch. Do not cap inputs before summing W.

Sampling must establish complete required-worker participation, logical-session
authority/deduplication, observation times and coherent applicable input identity.
Physical ListSessions pages are not a WLC sample. #3320's sequential model is not a
production sampler or C ordering/lifetime proof. Specify construction/update/ties
and continuation with actual selected selector code before activation; equal output
needs active-selector provenance, not empty-patch success.

Observations expose exact `uint64` totals for one accumulation period, including
aggregates. [#3121](https://github.com/yanet-platform/yanet2/issues/3121) defines
the rollover mechanism and conformance evidence. Never sum a shared cell twice.

### Deterministic selector and sampling construction (D02 proposal)

HASH encoding is specified independently of C struct padding/host endian. Start
raw reflected CRC32C with accumulator 0, polynomial 0x82f63b78, no final xor. Feed
one base IPv4 Protocol/IPv6 Next Header byte, source then destination address bytes
in network order (4+4 or 16+16), TCP/UDP source then destination port bytes unless
portless, then four little-endian bytes of validated metadata flowLabel. Echo is
handled by its separate packet path, not a scheduler input here. Use the resulting
unsigned 32-bit value modulo the nonempty logical selector length. This concretizes
the inspected historical `dataplane/metadata.cpp` calcHash plus
`worker.cpp:4059–4060` extension; it preserves the legacy little-endian metadata
convention without depending on host representation. HASH still emits contiguous
eligible weight runs in authored order; WRR retains its specified threshold builder.

At fresh packet ingress initialize flowLabel to 0, including recycled buffers.
Successful IPv6 decapsulation sets it to the removed outer header's low 20 bits;
IPv4 decapsulation preserves it, nested IPv6 decapsulation replaces it, and redirects/
clones preserve the lineage value. It is not overwritten merely by parsing the
current inner IPv6 header. Historical ordinary decap captures this label, but
`worker.cpp` early_decap at the historical pin removes IPv6 outer headers without
capturing it. Uniform capture in that early path is an explicit target difference,
not preserved historical placement; test ordinary and early decap separately.
baseline YANET2 `modules/decap/dataplane/dataplane.c:51–55` is one producer, not proof
that every ingress/clone initializes it. Audit every producer and reject unproved
metadata lineage before claiming compatibility. Golden vectors must independently
check byte stream and CRC for both families, extension headers, portless mixed
transport, nonzero removed-outer versus inner label, nested decap and recycled mbuf.

Use the historical [chash source](https://github.com/yanet-platform/chash/tree/187845fe9503d03822d9a8bf52a57e4c521682ed)
as the selected WLC algorithm reference, not a new quota/contiguous-run replacement.
The historical YANET1 gitlink pins this exact revision. Inspected `service.ipp`
calls initial Adjust from Make and never from Update; `config.hpp` sets L=1600*N,
100 side rings, 16 heads per weight and MT19937 seed 42. This is a required port
and adaptation, not a claim that current l3b implements it.

Make retains all configured bindings, including zero/ineligible members. Generate
100 MT19937 salts, build 8096-cell unweighted side rings, then generate L MT19937
cached values. Assign bit-reversed positions (ceil(log2(L)) bits, skipping >=L)
through successive side rings and rebalance after each 16*N assigned heads. Each
binding ends with 1600 head positions, of which initially 16*effective_weight
are enabled. Target deterministic map traversals use canonical full-key/incarnation
order instead of C++ unordered_map iteration; over/underfull lists use that order,
moving the last excess head to the first deficient binding. Canonical comparison
is unsigned lexicographic order of encoded identity bytes below. Incarnation bytes
are the protected real identifier, validated at 1..256 bytes before BE16 encoding,
not address-derived or process-local. Side-ring region ties
use that same identity order; equal-end regions retain its first identity. A sole
region ending at zero fills the side ring with its identity, never reads an empty
vector. This is an explicit safe boundary adaptation to unweighted.hpp.

Builder hash bytes are family byte 4/6, network-order address, port-presence byte,
BE16 port when present, BE16 incarnation length and incarnation bytes. Use chained
CRC32/IEEE (reflected 0xedb88320, initialize salt xor 0xffffffff, final xor
0xffffffff), not packet CRC32C. These canonical bytes and deterministic traversal
are target choices, not historical struct-memory or permanent cell-map compatibility.
Region end is hash modulo 8096; each interval up to its end uses that region's
identity and the final interval wraps to the identity of the first occupied
interval (a zero-ended region itself has no cells). Lookup uses the nearest
preceding enabled head with cyclic seam repair, as lookup.ipp.

Initial Adjust uses target=floor(requested_heads*L/total_requested_heads), low=
floor(9*target/10), high=floor(11*target/10), all exact widened arithmetic. In canonical
order perform one complete enable pass, then a separate complete disable pass,
not per-binding enable+disable. Enable next head while cells<low, disable last
enabled while >high, stopping at bounds and updating statistics at each step.
Update clones committed heads/enabled/requested/lookup/statistics. Skip a binding
whose requested count equals 16*new_weight even if Adjust changed enabled count;
reorder must not undo Adjust. Only changed requests generate a difference from
previous enabled prefix to new requested prefix. On order is authored binding
order then stored head-vector order. Apply on patches,
repair seam, then sorted off patches split around first on position, or the first
canonically ordered enabled head if only off changes exist. Do not call Adjust.
Preserve the full L-position/head continuation when all weights are zero, with
invalid lookup cells and public live length zero. All-zero Make initializes invalid
statistics to L; final-disable Update uses DisablingUpdate, never searches for a
surviving head. Zero→zero preserves continuation. Empty→nonempty Update enables
existing heads and fills their intervals, never calls Make. Membership change uses
Make; authored reorder is Update with stable full-key/incarnation identities.
No active state is rollback scratch; commit heap continuation only after proven
publication. Takeover still rebuilds capped base. No bit-identical historical
placement, exact shares, restoration or churn bound is promised.
Adapt the pinned Lookup::Init statistics defect by counting the trailing interval:
N1/weight100 requires Stats[A]=1600, not the source's1599. After each completed
operation nonnegative statistics equal the full lookup histogram and sum to L,
including invalid cells. Interval arithmetic uses checked subtraction/addition,
never unsigned underflow. Reference recomputation counts every lookup cell.

Compact expected vectors: N=0 has no heads/lookup; N=1, A positive has lookup A×1600
regardless of side-ring salts; Update A→0 keeps all 1600 heads with public length0;
Update 0→A restores A×1600 without Make/Adjust. Failed publication at either change
preserves the preceding committed continuation. Multi-real reference vectors must
compare complete heads, enabled prefixes, lookup and statistics against the pinned
algorithm with the specified deterministic adaptations; this remains a mandatory
implementation gate, not passing evidence from a weight histogram.

Two-real reference vector uses authored A=192.0.2.1:80 and B=192.0.2.2:80, both
incarnation ASCII `i1`. Encoded keys are `04c000020101005000026931` and
`04c000020201005000026931`; L=3200, canonical numeric test IDs A=0/B=1,
invalid=0xffffffff. SHA-256 below hashes every cell as BE32 in lookup order;
head hashes use each identity's stored 1600 head positions as BE32 in vector order.
A heads: `1ab7051b9d429e6ca0ceaf3a650e808cdddb3356071c043ebd5bc682c1968da0`;
B heads: `49bf4e7965d29ef430d8491699504ed6ddafa6a6da697ee43645878003287446`.
They remain unchanged through this same-membership sequence.

| Operation, in order | Requested/enabled heads A,B | Actual cells A,B,invalid | Complete lookup SHA-256 |
| --- | --- | --- | --- |
| Make weights[1,2], initial Adjust | [16,32]/[16,32] | 1056,2144,0 | `175902b0469e93db07c4b740ddc8ae3f2ed56bc4fbb42eb0f49d32d891d6e61d` |
| Update unchanged[1,2] | Same | Same | Same |
| Update[0,2] | [0,32]/[0,32] | 0,3200,0 | `526af2849bb38bbe8df3f99db88f0380713f8596133fdcd119b9b01ee9fdc4f3` |
| Update[0,0] | [0,0]/[0,0] | 0,0,3200 | `251941c68a174d6106ddc3799a65356749e7aea97645c2788b7dadff643c4f71` |
| Update[1,0] | [16,0]/[16,0] | 3200,0,0 | `59ec91dcb7dc65b5f928091cb0e25c26729a0a4453ebe7d8244fc1ceae7d9712` |
| Update[1,2] | [16,32]/[16,32] | 1056,2144,0 | `175902b0469e93db07c4b740ddc8ae3f2ed56bc4fbb42eb0f49d32d891d6e61d` |

Offline reference arithmetic produced these proposed vectors, not product tests.
Side-ring unit oracles: regions(A,0),(B,5) yield B×8096; equal-end(A,5),(B,5)
retain A and yield A×8096; sole(A,0) safely yields A×8096. Requested-equality oracle:
given requested[A]=16 and enabled[A]=17 after Adjust, unchanged weight1/reorder
preserves enabled17, not16. Failed publication of any row preserves the preceding
committed heads/prefixes/lookup/statistics; subsequent Update starts there.

Packet-hash golden bytes/CRC: IPv4 TCP 192.0.2.1:12345→198.51.100.2:443, metadata0:
`06c0000201c6336402303901bb00000000` → `9fa1ee17`. IPv6 UDP
2001:db8::1:12345→[2001:db8::2]:53, metadata0x12345:
`1120010db800000000000000000000000120010db80000000000000000000000023039003545230100`
→ `a958a3f6`. Independent documentation arithmetic checked these values; product
conformance, including complete multi-real builder vectors, is **NOT RUN**.

For sampling, provision one bounded request/reply slot for each required worker
incarnation in the captured serving roster, including idle workers. A sample identity
binds owner epoch, VS incarnation, input generation, chain identity, exact roster
and a non-reused attempt serial. Serialize attempts per VS; do not issue the next
until all old request/reply slots are completed or their writers fenced. Slots use
REQUESTED/RUNNING/COMPLETE/CANCELED with that serial; cancel stops admission, not a
running writer. Late replies cannot overwrite a different serial. Adjudicate each
attempt once; a lost completion never repeats streak charging/reset. An empty
required roster is an unavailable sample, not a zero-total valid sample or modulo0.
Assign each exact-key stripe to exactly one captured worker by stripe index modulo
roster length; this is scan ownership, not packet/session ownership. Every worker
services its request from the worker control loop even without RX, not an empty
l3b packet handler. Each enumerates every physical front/back slot once in immutable
layer/bucket/slot order and processes only keys assigned to its stripes. For each
candidate permit a finite positive retry budget R; count at most once after a
validated read, and never restart the enumeration cursor. Reserve exact-key scratch
for at most S=front_slots+back_slots candidates and work <=S*(R+1) validations per
worker; exhaustion invalidates the whole attempt. S is checked <=uint64 maximum at
attach, so each shard's uint64 count cannot overflow even under slot reuse; retries
do not add counted candidates. Enumerate every physical front/back key, including
suppressed back slots; suppression affects authority resolution, not candidate
discovery. Revalidate exact
key/slot identity, and deduplicate keys within its admitted bounded scratch space.
For each resolved logical key sample session time at protected authority read; count
only live authority whose stored real key/incarnation belongs to the sample's eligible
set. Front suppresses back even if expired/ineligible. Different stripes may have
different observation times; this is a complete bounded scan, not a global instant.
Return per-real exact counts, observation interval, identities and complete/failure
status. No partial reply, omitted worker or missing real is interpreted as zero.
A continuously live back key copied into an already-scanned front slot remains a
candidate through its immutable back slot: resolve its current front authority
under the stripe and count it once. Required vector: scan front, copy back→front
and suppress back, then scan back; expected live count1, not0 or2. If the new front
has expired before that protected read, count0 without reviving the suppressed back.

Pin the captured chain for the whole scan; a chain change before adjudication
invalidates the attempt rather than mixing generations. Disjoint stripe assignment
plus exact-key dedup counts each observed logical authority at most once; current
stripe revalidation prevents physical front/back copies from double counting.
Use exact integer counts: shard counts are bounded by admitted physical slots and
use a proved sufficient width; if that width cannot represent the admitted geometry,
reject geometry at attach, not a mathematically valid sample later. Aggregate and
calculate using big.Int. At adjudication recheck owner/input/roster/chain and all
complete replies, charge an invalid attempt once to that input generation or expose
unavailability if attribution is uncertain. A slow complete current scan remains
valid; cadence targets are not timeouts. Equal output confirms only the exact active
result/continuation under the existing final guards; failed activation never becomes
confirmation. Provision/measure scan scratch, per-worker service and full duration;
none is established by the sequential model or calculator tests.

Physical counters use independent producer-owned semantic cells with immutable
counter/storage incarnation. Packet/session producers use one non-reentrant worker
incarnation; management/health/publisher/sampler producers use separate serialized
owner-executor shards, never write a worker shard. Helpers require their own fixed
shards. Migration fences the former writer before transfer; raw shared uint64
increments are not permitted. Multiple handlers are safe only when sequential on
that same executor. Shard admission exhaustion makes the corresponding counter group
unavailable, never blocks forwarding/configuration. Store uint64 low word plus
uint64 wrap word under an atomic sequence protocol: writer sets odd, increments
with carry, then publishes even; reader accepts only matching even sequence reads.
All concurrently accessed words are atomics, not a C data race disguised as seqlock.
Sequence and wrap exhaustion mark the storage unavailable before reuse/wrap; never
silently reset or saturate into a plausible value. A stopped odd writer requires
non-resumption/recovery or unavailable, not guessed zero. Compute exact internal
values without truncation; aggregate/deduplicate by immutable semantic/storage/
producer-executor incarnation, not per link or array position. Public readback
uses the main contract’s `uint64` period rollover and group `UNAVAILABLE` rules.
Gauge/session counts are sampled authority, not event-counter differences. Reset
creates a new storage incarnation only under existing reset/accumulation rules;
same-incarnation takeover preserves values. Tests must force low-word carry, torn
reads, writer interruption, incarnation change, duplicate links and aggregate >2^64.
This physical design and all new builder/sampler vectors remain **NOT RUN**.

Counter protocol is SC throughout. Sequence is uint64, initialized 0 before exposure;
AVAILABLE state is a separate atomic. For delta in 0..uint64_max compute low'=low+delta
mod2^64 and carry=(low'>=low ? 0 : 1), wrap'=wrap+carry, with checked wrap arithmetic.
Value is wrap*2^64+low. Before changing payload require sequence <=uint64_max-3 and
representable wrap'; otherwise publish UNAVAILABLE without wrapping. Publish odd
sequence+1, low'/wrap', then even sequence+2. Reader checks AVAILABLE, matching even
sequence around payload reads, then AVAILABLE again, within a finite positive
attempt budget; exhausted reads return only that observation group unavailable.
An interrupted odd update is not reconstructed from guessed increments: after
non-resumption leave the storage unavailable unless a separate exact intent proves
completion. This cannot stop packet/configuration progress. Internal carry vector:
(wrap=7,low=2^64-2)+3 → (8,1), aggregate exact value 8*2^64+1; sequence2^64-2
cannot accept another update. Two producer shards with values 5 and7 aggregate12,
whereas two links to the same shard with value5 still contribute5.

### Observation fencing proposal

This proposal addresses repeated configuration content, without treating F39 or
a content revision as fencing. It requires owner/publisher approval and actual
shared-memory recovery proof before #2776.

1. The authoritative receiver owns an assignment activation identity. Every
   activation, including A→B→A, remove/re-add or assignment transfer, issues a
   fresh identity even when content is byte-identical. It binds actual instance,
   VS/real incarnations, authenticated source assignment and checker configuration.
   Identity issuance is recoverable, never returned to a pool and never wraps;
   exhaustion rejects activation before mutation. A restarted receiver unable to
   recover/fence issuance stays unavailable instead of resetting it.
2. An authorized source initializes a receiver-approved stream for that activation.
   The receiver records the sole current stream identity before accepting its
   complete view. Replacing it fences the old stream. Every message carries exact
   activation and stream identities plus sequence and immutable configuration
   context. An authenticated source name alone grants neither activation nor epoch.
3. The receiver checks current assignment/activation/stream under the same ordering
   as observation acceptance. Different content at a used sequence conflicts;
   duplicates never refresh age. A gap closes delta admission until complete-view
   resynchronization in a fresh stream. Sequence exhaustion also requires a fresh
   stream; no modulo comparison or reset under the same identity.
4. Reconnect cannot manufacture probe success. Only complete trustworthy checker
   history with unchanged proven continuity can preserve streak/eligibility.
   Stream identity is not that history. Configuration activation changes require
   an explicit history-transfer decision consistent with retries hysteresis; absent
   proof initializes unknown/unhealthy and streak zero.
5. Keep stale exclusion in current activation/stream authority and acceptance order,
   separately from the bounded per-message outcome cache. Evicting an outcome does
   not lower sequence or reopen an old stream. Recovery with lost ordering evidence
   fences the stream and requires fresh initialization, never replay.
6. Apply observation acceptance once, then publish through the same descriptor
   owner. The outcome names actual publication provenance or “superseded before
   publication”, never an invented generation. Publishing failure preserves active
   state but does not turn accepted input into a pre-accept rejection.

For a delayed `(activation=a1,stream=s1,seq=k)` from the first A, the later A uses
`a3 != a1`, so it rejects regardless of repeated revision/content/sequence. Within
one activation a delayed old stream fails current-stream equality. Within one
stream a repeat is duplicate/conflict, never fresh evidence. This argument depends
on non-reused issuance, authoritative final checks and fencing of obsolete receivers;
packet fields, process-local counters and opaque metadata do not establish them.

Required counterexamples: owner crashes around activation issuance/installation;
an old admitted validator resumes after takeover; source restart resets sequence;
configuration A→B→A occurs while a batch is prepared; real address/slot reused;
cache exhaustion/eviction; both initialization and ACK are lost. Every case must
retain prior valid publication or report unavailable/unknown without stale enable.

Freshness requires separately approved age evidence and numerical budgets. A
source monotonic timestamp cannot be compared directly with the receiver clock.
The concrete clock-domain mapping, error interval and resynchronization proof remain
open; any scheme must expire using the oldest permissible observation time and
remaining receiver-monotonic lifetime. Receipt, reconnect and heartbeat grant no
new probe lifetime. The fencing proposal does not solve clock trust by itself.

### Actual-hop trust and routing

**Selected health-expiry guarantee (D08):** logical expiry at the control plane does
not by itself stop a worker using an old published descriptor. Publication may
fail and adoption may lag. An announcement lease also does not reject traffic
already arriving at l3b. The selected requirement is a hard worker-enforced
cutoff at observation expiry; its implementation has not been proved or run.

The selected design boundary is descriptor-carried health and stream validity deadlines
in a proved dataplane-comparable clock domain, checked on both selection and pin
validation even when no replacement can publish. The earliest applicable required
checker/stream deadline bounds eligibility; expired evidence follows existing
invalid-pin disposition, never merely the selector's retained enabled bit. The
clock mapping must be conservative under uncertainty/restart; unverifiable mapping
cannot establish eligibility. Immutable stale descriptors may under-admit after
recovery but must not extend a deadline. This requires explicit packet-path,
status/counter, clock-domain and publisher integration proof before adoption.

**Concrete clock/age proposal for review (not yet approved):** do not use the
existing wall-clock helper as a monotonic proof. At the pinned implementation,
`lib/dataplane/time/clock.c:8–26` anchors TSC to CLOCK_REALTIME;
`dataplane/worker.c:395–403` samples it once per round, and
`lib/controlplane/agent/agent.c:135–168` returns the maximum last-published worker
time, not a fresh sample from each worker. None establishes the selected cutoff.
`lib/dataplane/config/zone.h:79–86` also records that the existing TSC clock is
initialized once without adjustment, allowing cumulative drift. Re-anchoring it
to BOOTTIME would need a separate bounded-error/suspend proof; it is not the
direct-kernel-read proposal below.

For the first correct implementation, use Linux CLOCK_BOOTTIME in the receiver
and enforcing workers within the same identified boot/time namespace. Read it
at the final eligibility check rather than using a cached round timestamp.
Recovery must verify that domain identity; a reboot, namespace mismatch, read
failure or unrepresentable conversion does not authorize forwarding. Suspend
counts toward expiry. This intentionally chooses correctness before an optimized
TSC mapping; the cost must be measured before qualification, not assumed negligible.
Absolute deadlines are not re-armed at descriptor installation or worker restart:
delayed adoption cannot add the transport/install delay to their lifetime. This
proposal applies only to the owner and workers on one host in the same verified
clock domain. Remote workers require a separately approved deadline-mapping proof;
a challenge to a remote probe source does not provide that worker-clock proof.

For a checker embedded in the source operator in that same trusted domain,
capture a fresh BOOTTIME
anchor immediately before the actual probe, and bind it to each completed result,
checker configuration and activation. This is the preferred local path: no network
challenge service is needed. A state-change-only callback is insufficient because
unchanged successful checks must also supply fresh evidence. A later status poll
or serialized wall-clock timestamp cannot stand in for that probe anchor.
This is planned operator integration, not an assertion that embedding exists,
nor a proposal to embed the checker into the owning control plane. The operator
still sends observations across the authenticated source-to-owner boundary and
cannot write effective state or shared memory. The owner validates anchor-domain,
activation and checker provenance before using the timestamp.

For a remote source only, a proposal avoiding an unproved clock offset is
receiver-owned single-use probe
challenges bound to activation/stream/checker identity. Record receiver send time
`s`; the trusted assigned checker must start and complete a fresh probe after
receiving that challenge, and bind the result to it. Cached/unsolicited results
cannot claim this proof. Since observation time `o >= s`, a deadline `s + A` for
approved maximum observation age `A` is conservative: whenever `t < s + A`,
`t - o <= t - s < A`. Delay consumes lifetime rather than granting new time.
Here `o` denotes the actual probe event expressed conceptually in the receiver's
time domain, not a remote timestamp to compare numerically. Fresh-probe causality
and authenticated context are required; a checker stamping a cached result later
does not satisfy them.
At receipt, expired challenges cannot establish eligibility. The active stream
deadline is checked independently; use the earliest required deadline. Challenges
do not fabricate success, reset hysteresis or refresh evidence on a heartbeat.

Represent deadlines in uint64 milliseconds with checked addition. Round the send
anchor down and the enforcing current time up; account for clock-read/quantization
uncertainty conservatively. This may expire early but never grants an extra
millisecond by rounding. Exhaustion rejects before issuance; replay exclusion
survives outcome-cache eviction through activation/stream sequence authority.
An older descriptor retains its own earlier deadline and cannot be renewed by
new evidence without a new publication. Loss of clock/activation authority closes
admission until reinitialization; restoring wall-clock bytes does not restore it.

The remote variant requires protocol/schema, trusted checker behavior and admission/
recovery implementation; it is not a proof that the existing transport supports
challenges. Specify bounded outstanding challenge admission, persistence/fencing
and response-loss recovery before adoption. Capacity and age/stream budgets remain
deployment inputs. Also prove the eligibility check's packet commit boundary:
already-transferred packets are not recalled, while a preempted worker may not
reuse an old check for a later forwarding decision. A clock optimization must
preserve that boundary and an explicit conservative error bound.
Single-threaded execution does not itself exclude OS preemption. Round caching,
preemption and decision-to-TX batching must be treated separately; neither a
packet-check timestamp nor this proposal proves a wall-time wire-transmission
cutoff. Any residual forwarding window needs an explicit definition and approved
budget before a bounded TX claim can be made.

The control-plane-only alternative allowing stale forwarding until publication/
adoption is rejected. This selection does not promise instantaneous global adoption
of every administrative or health update: it specifically bounds evidence expiry.
Required vector: health expires while publication fails and an old worker keeps
processing pinned and new traffic; assert the approved cutoff semantics at the
worker, not just an unknown status or route withdrawal.

At baseline `controlplane/gateway/backend.go:130–140` copies incoming metadata and
removes token/admission fields before proxying. This does not prove delivery of an
authenticated user principal to an arbitrary backend. Trace registration, service
transport and enforcement for the actual deployment before binding assignments.
Reusing platform auth is required; a new unverified source_id header is not a fix.

For the engineering proposal, use the existing gateway-hosted builtin registration
path for the observation receiver, not a transparently proxied remote backend.
`gateway.go:519–576` installs platform auth interceptors and registers builtin
handlers on the same servers. Supply the receiver a composition-root principal
resolver over the authenticated server context; do not copy credentials into a new
header or import an inaccessible Go `internal` package into the module. The resolver
returns authenticated principal/group/auth-method facts or unavailable, never trusts
payload identity. Enforce auth-enabled/nonanonymous admission for this receiver;
platform method permission alone is not an assignment. A protected assignment table
binds that principal to exact instance/VS/real/checker activation scope. Check it on
initialization and again at final acceptance; delegated work retains the same scope,
not ambient gateway privilege. Operator-to-gateway uses the already configured
platform credential/transport; concrete principal names and grants remain D06.
Reject direct backend exposure or auth-disabled deployment for trusted observations.
If deployment requires a remote receiver instead, this proposal does not certify
that hop: it stays unavailable until an authenticated principal-delivery mechanism
is independently reviewed. No new homegrown delegation token is selected.

Bounded protocol state is provisioned per assigned checker, current activation and
current stream. Reserve initial-view validation space, one accepted-history slot,
one pending-disable coverage record and bounded result-cache/challenge slots before
acceptance; batch admission reserves the entire batch first. Retire obsolete stream
payloads only after admitted validators end, while current-identity/high-watermark
state continues fencing them. No per-historical-stream tombstone list is needed:
exact equality with the sole non-reused current identity rejects all older streams.
Eviction removes only reply detail, not acceptance/disable state. Capacity exhaustion
rejects without partial acceptance, sequence advance or lease renewal. Missing
recovery evidence fences the stream and initializes unknown; pending-disable records
remain attached to the surviving target until their exact publication settles.

For remote challenges in the conditional clock proposal, each slot binds a fresh
non-reused challenge identity, activation/stream/checker, send anchor and expiry.
Accept one result exactly once under sequence/assignment ordering, then release the
slot only after its acceptance outcome is settled. Lost replies do not make it
reusable; retries reconcile by message identity. Expired unanswered slots may retire
after fencing their identities and ending validators; delayed responses fail current
slot/identity/expiry checks. Restart either recovers the same slot/domain evidence
or fences the whole stream; it never restamps send time. Outstanding-slot limits
and cache retention are D06 resource parameters, not unbounded queues.

Conditional BOOTTIME qualification has explicit pass predicates: owner/operator/
workers report the same verified boot and time-namespace identity; injected domain
mismatch/read error/checked-add overflow fails closed; suspend/resume consumes age;
wall-clock jumps do not renew it; delayed descriptor adoption and worker restart
preserve absolute deadlines; every new-selection, pin-hit, OPS and reverse-delivery
path checks required deadlines at its forwarding decision. Packet preparation and
session reclassification precede the final fresh clock check; a preempted attempt
must repeat validation before a new decision. Define the decision as that completed
validation, not eventual NIC transmission: preemption after the decision does not
constitute a new decision or prove a TX bound. No wall-time decision-to-wire bound
is promised. Measure direct-read cost and full packet-path cost under the D06 target
matrix, with publication failing and idle/lagging workers, before admission. These
are required experiments, **NOT RUN**. They validate a conditional engineering
proposal only; they do not approve BOOTTIME as the selected clock source.

Health combines only trusted, current required checkers with administrative state;
required checkers aggregate AND. A real with an empty required-checker set is
health-ineligible even when administratively enabled.
Normal withdrawal follows required disabled publication; emergency withdrawal
and permit expiry do not await CP availability or successful disable publication.
The disable obligation remains. Recovery announcement follows
publication and scoped serving readiness. Independent announcement lease cannot
outlive health eligibility. An accepted intermediate health disable must reach a
successful disabled-state publication before recovery-enable publication for that
real/activation; a newer success cannot coalesce away the disable. This orders
publication, not all-worker adoption, and does not promise every worker observes
the intermediate state. Failed publication retains the obligation; the bounded
admission/recovery proof must preserve it without requiring unbounded history.

The selected removal rule is publish-before-removal: O03 removing an affected
real incarnation and O04 deleting its VS cannot accept until every accepted
disable obligation on those identities has a proven successful disabled-state
publication. Pending is FailedPrecondition after CAS/target checks; uncertain
authoritative obligation/settlement state is Unavailable. Reject the whole
operation without implicit waiting, queueing, destructive retry or partial removal.
The final structural check is serialized with observation acceptance/settlement;
obsolete validators cannot accept a disable after its target has been removed.
Structural removal, expiry and recreation are not terminal-discharge shortcuts.
After successful disabled publication, no all-worker adoption wait is added;
retained old-worker forwarding remains governed by the existing expiry/invalid-pin
rules. Failed publication may prevent removal indefinitely. The exact bounded
record/provenance, authority enrollment, crash recovery and retained-reader proof
remain engineering work, not solved merely by selecting this ordering.

### Required emergency VIP withdrawal

Before first/repeated announcement confirm required Service ACL against current
intent,policy binding,Service incarnation and relevant worker cohort including
idle workers. Health,publication/serving readiness and ACL are joint gates;
successful RPC,old Ready,protective deny or empty-Service NOT_APPLICABLE is insufficient.

MVP requires withdrawal on Monalive,l3b operator or announcer crash/hang/restart,
and no reannouncement before confirmed withdrawal and fresh final readiness.
Selected BIRD `protocol monalive` periodically requests prefix confirmation and
independently removes unconfirmed routes. Announcer sends according to operator
Ready covering current health,l3b and required ACL. Include-only does not provide
this guarantee; no separate watchdog process is mandatory. Fast restart before
expiry still requires incarnation fencing and confirmed withdraw→reannounce.

All timing is configurable: per-VS session TTL,clock uncertainty,health freshness/
liveness,Ready timeout/staleness,polling,hysteresis,lease,BIRD scan/check and BGP
hold. Effective settings are observable,owned in their respective contexts,not
all moved to VS or Monalive. Defaults are allowed; hidden hardcoding is not.
End-to-end withdrawal bounds include detection,BIRD checks and cleanup,not lease
alone. No historical timing example becomes a target default or proved bound.

Failure-injection acceptance(**NOT RUN**):

| Stimulus | Required outcome | Timing and recovery evidence |
| --- | --- | --- |
| Monalive crash/hang | Old health cannot renew; withdraw every managed VIP | Configured health/stream freshness and total bound; new trusted evidence before recovery |
| Operator crash/hang or RPC serving stale Ready | Announcer stops all managed confirmations; withdraw every managed VIP | Ready timeout/staleness and total bound; current l3b/health/ACL result |
| Announcer crash/hang with BIRD alive | BIRD independently removes all managed VIPs and exports withdrawal | Configured scan/check and measured cleanup bound,not BGP hold time |
| Fast restart of any of the three before expiry | Withdraw all managed VIPs before reannounce; old process cannot restore them | Incarnation,fencing,removal confirmation; TTL alone is no proof |
| Lost protocol requests/confirmations | Unconfirmed routes expire; datagram is not application ACK | Include BIRD cadence/application; unproved outcome blocks recovery |
| Delayed,duplicate,reordered confirmations across restart | Old messages cannot renew/restore withdrawn routes | Fence already-sent messages,not only live senders |
| CP unavailable or disable publication fails | Emergency withdrawal/expiry proceeds; disable obligation persists | Independent BIRD cleanup; recovery waits for publication/serving/ACL |
| BIRD/BGP-peer loss with GR/LLGR | Peer removes within agreed budget without unaccounted stale retention | Check hold separately; disable GR/LLGR for VIPs or include retention in bound; no stale restoration |

Capture stimulus and route removal at BIRD and peer,configuration revisions,
effective timings,all managed VIPs and fresh recovery gates. For failures and
fast restarts of the three managed components, include an unrelated BIRD route
and verify the l3b withdrawal leaves it untouched. Announcer state
alone proves neither BIRD nor peer application. The online barrier is confirmed
removal from the managed BIRD table and export to every dependent peer,ordered
before reannounce. BGP has no per-withdraw application ACK; peer capture is
qualification evidence,not an invented synchronous RPC.

## Lab, stand and qualification

Reuse the Lab/functional framework and versioned report formats. A scenario report
identifies source/build/config revisions, topology, actual instance/worker cohort,
run authorization, stimulus/capture points, selected-real evidence, clock domains,
reset result and owned-resource cleanup. A packet reaching any backend is not proof
that it reached the intended eligible real under the intended generation.

Exercise management races/restart, active traffic during structural updates and
resize, expiry and health flaps, session authority, ACL decisions, every inner/outer
family and tunnel pair, Echo and ICMP/fanout, WLC validity/publication failures,
readback/adoption/counters and installed clients. Include sanitizer/race/functional
lanes where applicable. A narrow smoke does not qualify the full lifecycle.

Exploratory baselines may inform a proposed workload, but the qualification manifest
must be approved and versioned before its run: hardware/NUMA/NIC/software matrix,
VS/real distributions, offered traffic/protocol/payload mix, sessions/churn/updates,
warmup, repetitions/duration, invalid samples, uncertainty, metric units/boundaries,
aggregation/percentiles and numerical memory/throughput/loss/latency/convergence/
soak limits. The historical 10000-VS/100000-real envelope is not the current
target. Qualifying 100000 VS with 1000 reals on average requires separate registry,
ABI and pool capacity evidence; it does not raise the current cap of 2097151.
Do not select passing limits after seeing qualification results.

Every claimed target has an accountable owner, run permission and retained evidence.
Missing evidence is unqualified, not a zero. Zero observed mixed-generation,
partial-publication or last-valid-state violations is necessary but does not prove
zero failure probability. Launch aggregates compatibility, safety, package,
functional, integration, resource and performance evidence; performance alone
cannot produce the verdict.

## Remaining decisions and completion record

| ID | Supplied specification and remaining acceptance boundary | Owner boundary |
| --- | --- | --- |
| D01 | Concrete authority/block-claim/history/catalogue/bootstrap/guard and session-journal/suppression/helpable-resize constructions supplied, with crash predicates and Service counter/parent rights. Required new primitives and ABI/attach migration are explicit; approved wire schema, implementation and executable cross-process proof remain downstream, not established by these documents. | #2725/#2728/#2719/#2736 |
| D02 | HASH bytes/CRC/metadata, pinned-source WLC builder/ties/statistics repair, bounded all-worker sampler and producer-counter arithmetic supplied with reference vectors. Port implementation, independent vector reproduction and runtime/timing qualification remain unverified. | #2764/#2765/#3121 |
| D03 | Ordinary checksum/UDP-length semantics selected; verify S5 IPv4-header checking, structural bounds, preservation, counter attribution and measured cost. | #2742 and packet owner |
| D04 | Activation/stream/history acceptance, builtin authenticated-context assignment enforcement and bounded retention/challenge construction supplied. Protocol approval, deployment principals/grants and implementation proof remain outstanding; remote proxy metadata is not certified principal delivery. | #2735/#2775 |
| D05 | Empty checker set ineligible and disable-before-recovery/removal selected; serialized history/publication/structural decisions, exact coverage receipts, retention and crash recovery specified. Implement and qualify without terminal-discharge shortcuts or all-worker adoption barriers; failed publication can block removal indefinitely. | Health/publisher owners |
| D06 | Numerical observation, stream, route and recovery budgets. Resource limits, workload, target profile and SLOs. | Deployment and qualification contributors prepare the manifest. The MVP owner approves it and separately authorizes each run. Verification remains independent. |
| D07 | Pinned scheduler/session sources, stated RFC boundaries and all17H/14N fixture-tree assertions reconciled with retained/deviating/external dispositions and source discrepancies. Bilingual parity and candidate-specific independent review are separate receipts; source inspection/fixture counts do not qualify target execution or every unrelated RFC/NAT subsystem. | Contract and compatibility reviewers |
| D08 | Hard worker expiry selected; conservative age/deadline provenance, conditional direct-BOOTTIME and pin/selection/failed-publication/restart/preemption validation specified. Clock choice remains unapproved; runtime experiments and numerical budgets are NOT RUN/unset, never inferred from the proposal. | Health, packet and publisher owners |

The candidate is not implementation-ready while dependent D01–D08 remain open.
Engineering choices must be resolved with evidence and specific proposals, not
relabelled as deployment inputs. Conversely deployment budgets, grants and approval
authority must not be invented by the implementer. An approval of this work plan
does not approve each unresolved contract or authorize production execution.

## Path to MVP completion

Status: owner decisions recorded 2026-11-11; implementation and qualification
have not started and everything below remains **NOT RUN**. This section records
deployment-decision inputs and sequencing; it does not approve any contract,
authorize production execution, or select passing limits after seeing results.

### Recorded owner decisions (D06/D08 inputs)

1. Scale: orientation 100000 VS with 1000 reals each on average, about 100
   million aggregate bindings per instance. The current registry limit
   (2097151) cannot hold that, so the owner selected a registry width
   increase. That raise is separate shared-struct/ABI engineering work, not
   part of this documentation iteration. A 27-bit width bounds the stated
   orientation with roughly one third headroom; exact width and pool geometry
   are implementation decisions.
2. Qualification environment: the owner-designated deployment hardware/software;
   the historical autotest suite is the regression base with
   the per-fixture dispositions of the D07 reconciliation. The SLO orientation, in the owner's words, is
   "be better than the historical deployment" (better-than); numeric memory, throughput,
   loss, latency, convergence and soak limits and percentiles are informed by
   exploratory baseline runs, which are not qualification evidence. Freeze
   these thresholds in the approved versioned manifest before the first
   qualification run. Do not revise them in response to qualification results.
   WLC targets stay as specified: 20 ms cadence, 10–20 ms
   start interval, full recalculation at most 20 ms aiming toward 10 ms.
3. Observation budgets(D04) are configurable,never hardcoded. Attempt timeout is
   a checker setting(default500ms,operational target100ms). Observation maximum
   age,stream liveness,clock uncertainty and pending-disable retention keep the
   health/ModuleConfig and activation contexts; health owners set defaults.
   Route lease and Ready timeout/staleness belong to operator/announcer;scan/check
   to BIRD and hold time to BGP,not one ModuleConfig. Effective values are
   observable and combinations validated before announcement.
   Retention is a resource bound only: exhaustion
   rejects or reports Unavailable while preserving the obligation, never an
   expiry that discharges it. The observation maximum age binds to the
   activation's checker configuration context; changing it mid-stream is a
   new activation, not a live deadline extension.
4. Resource limits are instance-attach configuration with startup validation.
   Defaults derive from the declared service/VS/real geometry, are explicit
   and inspectable through status and logs, and exhaustion follows the named
   dispositions of the specification without silent shrinkage. Historical
   constant orders are orientation, not qualified capacity.
5. Clock policy (D08): suspend-inclusive accounting stays in force — the
   specification's deadline language is unchanged. CLOCK_BOOTTIME remains the
   selected first implementation, with the owner approving the clock choice
   only after the specified cost experiments. If performance evidence later
   reopens the question, any alternative requires its own proof against the
   same predicates, including suspend accounting.

### Stages to an MVP

| Stage | Content | Exit criterion | Depends on |
| --- | --- | --- | --- |
| S1 | Wire schema and API derived from the F/O tables | Approved schema proposal | — |
| S2 | D01 typed-handle publisher,pools and recovery | Design signoff,implementation and independently executed applicable cross-process/crash/reader proof from the authority/recovery construction,not merely a plan,before dependent enabled-real traffic | S1 |
| S3 | D02 pinned-source selector port, sampler and counters | Implementation plus an independently built reference run reproducing the multi-real head/lookup vectors | S1 |
| S4 | D03 S5 endpoint-check verification and packet-path cost | Measured report on the qualification environment | Implementation, bench access |
| S5 | D04/D05 observation protocol and publish-before-removal | Approved protocol, principals and grants, then implementation | S2 |
| S6 | Qualification against the approved manifest | Pass every applicable approved limit and criterion on the pinned candidate under the approved clock policy | S2–S5 and S7 clock decision |
| S7 | D08 exploratory BOOTTIME cost experiments before qualification | Measurements and owner clock decision,not qualification pass | S5 |

The order is `S5 → S7 → S6`. S7 requires an approved protocol and separate
run authorization; it produces no numerical qualification verdict.
If S7 selects a different clock, update the affected S5 implementation and
repeat its proofs, including suspend. Repeat affected S4 measurements, including
direct clock-read and full packet-path cost, for the selected implementation.
Approve the clock and versioned threshold manifest with the applicable evidence,
then authorize S6 separately. A run exceeding an applicable limit does not
pass S6. Approval, run authorization and verdict are distinct.

### Recorded open engineering forks

- Registry width and pool geometry for the raised cap (shared-struct/ABI work).
- Formal SLO numbers, informed by exploratory baseline runs and fixed before qualification.
- The MVP owner approves S1/S5, run authorization and release. Implementation
  coordination and independent verification remain separate from acceptance.
