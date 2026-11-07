# l3b Representation and Operation Contract

Normative contract for [#2718](https://github.com/yanet-platform/yanet2/issues/2718).
This specifies target behavior, not implemented behavior or executed evidence.

## Contents

- [Reading This Contract](#reading-this-contract)
  - [Normative Language](#normative-language)
  - [Glossary](#glossary)
  - [Limit Units And Scope](#limit-units-and-scope)
- [1. Scope And Boundaries](#1-scope-and-boundaries)
- [2. Configuration And Observation Inventory (F01-F39)](#2-configuration-and-observation-inventory-f01-f39)
- [3. Operation Inventory (O01-O17)](#3-operation-inventory-o01-o17)
- [4. Cross-Cutting Obligations (C01-C08)](#4-cross-cutting-obligations-c01-c08)
- [5. Management Surface, Permits And Error Order](#5-management-surface-permits-and-error-order)
  - [Atomic Real Vectors And Operator Metadata](#atomic-real-vectors-and-operator-metadata)
  - [Complete Identity Non-Reuse](#complete-identity-non-reuse)
- [6. Accepted Readback And Pagination](#6-accepted-readback-and-pagination)
  - [Live Service Status](#live-service-status)
  - [ModuleConfig Adoption And Counter Observations](#moduleconfig-adoption-and-counter-observations)
  - [Semantic Counter Catalogue](#semantic-counter-catalogue)
- [7. Session Ownership, Capacity And Inspection](#7-session-ownership-capacity-and-inspection)
  - [Concurrent Session Authority](#concurrent-session-authority)
- [8. WLC Contract](#8-wlc-contract)
  - [WLC Inputs And Observations](#wlc-inputs-and-observations)
- [9. Publisher, Reader Protection And Lifecycle Direction](#9-publisher-reader-protection-and-lifecycle-direction)
- [10. Scheduler, OPS, Weights And Identities](#10-scheduler-ops-weights-and-identities)
- [11. Outer Source Policy](#11-outer-source-policy)
  - [Address Roles For Network Emission](#address-roles-for-network-emission)
  - [Selected VS Source And Family Transitions](#selected-vs-source-and-family-transitions)
- [12. Packet Surfaces](#12-packet-surfaces)
  - [Packet Drop Precedence](#packet-drop-precedence)
  - [Packet Path Rules](#packet-path-rules)
  - [External Fanout Configuration And Clone Headers](#external-fanout-configuration-and-clone-headers)
  - [Adoption And Fanout Boundary Conformance](#adoption-and-fanout-boundary-conformance)
  - [MSS And Ordinary Encapsulation](#mss-and-ordinary-encapsulation)
  - [l3b-To-Real Outer Headers](#l3b-to-real-outer-headers)
- [13. Service Policy Boundary](#13-service-policy-boundary)
- [14. Native Policy Mapping](#14-native-policy-mapping)
  - [Native Path Consequences](#native-path-consequences)
  - [Equality, Counters And Empty Policy](#equality-counters-and-empty-policy)
- [15. Policy Identity And Revision](#15-policy-identity-and-revision)
- [16. Format Contract](#16-format-contract)
  - [Complete Field Presence Matrix](#complete-field-presence-matrix)
  - [Byte-Page Framing, Cursor Errors And Read-Only Identities](#byte-page-framing-cursor-errors-and-read-only-identities)
- [17. Counter Identity](#17-counter-identity)
- [18. Conformance Obligations](#18-conformance-obligations)
  - [Validation And Retained-Behavior Boundaries](#validation-and-retained-behavior-boundaries)
  - [Multi-Fault Counter Vectors](#multi-fault-counter-vectors)
- [19. Downstream Boundaries](#19-downstream-boundaries)

## Reading This Contract

The main contract covers configuration, operations, session authority and
readback. The [packet companion](l3b-packet-contract.md) contains the complete
section 12 packet rules and its embedded vectors. Section 12 below preserves
the original entry anchors; section 18 retains its independent conformance cases.

### Normative Language

The uppercase keywords MUST, MUST NOT, SHOULD and MAY have the meanings of
[RFC 2119](https://www.rfc-editor.org/rfc/rfc2119.txt) and
[RFC 8174](https://www.rfc-editor.org/rfc/rfc8174.txt). Declarative defaults,
ranges, rules and explicit prohibitions are binding even without a keyword;
lowercase mandatory wording does not weaken them. Descriptions of possible
outcomes are not permissions. RFC quotations describe the cited RFC's force,
not an additional product-compliance claim. This convention also applies to
the packet companion.

### Glossary

| Term | Meaning here |
| --- | --- |
| VS | One virtual service with an immutable network selector and one incarnation. |
| Real binding | One configured full real key belonging to one VS incarnation. |
| Binding charge | One configured real-to-VS binding: the same endpoint in two VSes counts twice; several ModuleConfig links to one VS count once. Disabled, zero-weight, unhealthy and unlinked members still count. |
| Selector cell | One real-ID position in a selector's logical lookup/reservation, not a binding or a physical-memory byte. |
| Accepted configuration | The coherent committed caller configuration and preserved manual state, identified by the owning mutation token; not proof of worker adoption. |
| Observation | Read-only runtime evidence with availability and provenance, not token-stable accepted configuration. |
| Incarnation | Opaque identity of one object or real-membership lifetime; same-name/key recreation does not revive the old identity. |
| Mutation token | Opaque conditional-write grant for one accepted version/context; not an incarnation, create permit or authorization. |

### Limit Units And Scope

| Bound | Unit | Scope |
| --- | --- | --- |
| 2097151 | Configured real bindings | Actual dataplane-instance registry, all three schedulers/all owners, including unlinked VSes. |
| 209715200 | Reserved HASH/WRR real-ID selector cells | Shared pool per generation; charge reservations, not occupied cells only. |
| 16384 (initial policy) | Ordered VS bindings | Complete candidate of one ModuleConfig, not the instance sum. |
| 4194304 (initial policy) | Encoded bytes | Complete accepted export of one object, before acceptance. |

These logical limits do not qualify capacity or guarantee allocation. Retained
physical memory is accounted separately, including current, candidate, retired
and deleted versions and all still-accessible resources (sections 6–9).

## 1. Scope And Boundaries

- There are six session timeout classes: `tcp`, `tcp_syn`, `tcp_syn_ack`,
  `tcp_fin`, `udp` and `other`.
- Configured duration/timeout values are uint32 milliseconds, 0..4294967295 (about 49.7 days),
  subject to any stated positive-only domain. Absolute monotonic times are
  uint64 milliseconds in their identified clock domain, not UTC. The server
  permit clock, dataplane session clock and external-owner clocks are distinct
  domains; values from different domains are not interchangeable. Widen a
  duration to uint64 before checked deadline addition; no wrap or saturation.
  Derived elapsed intervals between same-domain monotonic uint64 millisecond
  timestamps retain uint64 range, with no narrowing or truncation to uint32.
  These intervals are not additional configurable durations.
- No protobuf package, tag, message spelling, C struct layout, attach
  mechanism or final public name is selected here. Final naming stays in
  [#2719](https://github.com/yanet-platform/yanet2/issues/2719). Standalone
  CHASH stays deferred to [#2904](https://github.com/yanet-platform/yanet2/issues/2904);
  shared hash machinery and WLC obligations are not deferred.
- Proof, implementation and audit stay downstream, not prerequisites to this
  semantic inventory: [#2725](https://github.com/yanet-platform/yanet2/issues/2725)
  owns publisher, session-chain, recovery and lifetime proof;
  [#2726](https://github.com/yanet-platform/yanet2/issues/2726) owns
  implementation with mandatory tests;
  [#2727](https://github.com/yanet-platform/yanet2/issues/2727) owns the
  independent evidence audit. Safe internal ACL invocation, compiler ownership,
  activation and lifetime proof are part of that downstream work.
- Every conformance obligation in section 18 is **NOT RUN**. This document
  makes no implementation, ABI, compatibility, qualification or production
  claim.

## 2. Configuration And Observation Inventory (F01-F39)

Each field carries its omitted/present/zero/empty/default/range semantics.
Section 16 adds the per-field binary protobuf, ProtoJSON and CLI YAML
representation and the protobuf/C consequences.

| ID | Field | Selected semantics |
| --- | --- | --- |
| F01 | Names and references | Exact case-sensitive `[A-Za-z0-9_.-]{1,79}`; required, empty rejects; no omission, control byte, normalization, trimming or first-character restriction; no automatic aliases. |
| F02 | VS VIP, protocol, destination port | Exact decoded address with independently optional protocol and destination port. Absent/null protocol selects TCP+UDP, not arbitrary IP; explicit protocol is TCP or UDP only, and explicit unspecified or unknown values reject. Absent port selects all ports for the selected protocol set; explicit zero is port zero, range 0..65535. One mixed VS shares its incarnation, real set and resources, but actual TCP/UDP distinguishes sessions. Immutable inside a live VS incarnation; an identity change is `FailedPrecondition`, a conflicting candidate is `InvalidArgument` under section 10's global selector-intersection rule. Mapped IPv6 endpoints in a concrete VIP field reject. |
| F03 | Real destination, family, port | Key is decoded family, address bytes and optional real port inside the VS incarnation; the optional real port domain is 0..65535; absent, zero and other ports stay distinct; no inheritance or implied rewrite; exact duplicate keys reject. Every stored real obeys section 11 DestSet, independently of administrative state, weight or health. |
| F04 | Real collection and continuity | Explicit nonempty members or explicit empty. Surviving keys keep accepted weights, administrative state, incarnations and pins; removed and readded keys are new and administratively disabled. Authored order is accepted state: a pure reorder advances the token and republishes while preserving surviving state. An explicit empty real collection leaves the VS still classified: traffic directed to it drops at selection rather than falling through to another rule or VS. |
| F05 | Configured and manual weight | Stored configured-weight domain 0..16777215 for WRR, HASH and WLC; a new or readded real defaults to 1 on omission. Manual weight is mandatory in the API; protobuf presence distinguishes missing from valid uint32 0. Omission is `InvalidArgument`. Zero excludes new selection in every mode. No clamping. |
| F06 | Administrative and health state | New and readded reals are administratively disabled; effective enablement is administrative enablement AND health eligibility; health never overrides an administrative disable. Manual enabled is mandatory in the API; protobuf presence distinguishes missing from valid false. External checker attempt timeout is configurable uint32 milliseconds, positive 1..4294967295, zero rejected, default 500 ms and operational target 100 ms, bound to checker configuration identity/revision under section 10, not a VS setter. |
| F07 | Scheduler | Create and full replacement select WRR, HASH or WLC explicitly; absent, unspecified and unknown values reject; partial real mutations do not resubmit the scheduler. There are no RR/PURR aliases, public hash masks, index masks or scheduler flags that redefine the selected scheduler. Standalone CHASH is deferred to #2904. |
| F08 | OPS and removed `pure_l3` | OPS is the only public VS flag, a separate boolean omitted as false; other typed booleans remain. TCP-only true is `InvalidArgument`; false/absent is valid. UDP-only and mixed true is retained and acts only on actual UDP. Session/transition rules and validation precedence are in [section 10](#10-scheduler-ops-weights-and-identities). The redundant `pure_l3` switch is rejected; portless mode derives from F02 port absence. |
| F09 | WLC power | Requested uint32 domain 0..100: absent/0 stores 0 and uses effective 10; 1..100 stores and uses the supplied value. Values >100 are `InvalidArgument`, not clamped or retained. Requested 0 differs from requested 10 in equality/readback. No public `max_weight`; the fixed selector-parameter ceiling 100 is not an input-weight limit. |
| F10 | WLC inputs, samples and confirmation | Complete all-worker sampling, generation-scoped fallback and guarded confirmation under [section 8](#wlc-inputs-and-observations). Requested cadence 20 ms, not a deadline or adoption SLO; derived results/observations are read-only. |
| F11 | WLC geometry and resource admission | Logical lookup length 1600*N over all configured unique real bindings including disabled and zero-weight members; an explicit empty selector at N=0. The aggregate binding charge is at most 2097151 per actual dataplane-instance registry across all three schedulers and agent owners, including unlinked VSes; physical admission is separate; all arithmetic is checked before narrowing or allocation. The 10000-VS and 100000-real values are unqualified planning inputs, not API caps. |
| F12 | Internal WRR cursor (not writable VS format) | Unsigned 64-bit. For a coherent nonempty expanded sequence of length L, `next = (cursor + 1) mod 2^64` and `slot = next mod L`; only a successful coherent selection commits `cursor = next`, and a later failure does not roll it back. Per worker and VS incarnation, shared across links, preserved across ordinary replacement and a control-plane restart. Wrap is ordinary arithmetic and may break strict cyclic order when L does not divide 2^64. Internal runtime state: no public cursor setter and no 32-bit narrowing. |
| F13 | `session_timeouts.tcp` | Optional uint32 milliseconds, domain 0..4294967295; omission defaults 60000; explicit 0 retained. |
| F14 | `session_timeouts.tcp_syn` | Optional uint32 milliseconds; omission inherits the accepted TCP value; explicit 0 retained. SYN without ACK selects this class regardless of FIN or RST, only on an otherwise permitted session operation; the separate rescheduling condition stays SYN with RST clear. |
| F15 | `session_timeouts.tcp_syn_ack` | Optional uint32 milliseconds; omission inherits SYN; explicit 0 retained. |
| F16 | `session_timeouts.tcp_fin` | Optional uint32 milliseconds; omission inherits TCP; explicit 0 retained. |
| F17 | `session_timeouts.udp` | Optional uint32 milliseconds; omission 60000; explicit 0 retained. |
| F18 | `session_timeouts.other` and container | Optional uint32 milliseconds; omission 60000; explicit 0 retained. A permitted ICMP-error refresh uses now plus `other`; Echo, query and UDP OPS never refresh. All classes expire at deadline <= now, and a configuration replacement does not rewrite old deadlines. The timeout container absent, null or `{}` means all children absent and resolves each default above; a scalar or list container rejects. |
| F19 | VS source bases M4 and M6 | VS-owned; omission materializes 0.0.0.0 and ::, with no interface or link fallback; supplied dormant values are retained; full replacement omission resets them. A partial real update preserves the materialized bases. Structural validation applies even to dormant families. |
| F20 | Optional IPv4 source override | Absent differs from explicit 0; a native bare IPv4 host address with no prefix metadata; the literal override or M4 supplies the outer source. A present zero override is structurally valid and fails output admission for a configured IPv4 outer family. |
| F21 | Optional IPv6 source template | Absent differs from present; present requires a raw address, omitted prefix means 128, prefix domain 0..128 including non-byte lengths, host bits preserved, the byte-splice algorithm of section 11, and SourceSet admission for every derivation input in each configured outer family. Raw template and prefix presence, not a normalized network. Per-real override remains deferred; class restrictions follow the role matrix, not a generic unicast blacklist. |
| F22 | Service policy binding | Required on VS create and full replacement: source-scoped identity (source namespace plus policy key), immutable revision and complete native ACL content, or an explicit empty-policy choice with the same metadata. Missing or null binding, identity, revision, or content without the explicit empty choice rejects as `InvalidArgument`; omission never means KEEP or allow-all. Explicit empty is deny-all. Partial real state/weight operations preserve the binding. Full semantics in sections 13-16. |
| F23 | Destination bindings | Nonempty ordered exact VS name and incarnation bindings, or explicit empty that unlinks without deleting the VS; duplicates reject; order affects accepted equality; the VIP, optional protocol and optional port come from the immutable VS identity; selector conflicts use section 10's global intersection rule, not a prohibition on the permitted wildcards. Separate ICMP paths are not ordinary TCP/UDP bindings. |
| F24 | `mss_fix` | Typed boolean, default false; actual-TCP eligibility, MSS transformations, authentication bypass and failure rules are in the [packet companion](l3b-packet-contract.md#mss-and-ordinary-encapsulation). Dormant for actual UDP, including mixed VSes; no public numeric MSS knob. |
| F25 | DSCP ownership | Per-VS DSCP policy is outside the l3b API. l3b inherits the inner traffic class and ECN into the outer header; the `dscp` module after l3b owns optional marking with its own configuration and readback, no shared VS token and no atomic two-module update. Destination-prefix marking cannot distinguish two VSes sharing a real, so this is pipeline and real-prefix scope, not per-VS equivalence. Marking must preserve ECN in both families and update the IPv4 checksum. Old packed or per-VS DSCP inputs reject even as null or 0. |
| F26 | Session size | Omitted or zero creation size resolves the current `l3b.policy.session_default_slots` (initially 65536) once at creation; that default or explicit positive input 1..1073741824 rounds upward to a power of two with a minimum actual size of 16 (1 becomes 16, 1000 becomes 1024); the size is shared across the workers of one VS in one instance. Resize omission or zero is `InvalidArgument`; a raw target below the accepted size is `FailedPrecondition` before rounding; an exactly equal target is a no-op; only a larger target rounds. A later default change never resizes a live VS, and no resource-driven downsize exists. Bounds are not allocation or flow qualification. |
| F27 | Encapsulation and NAT | Typed VS mode IPIP or GRE with IPIP as default; unknown or explicitly unspecified values reject. Minimal GRE v0 with a four-byte header and no optional key, checksum or sequence. The existing IPIP and GRE helpers are reused with no new header builders and no required separate pipeline hop; MSS precedes encapsulation. NAT is outside the reviewed mode set and is rejected rather than mapped to GRE. |
| F28 | Mutation token, incarnation, real target | Expected token required, 1..256 bytes; real target is full F03 key plus required incarnation, 1..256 bytes. Scope, malformed-input errors, target outcomes and the sole complete existing-object order are in [section 5](#5-management-surface-permits-and-error-order). |
| F29 | Create preparation | SERVICE or MODULE_CONFIG plus exact name; opaque 1..256-byte context-bound grant, lifetime 300000 ms, read-only uint32 valid_for_ms=300000 at issuance; registry cap 16384 across both kinds/all issuers. No upsert, reservation, payload binding or exactly-once. [Section 5](#5-management-surface-permits-and-error-order) defines separate create/prepare rules, malformed fields, permit failures, recovery and expiry-issuance errors; its existing-object order is not a create-token stage. |
| F30 | Accepted configuration readback | A coherent snapshot with the object incarnation, accepted token, encoding version, total bytes, offset, chunk, completion and next cursor. The snapshot separates the replacement descriptor from read-only metadata such as the actual accepted capacity, administrative state and real incarnations. WLC result and provenance, health and drain observations stay outside the token-stable bytes. One token and encoding version identify stable bytes; map-like ordering is fixed by the encoding rules, not by a library's deterministic mode. Configuration and token are read from one coherent protected state, coordinated with the final acceptance section and the owner epoch, so a new configuration is never paired with an old token. |
| F31 | Name-list responses | ASCII lexical inventory with a membership-revision, instance, epoch and position cursor; names, revision, completion and next cursor come from one coherent protected membership state per RPC (section 6), not necessarily newest at reply. Create and delete invalidate subsequent continuation requests; content changes alone do not. Bounded output and work, and authoritative recovery gating; an empty response is not recovery success. |
| F32 | `ListSessions` request | VS name plus limit and opaque cursor; omitted or 0 limit uses the centralized default (initially 100), a positive value up to the maximum (initially 1000) is accepted, a larger value is `InvalidArgument` rather than clamped. An empty cursor starts; an opaque continuation binds purpose, instance, VS incarnation, owner epoch, observed chain generation, finite initial layer watermarks and position. No cursor registry, TTL or between-RPC pin. A changed record limit shapes the next page, not the identity of the query, so it does not invalidate an in-flight continuation. |
| F33 | `SessionRecord` and `ListSessions` response | Coherent physical records with the flow key including actual TCP/UDP even in a mixed VS, the stored real key and incarnation, the expiry and the layer identity and role; a portless session reports both ports absent rather than fabricated 0. Expiry and the one page clock reading are uint64 milliseconds in the same identified dataplane clock domain; that reading serves both the expiry predicate and the response. The page reports the VS and chain identity. Cross-page duplicates are allowed and no unique-flow or snapshot guarantee is given; `complete` means the captured traversal end, and the next cursor is empty exactly when complete. A successful sparse page may return zero rows and still advance. |
| F34 | Network, range and transport presence | Decoded family identity, independently optional VS protocol and port, actual session transport, source rules, and explicit-empty top-level collections. F02/F03 bare hosts, F19-F21 raw sources and the native ACL networks and ranges of section 14 are deliberately different types; generic network normalization must not erase raw source-template bits. Section 16 carries the per-field binary, ProtoJSON and YAML null, empty and missing matrix. |
| F35 | Centralized operational policy | One authoritative location, the `l3b.policy` section of the control-plane configuration, for constants and suitable configurable limits; the CLI uses server defaults and lower layers receive the resolved budget. The initial read limits of 100 default records, 1000 maximum records, 4096 scan steps, a 256 KiB response and a 1024-byte cursor are provisional; the reviewed 4 MiB export cap and 16384 module-binding cap are unqualified. Policy changes are initially restart-required, hard API domains are not adjustable upward, no cap is bypassed automatically, ACL-owned limits are not duplicated in l3b, and lowering a limit must not make accepted configuration unreadable. |
| F36 | Service runtime status | Separate read-only `GetServiceStatus` observations: VS identity/configuration context, per-real health and published effective enablement/selector weight, WLC mode/freshness/applicability/provenance, scoped readiness and drain/cleanup phases. Bounded live pages, not an accepted export or cross-page snapshot; accepted-config changes abort continuation, runtime-only changes do not. Pending deleted-VS cleanup is addressed by incarnation, not a reused name; no infinite completed-history promise. Section 6 defines the surface. |
| F37 | ModuleConfig runtime status | Read-only `GetModuleConfigStatus`: accepted incarnation/token, target publication, authoritative relevant worker cohort and adoption `ALL_ADOPTED`, `PENDING`, `UNAVAILABLE` or `NOT_APPLICABLE`; idle workers count. Separate from accepted export, WLC readiness, reader drain and latest-VS acquisition. Section 6 defines availability and provenance. |
| F38 | Counter observation groups | Full semantic catalogue in section 6, including every counted-drop path, whether implemented or not. Read-only current-binding projection in O16 (VS and full-key/incarnation real groups) and O17 (module totals/reasons and ModuleConfig/family/VIP Echo), with semantic identity, accumulation identity, values and observation provenance. Shared cells read once; no sum of independent retained versions. Retain accumulation identity iff the complete contributing cell set and accumulated state continue without reset. External-owner counters stay external. |
| F39 | Operator metadata | Independent optional `metadata` strings on VS, each real and ModuleConfig: 0..256 decoded UTF-8 bytes, valid UTF-8, no NUL. Omission, textual null and empty normalize to empty, including on replacement; no trim, case or Unicode normalization, interpretation, uniqueness or parent inheritance. O02/O03/O06/O07 write them through full configuration; all metadata participates in normalized equality after survivor preservation. Real metadata belongs to the owning VS token; ModuleConfig has its own token. Metadata-only changes publish normally and advance the owning token once; equal candidates no-op, stale tokens remain `Aborted`. Partial operations, resize, runtime and recovery preserve all strings; recreation uses only newly supplied/default strings. No extra setter, numeric ordering or adoption proof. |

F35 configurable domains: `session_default_slots` is 1..1073741824 resolved
with F26 rounding; `list_default_records`, `list_max_records`,
`list_max_scan_steps`, `read_max_response_bytes`, `config_max_export_bytes`
and `module_max_bindings` are positive uint32 with defaults 100, 1000, 4096,
262144, 4194304 and 16384 respectively, and `session_default_slots` defaults
to 65536. Default records must not exceed max records; page framing and at
least a usable chunk or record must fit; incompatible reductions reject
activation. Cursor 1024, permit 256 bytes/300000 ms/16384 records, policy metadata 256, operator metadata 256, weight and
other hard domains cannot be raised through this section. Native ACL limits
are not configurable l3b quota copies. Rule and collection resource failure is
`ResourceExhausted` even below a representation ceiling; there is no
workload-fit guarantee.

`module_max_bindings` limits the complete candidate list of one ModuleConfig at
create or replacement, not the sum across an instance. Two ModuleConfigs with
10000 bindings each satisfy this cap alone under the default 16384 policy. Retained older versions do not
consume this logical cap again, but remain physically accounted until reclaimed.
Other admission limits still apply; passing this object-size bound does not
guarantee that resources exist for publication.

## 3. Operation Inventory (O01-O17)

One public l3b service exposes seventeen operations; there is no second
session-table service. The names are contract inventory, not a protobuf
package, tag or C ABI freeze.

| ID | Operation | Selected semantics |
| --- | --- | --- |
| O01 | `PrepareCreate` | Explicit absence preparation, not a `Get` side effect; a distinct bounded grant, no name or resource reservation, no payload binding. An existing target is `AlreadyExists` without a new grant; fixed expiry and invalidation of all permits from the preceding absence interval apply. An unrepresentable server-side expiry returns `Unavailable` with no grant under section 5's order. A lost response may retain a record; no deduplication and no invisible extension. |
| O02 | `CreateService` | Full configuration including required F22 policy binding and optional F39 VS/real strings plus a create token, absent target only; an existing valid request is `AlreadyExists` even when equal; duplicates, families and limits are validated before acceptance; new reals are administratively disabled. The first mutation token and incarnation are returned; there is no upsert. A known pre-accept failure preserves state; an uncertain outcome uses readback, not retry. |
| O03 | `UpdateService` | Full replacement including optional F39 VS/real strings and expected VS token; absent is `NotFound`; a stale token is `Aborted` even when equal. After survivor preservation, an equal normalized candidate including all metadata is a no-op; any change follows normal admission/publication and advances the VS token once. Omitted metadata resets to empty; surviving reals take submitted/default metadata while preserving accepted weight, administrative state, incarnation and pins. Session deadlines and accepted capacity are preserved, and the VS network identity is immutable. The F22 boundary requires the complete policy binding on replacement, rejects changed content under the same identity/revision against the current binding or a retained shared revision the candidate would reuse, and treats changed identity or revision as a configuration change. The writable descriptor excludes resize-only and other read-only metadata. |
| O04 | `DeleteService` | The current VS token; absent is `NotFound`; accepted incoming links are `FailedPrecondition` with no cascade. Accepted deletion automatically owns session cleanup; old configurations and readers delay reclamation, and there is no operator state-delete command. An accepted deletion accepts no resurrection of the deleted VS through a delayed create or link, and a VS recreated under the same name receives fresh session state: old affinity is not resurrected by name reuse. |
| O05 | `ListServices` | Bounded authoritative lexical inventory with per-RPC membership coherence under section 6, membership-bound continuation, and `Unavailable` during recovery or bounded coherence failure; no usable partial page on failure and no permit or token issuance side effect. |
| O06 | `CreateModuleConfig` | A distinct create with the complete explicit members or empty binding collection, optional F39 ModuleConfig string and a create token, no upsert; every link is validated by target incarnation and any invalid link rejects the whole operation. No internal common-policy field. |
| O07 | `UpdateModuleConfig` | Full replacement including optional F39 ModuleConfig string with its own expected token; absent is `NotFound`, an equal normalized state including metadata is a no-op, a stale token is `Aborted`. Omitted metadata resets to empty; metadata-only changes take normal preparation/admission/atomic publication and advance its token once. Unlink before VS delete; a delete-first link is `NotFound` or `Aborted` after recreation. Ordered exact bindings replace arbitrary destination rules; no internal common policy. |
| O08 | `ListModuleConfigs` | The same bounded lexical membership inventory and recovery semantics as `ListServices`; it is not a substitute for the full ModuleConfig snapshot. |
| O09 | `UpdateRealServerState` | Atomic nonempty target vector for one VS under one expected token and one publication; each target requires full key, incarnation and explicit enabled bool. All-or-nothing partial administrative mutation, not health control; preserves other fields including source bases and F39. Missing/null/empty vector or duplicate normalized full keys (even different incarnations) is `InvalidArgument` before token validation. After token validation, first target failure in authored order wins; validate final-candidate weights, not sequential intermediate states. All-equal valid vector is a no-op; any change advances the token once. |
| O10 | `UpdateRealServerWeight` | Atomic nonempty target vector for one VS under one expected token and one publication; each target requires full key, incarnation and explicit weight in F05's domain for all three modes. Same structural, target-order, final-candidate and all-or-nothing rules as O09; no clamping, implicit splitting or full replacement. Preserves source bases and F39; surviving-real replacement preserves these weights. All-equal valid vector is a no-op; any change advances the token once. No new target cap; bounded transport/resource framing remains downstream. |
| O11 | `GetService` | A full coherent accepted snapshot including F39 VS/real strings and token using the byte-page contract; a fresh absent object is `NotFound`, a stale continuation is `Aborted`, and recovery is `Unavailable`. No permit allocation and no mixed accepted and runtime snapshot. A returned token may still be stale after the read, so the conditional write remains required. |
| O12 | `ListSessions` | Bounded physical-record inspection with no refresh and no logical-dedup promise, under the selected page, identity, clock and continuation rules. |
| O13 | `GetModuleConfig` | The full accepted typed snapshot including ordered identity-bound links, F39 ModuleConfig string and its own token, under the same versioned byte-page rules as `GetService`. The state must be recoverable rather than reconstructed from an empty local map. |
| O14 | `ResizeServiceSessions` | Name, the existing VS token and a required positive size. After domain, existence and token checks, a raw target below the accepted size is `FailedPrecondition` before rounding, an exactly equal target is a no-op even with a back, growth while a back exists is rejected, and a larger target without a back proceeds to preparation and admission. A stale token is `Aborted` before target checks. Accepted growth advances the VS token; traffic and back retirement do not. All F39 strings are preserved. One recoverable capacity, chain, token and export outcome; no second independent acceptance and no public state token. |
| O15 | `DeleteModuleConfig` | Name and expected token under the existing-object ordering; absent is `NotFound` and any accepted incoming reference, including a detached function, is `FailedPrecondition`. No cascade and an empty success response; accepted deletion removes outgoing VS links while old readers delay physical reclamation. A late cleanup failure does not undo the accepted deletion. |
| O16 | `GetServiceStatus` | Read-only bounded live status, separate from O11's immutable accepted export. Observe the current VS or pending cleanup identified by VS incarnation, with F36 and F38 VS/real groups and section 6 consistency rules; each real group carries full key and incarnation. Runtime changes neither advance the mutation token nor invalidate a traversal bound to unchanged accepted configuration. |
| O17 | `GetModuleConfigStatus` | Read-only bounded live ModuleConfig adoption and F38 module totals/reasons and Echo counter groups under F37 and section 6. O13 remains the accepted export. This adds no counter-specific RPC or synchronous publication barrier. |

Successful O02/O06 creation returns the created object's incarnation and first
mutation token. O03/O07 replacement returns that object's incarnation/token for
the accepted version; O09/O10/O14 returns the owning VS incarnation/token.
A normalized no-op returns the same pair without a new version. O04/O15 deletion
returns empty success, not a new token. The pair identifies this operation's
accepted result, not an arbitrary latest version read while sending the reply.
Another writer may already have made the token stale; conditional mutations still
validate it. Full configuration and actual session capacity use existing readback,
not a mandatory full snapshot in every mutation response.

All existing-object mutations use the complete [section 5 error order](#5-management-surface-permits-and-error-order).
Create, prepare and read continuations retain their separate rules there.

## 4. Cross-Cutting Obligations (C01-C08)

| ID | Obligation |
| --- | --- |
| C01 Presence and parsing | Optional text null or omission maps to absence before the field default; a required null or absent value rejects; a required boolean or weight retains false or 0; collections are explicit; read-only metadata is rejected as a writable field. Writable F39 strings are distinct: omission/null/empty normalize to empty, strict string types and decoded UTF-8 byte limits apply. Binary protobuf uses presence and oneof where required, not textual null. A full replacement omission is not KEEP. Section 16 carries the field-by-field matrix; F36-F38/O16-O17 semantic states and errors are selected in section 6, while exact binary/text schema and cursor codecs remain preimplementation work, not completed format coverage. |
| C02 Identity and equality | Exact names, decoded addresses, independently optional VS protocol and ports, actual session transport, incarnations, authored real order, ModuleConfig binding order, the raw requested power and accepted no-op equality agree across clients and C. F39 strings participate in full normalized equality after survivor preservation; the same strings with changed content and arbitrary earlier/new strings are allowed under current CAS, subject to all other rules. The F22 equality compares source-scoped identity, revision and the native normalized accepted content including order and counter-relevant identity; same identity/revision with changed content rejects against the current binding or a retained shared revision the candidate would reuse, and changed identity or revision changes configuration even for equal behavior. Stale-token precedence survives, and semantic equality is not inferred from serialization bytes. |
| C03 Errors and uncertain outcomes | The complete [section 5 error order](#5-management-surface-permits-and-error-order) keeps error classes distinct and create, prepare and read rules separate. A timeout or lost reply is not non-application; there is no auto-renewal, deduplication or exactly-once promise. |
| C04 Deadline and publication boundary | Admission and preparation are cancellable; the final section is bounded with no mid-commit rollback; success need not await worker adoption or reclamation. |
| C05 Protobuf and C boundary | A separate versioned incompatible service and package keeps the product, module and CLI identity; there is no silent old-server fallback and no old mutation bypass. Removed fields and tags are reserved, the creation-envelope size is not the old VirtualService tag 6, and real and session identities and shared-memory changes need explicit ABI treatment. The common filter types (`Device`, `VlanRange`, `Fragment`) stay imported common definitions, never redeclared as l3b predicates. Exact package and tags, generated types, C widths, layout and attach migration stay downstream; no freeze, implicit legacy compatibility, guessed shared-memory layout, rolling upgrade or transparent downgrade promise. |
| C06 CLI and direct-client parity | Both use the authoritative permits, tokens, names, timeout and power semantics and the centralized defaults; a complete document or an error is returned, never a partial replacement. O09/O10 target vectors keep their complete atomic, authored-order semantics through both: one request per vector, no splitting into sequential single-target calls and no partial success. An external DSCP conversion must not mask invalid input or merge conflicting shared-real policies. |
| C07 Readback and query scope | Accepted, intended or effective, and active observations stay distinct; output and scan work are bounded; record lifetime is preserved. F39 is accepted writable configuration, not worker-adoption proof; its changes invalidate configuration-bound export/status cursors, not name membership or session traversal solely by the label. Administrative readiness, WLC freshness and worker convergence are different. There is no requirement to persist confirmation history or expose every internal version. |
| C08 Acceptance evidence | The service, CLI and conversion boundary cases and the failure, identity and readback examples of section 18 are required obligations, **NOT RUN**; unrelated executions do not verify this contract. Downstream work remains subject to section 19. |

## 5. Management Surface, Permits And Error Order

The VS mutation token covers configuration, manual administrative and manual
weight changes, deletion and accepted capacity growth; ModuleConfig has its own
token. Health, WLC, session traffic and automatic back retirement do not advance
it. A real target is the full F03 key plus a required 1..256-byte incarnation;
missing or malformed shape is `InvalidArgument`. Target resolution under the
order below gives `NotFound` for an absent key and `Aborted` for a mismatched
incarnation. The [atomic vector rules](#atomic-real-vectors-and-operator-metadata)
retain authored-order target failures and all-or-nothing application.

- Creation is prepared, not implied: `PrepareCreate` observes absence under the
  mutation ordering and issues a grant of 300000 ms on authoritative server
  monotonic time, checked again at final creation acceptance, with at most
  16384 retained permit records per actual instance registry. A token is
  opaque, 1..256 bytes, copied without interpretation, and is neither
  authorization, a mutation token, a deduplication key nor a payload binding.
  One accepted creation invalidates every permit from the preceding absence
  interval, and deletion never restores old authorization.
- Each permit is bound to its issuance's exact object kind, name, dataplane
  instance and owner context, including owner epoch. A well-shaped token unknown
  to the authoritative permit registry or bound to another such context is
  `Aborted` when the target is absent; an otherwise
  valid request for an existing target remains `AlreadyExists`. An absent, empty
  or oversized request token field is `InvalidArgument`; missing means the
  request field, not a registry entry. Recovery remains `Unavailable`.
- The 300000 ms lifetime starts at the coordinated issuance decision. Issuance
  computes expiry by checked uint64 addition after widening the duration; an
  unrepresentable expiry returns `Unavailable` without a grant, wrap or saturation.
  This is server-side unavailability, not malformed caller input. The check is
  at issuance after existing request validation and target-absence determination;
  an otherwise valid present target still returns `AlreadyExists` before issuance.
  Issuance time, now and expiry are uint64 milliseconds in the identified
  authoritative server monotonic clock domain. Readback `valid_for_ms` is the
  uint32 lifetime 300000 at issuance, not an absolute time or automatic extension.
  A permit is invalid at `now >= expiry` in that domain. Expiry
  after acceptance does not roll back an accepted create. This is a permit
  lifetime, not a VS lifetime or name/resource reservation.
  The record cap spans both object kinds and all issuers. Admission counts
  pending issuance reservations and expired/invalid records still accessed by
  in-flight work until safe reclamation. Reclaimable obsolete records may be
  removed; active grants must not be silently evicted to admit another requester.
  Exhausted record capacity or established allocation/reservation failure
  returns `ResourceExhausted` from `PrepareCreate`; indeterminate authoritative
  accounting during recovery returns `Unavailable`. Owner turnover invalidates
  old permits but does not free still-accessible storage. The cap does not
  guarantee that every deployment can issue 16384 grants.
- A known rejection before acceptance, including invalid input, resource
  exhaustion, cancellation or a preparation failure, neither consumes nor
  extends the permit. It remains usable only until its original expiry or
  invalidation by another accepted creation or ownership transition. Acceptance
  consumes the grant and invalidates the entire preceding absence interval
  atomically with creation, without a replay window. An uncertain outcome is
  not a known rejection and does not authorize replay. Expiry or invalidation
  during preparation rejects before acceptance. The final coordinated recheck
  uses the same precedence: a present target yields `AlreadyExists`; an absent
  target with an invalid permit yields `Aborted`, with recovery still gated by
  `Unavailable`. Only safely reclaimable
  unpublished resources are released, while in-flight accesses stay protected
  and accounted.
- Mutations of an existing object carry a required expected token of 1..256
  bytes. A stale, unknown or wrong-context token is `Aborted` even when the
  desired state is equal, and a structurally valid request against a genuinely
  absent object is `NotFound` before the token is checked. A normalized
  equality succeeds with the same token and no publication, rebuild or
  deadline rewrite.
- Mutation-owner recovery starts a new management epoch. Every preceding-epoch
  VS or ModuleConfig mutation token becomes stale even if configuration is
  unchanged: after recovery, an otherwise valid request for an existing object
  with that token returns `Aborted` and the client must reread state. Stable VS
  and session identities are not recreated. Restarting only a CLI, operator or
  Gateway does not invalidate tokens while the mutation owner survives without
  losing state. Until authoritative recovery completes, mutations, accepted
  readback and listings remain `Unavailable`, not empty or `NotFound`.
- The error order is fixed: management recovery gate (`Unavailable` rather
  than invented empty state), then request-local structural and domain
  validation, then owning-object lookup, then the expected token, then target
  resolution and state-dependent preconditions, then normalized equality, then
  resource admission and acceptance. A compiler error is not automatically an
  out-of-memory condition. For either create kind, an otherwise valid request
  whose target exists returns `AlreadyExists` before checking whether its
  bounded permit is unknown, expired, consumed or invalidated. When the target is absent,
  such a permit returns `Aborted`. Thus a competing create loses with
  `AlreadyExists` while the winner remains present; deleting the winner does not
  revive the losing permit. Missing or structurally invalid required token fields
  remain `InvalidArgument`, and recovery remains `Unavailable`. This does not
  define precedence for every combination of malformed request fields. A read
  continuation has its own identity rules rather than a mutation-token stage.
  Local text syntax errors can precede a connection and do not prove that the
  server passed recovery. A no-op never waives validation. Identity/revision
  conflict with retained state is a precondition comparison after the expected
  token, not a reason to replace stale-request `Aborted` with `InvalidArgument`.
  Compiler grammar errors, allocation exhaustion and authoritative unavailability
  stay distinct, as do `InvalidArgument`, `NotFound`, `AlreadyExists`, `Aborted`,
  `FailedPrecondition`, `ResourceExhausted` and `Unavailable`.
- Cancellation and a deadline do not prove that a change was not accepted.
  There is no automatic create or update replay with a fresh token after an
  uncertain result, and no automatic permit renewal. An uncertain, cancelled
  or failed outcome never discards shared memory automatically and never
  bypasses a cap.
- An accepted deletion has one irreversible outcome. A lost response or a late
  cleanup failure does not roll it back, and memory of old versions stays
  accounted until it is really released.

### Atomic Real Vectors And Operator Metadata

O09/O10 contain a required nonempty vector for exactly one VS, with one expected
token. Every member requires the full decoded real key, its incarnation and the
operation's value (enabled or weight). Missing, null or empty vectors, null
members, missing fields, out-of-domain values and duplicate normalized full keys
are structural `InvalidArgument` before the expected-token check. Two entries
for the same key with different incarnations are still duplicates. After owning-VS
lookup and token validation, resolve targets in authored order: the first absent
key is `NotFound`, the first incarnation mismatch is `Aborted`. No earlier member
is applied when any target fails. Apply all proposed values to one candidate and
validate its final state, including weight admission; a transient sequential sum
must not reject a valid final candidate. One changed candidate publishes once and
advances the token once; an all-equal valid vector returns the same token. These
partial operations preserve F39 and all unrelated fields. Neither operation splits
implicitly, replaces the full configuration or introduces a target-count cap;
bounded transport/resource framing must be specified downstream.

F39 denotes independent optional `metadata` strings on the VS, each real and
ModuleConfig. Each is 0..256 decoded UTF-8 bytes, valid UTF-8 with no NUL.
Omission, textual null and empty string normalize to empty. Preserve whitespace,
case and Unicode without trimming or normalization. Do not parse numbers, epochs,
hashes or JSON; no uniqueness or parent inheritance is required. The spelling is
semantic, not a final wire name, tag or ABI. F22 policy identity/revision and
read-only systemic metadata remain separate and unchanged.

O02/O03 write VS and real metadata; O06/O07 write ModuleConfig metadata through
full configuration, with no extra setter or RPC. Full replacement omission resets
each string to empty, never KEEP. A surviving real takes submitted/default metadata
while keeping its accepted weight, administrative state, incarnation and pins.
Real metadata belongs to the owning VS CAS; ModuleConfig has its own CAS and does
not write linked VS/real metadata. Preserve survivors before full normalized
equality, which includes every metadata string. Equal candidate/current CAS is a
no-op. A metadata-only change takes normal preparation/admission/atomic publication
and advances the owning CAS once. A stale CAS remains `Aborted` under the existing
error order; metadata never bypasses validation or admission.

The same metadata with changed content, arbitrary earlier/new strings and rollback
under current CAS are allowed subject to all other rules. There is no numeric
ordering, monotonic sequence, overflow or rollback restriction for F39. O09/O10,
resize and runtime preserve all metadata and retain their existing token rules.
Recovery preserves accepted strings while old CAS tokens still become stale.
Recreated objects and readded reals have new incarnations and only newly
supplied/default strings; equal text revives no old identity, state or pin. Metadata
is neither a fingerprint of current manual overrides nor proof of worker adoption;
the existing rules for systemic owner epochs, tokens, incarnations and internal
publication versions stay unchanged. No version ledger, token replacement or automatic retry is added.

### Complete Identity Non-Reuse

The identified context describes what an identity denotes, not a boundary after
which its opaque value may be recycled. Complete identities must remain
non-aliasing to their validators or consumers; components remain opaque:

| Identity | Identified context | Non-reuse constraint |
| --- | --- | --- |
| Mutation token | Instance, object kind, name, incarnation, owner epoch, accepted version | Historical and current grants must be distinguishable from the complete presented token plus actually supplied and validated request context, across versions, takeover and same-name recreation. |
| Create permit | Instance, owner epoch, kind, name, absence interval, individual grant | A later grant or absence interval must not revive an expired, consumed or invalidated permit in the presented/validated context. |
| Object incarnation | Instance, object kind, creation lifetime | Distinct creation lifetimes must not alias through the complete identity and its validated context; name reuse does not revive an old incarnation. |
| Real incarnation | Instance, VS incarnation, full real key, membership lifetime | Removal and readdition must not revive the old membership identity in the complete validated context. |
| Membership revision | Instance, owner epoch, list kind | A changed membership or epoch must not make an invalidated continuation current again; separate list-kind scopes remain independent. |
| Session-chain generation | Instance, VS incarnation, topology lifetime | A later topology must not revive an invalidated chain identity or continuation in the complete validated context. |
| Allocation identity | Existing instance/allocation-serial reservation domain | Preserve non-reuse of the complete allocation identity in that domain; storage-location reuse is not identity reuse. |

For mutation requests carrying a name and expected token, the same complete token
bytes MUST NOT become valid again across accepted versions, owner takeover,
same-name delete/recreate or endpoint reuse when the actually supplied and
validated request context cannot distinguish the historical grant from the current
one. Version, epoch and incarnation are not reset boundaries. Changing only
server-hidden version, incarnation, instance or epoch does not distinguish the
input; returning a read-only incarnation beside the token does not supply that
incarnation on a later mutation. This protection survives full reclamation of the
old object's state, not only the lifetime of its retained storage.

Wrong-context tokens still reject with `Aborted` under the existing error order.
A different RPC kind or instance label alone does not prove that validation can
reject a token from the other context: the actual presented value/context must
distinguish the grants. Local components may repeat only when the complete
presented and validated context remains distinguishable without weakening these
checks. This does not require global byte uniqueness across all names or systems,
choose a codec or new RPC field, or promise cryptographic unforgeability.

Issuance metadata survives takeover. Parent domains must distinguish old requests
even after endpoint reuse and fence old validators/participants before a new domain
can issue. Lost or uncertain authoritative metadata forbids issuance: recovery
returns `Unavailable`, never resets a sequence because a registry is empty.
This promises neither infinite issuance, automatic epoch rollover, UUID collision
immunity, a permanent per-value ledger nor a history TTL. ABA names/content do not
restore old identities.

Fencing is required but does not replace non-aliasing: a delayed old client request
must not become valid at the new owner. Permit TTL expiry, consumption and record
reclamation likewise do not authorize reuse that revives an old grant. The existing
`AlreadyExists` precedence for a present create target and `Aborted` for an absent
target with an old permit remain unchanged. Counter accumulation is a separate
consumer-visible identity under section 6, with its own group-only unavailability.

Known exhaustion of a required new identity returns `ResourceExhausted` at resource
admission, after normal validation, token checks, preconditions and no-op equality.
Stale requests retain `Aborted`; validated no-ops require no new semantic identity.
Create/prepare keep their existing precedence. Automatic back detachment that
cannot issue a fresh chain generation stays pending and physically accounted;
it neither recycles a generation nor admits a third table. Service and ModuleConfig
list revisions are independent: changing one membership does not invalidate the
other list. Chain topology changes invalidate session cursors; traffic and
metadata-only changes do not.

## 6. Accepted Readback And Pagination

- The accepted configuration readback is a typed snapshot delivered in stable
  encoded byte pages. The envelope carries the object incarnation, the
  accepted token, the encoding version, the total byte count, the offset, the
  chunk, the completion flag and the next cursor. One token and encoding
  version identify one byte sequence, so map-like field order is fixed by the
  encoding rules.
- The snapshot separates the writable replacement descriptor from read-only
  metadata such as the actual accepted capacity, the administrative state and
  the real incarnations. Supported input adapters reject read-only metadata
  supplied as a writable field. WLC result and provenance, health and drain
  progress are labelled observations and never change the token-stable bytes.
- Writable F39 is part of that replacement descriptor, not read-only systemic
  metadata. O11 coherently exports VS and every real's strings with accepted CAS;
  O13 exports the ModuleConfig string with its own accepted CAS. Recovery preserves
  these strings. They do not prove worker adoption. Charge their encoded bytes to
  existing request/export/resource budgets; no new configurable metadata quota is
  added, and the 256-byte domain cannot be raised.
- A configuration, manual or capacity change aborts the continuation with
  `Aborted`; a no-op, a WLC publication or a health publication does not.
  After an owner restart the old cursor is invalid.
- Metadata-only changes invalidate the owning configuration-bound export/status
  cursors under the same rules. They do not change name membership or invalidate
  session traversal solely by the label.
- A client accepts only a contiguous sequence of one identity, token, encoding
  and total length, and the CLI returns the complete document or an error. A
  client may impose a stricter local memory limit, but it reports a local
  failure rather than a truncated configuration.
- Before accepting any state-changing operation, prepare and account the exact
  encoded snapshot, including all accepted fields. If its total
  exceeds `config_max_export_bytes`, reject with `ResourceExhausted`, preserving
  accepted state and CAS; never truncate. Exactly the cap passes only this bound,
  not other admission checks. Passing `module_max_bindings` alone guarantees
  neither encoded-export fit nor physical fit. Preserve section 5's validation,
  token and no-op precedence. The export is not a second authoritative copy.
  Each page RPC protects its memory until the
  copy completes, and an obsolete export is freed only after its protected
  readers and outstanding C accesses finish.
- Name lists are ASCII-lexicographic with a membership-revision, instance,
  epoch and position cursor. Create and delete invalidate subsequent continuation requests;
  content changes alone do not. Bounded record, response and work limits
  apply, and sorting the whole inventory on every page RPC is not allowed.
- For `ListServices` and `ListModuleConfigs`, names, membership revision,
  `complete` and the next cursor come from one coherent protected membership
  state per RPC. Revision stays inside the existing opaque cursor; no new
  public revision field is required. If matching state was protected/validated
  before a concurrent membership change, the RPC may finish an old coherent
  page with its old-revision cursor. It must not relabel old names or position
  with the new revision. There is no newest-at-reply guarantee; a following
  request with that cursor after the change is `Aborted`.
- A fresh empty-cursor request may restart against a new membership state within
  bounded work. A continuation never silently transfers its old position to a
  new revision: a valid stale incoming cursor is `Aborted`, while a page whose
  matching state was protected/validated before the change may complete as above.
  If a coherent page cannot be completed within bounded work, return `Unavailable`
  with no usable partial page or cursor. A concurrent mutation alone is not
  failure. Protection, seek, scan, copy, encoding and recheck all consume bounded
  work; unbounded retries and progress under continuous churn are not promised.
  This adds no between-RPC snapshot, pin, history registry or TTL, chooses no
  concurrency algorithm or numeric budget, and covers names only, not a snapshot
  of all runtime fields.
- The readback is coordinated with the final acceptance section and with the
  owner epoch, so it never exposes a new configuration paired with an old
  token. The returned configuration and its token come from one coherent
  protected state, and a read that cannot establish that pairing is not
  answered with a mixed snapshot.

### Live Service Status

`GetServiceStatus` is the runtime observation surface; `GetService` remains the
immutable accepted-configuration export. Status is read-only and carries VS
incarnation and accepted configuration-token context, with separately identified
observation provenance rather than pretending those classes were read atomically.
It includes:

- Per-real full identity/incarnation, health observations, published effective
  enablement and selector weight; these are not independent configuration setters.
- WLC mode, freshness, applicability and result provenance, distinguishing a
  published result from a later confirmation and from worker adoption.
- Separate readiness scopes and their observations, not one unqualified ready bit.
- Drain/cleanup phases distinguishing expiry, reader-safety and reclamation
  waits, without a completion percentage or ETA guarantee.

Large results use bounded live pages under the centralized read/work limits.
Each page labels its configuration and observation context; health/WLC can change
between pages, and clients must not assemble a fictitious simultaneous snapshot.
Continuation is bound to the VS incarnation and accepted configuration, not to
each health/WLC update. Accepted-configuration changes abort continuation;
runtime-only changes do not. Observation identities do not grant mutation rights.
Unavailable observations must not be fabricated as zero, healthy or ready.

Pending cleanup after deletion is addressed by the old VS incarnation; a new VS
with the same name never supplies the old object's status. The surface exposes
retained pending work, not an infinite ledger of completed deletions.

Each observation group carries `AVAILABLE` with its typed value and provenance,
`UNAVAILABLE` when that source cannot be observed, or `NOT_APPLICABLE` when the
group does not apply. There are no fabricated zero/healthy defaults. Provenance
identifies the observed source/generation separately from accepted configuration;
availability does not assert a simultaneous cross-group snapshot. Missing whole
authority or protected page context returns RPC `Unavailable`, with no usable
partial page; one unavailable observation source marks only that group unavailable.

O16 targets either the current name or a deleted incarnation's retained cleanup.
A fresh absent lookup is `NotFound`; a valid stale continuation is `Aborted`.
If its cleanup anchor disappears between pages, continuation is `Aborted` and a
fresh lookup is `NotFound`, not evidence that cleanup completed. An already
protected in-flight page may finish. `complete` means traversal ended, not cleanup
completion. Malformed/context cursors retain the existing cursor error taxonomy.
No between-RPC pin, retention TTL or completed-history guarantee is added.

### ModuleConfig Adoption And Counter Observations

O17 returns the accepted ModuleConfig incarnation/token, target publication and
authoritative relevant worker cohort. Adoption is `ALL_ADOPTED` only when that
cohort has adopted the target, `PENDING` when adoption remains, `UNAVAILABLE` when
it cannot be established, and `NOT_APPLICABLE` when adoption does not apply.
Derive the cohort from the current authoritative published execution topology for
this ModuleConfig, not recent traffic. Include idle participants. No active execution
participants, including detached-only references, means `NOT_APPLICABLE`, never
vacuous `ALL_ADOPTED`. Joining participants remain `PENDING` until acknowledgement.
Exclude a departing participant only after authoritative withdrawal and fencing
against future acquisition through that participation. Observe cohort identity and
adoption acknowledgement evidence coherently; unknown topology or fencing means
`UNAVAILABLE`. Cohort changes are runtime provenance: they neither mutate the
config token nor invalidate counter pages. `ALL_ADOPTED` applies only to the
observed cohort/context, not permanent readiness. Publication success still precedes adoption;
this neither forces newest-version acquisition nor equates adoption with WLC
readiness, reader drainage or acquisition of the latest linked VS descriptor.
Use bounded live observation groups with provenance and the same authority/error
separation as O16; accepted-context changes invalidate continuation, runtime-only
changes do not. A fresh absent ModuleConfig is `NotFound`, stale context `Aborted`.

F38 adds VS and real counter groups to O16, module totals/reasons and Echo groups
to O17, not more counter RPCs. Real groups carry full key and incarnation.
Project the current binding only. Semantic identity remains VS-incarnation/tag
for policy counters and ModuleConfig/family/VIP for Echo; do not arbitrarily
attribute a shared-VIP reply to one VS. Shared contributing cells are read once,
and independent old-version cells are never added to the current series. Values
need not belong exclusively to traffic under the current configuration.

Every available series includes an accumulation identity. Retain it iff the full
contributing cell set and its accumulated state survive without reset. New cells,
any reset, changed contribution membership or unprovable continuity require a
fresh identity; until safe issuance/binding is possible the observation is
unavailable. A configuration-only change referencing exactly the same continuing
cells preserves it. This supplies no permanent history or counter continuity
guarantee. Exact wire/text schemas, cursor codecs and protected-lifetime proof
remain downstream; the semantic states and outcomes above are selected.

The full counter-series identity is `(instance, parent-object incarnation,
semantic key, accumulation identity)`. The semantic key distinguishes module
totals/reasons, VS totals/reasons, full-real-key/incarnation totals/reasons, native
policy tags and `(family, VIP)` for Echo within a ModuleConfig incarnation,
never merely a name.
An accumulation identity MUST NOT repeat in that domain throughout the entire
parent incarnation, including after a binding disappears and reappears. Issuance
metadata must survive takeover; a config-token change, empty registry or lost
metadata never authorizes reset. A local value may repeat only in a provably
distinct parent domain visible to the consumer. No automatic object-incarnation
rollover, permanent per-value ledger or infinite issuance guarantee is required.

Accumulation-identity exhaustion or uncertainty makes ONLY the corresponding
observation group `UNAVAILABLE`; it MUST NOT block forwarding or ordinary
configuration operations and is not mutation `ResourceExhausted`. Missing whole
authority still returns RPC `Unavailable` under the existing rule. Restored
identical cells and accumulated state with proven issuance metadata may retain
the prior identity under the iff rule above; otherwise require a fresh safely
issued identity before reporting that group as available.

Counter numeric domains and observable arithmetic at overflow remain a separate
preimplementation obligation; no physical width, wrap or saturation behavior is
selected here. Accumulation identity alone does not settle that obligation;
[#3121](https://github.com/yanet-platform/yanet2/issues/3121) owns that deferred work.

### Semantic Counter Catalogue

The [packet drop precedence](l3b-packet-contract.md#packet-drop-precedence) is normative for each
packet-owner invocation: the first terminal fault in that sequence supplies its
single primary drop reason. Do not evaluate later dependent checks or increment
their drop reasons. This is attribution among existing outcomes, not additional
rejection or rollback of earlier native COUNT, consumed WRR positions, packet
preparation or committed session effects. Existing no-partial-session rules
remain. Funded peer attempts are separate units and continue independently.
Stage labels below are documentation references, not counter tags or wire fields.
Shared failure reasons have branch-scoped rows; repeating a reason in S4/S5/S6
does not create a new reason or permit ordinary-only checks on another branch.

This is the complete target semantic catalogue, including paths not implemented
today, not an inventory of currently available counters. Each owner invocation
records input packets/bytes, successful output packets/bytes, and terminal dropped
packets/bytes with exactly one primary terminal reason. Input and drop bytes use
the actual packet-buffer length snapshotted at that owner's entry before mutation;
output bytes use the prepared packet at successful handoff after any required
session commit. Count present L2/VLAN bytes, not absent FCS or preamble. Successful
handoff is not NIC receipt; later transmission loss does not undo it. Module, VS
and real views overlap and MUST NOT be summed as independent traffic. Attribute
only identities established safely in the protected processing context: pre-VS
drops have module attribution only, and no unknown VS or real is invented. If even
the parent module is untrusted/unavailable, use existing infrastructure diagnostics
rather than fabricate a module series. Acquisition-failure storage remains reachable
without a descriptor as required by section 9.

The following reason families are semantically distinguishable; final tag spelling,
layout and numeric widths are not selected. A row listing alternatives requires
distinct reasons for those alternatives, not one catch-all counter. Relevant VS
and real views apply only after their identity is safely established.

| Counted outcome/reason | Stage | Owner and observation attribution |
| --- | --- | --- |
| Malformed/truncated packet or invalid length at base-IP validation | S1 | Detecting owner; module only in l3b, external envelope parser stays external. |
| Malformed/truncated ICMP header or invalid ICMP message length | S3 | Same structural-failure family, at the detecting ICMP owner before dependent checksum/type access; no invented child. |
| Missing required TCP/UDP transport fields or invalid transport bounds | S5 | Same structural-failure family, before dependent lookup; module only in l3b. |
| Real IPv4/IPv6 fragment | S2 | Detecting owner before transport/VS lookup; module only in l3b. |
| Unsupported upper protocol, live AH, ESP or No Next Header | S2 | Detecting owner before dispatch; distinct unsupported reasons, no fabricated VS. |
| IPv4 LSRR/SSRR option with a remaining route; malformed IPv4 source-route option length/pointer | S2 | Detecting l3b invocation before dispatch; module only, two distinct reasons; quoted options stay structural quote reasons. |
| Extension bound/length failure, ninth extension, misplaced/repeated HBH, malformed TLV, rejecting unknown-option action, nonzero live Routing Segments Left | S2 | Detecting parser owner; distinguish each admission failure; quoted structural failures remain quote reasons. |
| Bad enclosing IPv4 checksum on ICMP, bad ICMPv4 message checksum, bad ICMPv6 message/pseudo-header checksum | S3 | Detecting ICMP owner before reply/query/fanout; distinguish checks. |
| Unsupported ICMP type; malformed/incomplete quote, quoted noninitial fragment or missing required quote fields | S4 | Detecting ICMP/parser owner; no query/session effect; external parser counters stay external. |
| No matching VS for ordinary traffic, including empty ModuleConfig | S5 | l3b module only; never attribute to an invented VS. |
| Echo-inactive (every linked VS at the family/VIP has an empty real set; dedicated reason, not no matching VS), forbidden Echo VIP source, forbidden Echo client source/destination role | S4 | Module totals/reasons; no reply increment or arbitrary VS/real attribution. |
| Echo eligibility acquisition failure; unclaimed Echo destination; Echo reply construction/buffer failure | S4 | Existing acquisition, no-matching-VS and preparation-failure reasons on the Echo branch, respectively; module only, no reply or invented VS/real. |
| Descriptor acquisition exhaustion | S5 | Pre-provisioned l3b acquisition-failure accounting, module and only safely known child context; never fabricated session miss. |
| Service-policy denial/no match/empty or COUNT-only terminal actions; runtime policy unavailable | S5 | l3b module and known VS; denial and unavailable distinct; native COUNT remains separate. |
| Session lookup/read failure; selector/coherent selection failure; no eligible real/empty selector; invalid live pin not authorized to reschedule | S5 | l3b module and VS, plus real only when safely resolved; distinct causes, not ordinary miss. |
| Deadline arithmetic failure; required insertion, refresh, copy-forward or rebind failure; suppression-resource admission failure; bounded authority revalidation/repreparation failure | S5 | l3b module/VS and safely established real, distinguish each required operation/cause; no partial session/suppression changes. |
| Malformed eligible MSS option/tail; required MSS packet-buffer space failure | S5 | l3b module/VS and selected real; valid logical option-space exhaustion is unchanged forwarding, not a drop. |
| Encapsulation representable-length failure; headroom/buffer failure | S5 | l3b module/VS and selected real; no mode fallback or successful output increment. |
| Reverse-delivery acquisition/read failure; required deadline, refresh, copy-forward, suppression, revalidation/repreparation or encapsulation length/buffer failure | S6 | Existing shared failure reasons on the delivery branch, distinct per cause; l3b module and safely established VS/real, no partial session/suppression effect or successful output. |
| Missing/expired session at reverse delivery after owned query; invalid reverse-delivery pin | S6 | l3b delivery owner with known VS, real only when established; no requery/reselect/refanout. |
| Absent, multiple, stale or unprovable ingress/service association | S6 | External distributor diagnostics, distinct association reasons; no fabricated l3b child attribution. |
| Indeterminate ownership; fanout not-ready (including unknown peers, missing family source or peers exceeding burst); insufficient budget | S6 | External distributor, distinct failures before clone reservation/output as applicable. |
| Funded clone allocation/complete-copy failure; funded clone encapsulation length/headroom failure | S7 | External distributor per failed attempt, distinct clone failures; remaining funded peers continue, with no refund. |

External original terminal-drop accounting and funded clone-attempt failures are
different units: one original has at most one primary terminal-drop reason, while
several funded peers may each fail. Successful fanout or a valid zero-peer set does
not count original consumption as a drop. External input/drop byte snapshots are
likewise per-owner entry; clone output uses each prepared clone at handoff. External
fanout totals/reasons remain in external-owner readback, not O16/O17.

Native policy COUNT packets/bytes retain native action semantics and are separate
from forwarding input/output/drop totals. Repeated COUNT before a terminal action
counts repeatedly; no deduplication or additive reconciliation with drop totals.
Echo reply packets/bytes belong to ModuleConfig/family/VIP and module output, not
an arbitrary VS. Ordinary and reverse-delivery successes have the safely resolved
VS/real context; an unknown child never gets a synthetic zero-key series. O16/O17
availability, provenance and accumulation rules above apply to the entire catalogue.

## 7. Session Ownership, Capacity And Inspection

F33 exposes each record's full network key as defined in section 10, not a
storage-layout abbreviation: semantic family, transport, client/VIP and ports
according to VS mode. Mandatory page VS-incarnation context disambiguates scope;
it need not be duplicated per row. Decode from record plus page without
`GetService` or current VS state. The stored real key/incarnation is separate
from the network key and is not reconstructed by current real lookup.

- For a permitted stateful insert or refresh, TCP timeout classification uses
  the following precedence. It does not admit a packet or authorize
  rescheduling: in particular SYN+RST does not reschedule a live invalid pin.

  | TCP flags | Timeout class |
  | --- | --- |
  | SYN and ACK, regardless of FIN/RST | `tcp_syn_ack` |
  | SYN without ACK, regardless of FIN/RST | `tcp_syn` |
  | No SYN, FIN present, regardless of ACK/RST | `tcp_fin` |
  | No SYN and no FIN, including RST alone | `tcp` |

  UDP uses `udp`; a permitted ICMP-error refresh uses `other`. Durations are
  uint32 milliseconds, widened to uint64 before checked deadline addition.
  Session now, t, deadlines, expires_at, page_now and retire_not_before are uint64
  milliseconds in the same identified dataplane monotonic clock domain. If
  `now > UINT64_MAX - ttl`, a required insert or refresh leaves the prior
  record unchanged and counted-drops the packet, with no wrap, saturation or
  infinite lifetime. OPS and read-only queries do not refresh deadlines or
  require the corresponding deadline-addition check; lookup still treats
  `deadline <= now` as expired.
  This is an affinity/expiry classifier, not a model of endpoint TCP state.
  A permitted RST without SYN/FIN therefore uses ordinary `tcp`, without an
  immediate-delete rule. Its logically live authoritative pin remains in WLC
  accounting until expiry, subject to the existing eligibility/authority rules.
  IPVS uses different TCP-state transitions; it does not universally delete
  immediately on RST.
- Session state belongs to one VS incarnation. It is created with the service,
  preserved across its replacement, and automatically reclaimed after an
  accepted deletion once old reader rights end; both the front and the
  draining back are covered. The cleanup duty survives a control-plane
  restart, delayed cleanup stays observable, and its retained memory stays in
  physical accounting. There are no independent public session-state-object
  creation, listing or deletion operations, and no operator state-delete command.
  Read-only inspection of a VS's session records remains public through
  `ListSessions` (O12).
- A positive-capacity state has a front and at most one draining back.
  Lookup checks the front before the back; inserts go to the front and an
  unsuperseded live back hit is copied forward. Required insertion, refresh or copy-forward
  failure counted-drops without stateless fallback or a partial record update.
  Lookup, insert, refresh, copy-forward and expiry continue during control-plane
  absence while dataplane and shared memory survive; no survival across a
  dataplane, shared-memory or host restart is promised. On demotion, freeze
  the monotonic maximum absolute-deadline watermark as `retire_not_before`.
  `now >= retire_not_before` makes the back eligible to begin retirement;
  `now < retire_not_before` does not. A safely frozen/demoted table proved never
  to have accepted a committed record/deadline is immediately eligible to begin
  retirement, without waiting for a nonexistent deadline. The proof includes
  operations crossing demotion; an earlier empty observation is insufficient.
  A previously populated but now logically empty table retains its monotonic
  historical watermark without reset. No numeric empty-watermark sentinel,
  demotion timestamp or clock-wrap policy is selected. Eligibility is not immediate detachment,
  free or growth: detach first, wait for reader observation safety (an RCU grace
  period), and only then free. Retained storage stays physically accounted until release,
  and growth remains rejected while a back physically exists. Fenced takeover may
  finish reclamation; its absence may delay reclamation, not invalidate
  sessions or expose a third table. Terminal VS deletion still waits for old
  reader rights but does not wait for session expiry as its cleanup criterion.
- Admission never evicts unrelated live sessions to make room for a new key,
  including in a full bucket. Safe expired-storage reclamation and authorized
  same-key replacement are distinct from such eviction. A silent eviction reported
  as insertion success cannot bypass the required-write counted-drop rule.
- Copy-forward first validates that the back pin is live and unsuperseded.
  For an admitted stateful packet, copying its real identity and applying the
  permitted refresh are one front commit: checked `now + timeout` for the
  packet's class, or `now + other` for permitted ICMP-error delivery. UDP OPS
  copies the stored absolute deadline without refresh. Ownership queries and
  inspection neither copy forward nor refresh. The back record and its frozen
  retirement watermark are never extended. Arithmetic, resource-admission or
  required-write failure preserves prior records and suppression and
  counted-drops; there is no partially published front or false supersession.
  An expired back cannot be revived. No `max(old_deadline, new_deadline)` is
  applied: a shorter timeout may expire the front before the retained back,
  and zero makes it immediately expired. A successful front commit still
  suppresses the old back; later packets follow ordinary miss/admission rules,
  never fall back to that displaced pin.
- For the exact decoded logical session key within one VS incarnation, a
  successfully committed newer front record supersedes the older back record.
  Front expiry, reclamation or slot reuse never restores that back record's
  authority, even while its physical deadline remains live. This also applies
  when an admitted reschedule changes the selected real. Suppression is scoped
  to the affected back-table generation, never just a hash or slot, and never
  crosses VS recreation or unrelated table generations.
  Lookup, copy-forward, ownership query, reverse-session delivery and WLC all
  honor this authority: a suppressed back record is not a logically live pin
  and cannot prevent a genuine miss or supply ownership or a delivery target.
  Physical inspection may still report the retained record.
  Required suppression resources are bounded, physically accounted and admitted
  before the newer front commits. New authority and suppression become effective
  atomically; failed admission or a failed front write preserves prior authority
  and suppression, subject to normal expiry, and required write failure
  counted-drops. No expiry/reuse window may expose the displaced back pin.
  Suppression survives front-slot reuse until observation-safe retirement of
  the affected back. Its metadata is part of physical session/resize admission,
  not free capacity hidden in the slot count; existing capacity numbers do not
  qualify its cost. The representation and concurrency proof remain downstream,
  with no prescribed tombstone layout or persistence across dataplane restart.
- The creation size resolves once: an omitted or zero initial size selects the
  current `l3b.policy.session_default_slots` (initially 65536), rounded under
  F26, and the resolved value becomes a concrete accepted
  size returned by readback. An explicit positive size in 1..1073741824 rounds
  upward to a power of two with a minimum actual size of 16. The size is
  shared across the workers of one VS in one instance and is not a guarantee
  of concurrent flows. A later default change never resizes a live VS, and
  recreation resolves anew.
- The capacity change is a separate operation under the shared VS token.
  After domain, existence and token checks, a raw target below the accepted
  front size is `FailedPrecondition` before rounding, an exactly equal target
  is a no-op even while a back drains, and a larger target is rejected while a
  back exists. 1024 to 1000 rejects, 1024 to 1024 is a no-op and 1024 to 1025
  requests an actual 2048 front. The asymmetry with creation is deliberate and
  visible in the service and CLI documentation.
- Accepted growth advances the VS token; traffic and automatic back retirement
  do not. It yields one recoverable capacity, chain, token and export outcome
  rather than a second independent acceptance after the chain push. A
  configuration replacement preserves the current capacity and session state,
  and its writable descriptor must not silently accept and ignore a capacity
  request.
- Inspection is a bounded read-only physical view. Records carry the flow key,
  the stored real key and incarnation (never a real resolved from the current
  configuration), the expiry and the layer identity and role. Front and back
  copies can appear on different pages, so the output is not a unique flow
  count and not a forwarding-pin decision. The view is live: concurrent
  changes can cause omissions and repeated flow identities, and finite initial
  watermarks stop inserts from extending the traversal indefinitely. The page
  uses one uint64 millisecond clock reading in the session deadline domain for both the expiry
  predicate and the response, and inspection never refreshes a session and
  never becomes a WLC sample.
  Omit front and back records with `expires_at <= page_now`; scanning them
  still consumes scan budget and advances traversal progress. Physical means
  non-deduplicated, not inclusive of expired records.
- The continuation binds purpose, instance, VS incarnation, owner epoch,
  observed chain generation, finite initial layer watermarks and position. It
  is validated before its positions or watermarks are trusted, and it has no
  TTL, no registry and no between-RPC pin. A resize or back detachment
  invalidates it through the chain generation; a metadata-only change does
  not. A change of the record limit shapes the next page, not the identity of
  the query. A fresh request for an absent VS is `NotFound`, a continuation
  for a deleted or recreated incarnation is `Aborted`, and recovery is
  `Unavailable`.
- The record-count, scan-step, response-byte and cursor bounds come from the
  centralized policy and are provisional. A page stops at the first applicable
  bound; a successful incomplete page may return zero rows and still advance;
  `complete` is true only at the captured traversal end, and the next cursor
  is empty exactly when complete. A record that does not fit an otherwise
  empty page is `ResourceExhausted` rather than an endless empty continuation.
  A record that cannot be read safely within budget is `Unavailable` with no
  usable partial page, and the client does not advance.

### Concurrent Session Authority

For ordinary admitted TCP/UDP forwarding, reclassify against current session
authority within bounded work rather than blindly overwriting a competing commit.
Keep the applicable protected VS descriptor; this does not require the newest
published configuration. The following table does not apply ordinary miss or
reschedule permissions to reverse ICMP delivery, which still cannot reselect,
rebind or create a session; ownership queries remain read-only.

Explanatory view for one exact logical key (not a storage layout or concurrency
algorithm; the normative rules above and below govern):

```text
lookup -> front authority, if live
       -> back, only if live and not superseded

successful newer front commit -- atomically suppresses --> older back authority
front expires / is reclaimed / slot reused -------------> suppression remains
physical inspection may still show that back; forwarding/query/WLC cannot use it
```

| Initial operation | Authority found on revalidation | Required outcome |
| --- | --- | --- |
| Insert-on-miss | Another worker committed a live eligible pin | Use it as a hit; do not overwrite it with the earlier selection. |
| Refresh-on-hit | The record changed | Re-read current authority and classify it; do not reinstate the old record or real. A deadline-only change is not a rebind. |
| Copy-forward-on-back-hit | Front authority appeared | Use current front authority, never republish the suppressed back. |
| Reschedule | Another operation rebound the pin | Reclassify the current pin: use an eligible hit; an invalid live pin permits rebind only under the existing UDP or TCP SYN-without-RST gate, otherwise counted-drop. |
| Any of these | The pin expired before commit and a genuine miss is established | Select a fresh eligible real; ordinary stateful forwarding requires new insertion. OPS selects statelessly without creating a logical session. |

An existing eligible pin is preserved; only a currently authorized reschedule can
rebind. Final prepared packet contents, including encapsulation, must match the
validated full real key AND real incarnation. If either differs from the prepared
target, reprepare and revalidate before handoff and any required commit. A changed
deadline alone does not require a different target, but refresh must be a valid
serially ordered write using checked `now + timeout` for the permitted class (or
`other` for permitted ICMP delivery), never a stale record restore or
`max(old_deadline, new_deadline)`. Shorter and zero deadlines remain allowed. OPS
keeps the stored absolute deadline: front live eligible hits and misses need no
session write, while back migration and invalid-pin rebind require their writes.

Revalidation, any repreparation and required commit must fit bounded work. If
coherent state cannot be established or a required write cannot be performed,
counted-drop without partial session or suppression changes by this operation;
normal expiry and independent concurrent operations remain possible. Front
authority and back suppression commit atomically, with no resurrection. Failed
preparation causes no session mutation by this operation; postcommit packet loss
does not roll back. This chooses no numeric retry budget, counter tags, lock/CAS
algorithm or ABA layout, changes no hash or WRR cursor rule, and promises no
progress under continuous churn.

Finish fallible packet preparation first, then sample the session clock once
as t inside each final bounded authority-validation/commit attempt. Use that
same t for liveness/expiry and any required checked
t+duration. Reclassification requiring repreparation abandons that attempt;
the next attempt samples a fresh t. Preserve serial ordering, shorter/zero
deadlines, OPS stored-deadline behavior and reverse-ICMP no-create/no-reselect.

## 8. WLC Contract

WLC names the legacy adaptive weighted hash scheduler: weights adapted from
sampled session load feed a hash selector. It is not the IPVS per-connection
least-connection scheduler. Configured weight 0 is quiescent: the real receives
no new selection while valid pins remain.

### WLC Inputs And Observations

Inputs are the original positive weights of administratively enabled,
health-eligible reals and one complete all-worker sample. The requested sampling,
computation, retry and reconciliation cadence is 20 ms. Targets are a 10–20 ms
start interval and full-recalculation duration <=20 ms, aiming toward 10 ms,
not a sample-validity deadline or adoption SLO. Within one input generation,
the first two consecutive invalid attempts retain the last applicable successfully
published result with degraded freshness; the third and subsequent invalid
attempts trigger capped-base fallback publication attempts and WLC not-ready.
Without an applicable result, fallback applies immediately; a valid sample resets
the streak once. A change of participating real identity, configured weight or
effective power starts a new input generation with streak 0. A complete current
valid sample is valid even when its source values are unchanged or late; an equal
active output uses guarded confirmation below. Requested WLC power is writable
configuration under F09; derived effective power, WLC results, active-state and
freshness observations are read-only, never independent caller setters.

- Go computes WLC on a requested 20 ms cadence from one complete all-worker active-session
  sample. For each eligible real i, let `w_i` be its original positive
  configured weight, `c_i` its sampled active-session count, `W = sum(w_i)`,
  `C = sum(c_i)` and `p` the effective power from F09. Compute:

  ```text
  if C < W: q_i = w_i
  otherwise: q_i = round(w_i * max(1, p * (1 - (c_i * W) / (C * w_i))))
  selector_weight_i = min(q_i, 100)
  ```

  The ratio uses real division, not integer truncation. Round to the nearest
  integer, with exact halves rounded upward (all values are nonnegative).
  If C is zero and the eligible set is nonempty, C < W applies; an empty
  eligible set yields an empty selector without evaluating the formula.
  For example, `p=10`, weights `[10,10]` and counts `[0,30]` give `W=20`,
  `C=30` and selector weights `[100,10]`. Apply the 100 cap before any
  narrowing that could overflow; never wrap or perform an out-of-range
  float-to-integer conversion. The dataplane consumes effective weights and
  contains no WLC formula. A missing required real, unavailable observation,
  source-count failure or malformed/out-of-domain input invalidates the whole
  sample; no partial calculation is published. Every valid complete sample must
  produce the exact mathematical result independent of machine intermediate
  width. Insufficient intermediate width is not an invalid-sample condition.
  For C>=W, an exact form is A=max(C*w_i,p*(C*w_i-c_i*W)): return 100 when
  2*A>=199*C, otherwise divide A by C with half-up remainder rounding. This is
  mathematical notation, not unchecked machine arithmetic; it introduces no
  numeric count width or count-domain limit.
- The requested cadence remains 20 ms. The start-to-start interval target is
  10–20 ms; independently, full-recalculation duration targets <=20 ms, aiming
  toward 10 ms. Measure the full duration from the start of sample acquisition
  through the complete all-worker sample, weight calculation and selector-result
  preparation, not just the formula. Worker adoption is separate. Record missed
  interval/duration targets; neither target is a sample-validity deadline or an
  adoption SLO. A delayed complete current valid sample stays valid, including
  after 20 ms. An incomplete, missing or failed attempt follows the existing
  three-attempt invalid-sample policy, and sustained failure may lead to
  fallback or not-ready. Feasibility at any scale is unqualified; missing a
  timing target creates no instant fallback. No measured performance is claimed.
- The eligible set is the reals that are administratively enabled,
  health-eligible and configured with a positive weight, each contributing its
  original configured weight rather than a pre-clamped value. An empty
  eligible set yields an empty selector; it does not divide by zero and does
  not independently clear sessions.
- Each `c_i` counts logically live authoritative sessions, not physical records.
  Count a logical key once across front/back copies and worker observations,
  within its VS incarnation, preserving address family, actual TCP/UDP transport
  even in a mixed VS, and the portless rule
  that excludes both transport ports. Attribute it only to its stored full real
  key and incarnation in the eligible WLC input set, while its authoritative
  deadline is unexpired in the sample's observation. A superseded back counts
  zero even after the newer front expires, is reclaimed or its slot is reused;
  an ineligible newer pin does not make the older pin countable.
  This predicate does not select a new sampling consistency model or promise
  a globally instantaneous snapshot or resize-invariant count under concurrent
  traffic. The complete all-worker sample, validity, cadence and failure rules
  remain required; the sampling mechanism must establish them before use.
- The output boundary is min(calculation, 100) for an applicable valid result,
  min(configured, 100) for a fallback or a missing applicable snapshot, and 0
  for a configured weight of 0, a disabled real or a health-ineligible real. A
  weight parameter is not an exact number of lookup cells and not a guarantee
  of packet shares, and a zero selector weight does not change administrative
  state or terminate pins.
- A retained result stays applicable only to the same participating real
  identities, their configured weights and the effective power. Any change to
  those inputs, including removal from the eligible set, invalidates the
  result and requires fallback for the current inputs. The invalidation on
  removal is a conservative choice, so a health flap can move a whole VS
  temporarily from WLC-shaped to base capped weights, and no upper bound on
  fallback duration is promised. A no-op does not invalidate the retained
  result; metadata-only changes do not change WLC input generation or invalidate
  the retained result. The invalid-sample count is scoped to the input generation, a valid
  sample breaks the sequence, and publication failures are tracked separately
  and neither increment nor reset that count.
  Any change of participating real identity, configured weight or effective
  power starts a new input generation with invalid-sample streak 0, together
  with the immediate fallback for invalidated inputs. A complete current valid
  sample counts as valid even when its source values are unchanged; validity
  needs no new marker and no numeric change.
- Construction and live update are distinct: a selector creation or a
  membership change uses construction with its initial Adjust, and a weight
  update with unchanged membership uses the update path without an additional
  Adjust. Preparation is private, publication is atomic, and a failure retains
  the previous version instead of mutating it as rollback scratch. The empty
  case is an explicit empty selector. These are the selected `Make` with initial
  `Adjust` and `Update` without additional `Adjust` semantics; silently replacing
  an ordinary same-membership update with full reconstruction is not permitted.
  A pure authored real reorder is not a membership change: WLC uses `Update`
  without an additional `Adjust`, including when reordering accompanies an
  applicable weight update with unchanged membership. F04 still advances the
  token and republishes while preserving surviving state; no identical-placement
  guarantee is implied; non-WLC construction follows section 10.
  The explicitly specified recovery rebuild below is a separate operation.
  This contract promises no bit-identical cell placement across implementations
  or versions, exact traffic shares, churn bound, A-to-B-to-A restoration or
  restart-stable placement. Before implementation, the downstream selector
  specification must define the selected construction/update algorithms, tie
  rules and conformance vectors, preserving the rules above. Those vectors
  verify the selected algorithm, not a permanent public cell-mapping format;
  weights and table length alone do not make an arbitrary builder conformant.
- Recovery after a control-plane mutation-owner takeover, with the dataplane
  and shared memory surviving, restores authoritative accepted configuration,
  administrative state, configured weights and identities, establishes
  exclusive publication authority, and prepares a fresh selector from base
  weights before adaptive activation resumes. The base activation is a
  fallback, not restored freshness, and WLC readiness returns only after a
  successful publication of an applicable result. The management epoch is not
  a WLC-input generation, and a restart cannot manufacture a retention window
  for a stale result. Sessions, deadlines, incarnations and surviving WRR
  cursors are not reset by the rebuild.
- Equal-output confirmation is a permitted alternative to republication, not a
  shortcut: it needs a complete valid sample from the current owner applicable
  to the current input generation, a separately established correspondence
  between the sample-derived result and the active coherent selector and its
  continuation base, and one linearized commit that validates publisher
  authority, applicability, the active per-VS result version and the sample
  ordering. An empty patch, equal numeric cells and equal configured values
  are each insufficient on their own. The published result provenance and the
  owner-local confirmation evidence are distinct, and the owner-local evidence
  need not survive its owner's epoch.
  An equal active output uses this guarded confirmation, not a new publication.
  An equal candidate whose activation FAILED cannot confirm the active output;
  confirmation still requires current active equivalence, the committed
  continuation, applicability and authority, and the retry rules are unchanged.
- The builder continuation state lives in the control-plane mutation owner's
  process heap, while the published dataplane state stays in surviving shared
  memory. The dataplane never depends on a pointer into the control-plane heap
  or into Go memory. Disposable freshness bookkeeping may live in the heap,
  but accepted-state and publication-provenance recovery cannot rely on it
  exclusively.
- Budgets: the logical lookup length is 1600*N over all configured unique real
  bindings including disabled and zero-weight members, with an explicit empty
  selector at N=0; the aggregate binding charge is at most 2097151 per actual
  dataplane-instance registry across all three schedulers and all agent owners,
  including unlinked VSes, with an endpoint in two VSes counted twice and
  multiple module-config links to one VS counted once. On replacement the
  charge is recomputed from the current accepted state reached through the
  protected handle and descriptor, not from a stale local map. Physical
  admission covers retained current, candidate, retired and deleted versions,
  surviving old-owner allocations, metadata, builder, staging, scratch and
  owner-local confirmation state, actual replicas and link overhead. A
  candidate is rejected before acceptance when logical or physical admission
  fails, and nothing is truncated, shrunk or partially accepted to make it
  fit.

## 9. Publisher, Reader Protection And Lifecycle Direction

The reference direction is selected; the concrete mechanism, its
cross-process proof and the implementation remain downstream (#2725, #2726).

- A registered handle identifies exactly one VS incarnation and persists
  across ordinary same-incarnation updates and owner takeover; a recreated
  same-name VS gets a different handle and incarnation. The handle's
  publication root identifies one immutable committed descriptor that binds
  the accepted configuration and its revision context to the coherent
  identities, eligibility, selector, source, policy and timeout settings and
  the result provenance. Accepted inputs and descriptor-to-result provenance
  must remain recoverably reachable in surviving shared memory.
- The descriptor carries version-matched counter catalogue and bindings and
  protected references to mutable counter values and surviving session and
  cursor resources. A reader uses the descriptor's local identifiers with the
  descriptor's storage. Live counter and session contents are not copied as
  part of immutable configuration preparation, and sharing a retained cell for
  the same semantic counter requires explicit identity correspondence,
  ownership and synchronized reference changes.
- Readers protect the handle, then safely acquire and validate the descriptor
  before dereferencing it, and hold that protection through every use of its
  policy, pin resolution, eligibility, selection, counters and forwarding. The
  selected starting point is fixed instance and worker-incarnation hazard
  slots and a bounded stable control-plane guard-slot pool provisioned before
  readers use them, with one guard exclusively owning a slot. The initial
  dataplane scope is one complete VS-processing invocation, and the guard
  starts before the descriptor and its matching counter bindings are
  resolved. A guard slot may be reused only when its previous guard can
  neither access the descriptor nor write or clear the slot again, and cleanup
  after an abnormal exit requires reliable evidence that the specific
  participant incarnation cannot resume.
- Reclamation is asynchronous, using the full stable slot roster and a
  detach-before-scan candidate cutoff, together with structural and dependency
  owner protection. No hazard scan or grace wait is added to the authoritative
  commit. A descriptor with zero hazards is not freed when a structural root
  or a frozen deleted-handle root still reaches it, and a descriptor-specific
  hazard does not imply descriptor-sized retention when the retained domain is
  a larger arena.
- Identity is split: the handle and incarnation identify the VS, an
  instance-scoped non-reused allocation identity identifies one descriptor
  allocation and its ownership record, and a reader or hazard location
  identifies a position rather than an allocation or a semantic version.
  Locator-based acquisition permits location ABA: a reader that never touched
  the removed allocation may acquire the current one, and the hazard then
  protects that location through use. Allocation identities are issued from
  checked monotonic reservation state during control-plane preparation, never
  on the packet path; discarded candidates do not return their numbers, and a
  takeover does not reset issuance. One allocation is published once. Storage
  at a location may be reused with a new allocation identity only after a safe
  release, and a stale retirement record must never free a newer allocation.
  Finite identity exhaustion must not wrap into an old identity inside one
  instance lifetime; the operation is rejected before publication and the
  committed state is preserved.
- A writer must not accept a stale candidate because the root again equals the
  same location. It safely acquires the current descriptor and validates the
  incarnation, the expected allocation identity where the continuation depends
  on it, and the relevant state, input, result and sample versions under a
  fenced final ordering. A location compare-and-swap is not an authoritative
  commit.
- Success is a bounded, linearized authoritative commit after the required
  authority, expected-state and applicability checks. Worker observation and
  physical reclamation are not prerequisites for that success and remain
  separate observable lifecycle states. A lagging worker can still process
  packets under the previous version, and post-commit lag is not a pre-commit
  rejection or a permission to roll back.
- For an eligible replacement of an existing live handle, the root switch is
  the single irreversible acceptance decision, and it selects the same logical
  accepted snapshot for management and packet processing. The prerequisites
  are that the descriptor anchors all accepted configuration and its revision
  context in surviving shared memory, that allocation identity,
  published-state version, user-configuration revision and runtime sample,
  input and result ordering stay distinct, that the current management epoch
  is governed by the protected owner protocol, that new storage ownership and
  prepared retirement obligations exist, that a coherent ready intent precedes
  the root operation and the final ordering rechecks authority, versions and
  applicability, and that there is no second independently authoritative
  accepted-configuration update after the switch. Definite settlement and
  transfer of prepared resource obligations follow inside the bounded final
  protocol, before success acknowledgement and before the next interfering
  transition, and must stay recoverable if interrupted.
- The crash-observation distinctions are fixed: evidence that the ready intent
  was never reached means the operation had no authority to switch the root; a
  ready intent with a still-current protected predecessor means the transition
  did not accept; a current prepared candidate with absent settlement means
  the transition was already accepted, so the derived state is reconstructed
  and the obligations settled without republishing; a definite settled outcome
  with a lost reply leaves the internal decision standing and the client
  reconciles; unavailable or inconsistent protected state forbids guessing
  acceptance or non-application.
- The takeover boundary is fail-closed and may leave management unavailable
  indefinitely: admission closure is ordered with authority transfer, and
  previously admitted conflicting mutation sections must complete or produce
  reliable evidence that the relevant participant incarnations cannot resume,
  including delegated helpers. Interrupted shared state is reconciled before
  its authority domain reopens; death evidence excludes future writes but does
  not repair partial allocator, issuance, commit or retirement updates.
  Coverage includes authority-owned shared effects during preparation and
  cleanup, and no phase is safe merely because it is called preparation. These
  sections are not held through worker adoption, hazard scanning or reader
  drainage, and lawful dataplane session and counter activity is not part of
  the transferred authority. Independent unrelated-VS admission continues in
  ready operation. No bounded takeover latency, forced termination, lock
  clearing or unsafe storage reinitialization is authorized.
- Close-once mutation admission uses bounded, safely enrolled stable records
  in surviving shared memory, with the participant-incarnation association
  known at claim time. The logical stages are free, held, execution ended and
  record handed off, and obligations settled or transferred. A live owner
  hands off a sealed record as its final atomic shared action, after all
  payload writes and helper activity, and never writes a settled marker
  afterwards. The gate and slot operations use sequentially consistent
  ordering as the initial reasoning model. A complete post-closure scan with
  suitable observations excludes remaining admitted protected effects; it does
  not authorize table reclamation or declare state reconciliation complete. An
  aggregate count is a hint only and cannot authorize the absence of work, a
  memory release or the subtraction of guessed work belonging to a dead
  process.
- The local allocator recovery direction makes protected, recoverably
  published ownership metadata authoritative for the local pool that backs
  published pages. The pool and allocation identity, the allowed predecessor,
  the target ownership and the retained evidence are recorded before the
  corresponding effect can become authoritative, and splitting extents and
  recording ownership are part of the coherent protocol. A later transition
  must not destroy the only outcome evidence for unsettled work. Recovery that
  cannot establish the result isolates the uncertain storage and rejects
  conflicting work. This direction does not prove common parent or registry
  recovery, and it introduces no power-loss or shared-memory restart
  guarantee.
- Acquisition exhaustion is explicit. The dataplane work item owns a finite
  positive attempt budget that a helper cannot reset, and one root-load,
  publication and validation cycle consumes one attempt, including a
  successful acquisition. On dataplane exhaustion there is one counted drop
  with a distinct acquisition-failure reason in pre-provisioned per-worker
  storage reachable without the descriptor, with no cached fallback, no
  fabricated session miss, no insertion or refresh and no cursor advance. On
  the control plane, no free guard slot is a fail-fast `ResourceExhausted`
  without an implicit queue or automatic retry, an exhausted root-validation
  budget before commit is `Unavailable` rather than `Aborted`, a cancellation
  or deadline observed before final acceptance is `Canceled` or
  `DeadlineExceeded`, an authoritative logical absence is `NotFound` because
  inability to acquire is not evidence of absence, and a definite or unknown
  post-commit outcome is preserved and reconciled.
- A scheduled WLC acquisition or read failure that can be attributed counts as
  one invalid whole sample in the correct input generation and order; it never
  omits a required worker or real and never fabricates zeros, and a superseded
  attempt is not charged to a different input generation. If attribution
  cannot be established safely, unavailability is exposed instead of an
  invented generation, streak charge or readiness claim. After a complete
  valid sample has been adjudicated, a later activation or confirmation
  failure is not a second invalid sample and neither undoes nor repeats the
  once-only reset of that generation's streak. It does not advance freshness
  without successful activation or confirmation, or overwrite newer sampling
  decisions. Preserve committed builder and prior active state; discard only
  unpublished resources. Repeated activation failure gives no bounded wall-clock
  staleness guarantee. The applicable-input rules still hold: preserving prior
  active state does not authorize committing new inputs with an inapplicable
  selector. Activation failure marks WLC not-ready without advancing freshness.
  Retry on the 20 ms tick using a fresh complete sample prepared
  from committed continuation, even if weights equal a previously failed
  candidate. No separate timer or queue of old pending samples is introduced.
  The first two invalid attempts may retain previously ready-but-degraded state,
  but cannot clear preceding activation-failure not-ready; the third attempts
  fallback. Only successful applicable valid activation/confirmation restores ready.
  Sampling, computation, retry and reconciliation share this 20 ms cadence;
  it is not a worker-adoption SLO or bounded staleness guarantee.

Unknown activation outcomes require protected authoritative reconciliation through
the existing publication/recovery mechanism, not another public retry RPC. Compare
operation/result identity, owner, input generation, committed descriptor and
committed builder continuation, never weights alone. Proven committed may confirm
only while applicable and current under ordering guards; proven not committed
discards the candidate and resumes fresh samples. If still unknown, stay not-ready,
do not advance freshness and block dependent publication, continuing reconciliation
on the existing cadence/recovery path. Superseded work cannot alter newer state.

Leaving WLC stops sampling and invalidates reusable adaptive state; WLC status is
not applicable outside WLC. Re-entry atomically installs the current-input capped
base selector with streak zero and not-ready until fresh valid activation or
confirmation. Failed switches preserve old state. WLC no-ops and authored reorders
are not exit/re-entry.

## 10. Scheduler, OPS, Weights And Identities

- Health eligibility is not implied by administrative enablement or recovered
  configuration. The health integration must provide an epoch-scoped complete
  snapshot before ordered deltas and heartbeats; reconnect or a gap requires
  a new complete snapshot. Missing authoritative eligibility and stale checker
  or stream state fail closed. A successful observation enables health
  eligibility; `failed_attempts > retries` disables it. Recovery must establish
  current authoritative eligibility, not fabricate healthy state from restored
  administrative state. The operator publishes through the Gateway and
  acknowledges generation and serving readiness. Disable publishes before
  route withdrawal; recovery publishes before announcement, and an independent
  route-announcement lease expires no later than health eligibility. Apart from
  the attempt timeout selected below, exact polling/retry intervals, heartbeat,
  health-eligibility and lease deadlines (durations uint32 milliseconds and
  absolute monotonic times uint64 milliseconds in identified domains), transport ownership and integration
  tests are mandatory health-integration prerequisites, not defaults supplied
  by this representation contract. They must be fixed before enabling that
  integration; administrative, health, WLC and serving readiness remain distinct.
- The public scheduler set is WRR, HASH and WLC. WRR is the cursor scheduler
  historically called PURR; HASH is the legacy hash scheduler historically
  called WRR. These historical names are not aliases or a compatibility API:
  RR and PURR inputs reject. RR-like configuration uses HASH with weights 1,
  without a promise to preserve legacy zero-weight selection; HASH with every
  weight 1 is equivalent to the legacy rr scheduler. WRR with every weight 1 is
  a cyclic per-worker order for that VS, not a global round robin. WRR's
  interleaved order and per-worker per-VS cursor deliberately differ from
  legacy PURR, which emitted contiguous runs from one counter shared by all
  services on a worker, so WRR is not legacy-identical. There are no public
  user-controlled hash masks, index masks or scheduler flags that can redefine
  the selected scheduler, and the implementation derives its representation
  from the accepted mode. Required WRR cursor state must be available before
  publication; missing cursor state rejects the candidate and never silently
  switches WRR to hash scheduling.
  HASH retains legacy inputs: base IP protocol/Next Header, source and
  destination addresses, TCP/UDP ports unless portless, and metadata `flowLabel`.
  That metadata is not necessarily the current inner IPv6 flow label. This is
  not a newly defined five-tuple hash; exact encoding, algorithm and metadata
  provenance remain downstream obligations.
- HASH rebuilds eligible runs contiguously in accepted authored order, emitting
  exactly the configured weight per positive eligible real. WRR builds an
  interleaved IPVS-like order over the same positive eligible reals: with
  `g = gcd(weights)`, the threshold starts at the maximum weight and descends
  by `g` down to `g`; each pass scans the reals in authored order and emits
  every real whose weight is at least the threshold. One sweep yields
  `sum(weights)/g` entries, and the expanded sequence repeats that sweep `g`
  times, so `L = sum(weights)` and each real appears exactly its configured
  weight times; the repetition is the same sequence, not a different policy.
  Weights `[4,3,2]` for reals A, B, C give `A,A,B,A,B,C,A,B,C`; `[2,2]` gives
  `A,B,A,B`. For this fixed coherent weight set, the thresholds above give
  the same choices as IPVS thresholds starting at `maximum - (g - 1)` and
  descending by `g` to 1: all positive weights are multiples of `g`.
  These are threshold passes, not flat authored runs of `weight/g` copies.
  Weights `[6,4]` give `A,A,B,A,B,A,A,B,A,B`, not `A,A,A,B,B,A,A,A,B,B`.
  Neither order promises a universal maximum-run bound or a global
  round robin across workers or VSes. Reorder rebuilds HASH runs and the WRR
  order, preserving surviving incarnations, sessions and WRR cursors. The
  existing 100*N and shared-pool admission rules still reject rather than
  truncate. WLC keeps its own Make/Adjust/Update construction, which is not
  contiguous, and neither HASH nor WRR promises perpetual cell placement.
- Health tracks consecutive completed failures. New real/checker state without
  trustworthy history starts streak zero and unknown/unhealthy, not healthy.
  A success resets zero and enables health eligibility; failure disables when
  streak > retries. Checker authority supplies a continuity marker and ordered
  observation sequence with a complete real-incarnation streak/eligibility
  snapshot. Same marker plus continuous sequence and complete authoritative
  history preserves the streak across transport reconnect; reconnect is not
  restart. Changed marker without restored history, missing history or unproven
  continuity initializes unknown/unhealthy and zero, never fabricated success.
  Stale old-marker deltas cannot enable a real. Administrative drain and health
  expiry remain independent constraints.
- The external checker owns configurable `attempt_timeout_ms`: uint32
  milliseconds, positive 1..4294967295; explicit zero or an out-of-domain value
  rejects checker configuration. Omission/null in checker configuration resolves
  to 500 ms; 100 ms is the operational target, not a different default or cap.
  This limits one check attempt, not polling interval, retry count, heartbeat,
  health-eligibility or route-announcement lease deadlines. Effective timeout
  accompanies checker configuration identity and immutable revision in every
  authoritative snapshot; missing/invalid effective timeout is untrusted and
  fails health closed. A timeout change creates a new checker revision with a
  coherent snapshot; old-revision observations cannot apply to it. This is
  external checker configuration/runtime provenance, not a writable VS field or
  VS mutation token, and the remaining integration deadlines stay prerequisites.
- The external checker owns effective `retries`, an integer in `0..255`,
  not a new writable VS field. Each authoritative snapshot carries it
  with checker-configuration identity and immutable revision, streak, eligibility,
  continuity marker and observation sequence; deltas belong to that revision.
  There is no implicit retries default. A missing or invalid threshold is untrusted
  and fails health closed, never interpreted as zero. Zero disables on the first
  completed failure. The streak representation MUST distinguish `retries` from
  `retries+1` without overflow: failure count 256 must be representable when
  retries is 255; no physical width is selected.
  A threshold change requires a coherent snapshot with proven history: preserve
  that history, lowering may disable immediately, and raising MUST NOT revive an
  already unhealthy real without a new success. Without trustworthy history use
  the existing unknown/unhealthy initialization. Old-revision deltas MUST NOT
  apply to the new revision. Checker revision is runtime provenance, not a VS
  mutation token. Administrative disable and health expiry remain stronger than
  checker eligibility; no other external checker default is inferred here.
  A snapshot claiming eligible=true with streak>retries contradicts these rules
  and cannot commit as trusted. This does not erase proven history or fabricate
  health: eligible=false below threshold remains valid hysteresis. No new
  immediate-disable state or grace timer is selected. Stream non-aliasing/fencing
  must prove stale exclusion before implementation; no epoch width or new
  exhaustion protocol is specified here.
- WRR, HASH and WLC all exclude configured weight 0 from new selection without
  disabling the real or killing a valid pin. Configured weights [0,3] therefore
  exclude the first real in every mode; there is no unit-weight exception.
  Changing the scheduler does not change stored weights.
- HASH and WRR retain configured weights in 0..16777215 without clamping.
  Each VS reserves `100*N` selector slots, where N includes every bound real,
  including disabled, unhealthy and zero-weight members. Configuration admission
  requires the exact sum of configured weights of ALL administratively enabled
  reals, independent of health, to be <= `100*N`. Actual eligible runs remain
  health-filtered; health success cannot overflow this logical reservation.
  The multiplier 100 sizes the reservation; it is not a per-real weight cap or
  a new distribution/tie rule.
  HASH/WRR share a pool bounded by 209715200 real-ID cells per generation:
  charge reserved regions, not only occupied cells. Each VS is bounded both by
  its reservation and by the remaining shared pool; unused VS reservations are
  not implicitly donated to other services.
  Validate creation, replacement, weight changes, administrative enablement,
  scheduler transitions into HASH/WRR and membership
  changes, including removal of zero-weight or disabled reals. An overflowing
  result rejects the whole mutation, preserving previous accepted/active state
  and token: no truncation, partial ring fill or skipping later reals. For
  example, administratively enabled weight 150 with a zero-weight member
  (`[150,0]`, N=2) fits 200 slots; removing the zero-weight member leaves N=1 and only 100 slots,
  so reject rather than clamp the survivor, even when it is unhealthy. At N=1,
  disabled weight 150 may be stored, but enabling it rejects even if unhealthy.
  This logical rule adds no generic runtime physical-allocation failure policy.
  O09/O10 validate the complete final candidate, never sequential intermediate
  weight sums. WLC's distinct
  `min(q,100)` output/fallback and `1600*N` geometry remain unchanged.
  The shared-pool number describes the historical sizing default, not reserved
  deployment memory, guaranteed allocatability or a promise that a build can
  allocate it. Logical bounds do not waive physical admission or require a
  static-array implementation.
- OPS is orthogonal to the scheduler and UDP-scoped. Entering OPS retains live
  session hits under all three schedulers; UDP supports WRR, HASH and WLC. Live
  hits do not refresh their deadlines, and misses select
  statelessly and create no session; leaving OPS atomically restores refresh
  on live hits and creation on misses. A mixed TCP+UDP VS retains true and applies
  OPS only to actual UDP; its TCP sessions retain ordinary stateful behavior.
  For a TCP-only service, true is `InvalidArgument` through both API and CLI;
  false and absence are valid and resolve to false. This request-local domain
  check applies to create and full replacement after the recovery gate and before
  CAS, even for an otherwise equal candidate or stale token; it is never a no-op
  or a true-to-false normalization. Mixed and UDP-only true remain valid.
  WLC observes session load, not new stateless-packet load.
  OPS belongs to VS configuration, not to a packet or session setting; entry
  and exit take effect through VS configuration publication. Switching alone
  neither deletes sessions nor rewrites their deadlines. Each packet uses its
  applicable coherent protected VS version under the existing publisher rules;
  this does not require every in-flight packet to adopt the newest configuration.
  OPS invalid-pin rebinding follows the rule below, without deadline refresh.
  OPS transitions do not relax disabled-real or invalid-pin dispositions.
- The WRR cursor belongs to each worker and VS pair, is shared by links to
  the same VS on that worker, and advances once after a successful coherent
  read of the selected slot from a nonempty ring. It does not advance for a
  live pinned hit, a policy rejection, an empty ring, a failed coherent read,
  Echo or a separate ICMP delivery, and a later readiness or packet-processing
  failure does not roll back the consumed position. It survives replacement, a
  selector rebuild, switching away from WRR and back, and a control-plane
  only restart while dataplane and shared memory survive. There is no continuity
  promise across dataplane, shared-memory or host restart, no required zero reset,
  chosen initial slot or persistence mechanism. Delete followed by create
  establishes new cursor state. It is
  internal runtime state, not a user-settable field, and advancement does not
  change the configuration token. Given a coherent nonempty expanded sequence of length `L`,
  compute `next = (cursor + 1) mod 2^64` and read `slot = next mod L` without
  narrowing the cursor. Commit `cursor = next` only if selection succeeds;
  computing `next` is not itself an advancement. The initial cursor position and
  first slot have no public guarantee. Wrap is ordinary arithmetic, including
  a discontinuity in cyclic slot order when `L` does not divide `2^64`.
- The immutable VS identity is the decoded family/VIP, optional protocol and
  optional destination port. Protocol and port are independently optional:
  absent/null protocol selects exactly TCP+UDP; explicit TCP or UDP selects only
  that transport. Explicit unspecified or unknown protocol rejects. Absent port
  selects all ports; explicit zero matches only port zero. Two selectors conflict
  iff family and VIP agree AND their protocol sets intersect AND their port sets
  intersect. Thus TCP:443 and UDP:443 may coexist, but a mixed :443 conflicts with
  either; TCP without a port conflicts with TCP:443 but not UDP:443. Distinct
  explicit ports may coexist. Conflict admission covers all accepted VSes in the actual
  dataplane instance, including unlinked VSes and different module/agent owners;
  a conflicting candidate is `InvalidArgument`, with no same-identity staging
  clone. A current-token replacement cannot change this identity, including
  changing between mixed and single-protocol selectors; stale-token precedence
  is unchanged. One mixed VS has one incarnation, real set, selector, WRR cursor
  per worker and shared session capacity/resource charge, not two child VSes.
  The redundant
  `pure_l3` switch is removed and the portless mode is derived from
  destination port absence. In portless mode the session identity excludes
  both transport ports, so admitted packets from one client address differing
  only in source and destination port share the same live pin within the same
  actual transport; TCP and UDP never share a session. The ports are
  not direct selection-hash inputs, and the exact hash encoding and label
  provenance are not frozen.
- The canonical logical session key within the existing VS-incarnation scope is
  `(family, transport, client_address, VIP_address, ports)`: family is IPv4 or
  IPv6, transport is the actual TCP or UDP even in a mixed VS (never a wildcard),
  and ports is either `Portless` (both absent) or
  `Specific(client_port, VIP_port)` (both present). Mode is exactly the VS mode;
  specific port 0 remains 0, not absence. Addresses are exact decoded bytes of
  the stated family, with no implicit IPv4/IPv6 folding. Valid packet-derived
  endpoints share one inner-header family; validation of mismatched externally
  constructed typed keys or records remains required. This adds no client-address
  allowlist and does not relax mapped-IPv6 VIP rejection.
  Ordinary TCP/UDP extraction uses client-to-VIP inner endpoints before
  encapsulation. Ordinary lookup, copy-forward, suppression, OPS, ownership
  query, reverse delivery, WLC deduplication and inspection share this identity.
  Links to the same VS share its scope; deleting and recreating the VS does not.
  The saved real key and real incarnation are a separate pin value, not the flow
  key. TCP flags, real endpoints and tunnel outer addresses are not session
  identity. Physical storage MAY omit VS-invariant components and derive them
  from protected immutable context, and MAY include scope bytes; a mixed VS cannot
  derive actual transport from its wildcard selector or omit that distinction.
  This defines
  no new namespace, C struct layout, protobuf field numbers, hash encoding,
  endianness or selected algorithm; the portless selection-hash rule above stays.
- The real key is the decoded address family, address bytes and optional real
  port inside the VS incarnation. Absent, zero and other port values stay
  distinct; exact duplicate keys reject the whole candidate; same-address
  different-port reals are permitted. A surviving key keeps its real
  incarnation across replacement, reordering and non-key attribute changes. A
  committed removal followed by readdition of the same key creates a new
  incarnation, administratively disabled, with the new configured weight input
  or its default, and it does not resurrect old administrative overrides,
  configured weights, WLC snapshots or session pins. Session resolution
  distinguishes the full key and the incarnation, directly or through a safe
  opaque handle, so a reused slot, address or allocator identifier cannot
  alias an old pin to a new real.
- Metadata-only changes do not change VS network or real identity, session-chain
  topology, session state/deadlines or the WRR cursor. Surviving real metadata is
  submitted/default configuration, not preserved manual state; changing or reusing
  its text never rebinds or revives a pin.
- A still-live pin whose real is effectively disabled (administratively
  disabled or health-ineligible), absent or has a different incarnation follows
  the invalid-pin disposition rather than an ordinary session miss.
  Resolution uses the current real configuration, so an old pin of a removed
  real never resurrects that real or its old source inputs. UDP may
  reschedule, TCP may reschedule when SYN is set and RST is clear including
  SYN-ACK, other TCP packets counted-drop, and an ICMP-error reverse-session
  delivery counted-drops without rescheduling. For admitted ordinary TCP and
  UDP traffic a genuine miss (a successful lookup establishing that no
  logically live pin exists, including logical expiry, but not a read failure
  and not an unresolved identity in a still-live pin) permits fresh eligible
  real selection, including TCP non-SYN. Stateful forwarding still requires
  session insertion under the fail-closed contract.
- On the ordinary UDP forwarding path under the applicable coherent VS
  configuration with OPS enabled, a live
  authoritative pin to a disabled, absent or reincarnated real requires eligible
  real selection and rebinding of the same logical session, preserving its
  stored absolute deadline. This is not insertion of a new session or refresh.
  Reverse-session delivery retains its separate no-reselection/no-rebind rule;
  ownership queries remain read-only.
  For a back pin, the rebind is one late atomic front commit with supersession
  under section 7's no-resurrection rule; the back deadline and watermark remain
  unchanged. No eligible real counted-drops without session mutation. Packet
  preparation or required-write failure counted-drops without partial changes
  to session state or suppression by this operation, subject to normal expiry
  and independent concurrent operations; there is no write-failure stateless
  fallback.
  Before the late commit, revalidate session authority, liveness and selected-real
  eligibility in that coherent configuration context. A pin expired at commit
  is an OPS miss: perform fresh eligible stateless selection without insertion,
  never revive the pin or blindly forward to the previously selected real.
  Do not blindly overwrite a concurrently changed pin; re-establish coherent
  current authority or counted-drop if bounded work cannot establish it. Packet
  preparation, including encapsulation, must agree with the finally validated
  real: reselection to another target cannot send the old target's prepared
  packet. These are outcome requirements, not a new synchronization algorithm,
  retry threshold or requirement to switch to the newest published VS version.
- Address identity is the decoded family and bytes, not the input spelling.
  Mapped IPv6 remains IPv6 and is distinct from native IPv4, with no implicit
  unmapping. Normalization happens before key comparison, duplicate detection
  and the accepted-state no-op comparison, and the accepted-configuration
  readback preserves the family.

## 11. Outer Source Policy

### Address Roles For Network Emission

These checks constrain network-emitting roles, not generic inner forwarding:

- **SourceSet** forbids IPv4 `0.0.0.0/8`, `224.0.0.0/4` and
  `255.255.255.255`; IPv6 `::`, IPv4-mapped addresses and `ff00::/8`.
- **DestSet** forbids IPv4 `0.0.0.0/8`; IPv6 `::`
  and IPv4-mapped addresses. Every stored real endpoint obeys DestSet even when
  disabled, zero-weight or unhealthy; this is not deferred until selection.
  Invalid real configuration rejects the whole VS candidate with `InvalidArgument`
  under the existing validation/error order, preserving accepted/active state/token.

RFC 1122 section 3.2.1.3(a,b) covers the entire IPv4 0/8, not merely exact zero;
its initialization exception does not apply to these originators. No class-only
127/8 or ::1 ban applies in any of the eight roles below, at either management or
dataplane boundaries. This does not establish local routing or egress support:
loopback emission constraints still require downstream integration proof
(RFC 1122 section 3.2.1.3(g), RFC 4291 section 2.5.3). No localhost test mode,
new gate or routing feature is introduced.
RFC 4291 sections 2.5.2 and 2.7 constrain unspecified and multicast sources;
mapped rejection also preserves the target's native-address domain. Legacy IPv4
destination-zero broadcast mode is unsupported in this endpoint model, not a claim
that every such legacy use is universally forbidden by the RFC. No node-local
tunnel mode is selected and no universally RFC-safe endpoint behavior is promised.

R = reject configuration; D = consume/count-drop Echo before reply; U = unchanged,
no new class ban, **not** guaranteed support; K = context/compatibility knowledge
boundary, **not** automatic rejection. VS source R applies to selected outputs
for ALL derivation inputs in configured families, not raw or dormant values.

| Address class | VIP stored | Real outer D | VS generated S | Clone fixed S | Peer/local D | Ordinary inner | Echo VIP->S | Echo client->D |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| IPv4 0/8 including exact zero | U | R | R | R | R | U | D | D |
| IPv4 127/8 | U | U | U | U | U | U | U | U |
| IPv4 224/4 | U | K | R | R | K | U | D | D |
| IPv4 255.255.255.255 | U | K | R | R | K | U | D | D |
| IPv6 :: | U | R | R | R | R | U | D | D |
| IPv6 ::1 | U | U | U | U | U | U | U | U |
| IPv4-mapped IPv6 | R (existing) | R | R | R | R | U | D | D |
| IPv6 ff00::/8, including scope 0 | U | K | R | R | K | U | D | D |
| Contextual directed broadcast, not suffix alone | U | K | K | K | K | U | K | K |
| Link-local 169.254/16, fe80/10 | U | K | K | K | K | U | K | K |
| 240/4 except limited broadcast | U | K | K | K | K | U | K | K |
| Ordinary addresses, private IPv4, ULA, anycast | U | U | U | U | U | U | U | U |

Directed broadcast needs authoritative subnet/link context: never infer it from
`*.255` or the F21 splice prefix. A /31 has two host addresses and /32 is a host
route. Link-local support needs zone/egress context that bare address bytes do not
prove; this adds neither a blanket ban nor a zone field. Multicast/broadcast
destinations do not inherit source bans; absence of a ban is not full multicast
tunnel support, including IPv6 scope 0. The rest of 240/4 remains a separate
compatibility policy. Private IPv4, ULA and anycast are not newly banned.
Class validation proves neither source ownership nor reachability. Context-dependent
support needs integration proof, but unknown subnet context does not make all
ordinary addresses unavailable. F02 storage/exact lookup and ordinary inner traffic
remain unchanged; this is not a global martian filter or a change to DHCP.

### Selected VS Source And Family Transitions

- The source policy is owned by the VS. Reals of the same outer family use
  that policy, and it does not vary by the linking module config or by the
  packet's module link. Per-real source override is deferred, not rejected,
  and any future extension must not change the byte-generation algorithm
  below. The configured family set follows the current configured reals only:
  an old pin of a removed real neither adds that family back to the configured
  families nor resurrects its old source bases or overrides.
- The bases are always materialized in accepted state: an omitted IPv4 base
  becomes 0.0.0.0 and an omitted IPv6 base becomes ::. An explicit default
  equals omission after normalization, while an absent override differs from a
  present zero-valued override. Inside a present IPv6 override an omitted
  prefix equals an explicit 128 and an explicit 0 stays distinct. A full
  replacement defaults omitted bases and clears omitted overrides; neither
  means KEEP.
- The selected algorithm: the outer IPv4 source is the literal override
  address when present, otherwise the IPv4 base M4, and neither the inner source
  nor an IPv4 prefix changes it. Without an IPv6 override the IPv6 base is
  M6: it is copied and bytes 8..12 are replaced by R, where R is the inner IPv4 source
  bytes for an inner IPv4 packet and the bytewise exclusive-or of the inner
  IPv6 source bytes 8..12 and 12..16 for an inner IPv6 packet. With an IPv6
  override the raw address is copied without normalizing it, then with prefix
  p and k = ceil(p/8), n = min(4, 16-k), the first n bytes of R replace bytes
  k..k+n, and for p <= 80 the last two bytes of the IPv6 base replace bytes
  14..16; that IPv6 base is likewise M6. The prefix domain is 0..128 including non-byte lengths, and no
  canonical network or byte alignment is required. For B=`2001:db8:1:2::`,
  p=64, M6 last bytes `1234` and client `192.0.2.1`, the output is
  `2001:db8:1:2:c000:201:0:1234`. Without an override, M6=`2001:db8:1:2::1234`
  and inner IPv4 client `192.0.2.1` yield the same
  `2001:db8:1:2:c000:201:0:1234`.
- The IPv4 override is represented as an optional native IPv4 host address
  with no prefix in accepted state, readback, equality or the source policy,
  and an address-valued text input must be a bare address without a CIDR
  suffix.
- Generated-output admission is part of normal validation, not of enable or
  traffic. For each outer family present among configured reals, including
  disabled, zero-weight and health-ineligible reals, the selected source
  branch is validated before publication against SourceSet for ALL derivation
  inputs R, not just observed traffic or one sample output.
  Structurally valid dormant-family
  inputs are stored but their output check is skipped while that family has no
  configured real. A failure rejects the complete candidate of one VS create
  or replacement with `InvalidArgument` and preserves the accepted and active
  state and its token; it does not rewrite a source, unmap it, choose an
  interface fallback or add a per-packet guard.
- This validates the selected generated output, not every raw base/template.
  Every active IPv6 template with p=0 fails admission: R can supply first byte
  ff even when both raw template B and base M6 are unicast. With p>=1 the first
  byte comes from B; without an override it comes from M6. A raw multicast M6
  is not alone a rejection when the selected template uses only its last two
  bytes and its generated outputs pass validation. The structural prefix domain
  remains 0..128 for storage, equality and round-trip, including dormant p=0;
  only active generated-output admission excludes p=0. A forbidden raw IPv4 base
  may likewise be replaced by a valid literal override. Removing the loopback
  ban does not remove ALL-R validation: B=::2,p=120 still fails because R=0
  produces ::. At p=121..128, k=16 and n=0, so R does not splice; B=::1 passes
  the class check. M6=::1 without an override can still generate an invalid
  IPv4-mapped output for some R. Check selected outputs, not just raw classes.
  No ACL exception or universally valid-source guarantee follows.
- Validate the complete replacement's family set. Adding its first real of a
  family, even disabled, activates that family's output check; removing its last
  real makes the family dormant, with structural validation and retention intact.
  Partial enable/weight changes must not become the first source validator.

## 12. Packet Surfaces

[12. Packet Surfaces](l3b-packet-contract.md#12-packet-surfaces).

### Packet Drop Precedence

[Packet Drop Precedence](l3b-packet-contract.md#packet-drop-precedence).

### Packet Path Rules

[Packet Path Rules](l3b-packet-contract.md#packet-path-rules).

### External Fanout Configuration And Clone Headers

[External Fanout Configuration And Clone Headers](l3b-packet-contract.md#external-fanout-configuration-and-clone-headers).

### Adoption And Fanout Boundary Conformance

[Adoption And Fanout Boundary Conformance](l3b-packet-contract.md#adoption-and-fanout-boundary-conformance).

### MSS And Ordinary Encapsulation

[MSS And Ordinary Encapsulation](l3b-packet-contract.md#mss-and-ordinary-encapsulation).

### l3b-To-Real Outer Headers

[l3b-To-Real Outer Headers](l3b-packet-contract.md#l3b-to-real-outer-headers).

## 13. Service Policy Boundary

- The operator or direct client supplies identity (source namespace plus
  policy key), immutable revision and complete native ACL content in the VS
  create or full replacement request, or explicitly chooses empty policy with
  the same metadata. Identity and revision are nonempty opaque source
  provenance, not authorization, addresses, allocation IDs or executable
  text. No registry, standalone policy CRUD, fetch-by-name or implicit latest
  revision is introduced.
- Missing or null binding, identity or revision, or missing or null content
  without the explicit empty choice, is `InvalidArgument`; omission never
  means KEEP or allow-all. Explicit empty is deny-all through the ACL-owned
  unconditional DENY adapter of section 14. Native content requires nonempty
  rules; zero-rule content rejects, and nested empty lists never imply the
  empty choice. Partial real state/weight operations preserve the binding.
- Equality compares identity, revision and the native normalized accepted
  content, including order and counter-relevant identity. Equal normalized
  content under the same identity/revision is equal; changed content under
  that identity/revision is `InvalidArgument` against the current accepted
  binding or any shared retained revision it would reuse. Different identity
  or revision is a configuration change even for equal packet behavior. This
  requires no global revision ledger, cross-VS transaction or
  counter-continuity guarantee.
- Existing validation and expected-token ordering applies: a stale token is
  `Aborted` even for equal content. Only an unchanged full accepted result
  under the current token is a no-op, preserving the token without
  republication or counter/deadline reset. A changed binding advances the VS
  token with coherent descriptor/readback. The descriptor retains the
  immutable compiled revision and protected lifetime; shared policy content
  never shares VS scheduler, real or session state.
- The service policy reuses the native ACL implementation for its grammar,
  parser and normalization, actions, matching order, defaults, compilation
  and rule counters. l3b binds the matched VS and passes the original packet
  into the bound check before session creation and real selection, and it does
  not re-interpret addresses and ports with its own engine. IPIP, GRE and
  source-template rules are not mixed with the ACL language, and not every
  standalone ACL capability is automatically a valid action inside l3b.
- Source-address and source-port predicates are the required minimum supported
  policy dimensions: both must be demonstrable on the reused ACL boundary, and
  a destination-port filter cannot substitute for source-port matching. An
  omitted source-port restriction keeps the required wildcard. Source and
  destination ports are not interchangeable.
- Unsupported internal actions reject rather than silently translate. Missing
  or invalid policy rejects the whole candidate and preserves accepted and
  active state; unavailable runtime checking fails closed. Query, Echo and
  error delivery retain their separate contracts.
- Empty lists are not blanket-rewritten into wildcard or none; the verified
  ACL contract for the relevant field and protocol path is used, and the
  absent-network, absent-port and absent-protocol cases, address families and
  ranges follow section 14.
- Validate input size, identity/revision representation bounds, grammar,
  actions and native compilation/resource limits before acceptance, with
  checked C count/length conversions and accounted retained policy memory. Use
  ACL-owned limits and existing request/export budgets, not duplicate l3b
  quotas. Source namespace, key and revision are each 1..256 opaque decoded
  bytes with base64 text (section 15). Invalid input, resource exhaustion and
  unavailable authoritative state retain the existing error order.
- The versioned public contract carries required presence, identity/revision
  and the native payload or explicit-empty choice, and returns the coherent
  accepted binding. Protobuf/C ABI consequences are presence-safe
  alternatives, lossless metadata, checked lengths, owned copied input and
  protected compiled-policy references; exact tags, layout and attach
  mechanics stay downstream.
- There is no internal common policy in l3b. Common rules are expressed by a
  separate ACL module in the pipeline, which is outside the l3b contract: it
  shares no token, no commit and no atomicity with an l3b change, and ordinary
  module composition does not imply one. Per-VS device and pipeline fanout
  remain deferred; neither a second policy engine inside l3b nor per-VS
  fanout is part of this contract.

## 14. Native Policy Mapping

The native message vocabulary is defined by the public ACL and common filter
schemas at commit `ea506c95b08681e3a333ed44175273bf5bca35ee`.
The complete accepted subset and its behavior are specified below.

The content branch of the F22 binding contains an ordered sequence of the
existing native ACL rule messages, not a standalone ACL update request. No ACL
config name, fwtable name, sync configuration or independent mutation endpoint
is accepted. The other branch is the explicit empty policy. Native content
requires at least one rule: `{}`, omitted rules and `rules: []` reject. No
zero-rule compilation is promised through an API whose public validator
rejects it. Every list element must be a non-null complete native message.
Lists remain ordered; numeric ranges are inclusive.

YAML native networks are strings `address[/prefix-or-same-family-mask]`.
Omitted mask means a host mask. Permit at most one slash with nonempty segments;
reject zones, brackets, whitespace and hostnames. IPv4 uses decimal octets without
ambiguous leading zeros; IPv6 accepts compressed or uncompressed notation and
retains its family, never unmapping. Normalize address AND mask. IPv4 masks are
contiguous; the already selected IPv6 domain permits an independent prefix mask
in each 64-bit half. Canonical output uses `/prefix` for globally contiguous masks,
including explicit `/0` and host prefixes. Other valid IPv6 masks render as
`address/maskaddress`. Render IPv6 in lowercase hex, compress the longest zero
run with leftmost tie-breaking, and never emit a dotted IPv4 tail. Parsing that
output must recover the same family and normalized network. F19-F21 raw source
fields are not this grammar and must not lose host bits through normalization.

| Native field | Selected mapping, defaults, ranges and errors |
| --- | --- |
| actions | Ordered 0..8; absent, null and `[]` normalize to empty. Only PASS, DENY and COUNT are accepted; CHECK_STATE, CREATE_STATE, LOG and unknown values reject even after a terminal action. No unsupported action is silently ignored. |
| Action.kind | Native zero PASS is retained, including `{}`; omitted or null kind is PASS. PASS=0, DENY=1, COUNT=2. `actions: []` drops on match; `actions: [{}]` passes. No UNSPECIFIED value is invented. YAML deliberately adapts to this default. |
| counter | Required: 1..127 UTF-8 bytes, no NUL, unique within the binding even without COUNT. Missing, null or empty rejects; no trimming or case folding. No positional default and no duplicate sharing. Native counter actions and storage stay ACL-owned. |
| devices / Device.name | A nonempty list rejects: service intent cannot depend on linking device context. Absent, null and `[]` normalize to empty. Even `Device{}` or an empty name rejects; no lookup or namespace is added. |
| vlan_ranges / VlanRange.from,to | A nonempty list rejects rather than silently dropping a deprecated predicate; absent, null and `[]` normalize to empty. No l3b VLAN semantics or limits are added. |
| sources4, destinations4 | Missing, null or `[]` is an empty list, not 0.0.0.0/0. Both lists empty disables the family except the native all-network-empty catch-all. Exactly one nonempty list rejects instead of accepting an inert family. Source-only IPv4 restriction needs an explicit destination 0.0.0.0/0. |
| sources6, destinations6 | Same independent family pairing; source-only needs an explicit destination ::/0. The family is never inferred from the VS family. A valid policy may match only the other family and deny this VS. |
| Network addr, mask | Both nested messages are required; absent or null address or mask and network `{}` reject. Present zero address and mask messages are valid numeric zeros, so `{addr:{},mask:{}}` is the explicit /0 network in ProtoJSON. IPv4 masks are contiguous, IPv6 masks are bi-contiguous; wrong family, zone suffix and malformed length or mask reject before C. Native conversion masks host bits; the family is preserved and IPv6 is never unmapped. |
| proto_ranges / ProtoRange.from,to | Absent, null and `[]` stay empty, not wildcard protocol. Endpoints are 0..65535 with from <= to; a missing or null endpoint is 0; `{}` is 0..0. The packed value is (IP protocol << 8) OR subtype: the TCP full range is 1536..1791, the UDP full range is 4352..4607; 6..6 is not TCP. No l3b protocol DSL or VS substitution. |
| src_port_ranges / PortRange.from,to | Absent, null and `[]` are wildcard source port on the native transport path. `{}` is port 0 only; a missing or null endpoint defaults 0, then from <= to is checked. Domain 0..65535. Never a destination-port substitution. |
| dst_port_ranges / PortRange.from,to | Same mapping, destination direction only. |
| fragment / Fragment.kind | Absent, null, `{}` or omitted/null kind means Any=0; None=1; Frag=2; other numbers reject. Frag is valid native intent but cannot admit real fragments through the earlier l3b fragment-drop floor. |
| Standalone name, fwtable_name_v4/v6, sync_config, rule counter values, compile metadata | Not binding input; textual keys reject even when null or default-valued. No state attach or creation, no log promise, no runtime-counter setter. |

Native network child representation is exhaustive: IPv4 addr.addr and mask.addr
are uint32 in 0..4294967295; IPv6 addr.hi/lo and mask.hi/lo are uint64 in
0..18446744073709551615. Missing or null scalar children default to zero, but
their containing address and mask messages must be present. ProtoJSON uses
nested messages with decimal-string uint64 output. YAML native family network
strings accept a bare host (full mask), CIDR (/0../32 or /0../128), or an
explicit same-family address/mask. IPv4 masks are high-one prefixes; IPv6
allows an independent prefix in each 64-bit half, not arbitrary holey masks. C
receives the native-masked network-byte-order address/mask, never host-endian
protobuf storage. These rules do not apply to the raw F19-F21 source
templates.

### Native Path Consequences

Native full-port side means an empty list OR a list whose first range is
[0,65535]. Path selection does not compute union coverage: [[1,2],[0,65535]]
is not full-port for that decision, while the reverse order is. The terms
wildcard and constrained below use that native predicate, not set equivalence.

| Networks and transport predicates | Consequence |
| --- | --- |
| All four network lists empty, no protocol or port restriction, fragment Any | Accept the native unconditional L2 catch-all with an empty device set; first match actions govern. Explicit /0 family rules remain distinct accepted content even if behavior agrees. |
| All networks empty but protocol or ports nonempty, or fragment None/Frag | Reject predicates the device-only path would ignore; do not widen or rewrite. Use explicit paired networks. |
| One complete family pair, the other absent | Only the explicit family participates; no missing-family wildcard. |
| Complete family, protocols empty, both port sides full | Native plain path has a vacuous protocol constraint, subject to fragment. |
| Complete family, protocols empty, a constrained port side | With Any/None fragment there is no ordinary TCP/UDP path. Frag may choose the plain fragment path without port checks, but l3b drops real fragments earlier. Accept this authored no-match rule; do not fill TCP/UDP or equate it to empty policy. |
| Complete family, TCP coverage | The TCP flag interval is tested as well as both ports; full subtype 0..255 is not a particular flag mask. |
| Complete family, UDP subtype excludes zero | No UDP membership or match; never widen to all UDP. |
| No match, or matched empty/COUNT-only actions | Drop; COUNT-only does not continue to the next rule. COUNT before a terminal action counts packets and bytes; COUNT after the first terminal action is not executed. Repeated COUNT actions are retained, not deduplicated. |

These restrictions adapt input safety, not the matcher. Native destination,
protocol and fragment matches are explicitly selected; device, VLAN and
fwstate/log contexts are excluded.

### Equality, Counters And Empty Policy

Validation and native address/mask conversion produce the normalized accepted
rule sequence. Scalar defaults are materialized, nil and empty repeated fields
normalize to empty, fragment absence normalizes to Any, and a network address
is masked with its mask. Every rule, action and list position, duplicate
range/network entry, counter string, mask, endpoint and protocol interval is
preserved. No sorting or merging of unions, no discarding of inert rules, no
removal of trailing actions, and no equation of policies by packet behavior.
Explicit full-range ports and omitted lists match as wildcard but remain
distinct authored lists. Equality uses native values, not compiler layout,
YAML spelling, wire bytes, allocator identity or hashes; the standalone ACL
request equality is not evidence for it.

The caller's exact (source, key, revision) tuple denotes immutable normalized
content; reclamation does not authorize redefining it. The server compares
against the current binding and any retained shared revision the candidate would
reuse, rejecting changed content under the same tuple in those comparisons.
No historical lookup is required once an old revision is no longer retained
and would not be reused; no permanent revision ledger is introduced. A
different tuple changes configuration even with equal rules. Equal policy
alone is not full-VS equality. Provenance bytes grant no authorization and no
token substitute.

Counter identity is VS-incarnation scoped plus the native counter tag, under
the collision checks of section 17 and protected descriptor/storage binding.
Shared compiled policy never merges mutable counters across VSes. All tags
participate in equality. No continuity across changed revisions is added; a
current-token full-result no-op rebuilds or resets nothing. A required
allocation or collision failure rejects the candidate.

The explicit-empty branch normalizes only to explicit empty, never to an
arbitrary all-deny user ruleset. Its ACL-owned adapter compiles a single
unconditional native DENY rule with no user predicate and no COUNT action,
using a private adapter counter identity disjoint from user rules. Readback
stays explicit empty with the supplied metadata; the synthetic rule is not
user intent. The native L2 catch-all and DENY action supply the source basis;
safe internal invocation, counter binding and lifetime still require proof and
tests downstream. This is not a claim that such an embedded callable API
exists today.

Native limits remain ACL-owned: at most 8 actions, counter bytes below 128,
native numeric domains and checked rule/list counts fitting the native uint32
counts, including derived intervals and multiplication before allocation or
narrowing. No `acl_max_rules` or `acl_max_entries` quota is reintroduced.
Common request/export budgets and accounted compiler/retained memory bound
accepted work; inability to fit is `ResourceExhausted`, never truncation.
Grammar and domain errors are `InvalidArgument`; unavailable authoritative
state is `Unavailable`. Downstream allocator and publisher proof does not
excuse unbounded preparation.

## 15. Policy Identity And Revision

Source namespace, policy key and revision are each opaque byte strings of
1..256 decoded bytes, with exact tuple equality, binary bytes on the wire and
textual base64. This is a uniform representation policy, not a native quota or
measured capacity. No parsing, charset or Unicode normalization, separators,
naming authority, namespace registration or hash-as-authorization is
introduced.

## 16. Format Contract

Binary protobuf keeps optional scalar and explicit collection
presence; ProtoJSON uses the selected protobuf schema, not custom Go
`encoding/json` methods. CLI YAML uses semantic fields and the native
address/network string adapters identified below. Serde JSON accepted as YAML
has YAML semantics and must not be advertised as ProtoJSON.

| Code | Binary protobuf | ProtoJSON | CLI YAML | Empty and zero meaning |
| --- | --- | --- | --- | --- |
| R | Required scalar, message or selected branch presence | Missing or null rejects after decode | Missing or null rejects | A wrong-shaped `{}`, `[]`, empty string or scalar rejects unless the field row explicitly allows it. |
| D(x) | Absent optional field means x | Missing or null means x | Missing or null means x | Explicit zero or false is retained unless the row states a zero default; empty string, object or list is not scalar absence. |
| OPT | Absent differs from present zero | Missing or null means absent | Missing or null means absent | Zero is retained; a malformed empty wrapper rejects. |
| COL | Required oneof: nonempty members wrapper or empty message | `{members:{items:[...]}}` or `{empty:{}}`; missing, null or unselected rejects | Same semantic branches | `members:{}`, missing, null or `[]` items and null elements reject. Only the explicit empty branch clears; `empty: null` is unselected, not clear. |
| NL | Native repeated field: absent and empty indistinguishable | Missing, null and `[]` mean empty; null elements reject | Missing, null and `[]` mean empty; null elements reject | Applies only to native rule lists, subject to section 14 validation. |
| RO | Not a writable message field | Any known read-only key supplied in input rejects, even null | Same | An observation, not a setter; binary unknown tags are not a general read-only detection mechanism. |
| X | Removed or unsupported | A known removed key rejects even null or 0 | Same | Separate service version and old-endpoint isolation prevent binary compatibility bypass; no blanket unknown-wire-field rejection promise. |

`members`, `items` and `empty` are semantic spellings for examples, not
reserved tags or a generated schema. An empty message for a typed fixed-width
native address is numeric zero, unlike an empty length-delimited raw host
address. The field table specifies which representation applies; no generic
`{}` ban overrides the native zero defaults. Binary has no null; a direct Go
caller's nil element is rejected before conversion, never dereferenced.

Parser discipline: reject unknown textual keys (including null), duplicate
keys including alternate snake/camel spellings, multiple selected choice
branches, null list elements, type coercions and lossy numeric conversions.
ProtoJSON admits its defined numeric strings and integral exponent forms, then
validates the decoded integer domain. YAML integer fields accept nonnegative
decimal integer scalars only, not booleans, floats or numeric strings; enum
fields accept declared names or integer values then domain-check, and booleans
accept true and false only. YAML names, F39 metadata and opaque base64 must be strings. YAML
merge keys, custom tags and alias expansion are rejected rather than adding an
unbounded expansion surface. Different lexical forms are acceptable only where
they decode to the same typed value. Multiple raw occurrences in binary
protobuf follow protobuf decoding and oneof rules; the service validates the
resulting selected value, not a fictitious history of discarded fields. The
duplicate-key and branch rejection above applies to text adapters.

Native YAML deviations are explicit: NL null lists mean empty; a native
action `{}` and null or omitted kind mean PASS as in protobuf; known numeric
enum values are accepted; native common range null endpoints remain zero. The
new adapter performs these mappings deliberately; no silent legacy CLI
fallback is allowed.

### Complete Field Presence Matrix

Codes apply to all three formats above. Validation errors are
`InvalidArgument` unless a state or resource error is expressly identified.
Full replacement applies defaults afresh, never KEEP; partial operations
contain only their own fields. Ceilings are domains and policy, not
qualification.

| ID and fields | Presence, empty, zero, defaults and range | Protobuf and C consequences |
| --- | --- | --- |
| F01 names and references | R string `[A-Za-z0-9_.-]{1,79}`; empty rejects; exact case, no trim or alias. | Length checked before 80-byte C storage. |
| F02 VS VIP, protocol, port | VIP R bare host, 4 or 16 decoded bytes; mapped IPv6 rejects. Protocol OPT: absence/null selects TCP+UDP only; explicit TCP or UDP only, explicit unspecified and unknown reject. Port independently OPT uint32 domain 0..65535; absent wildcard, 0 is port zero. | Raw bytes plus family and separate protocol/port presence; no C zero sentinel or unspecified-as-absence mapping. Binary decoding must preserve protocol presence. Identity immutable; global protocol/port-set conflict `InvalidArgument`; current-token identity change `FailedPrecondition`. |
| F03 real destination and port | Destination R native IPv4 or IPv6 bare host satisfying section 11 DestSet for every stored real, regardless of eligibility. Port OPT 0..65535; no inheritance or rewrite. Duplicate decoded keys reject. | Full family, bytes, presence and value key, not position; no unmap or fallback. |
| F04 real collection | COL; order retained; no null elements. Empty keeps the VS classified and drops at selection. Aggregate bindings at most 2097151 across all owners and unlinked VSes, plus physical and export bounds. | Explicit choice, checked count and lengths. Surviving incarnations and state preserved; a readded key is a new disabled real. |
| F05 new-real configured weight; manual weight | New or readded real D(1); explicit 0 retained; 0..16777215 for all schedulers. A surviving real ignores a submitted initial weight as a setter: the current weight is preserved and the supplied domain is still validated. Manual weight R uint32, 0 valid, same domain; missing or null rejects. | Presence-safe manual field, no clamping; survivor behavior comes from the replacement rule, not a stale snapshot overwrite. |
| F06 manual enabled; administrative, effective, health observations | Manual enabled R bool; false valid. A new real is administratively disabled. Descriptor administrative, effective and health fields are RO. Effective is administrative AND health, never a default healthy. External checker attempt_timeout_ms D(500) milliseconds, positive uint32 1..4294967295, zero rejects; operational target 100 ms. | Separate input and response types; no bool-zero absence sentinel. Checker configuration identity/revision binds the effective timeout under section 10; not a writable VS field. Health transport and freshness keep their own owner and are not an extra setter. |
| F07 scheduler | R enum WRR, HASH or WLC; no default; unspecified, unknown, RR and PURR reject; CHASH unsupported here. | Checked enum mapping; masks and flags are X; no historical aliases. |
| F08 OPS; pure_l3 | OPS D(false); UDP-only and mixed VS true retained, acting only on actual UDP; TCP-only true is InvalidArgument, false/absent valid. pure_l3 is X, including false and 0. | Typed bool; request-local rejection after recovery and before CAS in API/CLI; portless derived solely from F02 port presence. |
| F09 requested WLC power; effective power; max_weight | Requested D(0), uint32 domain 0..100. Absent/0 stores 0, effective 10; 1..100 stored/effective as supplied; >100 InvalidArgument. Requested 0 and 10 differ in equality/readback. Effective and applicability RO; max_weight/default_wlc_power X. | Validate before narrowing and CAS; no clamping or out-of-range retention. |
| F10 WLC samples, results, freshness | RO; not fields of create, replace or manual requests. One complete sample and requested 20 ms sampling/computation/retry/reconciliation cadence; start interval target 10–20 ms and full-recalculation duration <=20 ms, aiming toward 10 ms under section 8, not adoption SLO or a time-only invalidity/fallback gate; no caller default or zero setter. | Observations are separate from the token-stable descriptor; no float coercion of integer counters. |
| F11 selector geometry, N, charges | Derived and RO; 1600*N checked; N=0 is an explicit empty selector; binding charge cap 2097151; physical exhaustion is `ResourceExhausted`. | Checked multiplication and count conversion plus retained-memory accounting; no caller cell count and no 100000-real cap. |
| F12 internal WRR cursor (excluded from writable VS format) | Internal runtime state, not a descriptor/input field; no public setter or required public observation. uint64 modulo advancement and lifetime unchanged. | uint64, not index or int32; source state independent of the configuration token; this inventory row defines no wire field. |
| F13 timeouts.tcp | D(60000), uint32 0..4294967295 milliseconds, zero retained. | Optional scalar resolved to a concrete uint32 before C; widen to uint64 before checked deadline addition in milliseconds. |
| F14 timeouts.tcp_syn | D(accepted tcp), same domain; zero retained. | Presence resolved before inheritance; classification follows section 7. |
| F15 timeouts.tcp_syn_ack | D(accepted tcp_syn), same domain; zero retained. | Presence resolved before inheritance. |
| F16 timeouts.tcp_fin | D(accepted tcp), same domain; zero retained. | Presence resolved before inheritance. |
| F17 timeouts.udp | D(60000), same domain; zero retained. | No zero-to-default conversion. |
| F18 timeouts.other; timeout container | other D(60000), same domain; zero retained. The container absent, null or `{}` means all children absent and resolves the defaults above; a scalar or list container rejects. | Query and Echo never refresh; a permitted error delivery refreshes to now plus other; OPS suppresses refresh only for actual UDP; a replacement preserves old deadlines. |
| F19 source bases M4 and M6 | D(0.0.0.0) and D(::); bare host strings in YAML, raw family address bytes on the wire; an empty string or bytes when present rejects. A native v4 or v6 family is required; no prefix. Dormant values retained. | No host-bit masking. The selected-family generated-output admission of section 11 still applies. |
| F20 IPv4 source override | OPT bare native IPv4 host; a present 0.0.0.0 is structurally valid and the selected-family output admission rejects it. A prefix suffix or an empty wrapper rejects. | Presence bit plus 4 raw bytes; absent differs from zero. |
| F21 IPv6 source template | OPT wrapper; a present wrapper requires a raw IPv6 host address R and prefix D(128) in 0..128; explicit 0 retained; `{}` rejects. Non-byte prefixes accepted; host bits kept. | Raw 16 bytes plus the resolved prefix, not common network normalization. Dormant structural validity and the configured-family all-output admission are unchanged. |
| F22 policy envelope | Binding R; source, key and revision R bytes, each 1..256 decoded bytes, base64 in text; empty string or bytes rejects. Exactly one of native nonempty rules or the explicit empty choice; section 14 covers every nested native field. | Owned length-delimited bytes, checked counts, a protected immutable ACL reference and tuple equality. |
| F23 ModuleConfig bindings | COL; each member has R name and R VS incarnation bytes 1..256; duplicates reject; order retained; empty unlinks only. No VIP, protocol or range fields on a link. | One atomic all-target validation; name and incarnation are separate, not token parsing. |
| F24 mss_fix | D(false) bool; true is retained and applies to actual TCP, dormant for actual UDP, including in a mixed VS. A numeric MSS input is X. | Typed bool; no new clamp value. |
| F25 DSCP | All old packed or per-VS DSCP inputs are X even null or 0; the separate `dscp` module owns marking. | No field in the new descriptor or C config; no shared token. |
| F26 initial_session_slots; new_slots; actual_slots | Create only: D(current `l3b.policy.session_default_slots`, initially 65536); explicit 0 also means that default; the default or a positive value in 1..1073741824 rounds to the next power of two with minimum 16. Resize: R positive in the same domain; zero rejects. Actual slots RO. An initial size inside a replacement is X. | A creation envelope separate from the descriptor; the raw resize value is compared before rounding: shrink `FailedPrecondition`, equal no-op, back-blocked growth `FailedPrecondition`. |
| F27 encapsulation mode | D(IPIP) optional enum; explicit unspecified, unknown or NAT rejects; GRE is supported with the fixed minimal header; key, checksum and sequence inputs are excluded. | Presence is distinct from enum zero; checked C mode mapping. |
| F28 expected token, real target vector/incarnation, returned identities | Expected token R bytes 1..256. O09/O10 vector R nonempty, no null members; each full F03 key, incarnation R bytes 1..256 and operation value R. Duplicate normalized full keys reject even with different incarnations. Returned object incarnation and token are RO bytes 1..256; link-target incarnations use the same width. | Copied length plus bytes. Structural vector errors precede token checks. Owner absence `NotFound`; stale token `Aborted` before target lookup; then first target error in authored order: missing real `NotFound`, wrong incarnation `Aborted`. Final candidate validated and published atomically; no new vector cap. |
| F29 prepare kind and name; create token; valid_for_ms | Kind R SERVICE or MODULE_CONFIG; name per F01; create token R bytes 1..256 bound to exact kind/name/instance/owner context; valid_for_ms RO uint32 milliseconds (300000 at issuance). The 16384 permit count is per registry, not per client. | A permit is distinct from a mutation token. An absent, empty or oversized request token field is `InvalidArgument`; a well-shaped unknown, expired, consumed or context-mismatched permit is `Aborted` for an absent target. Unknown means absent from the authoritative registry, not absent from the request. An otherwise valid existing target is `AlreadyExists`; recovery or an unrepresentable server-side expiry at issuance is `Unavailable`, with no grant under section 5's order. |
| F30 GetService and GetModuleConfig request and snapshot | Request R name; cursor D(empty) bytes 0..1024. Response: incarnation and token R nonempty bytes; encoding version R positive uint32; total and offset uint64 with offset <= total; chunk bytes; complete bool; next cursor bytes 0..1024. All response fields are RO. An empty chunk is valid only at completion; an incomplete success must advance the offset. | total <= the configured export cap (initial 4194304); chunk plus envelope <= the response cap (262144); checked size_t copies. No mixed snapshot, gap, overlap or overflow. An unknown encoding rejects client assembly, never a partial replacement. |
| F31 list names request and response | No owner name; cursor D(empty); limit D(server default); 0 also means the default; a positive value at most the server maximum. Response names per F01 in ASCII lexical order; an empty page is permitted with an advancing cursor; complete if and only if the next cursor is empty. Names, membership revision, complete and next cursor obey section 6's per-RPC coherent-state rule. Membership identity, instance, epoch and position are RO and carried inside the opaque cursor, not a new revision field. | Uses the F32 bounds for lists; no caller position or revision setter; checked counts and bytes; membership invalidation only on create and delete. Bounded coherence failure is Unavailable with no usable partial page/cursor. |
| F32 ListSessions request | R VS name; cursor D(empty) bytes 0..1024, empty starts; limit D(100 initially); 0 means the default; 1..1000 initially; above the maximum rejects, no clamp. | An opaque cursor, not the old uint64 cursor. The server resolves one policy snapshot per page; an unchanged limit is not required on continuation. |
| F33 SessionRecord and response | The entire response is RO. Each row exposes the full network key: actual semantic IPv4/IPv6 family, actual TCP/UDP protocol even in a mixed VS, client and VIP addresses, and both ports absent for Portless or both present for Specific, including 0. Mandatory page VS-incarnation context disambiguates logical scope; no per-row scope duplication is required. The separately stored real key and incarnation 1..256 are required, not reconstructed from current real lookup. Decode with record plus page, without GetService or current VS state. expires_at and page_now are uint64 milliseconds in the same identified dataplane clock domain, with no UTC claim; layer identity and front or back role are required; a zero numeric time is representable. Rows may be empty on an incomplete advancing page. | Complete if and only if the next cursor is empty; uint64 clocks and lossless typed identities; no public memory address. An unsafe read is `Unavailable`, an oversized record is `ResourceExhausted`, and a failure yields no usable partial page. |
| F34 networks, ranges, transport presence | F02 independently optional VS protocol/port, F03 optional real port and mandatory actual session transport stay distinct. F02 and F03 bare hosts, F19-F21 raw sources and the section 14 native networks and ranges are deliberately different types; no blanket normalization. | The per-field matrix above applies to every field rather than copying old l3b network semantics. |
| F35 l3b.policy configuration | Not an RPC mutation surface. A missing, null or `{}` section selects defaults; each optional setting missing or null selects its default; explicit zero is invalid for positive budgets. Settings and defaults are in section 2; unknown keys reject. | Checked integer arithmetic, restart-required changes, and the all-accepted-object readability check before a reduction. The CLI sends absence or 0 only on paging and initial-size request fields, not on policy configuration. |
| F36 GetServiceStatus | Read-only observation groups; current VS identity or incarnation-scoped pending cleanup; accepted-token context distinct from runtime provenance. | Section 6's bounded live pages and configuration-bound continuation; reuse existing applicable identity/read-limit domains. Exact schema and cursor/error representation must precede implementation; no fabricated observation defaults or infinite completed history. |
| F37 GetModuleConfigStatus | RO accepted incarnation/token, target publication, authoritative worker cohort and typed adoption state under section 6. | No input default/setter, no exclusion of idle workers; codecs/layout remain downstream. |
| F38 Counter observations | RO semantic identity, accumulation identity, values and provenance in O16 VS/full-key-and-incarnation real groups and O17 module totals/reasons/Echo groups, typed available/unavailable/not-applicable. Full section 6 catalogue includes unimplemented paths; external counters stay external. | Current binding only, shared cells once; no fabricated zero or unknown child attribution, historical aggregation or continuity promise; exact codecs/layout and #3121 numeric/overflow decisions remain downstream. |
| F39 VS, real and ModuleConfig metadata | D(empty) protobuf string; ProtoJSON/YAML string, 0..256 decoded UTF-8 bytes, valid UTF-8, no NUL. Missing, textual null and empty normalize to empty on create/full replacement. Reject numeric/bool/object/list coercion; YAML `"10"` is text, 10 rejects. Preserve whitespace, case and Unicode without trim/normalization; no interpretation, uniqueness or inheritance. | Check decoded UTF-8 byte length before C; owned length-delimited data, no truncation or selected physical layout. All strings enter normalized equality after survivor preservation; real strings use owning VS CAS, ModuleConfig its own CAS. Coherent accepted readback, normal admission/publication and existing budgets apply. Partial/runtime/resize/recovery preserve strings; recreation uses newly supplied/default strings. Final wire names/tags/ABI remain downstream. |

### Byte-Page Framing, Cursor Errors And Read-Only Identities

F28 mutation tokens retain the complete-value non-reuse rule in section 5.
Response-only incarnation metadata is not an additional expected-incarnation
input for name-plus-token mutations. Opaque cursor context must actually travel
with the request and be validated; hidden current server state is not a substitute.
This selects no encoding or additional request field and changes no cursor errors.

Byte pages compute end = offset + chunk byte length with checked arithmetic
and require 0 <= offset <= end <= total. `complete` is true if and only if
end == total; the next cursor is empty if and only if complete. An incomplete
successful page has end > offset. The framing admits total = 0, offset = 0, an
empty chunk, complete = true and an empty next cursor; it does not waive the
mandatory snapshot identities or validation of the assembled typed document.
Clients reject early completion, extra bytes, noncontiguous offsets and any
inconsistent terminal flag or cursor, rather than expose a partial document.

An overlong, malformed, invalid-integrity or wrong-purpose cursor is
`InvalidArgument`. A structurally and integrity-valid continuation for another
requested owner or instance, or whose bound incarnation, owner epoch,
membership revision, accepted token, encoding version or chain generation is
no longer current for its read operation, is `Aborted`. Wrong purpose is a
request-domain error; wrong owner is a continuation-context mismatch, not
owner `NotFound`. A fresh empty-cursor read of a missing named object is
`NotFound`. Recovery or inability to establish authoritative validation state
is `Unavailable`, not fabricated invalidity or an empty successful page. These
are status dispositions, not an implemented cursor codec or a claim that
policy provenance or content hashes authenticate cursors.

For name lists, current membership validation follows section 6's protected-state
rule, not a newest-at-reply requirement. A valid stale incoming continuation is
`Aborted`; inability to complete a coherent page within bounded work is
`Unavailable` with no usable partial page/cursor. Recovery gating, malformed-cursor
`InvalidArgument` and continuation-context `Aborted` remain unchanged; these rules
add no exhaustive precedence for requests with multiple simultaneous faults.

Response-only metadata has no input default: mandatory response identities
must be present and valid, or the client rejects the response rather than
fabricate a zero identity. Response clock-domain and layer identities and the
exposed instance, epoch, chain and membership identities use the same 1..256
opaque-byte domain; no public numeric ordering or decoding of these identities
is promised. The flow protocol is TCP or UDP and its addresses retain exact
family and bytes; portless records omit both transport ports rather than
invent zero. The layer role is exactly front or back; an unknown or
unspecified role is an invalid response. These are finite semantic types, not
a shared-memory identity layout or new caller inputs. Opaque page cursors
carry their required context; internal components need no new caller fields or
registry. ProtoJSON output emits uint64 as decimal strings; YAML output emits
lossless nonnegative decimal integer scalars, not quoted numeric strings or
floating-point values, matching its integer input rules through `UINT64_MAX`.
Both formats emit bytes as canonical padded base64; input accepts ProtoJSON
base64 variants and normalizes them to
decoded bytes. YAML tokens, cursors and policy metadata use the same base64
convention. Host-address YAML uses unzoned bare IPv4 and IPv6 strings; wire
host fields carry raw 4 or 16 bytes (ProtoJSON base64), not network messages.
Raw IPv6 templates preserve every bit and render without unmapping.

## 17. Counter Identity

The counter namespace has no public collision-bucket knob. The naming constraint
is `b2/<kind>/<sha256>` over a versioned, length-prefixed canonical typed
identity containing normalized parents and the object key, with a lowercase
digest that is never truncated, suffixed or probed. This is not yet a byte-level
interoperability format: final names, the complete kind vocabulary, ordered
typed fields, encoding version, byte order and length encoding, and canonical
test vectors belong to the naming contract in #2719. They must be specified
before independently generated counter names are used together; consumers must
not invent local encodings or treat this template as a complete hash recipe.
The identity, collision and stability requirements here constrain that work.
No public field sizes,
widens, narrows or selects a hash bucket or collision table. A duplicate
identity rejects the whole candidate instead of being spread into another
bucket, and a generated name mapped to different bytes is rejected before
allocation. Unchanged identity and size retain the name.

## 18. Conformance Obligations

The following policy-boundary vectors are **NOT RUN**. Use otherwise valid input
and inject each listed fault independently; assert semantic outcomes, not final
counter tags, wire layouts or implementation-specific hash values.

### Validation And Retained-Behavior Boundaries

These vectors are **NOT RUN**; they supplement, not replace, the independent
cases below and the packet companion's embedded cases.

- F09 through binary protobuf, ProtoJSON and CLI YAML: absent/0 stores requested
  0 with effective 10; 1 and 100 store and use their supplied value. Requested
  10 and requested 0 remain different accepted values in equality/readback.
  101, 1000 and UINT32_MAX each return `InvalidArgument` without accepted-state,
  active-state or CAS effects. For replacement, repeat with current and stale
  CAS: request-local domain rejection precedes CAS, but recovery remains first
  (`Unavailable`). No clamping or out-of-range retention; other type/domain rules
  and real-weight/output-cap/WLC mathematics are unchanged.
- F08 API/CLI create and full replacement of TCP-only VS: true returns
  `InvalidArgument`, including otherwise equal candidates and stale CAS, after
  recovery and before CAS, with no accepted-state/session/CAS effects. False
  and absence remain valid. UDP-only and mixed TCP+UDP true is retained; actual
  UDP follows OPS, actual TCP stays stateful and eligible TCP MSS still applies.
  Preserve mixed identity, transport-distinct sessions and pure_l3 rejection;
  no TCP true-to-false readback, no-op or drift exception is allowed.
- Independently inject IPv4 Source Quench(type4) and Redirect(type5) into l3b
  and the external distributor, with and without a matching live session and
  with fanout ready/not-ready (including sufficient budget). With valid IP/ICMP
  integrity, each counted-drops as unsupported type at the detecting owner before
  quote parsing, association or ownership query, even if a malformed quote or
  later fault is armed. Assert zero session effects, real selection,
  encapsulation, reply, clones, fanout and token spending. A bad enclosing IPv4
  checksum wins first; with that valid, bad complete ICMP checksum wins first,
  with zero unsupported-type reason and no later processing. Controls retain
  every supported IPv4 DU/TE/Parameter Problem and IPv6 DU/PTB/TE/Parameter Problem
  behavior, including codes, quote parsing, ownership and delivery/fanout rules.
- A ModuleConfig passes module_max_bindings but its exact encoded snapshot
  exceeds config_max_export_bytes: changing create/replace rejects with
  `ResourceExhausted`, preserving prior accepted state/CAS (or absence), never
  truncating. Exactly config_max_export_bytes passes only this bound; physical
  or other admission may still reject. Repeat through accepted-state-changing
  manual/capacity operations as applicable; preserve validation, stale-token and
  validated no-op precedence, plus complete readback of every accepted export.
- On an otherwise permitted stateful operation, RST without SYN/FIN uses `tcp`,
  not immediate deletion. A live authoritative eligible pin remains counted by
  WLC until expiry, then stops; use one positive tcp timeout and its exact expiry
  boundary. Keep all six classes and every SYN/ACK/FIN/RST combination; an invalid
  live pin still cannot use RST to bypass reschedule admission.
- For admitted Echo to a linked family/VIP, a nonempty configured real set whose
  members are all disabled, zero-weight or unhealthy still permits one reply,
  subject to the existing address-role/integrity/geometry checks; all-empty stays
  Echo-inactive. At handoff TTL/Hop Limit is65. Exactly one later routing decrement
  yields64; do not assert64 for zero or multiple decrements or general egress.
  Preserve every Echo transformation and the fixed IPv4 configurability deviation.
- With surviving dataplane/shared memory, CP-only restart preserves each WRR
  cursor under the existing lifetime rules. A dataplane/shared-memory/host restart
  carries no continuity, zero-reset, selected-first-slot or persistence requirement;
  conformance must not assert such a restart position.

- O09/O10: absent, null and empty vectors, a null member, missing full key,
  incarnation or value, and out-of-domain values each yield `InvalidArgument`,
  including with a stale token. Explicit enabled=false and weight=0 are present
  values, not absence. Same decoded IPv6 key in compressed/uncompressed spelling
  twice rejects even with different incarnations. Same address with absent port,
  port0 and port65535 are distinct full keys, not duplicates.
- With a well-shaped vector and stale token, an absent first real or wrong first
  incarnation still yields `Aborted` before target resolution. With a current
  token, `[missing-key, wrong-incarnation]` yields `NotFound`; reverse the authored
  order and get `Aborted`. Put a valid changed member before either failure:
  no member, selector, token or F39 changes. Repeat with final-candidate admission,
  allocation, build and known pre-accept publication failure; never implicitly split.
- HASH/WRR N=2, both admin-enabled, weights `[150,50]`: a weight vector authored
  as second->150 then first->50 accepts final `[50,150]`, not transient sum300.
  With weights `[150,150]` and admin states `[true,false]`, enable second then
  disable first accepts final sum150, not transient300. Final sum201 rejects
  atomically even if reals are unhealthy. An all-equal valid vector preserves
  token and publication; one or several changed members advance once. Resource
  framing must be bounded downstream, not implemented as an invented target cap.
- F39 boundaries in all three scopes (VS, each real, ModuleConfig), on O02/O03/O06/O07
  as applicable, in binary protobuf, ProtoJSON and YAML: omission, textual null and
  empty normalize to empty; binary has no null. Decoded UTF-8 lengths 0, 1, 255 and
  256 accept, 257 rejects with `InvalidArgument` before CAS even with a stale token,
  under the recovery gate. Test ASCII, 128 copies of `é` (256 bytes), 64 copies of
  `𐐀` (256 bytes), and either accepted boundary plus `a` (257 bytes).
  Literal and escaped text, including `"\u00e9"` and `"\ud801\udc00"`, have the
  same decoded-byte checks. Reject invalid UTF-8, lone surrogates and embedded NUL,
  including escaped `"\u0000"`, before C; never truncate to fit.
- F39 types and exact text: YAML `"10"` is text while 10, true, `{}` and `[]`
  reject, as do numeric/bool/object/list ProtoJSON inputs; string `"null"` stays
  text, not absence. Preserve spaces, tabs, newlines, case and Unicode; `" x "`
  differs from `"x"`, and composed `"é"` differs from decomposed `"e\u0301"`.
  Numeric-, epoch-, hash- and JSON-looking strings stay uninterpreted. Equal
  strings may occur on different objects; no uniqueness or parent inheritance.
  With nonempty ModuleConfig/VS metadata, omitted child metadata still becomes empty.
- F39 equality/CAS: for each scope, equal full normalized candidate/current CAS
  no-ops without preparation, publication or token change. Change only the VS
  string, one real's string, several VS/real strings together, or the ModuleConfig
  string: normal preparation/admission/atomic publication advances the owning CAS
  once, not unrelated tokens; real strings use VS CAS. With stale CAS, equal and
  different valid metadata both return `Aborted`. With current CAS, the same
  string and changed timeout or ModuleConfig binding content may publish subject
  to other rules; arbitrary earlier/new strings and rollback to earlier valid
  content/labels are allowed, without numeric comparison or a version ledger.
- After accepting nonempty VS/real strings, manually change real weight1->3 and
  enable it. Resubmit the full descriptor with initial weight1/current CAS and
  equal metadata: preserve weight3/admin state/incarnation/pins before equality,
  and no-op when all other normalized fields agree. Submitted new real metadata
  replaces its old text without changing that preserved state. Independently omit,
  use textual null or empty for each nonempty VS/real/ModuleConfig string on full
  replacement: reset to empty, never KEEP, even for a surviving real; advance the
  owning CAS only if the full normalized candidate changes. New/readded reals
  take submitted/default strings and weight, start disabled with new incarnations,
  and never revive old pins even with identical text.
- O09/O10, equal resize and accepted growth preserve all F39 strings; equal partial
  updates preserve CAS, changed partial admin/weight updates and growth advance it
  under existing rules. Health, WLC, sessions and back retirement preserve strings
  and their existing token rules. Recovery preserves all accepted strings but
  invalidates old CAS; reread, then an otherwise valid update with current CAS may
  reuse any string. Actual authorized delete/recreate uses new object/real
  incarnations and only newly supplied/default strings, never inherited old labels;
  equal text restores no old token, identity, state or pin.
- Metadata-only changes preserve VS network and real identity, session-chain
  topology/generation, WLC input generation and retained-result applicability,
  session state/deadlines and WRR cursor. Check all three scopes without treating
  labels as worker-adoption evidence or changing rules for systemic owner epochs,
  tokens, incarnations, internal publication versions or F22 policy identity/revision.
  The owning CAS still advances on an accepted metadata change as specified above.
- O11 exports VS/real strings and O13 the ModuleConfig string, each coherently
  paired with accepted CAS through byte pages and recovery. Metadata-only changes
  abort the owning configuration-bound O11/O13/O16/O17 continuation with `Aborted`;
  no-ops do not. Name-list O05/O08 membership and O12 session traversal stay valid
  solely with respect to the label change, subject to their other invalidators.
  An accepted string never proves worker adoption; status provenance stays separate.
- F39 bytes consume existing request/export/resource budgets, including many
  individually valid 256-byte strings. Export-cap or allocation/admission failure
  returns `ResourceExhausted` without changing accepted state, metadata or CAS;
  known pre-accept preparation/publication failures likewise preserve prior state.
  Enforce existing request framing and checked encoded-byte/page limits, not a new
  configurable metadata quota. A 257-byte string remains invalid even with ample
  budgets; accepted strings round-trip completely without truncation, and lowering
  a budget must not make accepted configuration unreadable.
- Accept exactly WRR/HASH/WLC; reject RR/PURR aliases and writable F12. For HASH,
  inspect selected input components while independently varying base protocol/
  Next Header, source, destination, each specific-mode port and metadata flowLabel.
  An IPv6 base Next Header naming an extension is not replaced by terminal TCP;
  metadata flowLabel different from the inner label remains the hash input.
  In portless mode, changing either port leaves direct hash inputs unchanged.
  Do not require a changed selected real for every change (hash collisions exist).
- WRR cursor=18446744073709551615 on a coherent ring L=3 selects next0/slot0;
  failed coherent selection does not commit the increment. Links share the worker's
  cursor; replacement, switch WRR->HASH->WRR and control-plane-only restart retain
  it, not an initial-slot promise. All three modes exclude weight0; HASH weights1
  do not promise legacy RR zero-weight behavior. Existing hit/drop/no-advance
  cases and postselection-failure consumption remain required.
- WRR/HASH order vectors (**NOT RUN**): reals A, B, C, all eligible, authored
  in that order. WRR `[4,3,2]` expands to `A,A,B,A,B,C,A,B,C` (L=9); HASH with
  the same weights stays contiguous `A,A,A,A,B,B,B,C,C`. WRR `[2,2]` gives
  `A,B,A,B` (g=2, sweep `A,B` repeated twice); with g=2, `[4,2]` gives
  `A,A,B,A,A,B` (sweep `A,A,B` repeated twice). WRR `[6,4]` gives
  `A,A,B,A,B,A,A,B,A,B` (g=2, sweep `A,A,B,A,B` repeated twice, L=10),
  distinguishing threshold passes from flat `weight/g` copies, which would
  give `A,A,A,B,B,A,A,A,B,B`. With B zero-weight or
  health-ineligible, `[4,3,2]` selects over A and C only, `A,A,C,A,A,C` (L=6).
  Reorder to C, B, A gives WRR `A,B,A,C,B,A,C,B,A` and HASH
  `C,C,B,B,B,A,A,A,A`, advancing the token once and preserving incarnations,
  sessions and the cursor value; the next read is `(cursor + 1) mod 2^64 mod 9`
  in the new sequence. Each real appears exactly its weight times per L.
  Two workers, or two VSes on one worker, advance independent cursors; no
  cross-worker or cross-VS alternation is asserted.
- WRR cursor wrap vectors (**NOT RUN**): with L=9 `[4,3,2]` and committed
  cursor `18446744073709551613`, four successful selections read slots 5, 6,
  0, 1, that is `C,A,A,A`, because `2^64 mod 9 = 7`; the run of three A is
  ordinary wrap, not a violated maximum-run bound. With L=3 and cursor
  `18446744073709551614`, two successful selections both read slot 0. A
  failed coherent read at any of these positions commits nothing; a later
  packet-processing failure after a successful read does not roll back.
- At successive20 ms ticks in one WLC input generation, three invalid samples
  retain applicable state for attempts1/2 and attempt fallback at3. Inject fallback
  publication failure: no claimed activation. A later valid sample resets once;
  activation failure retries from a fresh sample on the next tick. An unknown
  activation reconciles before dependent publication, not by weights alone;
  a worker still pending after20 ms does not violate an adoption SLO.
- WLC generation vectors (**NOT RUN**): with an applicable published result,
  invalid attempts 1 and 2 retain it degraded. Then change one participating
  real's configured weight; separately, the effective power; separately, the
  participating real identity. Each starts a new input generation with streak
  0 and immediate fallback for the invalidated inputs; the next invalid attempt
  is attempt 1 of the new generation, not attempt 3, and a valid sample of the
  new generation may activate. Controls: a validated no-op or pure reorder
  between attempts 2 and 3 keeps the generation, so attempt 3 attempts fallback.
- WLC valid-sample and confirmation vectors (**NOT RUN**): after two invalid
  attempts, a complete current sample whose source counts equal the previous
  sample counts as valid and resets the streak once, with no new marker or
  numeric change. If its result equals the active output, guarded confirmation
  applies and no new publication occurs; independently break current active
  equivalence, committed continuation, applicability or authority and require
  no confirmation and no restored readiness. A candidate equal to the active
  output whose activation FAILED does not confirm it; the next tick retries
  from a fresh sample under the unchanged retry rules.
- WLC cadence vectors (**NOT RUN**): a complete current valid sample that
  takes 21 ms through sample acquisition, weights and selector-result preparation
  misses the <=20 ms duration target but counts as valid, causes no instant
  fallback and violates no validity deadline. A 15 ms start interval with a 21 ms
  full duration meets only the interval target; a 25 ms interval with a 10 ms full
  duration meets only the duration target. Worker adoption later than 20 ms is
  separate. A sample missing one worker, a missing sample and a failed attempt
  each count as invalid under the three-attempt policy instead. No measured
  performance or feasibility at any scale is asserted.
- For each of the eight address roles in section11, separately use127.0.0.1,
  127.255.255.255 and::1 at the applicable management and dataplane boundaries:
  no class-only rejection. For generated IPv6 source use B=::1,p128 to isolate
  that class test. Keep every other matrix restriction. B=::2,p120/R=0 yields::
  and rejects; each p121..128 with B=::1 has no splice and passes class admission.
  Without override, M6=::1 and R=0000ffff yields::ffff:0:1 and rejects ALL-R.
  B=2001:db8:1:2::,p64,M6 last bytes1234,client192.0.2.1 yields exactly
  2001:db8:1:2:c000:201:0:1234. These are not routing/egress-support proofs.
  Without an override, M6=2001:db8:1:2::1234 and inner IPv4 client 192.0.2.1
  yield exactly the same 2001:db8:1:2:c000:201:0:1234 (source base M6).
- Loopback Echo vectors (**NOT RUN**): Echo Requests from 127.0.0.1,
  127.255.255.255 and ::1 to an active VIP each produce one ordinary reply
  addressed to that request source and one reply-count increment, with no
  class-only rejection. Do not assert node-local delivery, network egress or
  an outside-host violation; that egress boundary remains unproved.
- Permit issued at t milliseconds expires at t+300000; create at expiry rejects when absent,
  while a create accepted before expiry remains alive afterward. Preparation
  reserves neither name nor resources. OPS is the only VS flag, not a prohibition
  on mss_fix or required enabled booleans; pure_l3 rejects and portless is derived.
  Native ACL limits (8 versus9 actions, counter127 versus128 bytes) remain native,
  not new l3b quotas. A trusted received clone never refans, even on not-owned;
  packet-asserted provenance is not trusted enforcement proof.

Counter delta vectors are **NOT RUN**. Start from known non-overflowing values and
one invocation with entry buffer length B. Each terminal fault below increments
input by(1,B), drop by(1,B), exactly its primary reason by1 and output by0 at the
detecting owner. Exercise every semicolon-separated alternative separately, with
all earlier checks passing. No assertion chooses overflow behavior (#3121).

| Injected fault | Counter boundary and exclusions |
| --- | --- |
| Truncated header; invalid declared length; missing required transport fields | Detect before dependent access; only safely established identities. |
| IPv4 MF; IPv4 nonzero offset; IPv6 M; IPv6 nonzero offset | Fragment drop before transport/VS; atomic IPv6 fragment is a non-drop control. |
| Unsupported upper protocol; live AH; ESP; No Next Header | Distinct unsupported reasons before dispatch, no VS fabricated. |
| Extension length/bound failure; ninth extension; misplaced HBH; repeated HBH; malformed TLV; unknown action01,10,11; nonzero live Routing Segments Left | Distinct parser reasons; otherwise valid action00 and Routing SL0 are non-drop controls. |
| Bad enclosing IPv4 checksum; bad ICMPv4 checksum; bad ICMPv6 message/pseudo-header checksum | Independently invalidate each check before reply/query/session/fanout. |
| Unsupported ICMP type; incomplete/malformed quote; quoted noninitial fragment; missing quote ports/required fields | No query/session effect; quote-parser accounting stays external. |
| No matching VS; empty ModuleConfig | Module-only drop, not an invented VS or real series. |
| Echo-inactive (every linked VS at the family/VIP has an empty real set); forbidden Echo VIP; forbidden Echo client | Module reason only; Echo-inactive is distinct from no matching VS; zero Echo reply count, no arbitrary VS attribution. |
| Live IPv4 LSRR/SSRR with a remaining route; malformed IPv4 source-route length/pointer | Two distinct reasons at the detecting l3b invocation before dispatch; exhausted LSRR/SSRR, Record Route and Timestamp are non-drop controls; quoted options stay structural. |
| Exhaust descriptor acquisition budget | Reachable pre-provisioned counter, no session-miss substitution or cursor advance. |
| Policy DENY; no match; explicit empty; matched empty actions; COUNT-only; runtime policy unavailable | Denial versus unavailable remain distinct; native actions do not replace terminal accounting. |
| Session read failure; coherent selector failure; no eligible real/empty selector; unauthorized invalid-live-pin reschedule | Distinguish causes, not an ordinary miss; real attribution only when safely resolved. |
| Deadline overflow; required insertion; refresh; copy-forward; rebind; suppression admission; bounded revalidation; repreparation failure | Independently fail each required step; no partial operation-owned session/suppression effect. |
| Malformed eligible MSS option/tail; required MSS buffer failure | No partial MSS mutation; valid full option space is unchanged forwarding, not a drop. |
| Encapsulation length; encapsulation headroom/buffer failure | No fallback or output increment; a preceding successful MSS mutation does not change drop bytes B. |
| Reverse delivery missing/expired session after owned query; invalid reverse pin | Delivery-owner drop without requery/reselect/refan; no invented real. |
| External absent; multiple; stale; unprovable association | Distinct external reasons before query/state/clones/tokens, never O16/O17. |
| External indeterminate ownership; unknown peers; missing needed source; peers above burst; insufficient credit | Distinct external primary reasons, no clone output/reservation on these admission failures. |
| Funded clone allocation; complete-copy; encapsulation length; headroom failure | Per-failed-attempt external counters, not additional original terminal drops; continue other funded peers without refund. |

- For a buffer containing14 L2 bytes,4 VLAN bytes and100 IP bytes, input/drop
  bytes are118, not100 and not118 plus absent FCS/preamble. After preparation
  changes length to142, required commit failure still drops118 with zero output;
  successful commit and handoff counts one output/142. Later NIC loss changes
  neither output nor committed session. Repeat without L2/VLAN in the buffer:
  do not invent their bytes. Each nested owner snapshots its own entry.
- One fully attributed ordinary drop can appear once in module, VS and real
  views, not three independent packets. Before real resolution omit real; before
  VS resolution omit both; unavailable parent uses infrastructure diagnostics.
  An unobservable group is UNAVAILABLE, not fabricated zero. O16 carries full
  real key/incarnation; O17 carries module reasons and Echo family/VIP. Apply the
  existing current-binding/shared-cell/accumulation-ID vectors to every group.
- `[COUNT,COUNT,DENY]` increments the native tag twice, one terminal drop and no
  output; `[PASS,COUNT]` does not execute COUNT. Successful ordinary/reverse handoff
  counts the known VS/real output; Echo increments one module/family/VIP reply
  and module output, never an arbitrary VS. With three funded peers, fail the
  first allocation and second encapsulation, hand off the third: two distinct
  clone-attempt failures, one clone output, no drop merely for consuming the
  original and no refunds. Valid zero peers likewise consume with no drop/output.

### Multi-Fault Counter Vectors

All vectors below are **NOT RUN**. For each original terminal drop, assert one
input/drop entry-byte increment, exactly the named primary reason, zero output
and no later reason increment or dependent processing. Prior permitted COUNT,
cursor/preparation effects are not undone. Unmentioned prerequisites pass.
Armed later failures are not permission to execute checks after the terminal fault.

| Vector | Combined faults and required attribution |
| --- | --- |
| MF01 Base before fragment | Invalid base declared length plus IPv4 MF -> base malformed/length reason only; do not trust fragment/transport fields from an invalid enclosing structure. IPv4 IHL option region exceeds available bytes while a complete remaining LSRR lies in the available prefix -> S1 region/length reason only, no individual-option scan or remaining-route reason. |
| MF02 Fragment floor | Valid base, MF plus malformed LSRR -> real-fragment only. Noninitial fragment plus missing required transport fields -> fragment only. No option/transport scan, VS lookup or later effects. |
| MF03 IPv4 wire order | Well-formed remaining LSRR before a malformed source-route option -> remaining-route only; reversed option order -> malformed-option only. Within one malformed option, no remaining-route attribution. |
| MF04 Options before protocol | Remaining LSRR plus IPv4 protocol 51 -> source-route only, before unsupported-protocol dispatch. |
| MF05 IPv6 wire order | Malformed TLV before Fragment(M=1) -> TLV only; real Fragment before the otherwise malformed later TLV -> fragment only. Within each otherwise bounded live HBH and Destination Options header, valid unknown action01/10/11 before malformed PadN -> rejecting-action only; reversed TLVs -> malformed-TLV only. Do not process later TLVs after either terminal fault. |
| MF06 IPv6 bound and tag | AH before Routing(Segments Left nonzero) -> AH only. Ninth supported extension with invalid internal length -> ninth-extension only, without reading that header. |
| MF07 Integrity before branch | Bad enclosing IPv4 and ICMP checksums plus forbidden Echo client -> enclosing-checksum only. Valid enclosing checksum plus bad ICMP checksum and malformed quote -> message-checksum only. In both families, structurally valid unsupported ICMP type plus bad complete-message checksum (IPv4 enclosing checksum valid; IPv6 pseudo-header correctly formed) -> message-checksum only, zero unsupported-type reason and no later dependent processing. |
| MF08 Echo ties | Forbidden VIP and client plus all linked real sets empty -> forbidden-VIP only; valid VIP plus forbidden client and empty sets -> forbidden-client only. No Echo-inactive/reply count. Valid roles but unclaimed VIP plus armed reply-construction failure -> no-matching-VS only, no reply preparation. |
| MF09 Acquisition before absence | Required descriptor acquisition fails while a lookup without authoritative context would appear absent -> acquisition-failure, not no-matching-VS or session miss; no cursor advance. Repeat for required Echo eligibility-authority acquisition before deciding VIP membership, with no reply. |
| MF10 Ordinary dependencies | Policy DENY plus armed session-read failure -> policy-denial only, no session read. Malformed eligible MSS plus armed encapsulation/commit failures -> MSS-malformed only, no encapsulation or session commit. |
| MF11 Encapsulation ties | Unrepresentable encapsulated length plus insufficient headroom -> length only, no headroom acquisition or commit. Repeat on reverse delivery after a valid owned query and live pin; no required refresh/copy-forward commit, requery or refanout. |
| MF12 External admission | Stale association plus armed ownership/readiness faults -> stale-association only, no query/tokens. Not-owned with unknown peers plus missing source/burst/credit faults -> unknown-peers only; known peers with the latter three -> missing-source only, no reservation. |
| MF13 Independent clones | Funded A allocation failure plus latent encapsulation fault -> allocation only. After successful allocation, complete-copy failure plus unrepresentable length -> copy only. After successful copy, unrepresentable length plus insufficient headroom -> length only. In each case B is valid and still attempted; no later A operation/reason, extra original terminal-drop reason or refund. |

The following additional bounded vectors are **NOT RUN**:

- Failed WLC activation followed by equal fresh weights still attempts activation
  on the 20 ms cadence. Two invalid samples cannot clear not-ready; the third
  attempts fallback. Unknown-outcome reconciliation distinguishes committed,
  not-committed and unknown using descriptor/builder and operation/owner/input
  evidence, not equal weights; unknown blocks dependent publication, superseded
  work cannot update newer state. Exit/re-entry, failed switch and no-op/reorder
  exercise stopped sampling, base-first zero streak and valid-sample recovery.
- Weights [10,10], counts [1,399], power10 give raw99.5 for the first real ->100.
  Scale both counts by an exact factor overflowing a chosen narrow intermediate:
  the same valid mathematical result is required. Cover below/at99.5, C<W, C=0,
  empty eligibility and actual source/domain/unavailable failures without inventing
  a count-width limit. Reordered HASH runs and the reordered WRR interleaved
  order use authored exact weights while preserving sessions, incarnations,
  cursor and reservation rejection.
- Health initialization unknown/streak0, success reset, retries/retries+1 failures,
  reconnect with same marker/continuous sequence, restored complete history,
  missing history/changed marker and stale old-marker success follow section10.
- Health threshold vectors (**NOT RUN**): with proven healthy history and
  retries=0 the first failure disables; with retries=255, streak255
  does not disable, failure256 does without wrap. Threshold256 rejects.
  Lower3->1 at streak2
  disables immediately; raise1->3 while already unhealthy at streak2 stays
  unhealthy until a new success resets0. Missing/invalid thresholds fail closed,
  not default0; threshold change without history stays unknown/unhealthy.
  A delayed success/failure from checker config revision A cannot affect B even
  with the same continuity marker. Proven reconnect/restoration keeps history;
  threshold revision does not mutate the VS token or override admin/expiry.
- Exhaust required issuance for all seven identity classes: stale plus known
  exhaustion is Aborted; validated no-op needs no new identity; an otherwise valid
  changing operation needing a fresh identity is ResourceExhausted. Lost metadata
  is Unavailable/no reset; parent reuse requires distinguishability and fencing. Kind-scoped
  name revisions do not cross-invalidate; exhausted detach stays pending/accounted
  without a third table. Delayed packet preparation crosses expiry; final attempt
  uses one fresh t for expiry/deadline, repreparation resamples; shorter/zero,
  OPS stored deadlines and reverse-ICMP restrictions remain.
- Mutation-token anti-revival (**NOT RUN**): for ModuleConfig name N, incarnation
  I1, accepted v1 and token T, delay `DeleteModuleConfig(N, T)`. Accept v2 with U;
  a v3 candidate MUST NOT issue T again in indistinguishable request context, even
  if v3's content equals v1. The delayed delete returns `Aborted` and preserves the
  existing object. Repeat across owner takeover with unchanged configuration.
- Delete N/I1, fully reclaim its state, then recreate N/I2: T MUST NOT be issued
  as a valid complete opaque token for N/I2 in that same request context. Delayed
  `DeleteModuleConfig(N, T)` is `Aborted` while N/I2 exists; genuinely absent N
  still returns `NotFound`. Repeat the protection for VS mutations. Returning I2
  in readback, without it being a mutation input, is not a distinguishing check.
- Exercise wrong-kind and wrong-instance tokens and endpoint reuse against the
  actual presented/validated request context, not a server-only domain assertion.
  Historical and current grants must remain distinguishable; local-component
  repetition is permitted only when the full context proves that distinction and
  wrong-context rejection. Fencing alone cannot make identical old input safe.
- With known issuance exhausted, a stale request is still `Aborted`; a current,
  otherwise valid changing request needing a fresh identity is `ResourceExhausted`
  at admission, without partial acceptance. A validated current-token no-op keeps
  its token and issues nothing. Unknown issuance metadata instead keeps the
  recovery gate `Unavailable`, even with an empty registry, without reset.
- Create-permit anti-revival (**NOT RUN**): after expiry, consumption, record
  reclamation, takeover or a later absence interval, a new grant cannot make an
  old presented permit valid again. Otherwise valid create while present remains
  `AlreadyExists`; absent with the old permit remains `Aborted`. Assert identities,
  state preservation and status codes, not human-readable output strings.
- O16 individual-source unavailability versus whole-authority failure, cleanup
  disappearance ->continuation Aborted then fresh NotFound, protected in-flight
  completion and traversal-complete-but-cleanup-pending. Counters read continuing
  shared cells once, exclude independent retained versions, retain accumulation ID
  only for identical continuing cells/state; reset/new cells/membership change or
  unproven continuity require new ID or unavailable. O17 includes an idle lagging
  worker in PENDING, then ALL_ADOPTED, and explicit UNAVAILABLE/NOT_APPLICABLE,
  without synchronous publication or inferred WLC/drain/latest-VS readiness.
- Counter-series vectors (**NOT RUN**): current binding A->B->A in one parent
  incarnation must not reissue A's prior accumulation ID for reset/new cells;
  proven restored same cells/state and issuance metadata retain it only under
  the iff rule. Token change, empty registry and takeover cannot reset issuance.
  Exhausted/uncertain issuance makes only the affected group UNAVAILABLE while
  forwarding and ordinary config operations continue; other groups remain usable
  unless whole authority is missing (RPC Unavailable). Shared current cells read
  once; independent retained cells stay excluded. Same-name recreated ModuleConfig
  has a distinct visible incarnation: identical Echo family/VIP and local ID must
  still produce a different full series identity, with no automatic rollover.
- YAML native /0, host default, IPv6 global prefix and bi-contiguous mask round
  trips retain family and normalized bits; canonical zero-run ties and hex tails,
  rejected extra slash/empty segment/zones/brackets/space/hostnames/ambiguous IPv4
  leading zeros, and unchanged raw F19-F21 host bits.
- External stale expected revision preserves complete state; valid empty versus
  absent/unknown peers, dedup/local exclusion, missing family source and generation
  change preserve coherent n/attempts. Replacement/restart/takeover/reattach retain
  credit; actual retirement/new incarnation starts full only after fencing;
  uncertain recovery never refills. Accepted/observed readback stays distinct.
- All four clone inner/outer families have fixed configured source, protocol4/41,
  TTL64/TC0,label0, exact header/length/checksum and unchanged inner. Whole-copy,
  headroom or length failure produces no partial/refund and continues others.
  IPv4 ID1/DF0 for both inner families retains the separately accepted risk,
  not mitigation. Recipient admission/decap/provenance/query and clock/precision/
  arithmetic/recovery proofs remain preactivation gates.

Selector admission vectors (all **NOT RUN**):

- HASH/WRR admission vectors (**NOT RUN**): accept representable configured
  weights 0 and 16777215 subject to distribution/resource admission; reject
  16777216 without narrowing. Check `[101,1]` and `[150,0]` at N=2, rejection
  after removing the zero/disabled second member when the first is admin-enabled
  even unhealthy, and exact admin-enabled weight overflow on create/replace/manual
  weight/admin-enable/membership/scheduler-entry into HASH/WRR. Disabled N=1
  weight150 may store; enabling rejects even unhealthy. Health recovery stays
  within the logical reservation; actual HASH runs and WRR orders still
  exclude unhealthy reals.
  Count disabled, unhealthy and zero-weight reservations across HASH/WRR; reject
  shared-pool exhaustion even with sparse occupancy and no implicit donation.
  Every rejection preserves accepted/active state and token. Verify zero-weight
  exclusion in every mode, HASH with weights 1, and unchanged WLC capped output and
  geometry. These cases specify admission, not placement or allocation success.

Packet disposition vectors (all **NOT RUN**):

- Live AH on TCP/UDP, Echo and error/fanout paths counted-drops before transport,
  ACL, sessions, selectors, responses, queries, clones or tokens, with mss_fix
  false/true and both rewriting/non-rewriting SYNs. Include IPv4 protocol51.
  Quoted AH instead permits structural parsing with encoded length/minimum12;
  truncated AH before required ports rejects, absent original payload alone does not.
- Live Routing headers: known/unknown types at SL0 may proceed under other checks;
  any traversed header with nonzero SL rejects before effects, even if another is0.
  Malformed bounds reject at either value. Quoted SL0/nonzero remain structural
  only, with base-address/reverse-port normalization, no segment-list execution
  or extraction. Incoming admitted ICMPv6 SL0 checksum uses current base destination.
- Echo with nondefault IPv4 options/TOS/ID/DF and IPv6 extensions/TC/flow label
  preserves fields and geometry/lengths, with only approved endpoints,TTL/Hop65,
  type/code/checksum changes. Codes1/255 yield correct full-header/message and
  pseudo-header checksums, not just a type delta. Retain id/sequence/payload,
  fragment/TLV/SL0/checksum rejects and live-AH drop; no partial reply or reply
  counter on drop. No source-route reversal, special RR/Timestamp update or
  retained-ID mitigation is inferred from these vectors.
- IPv4 source-route vectors (**NOT RUN**): on live IPv4 TCP, UDP and Echo
  Requests, separately for LSRR (131) and SSRR (137) with option length 11,
  pointers 4 and 8 are remaining routes: counted-drop with the source-route
  reason before dispatch, with no VS lookup, ACL, session, selector or cursor
  effect, Echo reply, reply count or encapsulation. Pointer 12 is exhausted:
  processing continues under the other checks, the option bytes stay unchanged
  and no source-route reason increments. Pointer 3, and a length extending
  past the IPv4 option area, count the malformed-option reason, never the
  remaining-route reason. Record Route and Timestamp options are non-drop
  controls. An ICMP error quoting an IPv4 header with a remaining LSRR/SSRR
  route is not rejected for that route and proceeds to structural extraction
  subject to other checks; a quoted malformed option keeps its structural
  quote reason.
- Trusted ingress scope cannot be self-selected by packet fields. Missing ports
  reject even for portless VS; quoted sourceVIP/source service port/protocol select
  unique VS then external service association before reverse-key port omission.
  No/multiple/stale/unprovable association consumes/count-drops before query/state/
  clones/tokens. Coherent older binding is usable; owned delivery to sameVS bypasses
  fanout readiness, not-owned uses only bound-service readiness/peers/budget.
  No postquery retargeting, recreated-name inheritance or new-VS bucket refill;
  indeterminate still drops without retry/fanout.
- For both Echo and supported errors in both families, bad ICMP checksums
  counted-drop before reply/query/session mutation/fanout. Separately cover valid
  IPv4 header checksum with bad ICMP checksum and bad IPv4 header checksum with
  valid ICMP checksum; ICMPv6 includes pseudo-header validation. Codes 1 and 255
  remain accepted only with valid checksums and all other admission checks.
- Incoming and quoted IPv6 chains within eight extensions: base-first HBH is
  permitted subject to other checks; later or repeated HBH counted-drops, with
  quote rejection before ownership query.
- IPv6 TLV vectors (**NOT RUN**): Pad1 in the last header byte, valid PadN with
  zero/nonzero length, missing PadN length and overrun length; unknown nonpadding
  options with each action `00/01/10/11`, both HBH and Destination Options.
  Live valid00 continues only after complete TLV validation; malformed00 and
  valid01/10/11 counted-drop before transport/ACL/session/selector/Echo/fanout,
  without Parameter Problem (including multicast destinations). Put malformed
  padding after a valid00 option to prove validation is not stopped early.
  In a quote, structurally valid versions of all four actions permit required-field
  extraction subject to other admission checks, without executing action bits;
  truncated TLV/header before required ports drops before ownership, no ICMP reply.
  A complete traversed chain and required ports with absent original payload does
  not fail merely for that absent body. Repeat at eight/nine extensions and retain
  HBH-position/fragment rules; ordinary TCP/UDP checksum behavior is unchanged.
- Authentication/MSS vectors (NOT RUN): valid MD5 length18 and AO length4 or
  longer before/after the first MSS, and without MSS, leave the entire segment
  unchanged at the MSS step, while normal ACL/session/encapsulation still applies.
  Wrong MD5 lengths, AO<4, truncated options and malformed tails after first MSS
  counted-drop before session commit with no partial mutation when enabled and
  eligible. Include SYN-ACK, duplicates, disabled policy and noneligible flags
  (no new MSS-specific check), and auth-shaped EOL padding (not a bypass).
- Generated-source vectors (NOT RUN): IPv4 literal224/4 rejects; unicast B/M6
  with p=0 and R starting ff proves every active p=0 rejects, dormant p=0 retains
  structural equality/round-trip; p>=1 with B starting ff rejects. Raw M6 starting
  ff with an active valid-B template using only its last two bytes is not rejected
  solely for that raw base. Cover disabled/unhealthy/zero-weight configured reals,
  VS create/replace InvalidArgument and unchanged old state/token.
- Multicast Echo vectors (NOT RUN): both multicast destination families with
  nonempty configured reals drop original without response/count/session/selection/
  encapsulation/fanout; header/admission/accounting vectors with both addresses
  passing SourceSet remain unchanged. Section 11 extends the source-class checks
  and section 12 checks both Echo legs, not just multicast VIPs.

- Address-role conformance (NOT RUN): cover every section 11 matrix row and role,
  including real eligibility independence, all-R versus raw-template admission,
  active/dormant family transitions, explicit unused external sources versus missing
  needed sources, pre-exclusion peer/local validation, and separate Echo VIP/client
  rejection with no reply or session effects. Include IPv4 base and override
  limited broadcast; bad base/valid override; B=::2,p120 with R=0 yielding ::
  rejects; B=::1,p121..128/k16/no splice passes the class check; M6=::1 without
  override can yield a forbidden mapped output. Cover loopback in all eight roles
  without a class-only rejection or emission-support claim. Active p0 rejection/
  dormant p0 retention and raw bad M6 with
  valid selected B. Exercise 192.0.2.255 under /24, /31 and /32 without a suffix or
  F21-prefix heuristic, identical fe80 bytes in different zones, 169.254/16 and
  240.0.0.1 without inventing a blanket ban or claiming contextual support.
- Enabled eligible no-MSS TCP without authentication bypass: 20-byte header plus payload grows to 24; 24-byte
  header with EOL padding grows to 28; 56 grows to 60; padded 60 stays unchanged.
  Insert MSS536 at offset20, preserving old options/payload at offsets +4 and
  checking data offset, lengths and checksums. Duplicate MSS values [1000,1460]
  stay unchanged; [1460,1460] becomes [1220,1460]. Required buffer-allocation
  failure counted-drops without partial packet mutation or session commit.

- Ordinary no-VS and empty-ModuleConfig traffic counted-drop without pass-through;
  unsupported ordinary IPv4 protocols drop while TCP/UDP, Echo and supported
  errors retain distinct dispatch. Eight supported IPv6 extensions reach the
  applicable path when otherwise valid; a ninth drops before transport/state.
- Mixed empty/nonempty VSes at one family/VIP produce one Echo reply; all-empty
  VSes produce none and counted-drop with the dedicated Echo-inactive reason,
  not the no-matching-VS reason. A disabled or health-ineligible configured
  real still permits Echo. Unlinked VSes and other ModuleConfigs do not contribute
  eligibility; a protected older coherent configuration is not forced to latest.
- Echo response counts share across TCP/UDP and ports at one module/family/VIP,
  separate across ModuleConfigs, and do not increment on no-response outcomes.
  Check source VIP, destination request source, family Echo Reply type, code zero,
  valid checksums and initial TTL/Hop Limit 65 at handoff; no session effects.
- Echo/code vectors (NOT RUN): both IPv4 and IPv6, nonzero identifier/sequence,
  empty and nonempty payloads including binary zero bytes, all preserved
  byte-for-byte. Exercise incoming codes 0, 1 and 255 for Echo Requests and each
  supported error type; admitted replies use code zero and delivered errors
  retain code/content. At the same codes, truncated lengths, invalid quotes,
  non-initial quoted fragments, genuinely fragmented incoming packets and failed
  ownership/delivery checks retain their rejection paths; unsupported types drop.
- Original consumption occurs once per distributor invocation for success, valid
  zero peers and every failure outcome. Success/zero peers add no consumption
  drop count, zero peers spend no tokens, and existing failure accounting remains.
  Inject allocation and encapsulation failures at early funded peers: every
  remaining funded peer is still attempted once, without retry/refund/rollback.
- After an owned query, remove or expire the session before delivery: counted
  drop, no query retry, refresh attempt, fanout, reselect, rebind or revival.
- Check all four inner/outer family pairs in both IPIP and GRE against the header
  matrix, including no encapsulation decrement, ID/fragment field and flow label,
  retaining fragment rejection, class/ECN inheritance and external DSCP scope.
  Do not reuse this matrix as the distributor-clone expectation.

Canonical session-key vectors (all **NOT RUN**):

- Ordinary client-to-VIP TCP/UDP and an admitted VIP-to-client quote produce the
  same key after swapping both quoted addresses and ports, using quoted transport.
- `Portless` has neither port; `Specific(0, 0)` preserves both zero ports and
  differs from absence. Vary both packet ports under one portless VS: the key
  and live pin remain equal.
- TCP versus UDP, IPv4 versus IPv6 and VS deletion/recreation stay isolated;
  two links to the same VS retain the same key and scope.
- In one mixed VS, identical addresses and ports with actual TCP versus UDP
  produce distinct logical keys, WLC counts and readback records; front/back
  copies of each key still deduplicate independently. Portless removes both
  ports but not actual transport. Shared incarnation, real set and resources do
  not merge these sessions or add a new HASH input/algorithm.
- Decode the full network key from a SessionRecord plus mandatory page
  VS-incarnation context without current VS lookup or per-row scope duplication.
  The saved real key/incarnation remains distinct, even after real recreation.
- Invalid quote VIP, protocol, family, fragment or missing required ports cannot
  bypass existing admission, including in portless mode, or reroute through a
  different VS to fabricate a key. Mismatched externally constructed typed keys
  and records retain validation. Echo and unsupported paths have no session
  effects. Rejection before successful session commit leaves no partial session
  mutation by this operation, subject to normal expiry and independent concurrent
  operations; loss after successful commit does not roll it back.

Service, CLI and direct-client tests must instantiate every matrix row with
omitted, null where expressible, explicit empty, explicit zero or false,
minimum, maximum, just-outside-domain and wrong-type forms. Binary absence is
not textual null. Include an unselected choice branch, empty members, null
elements and RO or X keys with null or zero, plus two equal decoded spellings
and genuinely distinct values.

Required discriminating vectors include:

- Optional VS protocol (**NOT RUN**): independently combine absent/null, TCP
  and UDP with port absent, 0, 443 and 65535 through binary presence, ProtoJSON
  and YAML. Absence/null means TCP+UDP only; explicit unspecified, unknown, empty
  string and wrong types reject. At the same family/VIP, TCP:443 plus UDP:443
  accepts; mixed :443 plus either rejects the whole candidate. Mixed portless
  conflicts with every TCP/UDP port selector; TCP portless plus UDP:443 accepts.
  Repeat across different owners and unlinked VSes; different family/VIP does
  not conflict. Omitted versus null protocol normalizes equal, but changing
  mixed to TCP at a current token is `FailedPrecondition`; stale token is `Aborted`.
  With OPS and mss_fix true, mixed UDP uses OPS and no MSS, mixed TCP remains
  stateful with eligible MSS processing; TCP-only OPS=true rejects rather than
  reading back false. ICMP quotes
  select the unique VS by actual quoted transport/port before portless key
  projection; quoted TCP refreshes other even with mixed OPS, quoted UDP does not.
- C06 batch parity (**NOT RUN**): through both the CLI and a direct client,
  an O09 and an O10 two-target vector with a valid changed first target and a
  missing-key second target at the current token each yield `NotFound` as one
  request, apply neither target and keep the token; a valid two-target change
  is one request, one publication and one token advance. Both paths return
  the same status and readback and never split a vector into sequential
  single-target calls or report partial success.
- Name-page coherence for both list operations (**NOT RUN**): protect r1 `[a,b,c]`,
  copy `[a]` with a continuation position after `a`, then delete `a` to form
  r2 `[b,c]`. Returning `[a]`, `complete=false` and an r1 cursor is allowed;
  attaching an r2 cursor to that old page/position is forbidden. A following
  request with the r1 cursor after the deletion is `Aborted`.
- Fresh empty-cursor listing may restart within bounded work against r2 and
  return a wholly r2 page/cursor; a continuation must not reuse its r1 position
  in r2. A matching continuation protected/validated before the deletion may
  finish a coherent r1 page. Failure to complete a coherent page within budget
  yields `Unavailable` with no usable partial page/cursor, not automatic failure
  merely because membership changed (**NOT RUN**).
- Content-only changes leave name-list continuation valid. Check completion and
  cursor against the same protected membership state: `complete` iff next cursor
  is empty, including terminal empty inventory and incomplete empty advancing
  pages. Reject mixed-state completion/cursor pairs (**NOT RUN**).
- Permit context: vary kind, name, instance and owner context independently;
  a well-shaped mismatch with an absent target is `Aborted`, an otherwise valid
  existing target is `AlreadyExists`, malformed input is `InvalidArgument`, and
  recovery is `Unavailable`.
- A supplied token of 1..256 bytes absent from the authoritative permit registry:
  absent target yields `Aborted`, otherwise valid existing target yields
  `AlreadyExists`, and recovery yields `Unavailable`. Contrast absent, empty
  and 257-byte request token fields, which yield `InvalidArgument` after recovery.
- WLC pure reorder and reorder with applicable weight changes at unchanged
  membership use `Update` without another `Adjust`, not `Make`; F04 token and
  publication effects remain, without asserting identical placement.
- Creation with omitted and zero size under `session_default_slots=1000` accepts
  1024 slots; after a policy change to 2048, new creations resolve 2048 while
  existing capacity stays 1024. Resize zero still rejects.
- In each of front and back, expiry before or exactly at `page_now` is omitted;
  expiry after it remains eligible for physical inspection, including duplicate
  copies. Expired-only scan work consumes budget and advances an incomplete
  empty page rather than returning expired rows or stalling.
- Changed normalized content under the same policy tuple rejects against the
  current binding and a retained shared revision the candidate would reuse.
  A no-longer-retained, non-reused revision requires no historical lookup;
  caller fixtures still preserve tuple immutability, not redefine it after
  reclamation or require a permanent ledger.
- A valid WLC sample following invalid samples resets that generation's streak
  once; subsequent activation or confirmation failure does not charge an invalid
  sample, reset the streak again, advance freshness, or alter committed
  builder/prior active state. Repeated failures and intervening input changes
  retain applicability checks without asserting bounded staleness or a new
  retry/fallback/readiness rule. These restoration vectors are **NOT RUN**.
- Six timeouts: container absent, null and `{}`; tcp absent versus 0, 1, 100,
  500, 60000 and 4294967295 ms; defaults for tcp/udp/other are 60000 ms;
  SYN and SYN-ACK inheritance through an explicit 0; 4294967296 rejects;
  all six classes; every SYN/ACK/FIN/RST combination under section 7.
  With uint64 millisecond now=4294967295 and ttl=1, expiry=4294967296;
  widen before addition rather than wrapping at uint32. With now=UINT64_MAX-1,
  ttl=1 reaches UINT64_MAX; ttl=2 fails without changing prior state.
  At zero timeout expiry equals the final-attempt now and is immediately expired;
  readback uses uint64 milliseconds with the same expiry predicate.
- Permit millisecond boundaries (**NOT RUN**): valid_for_ms=300000 at issuance;
  create at t+299999 may accept, at t+300000 or later rejects with `Aborted`
  for an absent target. A present otherwise-valid target retains `AlreadyExists`.
  After existing validation and absent-target determination, PrepareCreate at
  t=UINT64_MAX-299999 cannot represent t+300000: return `Unavailable` with no grant,
  no wrap or saturation, not `InvalidArgument` for malformed caller input;
  preparation never extends expiry and accepted creation is not rolled back.
- VS and real ports absent versus 0 versus 65535; duplicate decoded keys and
  reordering; immutable identity; surviving manual state; removed and readded
  identity.
- IPv4 override absent versus 0.0.0.0; raw IPv6 host bits with prefixes 0, 1,
  80, 81, 127 and 128; dormant-family validation and selected-family
  generated-output rejection.
- Native rules missing, null and `[]` versus the explicit empty policy;
  `actions: []` versus `[{}]`; COUNT-only; COUNT then PASS versus PASS then
  COUNT; duplicate COUNT; an unsupported trailing action; 8 versus 9 actions;
  unknown kind; zero PASS.
- Each family-pair presence combination; the all-network-empty catch-all
  versus the no-network port-constrained rejection; contiguous IPv4 and
  bi-contiguous IPv6 mask boundaries; explicit zero address and mask versus
  missing nested messages.
- Empty protocol with full ports versus constrained ports; TCP flags; UDP
  subtype zero versus nonzero; independent source and destination addresses
  and ports with source-port omission preserved as wildcard; a `{}` range
  0..0; reversed and 65536 endpoints. With empty protocols, compare port lists
  [[0,65535],[1,2]] and [[1,2],[0,65535]]: native path selection differs
  despite equal port unions.
- Required unique native counter tags including UTF-8 byte length 127 and 128,
  NUL and duplicates; the empty policy's private adapter identity cannot
  collide with a user rule.
- Metadata decoded byte lengths 0, 1, 256 and 257 for each component; base64
  variants; tuple-separator ambiguity; the same identity/revision with changed
  normalized content; current-token full-result equality and stale-token
  equality; differing metadata with equal packet behavior. Readback carries
  the complete accepted binding.
- Read cursors empty, maximum, overlong, malformed, wrong purpose, wrong owner
  and wrong epoch; bounded sparse advancing pages; an oversized single record;
  rejected partial export; unknown encoding; uint64 precision; byte-page
  contiguity and the empty terminal page. Assert each cursor status of
  section 16 plus early and late completion, an inconsistent next cursor,
  checked end arithmetic and total = 0 framing independently.
- Every operation's missing target, duplicate, stale token, no-op, link race,
  cancellation or unknown outcome and cleanup obligation, and whole-candidate
  preservation on parse, compile, allocation, counter and publication failure.
- Mutation-owner recovery with unchanged configuration still invalidates old
  VS and ModuleConfig tokens without recreating session identity; a CLI,
  operator or Gateway-only restart does not. Recovery gating precedes absence
  and token checks; clients reread after recovery.
- Create permit reuse after each known pre-accept rejection, without extending
  its expiry; invalidation during preparation and at acceptance; uncertain
  outcomes must not trigger automatic replay.
- Health missing at creation, stale checker or stream, reconnect, gap and
  recovery: no implicit healthy state, complete snapshot before deltas,
  administrative-disable precedence and publication-before-announcement order.
- Checker timeout (**NOT RUN**): omission/null resolves to 500 ms; explicit
  1, 100, 500 and 4294967295 ms retain their values; zero, 4294967296 and wrong
  types reject. The 100 ms target changes neither default nor accepted range.
  Bind effective timeout to checker identity/revision; missing/invalid effective
  snapshot timeout fails closed and old-revision observations cannot enable after
  a timeout change. Verify checked uint64 millisecond attempt deadlines without
  overflow, separately from retries, polling, heartbeat and lease deadlines;
  this contract does not supply those remaining integration values.
- Counter identity duplicates and collisions reject without repair; the final
  naming contract must supply byte-level golden vectors before interoperability
  testing, rather than deriving them from an implementation-local encoding.
- WLC empty eligibility, zero total sessions, `C < W`, `C = W`, half-rounding
  boundaries, the `[10,10]` / `[0,30]` example, output capping and checked
  arithmetic failure; missing workers or reals invalidate the entire sample.
- Logical WLC deduplication across front/back and workers; exact family and
  portless identities; expired, ineligible and removed/readded real incarnations
  excluded. A newer front suppresses a still-live back after front expiry,
  reclamation and slot reuse, consistently for lookup, ownership query and
  delivery; physical inspection may still show the back. Failed front or
  suppression-metadata admission preserves prior authority without partial
  suppression; bounded accounting and observation-safe retirement are verified.

- Copy-forward for every timeout class and permitted ICMP other; OPS stored
  deadline; query/inspection no write; expired back; shorter/zero front deadline;
  unchanged back/watermark; arithmetic/admission/write failures with no partial
  authority change. Never max(old,new) or revival of a suppressed back.
- Back retirement: `now < retire_not_before` is ineligible; equality is eligible
  to begin retirement, including `now == retire_not_before == UINT64_MAX` for a
  representable deadline. `UINT64_MAX` is an abstract domain bound, not a
  real-clock prediction or a clock-wrap policy. Equality neither implies immediate
  detachment/free/growth nor bypasses reader rights, RCU grace, physical accounting
  until release, growth rejection while a back physically exists, or the one-back/
  no-third-table rule. Terminal deletion still waits for reader rights, not expiry.
- Capacity/refill vectors (NOT RUN): invalid whole-config burst<n preserves prior
  state. Independently exercise runtime n>burst: immediate not-ready counted-drop,
  no clones/reservation despite elapsed time. Only configuration or peer-group
  change permitting full funding exits; no implicit growth, accepted-invalid
  external update or corruption-only assumption. Contrast n<=burst with temporary
  credit shortage, where refill can fund n; policy/peers/local/budget stay coherent.
- Clone bucket millisecond refill (**NOT RUN**): from zero credit, rate=3 clones/second
  and burst=2, elapsed 100 ms yields 0.3 clone; three such intervals yield 0.9,
  the fourth 1.2, allowing one reservation and retaining 0.2. A 1000 ms interval
  caps credit at 2 and discards excess; the next reservation cannot recover it.
  Long idle: elapsed_ms=4294967296 (>UINT32_MAX) from zero credit at rate=1 and
  burst=2 must refill to 2; a uint32-truncated interval of 0 is forbidden.
  At uint32 maximum rate and burst with a very large representable uint64 elapsed
  interval, reach the cap without overflowing rate times elapsed; no float or refund.
  Clock regression/error handling remains a preactivation proof obligation.
- Clone bucket fractional refill/cap, shared-worker contention, new-state full
  versus surviving control-plane restart/takeover, old-rate update accrual and
  carry/clamp, unchanged policy and peer churn, zero/exact/insufficient funding,
  peers above burst, and no refund after reservation failures. Numeric/clock
  bounds and error handling must be specified before activation.
- Selected selector algorithms/ties/vectors before implementation; creation and
  membership versus unchanged-membership Update, empty selector and failure
  preservation, no silent full rebuild. These are algorithm-conformance vectors,
  not portable-placement guarantees; explicit recovery rebuilding is preserved.

- Concurrent session authority (**NOT RUN**): two workers miss the same key and
  prepare for different full real keys; after one commits a live eligible pin,
  the other adopts it as a hit and reprepares rather than overwriting it. Repeat
  with equal full real keys but different real incarnations, adopting only a pin
  eligible under the packet's applicable protected descriptor. A mismatching
  prepared target must never reach handoff.
- Protected-descriptor conflict (**NOT RUN**): the competing winner is valid only
  under newly published D2 but invalid under the packet's still-protected D1.
  Apply the existing invalid-pin reschedule gate or counted-drop under D1; do not
  force D2 adoption or treat D2-only eligibility as an eligible D1 hit.
- Concurrent same-pin refresh (**NOT RUN**): two refreshes use different timeout
  classes or different protected timeout configurations, with time advancing
  between preparation and commit. Require valid serially authorized checked
  deadline writes under each applicable context, not stale record restoration,
  max(old,new), monotonic session deadlines or a newly invented clock policy.
- Zero-timeout competing front (**NOT RUN**): a competing front commit suppresses
  the back with zero timeout and is expired when the loser revalidates. Establish
  a genuine miss and apply ordinary stateful insertion or OPS stateless selection;
  never resurrect the suppressed back even if its physical deadline remains live.
- Changed refresh (**NOT RUN**): re-read a concurrently changed record without
  reinstating the old real/record. A deadline-only change is not rebind; verify
  a valid serially ordered checked refresh, including shorter and zero timeouts,
  without max(old,new) or a monotonic per-session deadline rule.
- Back/front conflict (**NOT RUN**): front appears during prepared back copy;
  use current front and matching preparation, never republish suppressed back,
  including after front expiry/reuse. Front commit and suppression stay atomic.
- Reschedule/expiry/OPS (**NOT RUN**): another operation rebinds first; adopt its
  eligible pin. For a currently invalid live pin, UDP and TCP SYN without RST
  may reschedule, while non-SYN TCP and SYN+RST counted-drop. Expiry before commit
  instead establishes genuine miss with fresh selection and ordinary insertion,
  including non-SYN TCP; OPS miss is stateless. OPS front live eligible hit/miss
  performs no write, but back migration/invalid rebind requires a stored-deadline
  write. Reverse ICMP still cannot reselect, rebind or create; query stays read-only.
- Failure/admission (**NOT RUN**): exhaust bounded work, fail repreparation or a
  required write, and inject final-target mismatch. Counted-drop without partial
  operation-owned state/suppression, allowing normal expiry/concurrent changes;
  postcommit loss does not roll back. A full bucket of unrelated live sessions
  cannot admit a new key through eviction, even a reported-success eviction;
  safely reclaimable expired storage and authorized same-key replacement are
  separate cases, not stateless failure fallback.
- Empty demotion (**NOT RUN**): prove never-populated with no crossing writer
  able to commit, and begin retirement eligibility without a deadline wait.
  Contrast a formerly populated now-empty table retaining its historical maximum,
  and a writer crossing demotion that prevents the never-populated proof unless
  resolved without a committed record/deadline. Neither eligibility nor empty
  observation permits immediate growth, detach/free, a third table or bypass of
  reader rights/RCU/accounting; cleanup may remain delayed. No numeric empty
  sentinel, demotion timestamp or clock-wrap behavior is asserted.
- Late session commit: MSS/encapsulation preparation failures leave session
  state/deadlines unchanged; required commit failure drops the prepared packet
  without partial authority change; postcommit transmission loss does not roll
  back. Include existing-pin refresh, new insertion, copy-forward and zero TTL.
- ModuleConfig binding cap: under the default 16384 policy, two 10000-binding objects pass this cap alone;
  a 16385-binding candidate exceeds that default per-object cap; retained old
  versions remain physically charged without duplicating the logical charge.
- Each mutation success pair identifies its own accepted result, including
  concurrent later writers; no-op preserves it, deletions are empty, and the
  client still checks expected tokens on subsequent mutations.
- Unsupported ICMP reaching l3b counted-drops without session/real/encap/error
  distribution work; routing/ND/local paths before l3b remain distinct.
- Status reads: config versus runtime separation, configuration-change cursor
  invalidation versus runtime-only changes, per-page provenance without a
  cross-page snapshot, and deleted-incarnation cleanup versus name recreation.

- OPS publication entry/exit preserves sessions and does not itself rewrite
  deadlines, including failed publication and protected in-flight old versions.
  Reverse-session delivery never inherits ordinary OPS rebinding; invalid pins
  on that delivery path still counted-drop without reselection.
  Front/back invalid-pin rebinding preserves the same logical session and exact
  absolute deadline for disabled, absent and reincarnated reals; back suppression
  survives front expiry/reuse. Exercise expiry between lookup and commit, fresh
  stateless reselection, concurrent pin changes and bounded incoherence, and
  final-real/prepared-encapsulation agreement after reselection. Preparation or
  required-write failure and no eligible real leave session/suppression unchanged
  by this operation; no resurrection, partial commit or failure fallback.

All obligations above are specification obligations, **NOT RUN**.

## 19. Downstream Boundaries

- [#2725](https://github.com/yanet-platform/yanet2/issues/2725) owns the
  concrete publisher, session-chain, recovery and lifetime proof of section 9.
- [#2726](https://github.com/yanet-platform/yanet2/issues/2726) owns the
  implementation with the mandatory tests of section 18.
- [#2727](https://github.com/yanet-platform/yanet2/issues/2727) owns the
  independent evidence audit.
- [#2719](https://github.com/yanet-platform/yanet2/issues/2719) owns final
  public naming; [#2904](https://github.com/yanet-platform/yanet2/issues/2904)
  owns the deferred standalone CHASH decision.
- Safe internal ACL invocation, compiler ownership, activation and lifetime
  proof, the exact protobuf package, tags, generated types, C widths, layout
  and attach migration, and the client inventory and migration remain open
  downstream work. Health transport and freshness ownership and scale
  qualification keep their own scopes. Performance work must establish its
  protocol and measured baseline before numeric qualification thresholds receive
  later accountable-owner approval. The 10000 VS / 100000 real values remain
  unqualified planning scale. Existing run permission and accountable owners are
  retained; this sequence neither authorizes execution nor qualifies performance.
  NAT rejection is fixed by F27 and
  section 12, not an open mode-selection decision.
- Nothing in this document is an implementation, an ABI or compatibility
  freeze, a capacity qualification or a production-readiness claim.
