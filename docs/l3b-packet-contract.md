# l3b Packet Contract

Normative packet companion to the [main l3b contract](l3b-contract.md).
This is the complete normative content formerly in its section 12, including
the vectors embedded there. The main contract retains section 12's navigation
anchors and all independent [section 18 conformance obligations](l3b-contract.md#18-conformance-obligations).
Its [normative-language convention](l3b-contract.md#normative-language), numeric
domains, identity, session authority and publisher rules apply here unchanged.
This specifies target behavior, not implementation or executed evidence.
Every conformance vector is **NOT RUN**; ordinary TCP/UDP checksum policy is **UNSET**.

## Contents

- [12. Packet Surfaces](#12-packet-surfaces)
  - [Packet Drop Precedence](#packet-drop-precedence)
  - [Packet Path Rules](#packet-path-rules)
  - [External Fanout Configuration And Clone Headers](#external-fanout-configuration-and-clone-headers)
  - [Adoption And Fanout Boundary Conformance](#adoption-and-fanout-boundary-conformance)
  - [MSS And Ordinary Encapsulation](#mss-and-ordinary-encapsulation)
  - [l3b-To-Real Outer Headers](#l3b-to-real-outer-headers)

## 12. Packet Surfaces

### Packet Drop Precedence

The sequence below orders the selected packet checks for multi-fault attribution,
separately at each detecting owner. Branches skip inapplicable stages; a stage
does not authorize an otherwise inapplicable check. Containing structure and
required authority are validated before dependent semantics, with bounds/length
before semantics within each element. Structural guards run where their fields
are required, not as an eager transport or quote scan before the fragment floor.
The catalogue scopes shared failure families to their producing branch/stage;
it does not add drop reasons. Management RPC precedence is unchanged.

| Stage | Normative sequence |
| --- | --- |
| S1 Live base structure | Validate the fixed IP header, declared lengths and the complete IPv4 option-region bounds against the available packet. Do not scan individual options or access transport here. |
| S2 Family admission | IPv4: real-fragment floor, then options in wire order, then upper-protocol dispatch (including unsupported AH/ESP). In one option, bounds/length/pointer validation precedes remaining-route semantics. IPv6: walk the chain in wire order; a ninth supported extension is rejected without reading its body. Within each supported header, bounds/length precede HBH-position and applicable Fragment, Routing or TLV semantics; TLVs follow wire order. Encountered AH/ESP/No Next Header or unsupported upper protocol rejects using the validated protocol tag, without inspecting an unsupported body. Then dispatch the admitted upper protocol. |
| S3 ICMP integrity | Bounds-check the required ICMP header/message first; for IPv4 validate the enclosing IPv4 header checksum, then the complete ICMP message checksum. ICMPv6 validates its complete message/pseudo-header checksum. Integrity precedes type, Echo roles and quote parsing. |
| S4 Branch admission | Reject unsupported ICMP types. Echo: addressed-VIP reply-source role, then request-source/reply-destination role, then acquire required coherent eligibility authority before deciding whether the VIP is linked and active/inactive; acquisition failure is not no-match, an unclaimed VIP uses no-matching-VS, and an all-empty linked set uses Echo-inactive. Only then prepare and hand off the reply, counting any construction failure without a partial reply. No ordinary TCP/UDP VS or service-policy admission, session resolution, real selection or MSS. Error: bounded quote parsing, including structural headers and required fields in wire order, before association/query. Quoted action bits and remaining routes retain their nonexecuting rules. |
| S5 Ordinary forwarding | Bounds-check required TCP/UDP fields before dependent lookup. Acquire required descriptor/configuration authority before using it to establish VS match or absence: inability to evaluate a match is not no-match. Then service-policy availability/evaluation, session resolution, eligible-real selection, MSS parsing/preparation, encapsulation representable length then headroom/buffer, required atomic session commit, handoff. Preserve existing operation-specific dependencies, bounded revalidation/repreparation and no-rollback rules. |
| S6 External association and delivery | Preserve trusted ingress binding; complete required quote validation before association/query. If association authority cannot be established, classify unprovable rather than fabricate multiple/stale/absent. With established authority, distinguish multiple candidates, a stale-only association, or no association. Query only a valid unique association; indeterminate drops before delivery/fanout. Owned uses local l3b delivery: acquire required authority, resolve the session and validate its live pin, prepare encapsulation with representable length before headroom/buffer, then any required atomic permitted refresh/copy-forward commit and handoff. Preserve operation-specific deadline, suppression and bounded revalidation/repreparation rules; no creation, ordinary service-policy/MSS, reselection, rebind, requery or refanout. Fanout readiness is not a delivery gate. Not-owned checks unknown peer configuration, missing needed source, peers-above-burst, then credit/reservation. |
| S7 Funded peer attempts | Independently for each funded peer: allocation, complete-copy, representable length, then headroom. Stop that failed attempt only; attempt all remaining funded peers without retries/refunds. These failures are not original terminal drops. |

**UNSET ordinary TCP/UDP checksum admission stage:** this amendment does not
select checksum validation/drop behavior or its final exact position in this
sequence, including offload assumptions or zero-checksum UDP/IPv6 handling.
The decision is required before any target-contract implementation or conformance
work whose result depends on ordinary TCP/UDP checksum behavior, and in any case
before full packet semantic acceptance. This explicitly incomplete packet-policy
area does not block the scoped representation/mutation inventory of #2718; it is
not a promise that malformed-checksum outcomes already agree across implementations.

On the first terminal fault, stop later dependent processing and count only that
primary terminal reason at this owner, with the entry byte snapshot. Earlier
native COUNT, successful WRR consumption or preparation are not rolled back;
session atomicity and postcommit behavior remain as specified. Funded clones use
their separate S7 attempts, not a global stop after the first failed peer.

### Packet Path Rules

- Once invoked, l3b consumes and counted-drops ordinary traffic with no matching
  VS, including an empty ModuleConfig; there is no implicit pass-through.
  Unrelated routing must occur before this invocation. Unsupported ordinary
  IPv4 upper protocols counted-drop, preserving separate TCP/UDP admission,
  Echo and supported ICMP-error paths rather than rejecting all IPv4 ICMP.
- Bounded validation and real-fragment rejection precede dispatch. Ordinary
  TCP/UDP traffic alone follows VS lookup, bound service-policy admission,
  session resolution and eligible-real selection, MSS processing and
  encapsulation, then, only if session mutation is needed, the required atomic
  session insert/refresh/copy-forward or authorized rebind commit before handoff.
  OPS front live eligible hits and misses do not require a write; back migration
  and invalid-pin rebind do. Packet-preparation failure creates no session and
  extends no deadline. A failed required commit counted-drops the prepared packet
  without stateless fallback or partial authority/suppression changes. After a
  successful commit, later transmission loss does not roll the session back.
  Lookup/selection alone is not a session commit; the same late-commit boundary
  applies to permitted reverse-session delivery refresh. Echo and ICMP errors use the separate paths below,
  not ordinary TCP/UDP VS or service-policy admission.
- Before Echo reply generation, apply SourceSet separately to the addressed VIP
  as future reply source and to the request source as a valid request source and
  future reply destination. A forbidden address on either leg consumes and
  counted-drops the original. Multicast/broadcast request sources fail because of
  their SOURCE role, not a global ban on such destinations.
  It performs no response-counter increment, session lookup/write, real selection,
  encapsulation or fanout. There is no alternative address or ICMP error reply.
  This does not change F02 storage/exact lookup or ordinary TCP/UDP admission. It is an
  explicit product limitation, not full RFC endpoint compliance: RFC 4443 says
  multicast Echo requests SHOULD receive replies, whose source MUST be unicast.
  A loopback request source (IPv4 127/8 or IPv6 ::1) is not rejected solely for
  that class and receives the ordinary reply subject to the remaining checks.
  RFC 1122 section 3.2.1.3(g) forbids 127/8 addresses outside a host; that
  restriction does not make a node-local reply an automatic violation. Whether
  the reply stays node-local or reaches a network egress is an unproved
  integration boundary, not supported egress.
- After both address-role checks pass, Echo eligibility aggregates only VSes linked in the invoked ModuleConfig's
  protected coherent configuration, not necessarily the latest published one.
  At least one configured real among those VSes at the addressed family/VIP
  makes it active, regardless of administrative enablement, weight or health.
  Echo signals configured VIP availability, not backend health: a nonempty set
  remains sufficient even when every real is disabled, zero-weight or unhealthy.
  An admitted IPv4/v6 Echo Request then produces one reply without session
  lookup, creation or refresh. If all those VSes have empty real sets, no reply
  is generated and the original counted-drops with a dedicated Echo-inactive
  reason, distinct from no-matching-VS accounting. The reply uses
  the addressed VIP as source, the request source as destination, the family's
  Echo Reply type, code zero and valid checksums. Its initial TTL/Hop Limit is
  65 at l3b handoff, not a promise of 65 after downstream routing.
  The historical rationale is one subsequent routing decrement: exactly one
  such decrement yields 64. Zero or multiple decrements give no general egress
  value guarantee; this adds no TTL/Hop Limit knob or new routing requirement.
  The fixed IPv4 TTL 65 has no knob: this is a known deviation from the
  RFC 1122 section 3.2.1.7 and RFC 1812 section 4.2.2.9 requirement that a
  fixed IPv4 TTL be configurable. This IPv4 requirement does not govern the
  IPv6 Hop Limit.
  In both families, preserve the request's identifier, sequence and payload
  byte-for-byte, including an empty payload.
  Transform the admitted request with unchanged header geometry, rather than
  constructing bare IP headers. Preserve IPv4 options, IPv6 extensions, TOS/Traffic
  Class, IPv4 ID/DF and other IP fields, and IPv6 flow label. Only the endpoints,
  TTL/Hop Limit, ICMP type/code and necessary checksums change as specified above;
  header lengths remain unchanged. Recompute or correctly update the IPv4 header
  checksum including options and the separate ICMP message checksum; for ICMPv6,
  account for the complete message and pseudo-header.
  Correct code changes from incoming 1 or 255 as well as type changes; a type-only
  checksum delta is insufficient. Existing fragment, TLV, Routing and checksum
  admission applies first; live AH never reaches Echo. No partial reply is emitted.
  This does not implement source-route reversal, special Record Route/Timestamp
  updates or full RFC endpoint conformance; preserving opaque options is not a
  claim of safe delivery in every reverse-path context. Retained IPv4 IDs carry
  known unmitigated reuse/reassembly risk, including Echo replies: a copied Echo
  ID is a conditional reuse risk under RFC 6864 section 4.3, not an unconditional
  violation. An Echo Request carrying an LSRR/SSRR option with a remaining
  route drops before dispatch under the IPv4 source-route rule below; an
  exhausted source route stays opaque. The later
  DF/MTU investigation includes Echo, without a new restriction or activation
  gate, unique-ID allocator or DF1 policy.
  Count each generated response once by (ModuleConfig, IP family, decoded VIP
  address), without protocol or port dimensions or arbitrary VS attribution.
  All linked VSes at that family/VIP share the counter; equal VIPs in different
  ModuleConfigs do not merge counters. All-empty and other no-response outcomes
  are not reply-counter events; existing counted-drop accounting still applies.
  This binding is distinct from VS-scoped native policy counters; F38 selects
  current-binding readback and accumulation identity, not continuity. The remaining
  counter naming and protected-lifetime implementation remain downstream.
- Supported incoming Echo Request and error types accept every code in 0..255;
  code alone is never grounds for rejection. Length, fragment, quote and
  ownership validation still apply, including the incoming real-fragment floor.
  This does not broaden the supported type set. Generated Echo Replies use code
  zero; delivered errors remain unchanged, including their incoming code.
- On ICMP paths, validate the checksum over the complete incoming ICMP message,
  including its header and all message bytes within the validated IP lengths,
  not just the fields inspected for dispatch or quote parsing. ICMPv6 includes
  the proper IPv6 pseudo-header; ICMPv4 also requires a valid enclosing IPv4
  header checksum. An invalid checksum counted-drops before Echo response,
  ownership query, session mutation or fanout. Accepted codes 0..255 do not
  bypass these checks. This selects no new ordinary TCP/UDP checksum policy
  and does not require checksum validation of a truncated quoted packet's body;
  quoted TCP/UDP admission remains unchanged.
- Supported ICMPv4 errors are
  Destination Unreachable, Time Exceeded and Parameter Problem;
  supported ICMPv6 errors are Destination Unreachable, Packet Too Big,
  Time Exceeded and Parameter Problem. The bounded quote identifies the VIP as
  quoted source and exposes TCP/UDP ports. Quoted first fragments are usable
  when ports exist; quoted non-initial fragments counted-drop; IPv6 atomic
  fragments are ordinary. These quote rules do not admit fragmented incoming
  error packets through the real-fragment drop floor.
  IPv4 Source Quench (type 4) and Redirect (type 5) are unsupported at both l3b
  and the external distributor. After existing IP/ICMP integrity checks, reject
  their type before quote parsing, association, ownership query or fanout, using
  the existing unsupported-type counted-drop family at the detecting owner.
  No session effect, real selection, encapsulation, reply, clone or fanout-token
  spending occurs. Routing/local handling belongs outside these paths.
  [RFC 6633](https://www.rfc-editor.org/rfc/rfc6633.txt) deprecates Source Quench;
  excluding Redirect is separate product policy, not a requirement of that RFC.
- For an admitted reverse ICMP quote in VIP-to-client direction, swap both
  addresses and both ports to recover the canonical client-to-VIP session key;
  take TCP/UDP transport from the quote, not outer ICMP. Retain quote VIP,
  protocol, family, fragment and port-presence validation. Portless identity
  does not waive required quote ports. A wrong quote cannot be rerouted through
  another VS to fabricate a matching key. Echo and unsupported paths produce no
  session effects. Packet rejection before a successful session commit leaves no
  partial session mutation by this operation, subject to normal expiry and
  independent concurrent operations. Loss after a successful commit does not roll
  it back. This does not prohibit internal parsing on admitted paths or introduce router-wide drop
  rules.
- An ICMP type outside the supported Echo Request and error paths, if delivered
  to l3b, counted-drops with a distinct unsupported-type reason. It invokes no
  session operation, real selection, encapsulation or error-distribution path.
  Traffic needing routing, Neighbor Discovery or local delivery must be directed
  to those paths before l3b. This is not a router-wide ICMP prohibition and does
  not alter supported-type, malformed-packet or fragment handling. Exact counter
  naming is part of the separate naming contract.
- Raw ICMP errors enter the separate error-distribution module, which performs
  existing IP/ICMP integrity checks and rejects unsupported types, including
  Source Quench and Redirect, before bounded quote parsing and offending-flow
  normalization. Before any ownership query, its trusted
  ingress binder selects the actual instance and allowed scope; packet fields
  cannot self-select either. Fully validate the quote, including required ports.
  For VIP-to-client quotes, use quoted source VIP, quoted source service port and
  quoted TCP/UDP protocol to select a unique immutable VS incarnation, then its
  unique external projected-service-incarnation association. Section 10's global
  protocol-set/port-set nonoverlap applies, including mixed VSes, not a new
  exact/wildcard priority. Keep actual quoted transport in the recovered key.
  Reverse base addresses and ports; omit ports from a portless session key only
  AFTER selecting the VS. Never extract a destination from a quoted segment list.
  Protect this coherent association context through query, handoff and budget
  use; it need not be newest and implies no cross-owner atomic transaction.
  Absent, multiple, stale or unprovable associations consume and counted-drop the
  original before query, state effects, clones or tokens. Do not retarget after a
  query. Recreated names cannot inherit an old incarnation's association; a new
  VS incarnation does not authorize resetting the external service's bucket credit.
  Concrete binder API, schema, table and fencing mechanisms remain downstream.
  Its read-only l3b ownership query
  protects one coherent session-chain generation and returns only `owned`,
  `not-owned` or `indeterminate`, never real identity or table memory.
  `owned` permits a separate handoff of the unchanged error to the same VS for
  reverse-session delivery to its enabled real; it is not a permanent delivery
  guarantee across expiry or state changes. Disabled, removed or reincarnated
  real hits counted-drop without rescheduling. Permitted delivery refreshes
  to now plus `other`, unless OPS suppresses refresh for actual quoted UDP;
  mixed-VS TCP still refreshes. It creates no session,
  selects no new real, rebinds no affinity and revives no expired record.
  Queries never mutate sessions. Query/delivery API placement, execution
  context, packet ownership and protected generation lifetime require
  downstream integration proof, not access to shared tables by the caller.
  If the session is absent or expired at delivery after an `owned` query, the
  error counted-drops without another ownership query, refresh attempt or peer
  fanout, and without reselection, rebind or revival.
  Fanout readiness is NOT a gate for this owned local delivery. A `not-owned`
  result uses only the bound external service's readiness, peers and all-or-none
  budget below; indeterminate handling is unchanged.
- The error-distribution module owns peer fanout and final error disposition;
  l3b performs no peer fanout. `indeterminate` consumes and counted-drops the
  original without retry or fanout; received clones never refan. Each projected
  service requires positive output-clone `fanout_rate` and `fanout_burst`, with
  burst at least the eligible peer count. One service-wide budget spans workers:
  a `not-owned` original atomically funds all eligible clones or emits none.
  Allocation and encapsulation failures have separate counters. Absent budget
  fields do not satisfy readiness. The budget is one token bucket per projected
  service, shared across workers: one token funds one output clone, the rate
  is a positive integral number of clones per second, and burst is a positive
  integral capacity in clones. Refill uses checked elapsed milliseconds between
  uint64 absolute times in one identified monotonic clock domain for the shared
  bucket. Derived elapsed_ms retains uint64 range even above UINT32_MAX;
  it must not be narrowed or truncated to uint32. Mathematical accrual is
  `fanout_rate * elapsed_ms / 1000` clones;
  retain fractional credit without floating point and cap total credit at burst;
  excess credit is discarded, not banked. The rate remains clones/second.
  A newly created budget starts full. A surviving service/dataplane budget
  keeps its credit across control-plane restart, takeover and configuration
  replacement; none of those events creates a fresh burst.
  A rate/capacity change first accrues under the old rate up to the transition,
  then carries credit capped by the new capacity. Peer-set changes do not refill
  it. Accrual, policy transition and reservation have one consistent ordering:
  a packet uses one policy and peer set, never mixed old/new values. For `n`
  eligible peers it atomically reserves `n` tokens or none. With insufficient
  credit it counted-drops without partial fanout; with no peers it spends none.
  Failures after reservation never refund tokens: admission budgets attempts,
  not guaranteed delivery. If peers grow beyond burst, fanout is not ready and
  counted-drops without cloning until the configuration or group permits full
  funding; capacity is never raised implicitly.
  Bounded numeric representation, clock precision/source, safe elapsed-time
  arithmetic and clock/arithmetic-error handling must be specified and verified
  before activation. The cap is not permission to overflow an intermediate:
  an interval sufficient to fill the bucket must yield the cap without unsafe
  multiplication. No particular clock hardware, lock or CAS layout is selected.
  The distributor consumes the original exactly once per invocation on every
  terminal outcome, including successful fanout, a valid zero-peer set and
  failure. This is packet ownership, not crash-proof exactly-once processing;
  only clones leave through peer fanout. Successful fanout and a valid zero-peer
  set add no drop count merely for consuming the original; zero peers spend no
  tokens. Existing failure and indeterminate-ownership accounting is unchanged.
  Each funded peer receives at most one clone attempt. After a peer-local
  allocation or encapsulation failure, attempts for all remaining funded peers
  MUST continue. There are no retries, refunds or rollback of emitted clones;
  atomic budget admission does not imply atomic network delivery. External
  fanout configuration and update/readback follow the contract below; integration
  proof remains required.

### External Fanout Configuration And Clone Headers

The external owner accepts whole-configuration updates under its own expected
revision, atomically binding accepted policy, peer/local membership, source
addresses and budget transition. Failure preserves its last valid state. This
revision is not an l3b token and provides no cross-l3b transaction. Each projected
service requires positive uint32 `fanout_rate` (clones/second) and `fanout_burst`
(clones). Absent required fields or peer configuration are not an explicit empty
peer set and cannot establish readiness. Existing external limits must be
reconciled before implementation; no peer node IDs or 65535-peer cap is implied.

Peers are explicit deduplicated (family,address) endpoints, with an explicit local
endpoint set. Before deduplication or local exclusion, validate EVERY explicit
peer and local endpoint against DestSet: an invalid peer cannot hide in the local
set. Validate EVERY explicitly supplied fixed clone source against SourceSet even
if its family currently has no eligible peer. This external configuration rule is
deliberately different from VS dormant source-template retention. A provided-invalid
endpoint or source rejects the whole external configuration, preserving its last
accepted/active state and revision; no external RPC error code is prescribed here.
Missing a needed family source instead follows the not-ready rule below, not a new
configuration-rejection rule. No new wire field, epoch or cross-owner transaction.
Matching valid local endpoints are excluded. There is no dynamic peer
health filter. Unknown configuration is not empty. One protected peer/source/
policy generation determines n and all funded attempts. Missing the configured
source for any eligible outer family makes the service not-ready, not a reason
to skip that family. This blocks admission BEFORE reservation: consume and count-drop
the original, emit no clones and spend no tokens. Valid empty peers spend no credit; unknown peers cannot use
the empty-success outcome. Preserve burst>=n, all-or-none reservation, no refunds,
one attempt per funded peer, mandatory remaining attempts and original consumption.

Budget identity is external deployment plus projected-service incarnation.
Replacement, restart, takeover and detach/reattach preserve credit. Only actual
retirement followed by a new incarnation can start full, after fencing the old
reservation authority. Uncertain recovery is unavailable, never reset/refill.
Preserve monotonic fractional credit, old-rate accrual then carry/clamp on policy
change, and no peer-churn refill. The clock domain cannot silently change beneath
surviving state. Clock-source/precision, safe arithmetic and recovery proofs remain
preactivation obligations; the semantic absolute-time unit is uint64 milliseconds,
not a selected physical layout.

External readback distinguishes accepted revision and complete service policy,
peer/local membership, configured sources and budget identity from observations:
observed generation, readiness, budget availability and fanout/failure accounting
with provenance. Missing observation is unavailable, never a fabricated full
bucket or empty peer list. Accepted configuration is not proof of activation.
The external owner's API must realize these meanings before integration; it is
not another l3b operation or authority over l3b sessions.

Clones use IPIP only. Outer family is the peer family; protocol/next-header is
4 for inner IPv4 and 41 for inner IPv6. Use the fixed configured source of that
outer family and peer destination, TTL/Hop Limit 64, traffic class zero and IPv6
flow label zero, preserving the complete inner packet unchanged. IPv4 has IHL 5,
total length 20+L, ID 1, fragmentation field zero (DF0), and valid header checksum.
IPv6 has payload length L and no extensions or jumbogram, where L is inner length.
All four inner/outer family pairs are supported; there is no IPv6 source-word
rewrite. Check complete-copy, headroom and representable lengths before emission:
after a coherent ready generation funds the set, a peer-local allocation or
encapsulation failure counts, emits no partial clone, refunds nothing and continues other funded
attempts. Recipient admission, decapsulation, clone provenance and the ownership-
query contract require integration proof before activation; source ACLs do not
constitute cryptographic authentication. Received clones never refan, and external
trusted-provenance enforcement remains unproved, not implied by this rule.

IPv4 clone ID1/DF0 is separately accepted with known, nonmitigated reassembly risk
for both inner families: downstream fragmentation with equal outer source,
destination and protocol and reused ID may corrupt or drop reassembly. This
choice does not itself block IPv4 activation or establish standards conformance;
repeating constant ID 1 with DF0 for the same source, destination and protocol
within the maximum datagram lifetime violates RFC 6864 section 4.3 non-atomic ID
uniqueness, an accepted known nonconformance. All other integration gates
remain. The later DF/MTU/PMTU/oversized/feedback
investigation must cover both l3b-to-real and distributor IPv4 paths. No unique-ID
allocation, automatic DF1 or mitigation is selected.

### Adoption And Fanout Boundary Conformance

The following vectors are required and **NOT RUN**:

- An empty active topology or detached-only references report `NOT_APPLICABLE`,
  never vacuous `ALL_ADOPTED`. An idle lagging or joining unacknowledged participant
  yields `PENDING`; a nonempty coherently observed fully acknowledged cohort yields
  `ALL_ADOPTED`. Withdrawing a lagging participant cannot exclude it before fencing;
  unknown topology/fencing yields `UNAVAILABLE`. After proven withdrawal/fencing,
  evaluate only remaining participants, or `NOT_APPLICABLE` if none remain.
- Cohort change preserves config token and counter-page validity. Adoption evidence
  from a different cohort/context cannot establish `ALL_ADOPTED`; adoption does not
  establish reader drainage or force latest linked-VS descriptor acquisition.
- Eligible IPv4 and IPv6 peers with only an IPv4 source consume/count-drop the
  original before reservation, with zero clones and zero token spend. Unknown peer
  configuration is not empty success; a valid empty set spends zero without a drop
  merely for consuming the original.
- With both sources and a coherent ready generation, reserve for the eligible set.
  A peer-local allocation/encapsulation failure counts and emits no partial clone;
  attempt all other funded peers without refund. Do not confuse this with missing
  configured-source admission failure. No new counter tag, timer or fallback.

### MSS And Ordinary Encapsulation

- The MSS policy is F24's typed boolean, default false, with no public numeric
  MSS knob. Actual TCP selects this processing even in a mixed VS, never actual
  UDP. Without the authentication bypass below, clamp the first valid MSS above
  1220 to 1220 and preserve a smaller value including 0.
  Option parsing stops at EOL, skips NOP, checks the other lengths, and the
  MSS option has length 4. On enabled eligible TCP SYN&&!RST (including SYN-ACK),
  walk all options through EOL/end before mutation, remembering only the first
  valid MSS. A malformed option tail even after that MSS, or failure to obtain
  packet-buffer space for an
  otherwise required MSS modification, is a counted drop with no partial
  modification. Insufficient logical TCP option space for the four-byte
  insertion is not such a failure: a valid header remains unchanged by the MSS
  step. A disabled policy or noneligible flags add no MSS-specific validation or
  rejection, including for malformed authentication options. Mutations correct
  lengths and necessary checksums. The constants 1220 and 536 are not path-MTU guarantees.
  A structurally valid TCP MD5 Signature (kind 19, length 18) or TCP-AO
  (kind 29, length at least 4 and within option bounds) anywhere before EOL
  bypasses MSS insertion and clamping: leave the entire inner segment unchanged
  in this step. Invalid MD5 length, AO length below 4 or a truncated option is
  malformed under the enabled eligible parser, not a bypass. EOL terminates
  parsing; padding after it is not authentication. No keys, authentication
  verification or new knob is introduced. Although MD5 clamping can be safe,
  the uniform bypass is deliberately simpler: insertion breaks MD5/AO, and AO
  may cover other options depending on hidden MKT state. Bypass skips only MSS,
  never normal ACL, session processing or encapsulation; live AH still drops.
  With no authentication bypass and no MSS, a valid TCP header of at most 56 bytes
  grows by four bytes: insert MSS 536 at TCP offset 20, before the old options.
  Do not reuse EOL/padding space. Preserve all original option and payload bytes
  with their packet-relative offsets increased by four; correct TCP data offset,
  lengths and applicable checksums. A full 60-byte header stays unchanged by
  insertion even if padded. Retain enabled TCP SYN without RST eligibility,
  including SYN-ACK; only the first valid MSS is processed, with later duplicate
  MSS values unchanged, not rejected merely for duplication or all clamped.
  Buffer-failure atomicity and the late session-commit boundary still apply.
  Malformed-option drops precede session commit and cause no partial packet
  mutation; this does not promise that a preceding WRR selection never advances.
- DSCP is not an l3b policy in the new API. l3b transfers the inner traffic
  class into the outer header and preserves ECN; the configured `dscp` module
  after l3b performs any marking, with its own configuration and readback, no
  shared VS token and no promised atomic two-module change. The operator
  declares the scope explicitly, because the module sees all traffic that
  reaches it in the pipeline, and because two services with different required
  marks that share a real cannot be distinguished by the current
  destination-prefix module. Migrating existing per-VS marks is not automatic:
  conflicting policies on a shared real need an explicit operator decision or
  an adapted classification, and a mark must not be silently chosen or
  extended to all traffic to that real. The shared marker must preserve ECN
  for both families and update the IPv4 checksum when marking.
- Encapsulation reuses the existing shared helpers for IPIP and GRE. l3b
  selects the real and computes the outer source from the source policy of
  [section 11](l3b-contract.md#11-outer-source-policy), and the library builds the headers, checks the headroom and
  returns the result. A typed VS mode selects IPIP or GRE, IPIP is the
  default, and an unknown or explicitly unspecified value is rejected. GRE is
  v0 with a four-byte header and no optional key, checksum or sequence, and
  the inner EtherType follows the packet family; the outer family follows the
  real and the outer source follows the source policy. The MSS step precedes
  encapsulation, a length or headroom error is a counted drop rather than a
  mode switch, and no separate encapsulation hop is required. NAT is outside
  the current mode set and is rejected rather than mapped to GRE.
- Packet parsing floor: every real IPv4 packet with MF or a nonzero fragment
  offset and every IPv6 packet with M or a nonzero fragment offset receives a
  dedicated counted drop before transport access, VS lookup, ACL evaluation,
  session, selector, or encapsulation.
- IPv4 source-route options: on live IPv4 packets, an LSRR (type 131) or SSRR
  (type 137) option with a remaining route, meaning a well-formed option whose
  pointer is not greater than its length, counted-drops before dispatch with a
  dedicated source-route reason: no transport-dependent processing, VS lookup,
  ACL evaluation, session/selector effects, Echo reply or encapsulation. A
  malformed option length or pointer in the currently visited option, including a pointer below the RFC 791
  minimum of 4 or an option length outside the IPv4 option area, counted-drops
  under a distinct malformed-option reason, never the remaining-route reason
  for that option. A terminal fault in an earlier option stops the scan.
  An exhausted option (pointer greater than length, which RFC 791 defines as an
  exhausted route) is unchanged, as are Record Route and Timestamp handling.
  l3b executes no route and rewrites no destination. Quoted IPv4 headers are
  validated structurally only: a quoted remaining route is not by itself
  grounds for rejection. RFC 7126 sections 4.3.5 and 4.4.5 recommend dropping
  these options by default behind a configuration knob; this drop is fixed with
  no knob, so it does not claim full RFC 7126 conformance.
- Live IPv6 processing walks at most eight bounds-checked Hop-by-Hop, Routing,
  Destination Options and Fragment headers. A ninth supported extension
  counted-drops before transport access or state effects. Encountered live AH
  counted-drops as unsupported before transport-dependent processing, ACL,
  session/selector effects, Echo or fanout, independent of `mss_fix`; this is not
  an authentication-failure claim. IPv4 protocol51 also follows unsupported drop.
  Quotes separately retain structural AH parsing using encoded length and a
  12-byte minimum within the existing eight-header and required-field bounds.
  A quoted AH is not by itself grounds for rejection or an authentication claim.
  Every traversed live Routing header must be structurally bounded and have
  Segments Left zero; nonzero counted-drops before the same effects. An unknown
  Routing Type with zero is opaque and allowed subject to other admission checks.
  Do not execute segment lists or rewrite destinations, and generate no Parameter
  Problem. This is limited admission, not full RFC routing-header compliance.
  Quoted Routing headers require structure only: nonzero quoted Segments Left
  alone adds no rejection. Normalize quoted base addresses and reverse ports,
  not segment lists; general ICMP error delivery for source-routed flows is not
  promised. For admitted incoming ICMPv6 with Segments Left zero, the pseudo-header
  uses the current base-header destination. No ordinary TCP/UDP checksum expansion.
  ESP, No Next Header and unsupported upper protocols counted-drop. An atomic
  IPv6 fragment (offset zero, M clear) follows ordinary processing. Exact-port
  VS and ACL port predicates require an available transport header; missing
  fields fail closed. These TCP/UDP port predicates do not precede Echo or
  the separate ICMP-error path; no second error parser is introduced in l3b.
  Hop-by-Hop is permitted only immediately after the corresponding IPv6 base
  header, both in incoming IPv6 packets and parsed IPv6 quotes. Misplaced or
  repeated Hop-by-Hop headers counted-drop even within the eight-header bound;
  reject an invalid quote before ownership query. This selects neither a general
  ordering for all extensions nor an ICMP Parameter Problem response promise.
  Live incoming packets on these l3b paths validate every Hop-by-Hop and
  Destination Options TLV completely within its validated extension-header
  boundary before transport-dependent processing, ACL evaluation, session/selector
  effects, Echo response or fanout. Pad1 occupies one byte; PadN requires a present
  length and all declared bytes within the header. Padding is the only explicitly
  supported option; all others are unknown for this admission policy. Unknown
  action bits `00` skip the structurally valid option; `01`, `10` and `11`
  counted-drop without generating Parameter Problem. This is a bounded admission
  policy, NOT full RFC option/error-signalling compliance, including for `10/11`.
  Quotes are separate: structurally validate available traversed headers/TLVs and
  mandatory fields under the existing bounds, HBH-position, fragment and port
  rules, but do NOT execute quoted option action bits or require the missing
  original packet body. Malformed or incomplete quotes before required fields
  counted-drop before ownership query, with no ICMP reply. This neither expands
  routing scope nor changes ordinary TCP/UDP checksum policy or the eight-header
  limit and ninth-header drop.

### l3b-To-Real Outer Headers

Both IPIP and GRE use this matrix, independent of the shared helper's future
implementation. Encapsulation itself performs no additional TTL/Hop Limit
decrement; a separate routing hop is outside this rule.

| Inner -> outer | Outer TTL / Hop Limit | IPv4 ID and fragmentation field | IPv6 flow label |
| --- | --- | --- | --- |
| IPv4 -> IPv4 | Copy inner TTL | Copy inner ID and fragmentation field | N/A |
| IPv6 -> IPv4 | Copy inner Hop Limit | ID 1, fragmentation field zero | N/A |
| IPv4 -> IPv6 | Copy inner TTL | N/A | Zero |
| IPv6 -> IPv6 | Copy inner Hop Limit | N/A | Copy inner flow label |

**Known risk accepted for the current contract, not mitigated:** copied IPv4
ID/fragmentation fields and IPv6-to-IPv4 ID 1/DF 0 remain unchanged. If outer
packets with the same source, destination and protocol reuse an ID and undergo
downstream fragmentation, their fragments can be misassembled, causing corruption
or drops. Under RFC 6864 section 4.3, IPv6-to-IPv4 ID 1 with DF 0 repeated for
the same source, destination and protocol within the maximum datagram lifetime
violates non-atomic ID uniqueness, an accepted known nonconformance.
The inner-fragment admission floor does not prevent downstream outer
fragmentation. Copied DF 1 prevents fragmentation only at compliant nodes; it
does not guarantee ICMP feedback. Investigation of DF 1 plus MTU/PMTU,
oversized-packet and ICMP-feedback policy is deferred pending an explicit
decision; this note selects no such policy or new traffic restriction.

The real-fragment admission floor, inner traffic-class/ECN inheritance,
external DSCP marking and length/headroom counted-drop rules remain unchanged.
This matrix does not specify the error distributor's peer-clone headers.

Inner traffic-class/ECN inheritance is RFC 6040 section 4.1 normal mode: both
ECN bits, including CE, are copied into the outer header and the inner header is
left unchanged. l3b selects no compatibility mode, which RFC 6040 section 4.3
requires unless the encapsulator knows that no egress uses a legacy
decapsulator. Deployment therefore requires compatible decapsulation at every
real, including dropping an inner Not-ECT packet that arrives with outer CE
(section 4.2). This deployment requirement is unproved here and is not an l3b
runtime check.
