# l3b Representation and Operation Contract

Normative contract for [#2718](https://github.com/yanet-platform/yanet2/issues/2718).
This specifies target behavior, not implemented behavior or executed evidence.

## 1. Scope And Boundaries

- There are six session timeout classes: `tcp`, `tcp_syn`, `tcp_syn_ack`,
  `tcp_fin`, `udp` and `other`.
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

## 2. Configuration And Observation Inventory (F01-F36)

Each field carries its omitted/present/zero/empty/default/range semantics.
Section 16 adds the per-field binary protobuf, ProtoJSON and CLI YAML
representation and the protobuf/C consequences.

| ID | Field | Selected semantics |
| --- | --- | --- |
| F01 | Names and references | Exact case-sensitive `[A-Za-z0-9_.-]{1,79}`; required, empty rejects; no omission, control byte, normalization, trimming or first-character restriction; no automatic aliases. |
| F02 | VS VIP, protocol, destination port | Exact decoded address, transport protocol and optional destination port; absent port is a wildcard within the protocol, explicit zero is port zero, range 0..65535. Protocol is TCP or UDP; unspecified and unknown values reject. Immutable inside a live VS incarnation; an identity change is `FailedPrecondition`, a conflicting candidate is `InvalidArgument`. Mapped IPv6 endpoints in a concrete VIP field reject. |
| F03 | Real destination, family, port | Key is decoded family, address bytes and optional real port inside the VS incarnation; the optional real port domain is 0..65535; absent, zero and other ports stay distinct; no inheritance or implied rewrite; exact duplicate keys reject; mapped IPv6 endpoints in a concrete real field reject. |
| F04 | Real collection and continuity | Explicit nonempty members or explicit empty. Surviving keys keep accepted weights, administrative state, incarnations and pins; removed and readded keys are new and administratively disabled. Authored order is accepted state: a pure reorder advances the token and republishes while preserving surviving state. An explicit empty real collection leaves the VS still classified: traffic directed to it drops at selection rather than falling through to another rule or VS. |
| F05 | Configured and manual weight | Stored configured-weight domain 0..16777215 for RR, WRR, WLC and PURR; a new or readded real defaults to 1 on omission. Manual weight is an optional uint32 field with mandatory presence: explicit 0 is valid, omission is `InvalidArgument`. No clamping. |
| F06 | Administrative and health state | New and readded reals are administratively disabled; effective enablement is administrative enablement AND health eligibility; health never overrides an administrative disable. Manual enabled is an optional bool with mandatory presence; false is valid. |
| F07 | Scheduler | Create and full replacement select RR, WRR, WLC or PURR explicitly; absent, unspecified and unknown values reject; partial real mutations do not resubmit the scheduler. There are no public hash masks, index masks or scheduler flags that redefine the selected scheduler. Standalone CHASH is deferred to #2904. |
| F08 | OPS and removed `pure_l3` | OPS is a separate boolean, omitted equals false. UDP supports all four schedulers; live hits are used without deadline refresh and misses select statelessly, creating no session. For TCP, omitted, false and true normalize to false in accepted state. The redundant `pure_l3` switch is removed; portless mode is derived from destination-port absence (F02). |
| F09 | WLC power | Requested power is a retained uint32: omission or explicit 0 stores 0, 1..100 is effective as given, and any other uint32 value is retained verbatim with effective power 10. There is no public `max_weight`; the selector-parameter ceiling is fixed at 100 and is not an input-weight limit. |
| F10 | WLC inputs, samples and confirmation | Inputs are the original positive weights of administratively enabled, health-eligible reals; one complete all-worker sample; one-second cadence. Within one input generation, the first two consecutive invalid attempts retain the last applicable successfully published result with degraded freshness; the third and subsequent invalid attempts trigger capped-base fallback publication attempts and WLC not-ready. Without an applicable result, fallback applies immediately; a valid sample resets the streak once. Requested WLC power is writable configuration under F09; derived effective power, WLC results, active-state and freshness observations are read-only, never independent caller setters. |
| F11 | WLC geometry and resource admission | Logical lookup length 1600*N over all configured unique real bindings including disabled and zero-weight members; an explicit empty selector at N=0. The aggregate binding charge is at most 2097151 per actual dataplane-instance registry across all four schedulers and agent owners, including unlinked VSes; physical admission is separate; all arithmetic is checked before narrowing or allocation. The 10000-VS and 100000-real values are planning inputs, not API caps. |
| F12 | PURR cursor | Unsigned 64-bit; increments modulo 2^64 before a successful nonempty selection, then modulo the ring length. Per worker and VS incarnation, shared across links, preserved across ordinary replacement and a control-plane restart. Wrap is ordinary arithmetic and may break strict cyclic order when the length does not divide 2^64. Internal runtime state: no public cursor setter and no 32-bit narrowing. |
| F13 | `session_timeouts.tcp` | Optional uint32 seconds, domain 0..4294967295; omission defaults 60; explicit 0 retained. |
| F14 | `session_timeouts.tcp_syn` | Optional uint32 seconds; omission inherits the accepted TCP value; explicit 0 retained. SYN without ACK selects this class regardless of FIN or RST, only on an otherwise permitted session operation; the separate rescheduling condition stays SYN with RST clear. |
| F15 | `session_timeouts.tcp_syn_ack` | Optional uint32 seconds; omission inherits SYN; explicit 0 retained. |
| F16 | `session_timeouts.tcp_fin` | Optional uint32 seconds; omission inherits TCP; explicit 0 retained. |
| F17 | `session_timeouts.udp` | Optional uint32 seconds; omission 60; explicit 0 retained. |
| F18 | `session_timeouts.other` and container | Optional uint32 seconds; omission 60; explicit 0 retained. A permitted ICMP-error refresh uses now plus `other`; Echo, query and OPS never refresh. All classes expire at deadline <= now, and a configuration replacement does not rewrite old deadlines. The timeout container absent, null or `{}` means all children absent and resolves each default above; a scalar or list container rejects. |
| F19 | VS source bases M4 and M6 | VS-owned; omission materializes 0.0.0.0 and ::, with no interface or link fallback; supplied dormant values are retained; full replacement omission resets them. A partial real update preserves the materialized bases. Structural validation applies even to dormant families. |
| F20 | Optional IPv4 source override | Absent differs from explicit 0; a native bare IPv4 host address with no prefix metadata; the literal override or M4 supplies the outer source. A present zero override is structurally valid and fails output admission for a configured IPv4 outer family. |
| F21 | Optional IPv6 source template | Absent differs from present; present requires a raw address, omitted prefix means 128, prefix domain 0..128 including non-byte lengths, host bits preserved, the byte-splice algorithm of section 11, and rejection of any generated unspecified or mapped output for the configured family. Raw template and prefix presence, not a normalized network. No per-real override and no new blacklist. |
| F22 | Service policy binding | Required on VS create and full replacement: source-scoped identity (source namespace plus policy key), immutable revision and complete native ACL content, or an explicit empty-policy choice with the same metadata. Missing or null binding, identity, revision, or content without the explicit empty choice rejects as `InvalidArgument`; omission never means KEEP or allow-all. Explicit empty is deny-all. Partial real state/weight operations preserve the binding. Full semantics in sections 13-16. |
| F23 | Destination bindings | Nonempty ordered exact VS name and incarnation bindings, or explicit empty that unlinks without deleting the VS; duplicates reject; order affects accepted equality; the VIP, protocol and optional port come from the immutable VS identity; broad or contradictory selectors reject. Separate ICMP paths are not ordinary TCP/UDP bindings. |
| F24 | `mss_fix` | Typed boolean, default false, dormant for UDP. For a TCP SYN without RST, including SYN-ACK, an existing MSS above 1220 is clamped to 1220 and a smaller value including 0 is preserved; 536 is inserted when the option is absent and four option bytes fit; a valid full header without room stays unchanged. Options are validated before mutation, EOL stops parsing, the MSS option length is 4, and a malformed enabled-policy packet or a missing buffer counted-drops. The constants 1220 and 536 are not path-MTU guarantees. No public numeric MSS knob. |
| F25 | DSCP ownership | Per-VS DSCP policy is outside the l3b API. l3b inherits the inner traffic class and ECN into the outer header; the `dscp` module after l3b owns optional marking with its own configuration and readback, no shared VS token and no atomic two-module update. Destination-prefix marking cannot distinguish two VSes sharing a real, so this is pipeline and real-prefix scope, not per-VS equivalence. Marking must preserve ECN in both families and update the IPv4 checksum. Old packed or per-VS DSCP inputs reject even as null or 0. |
| F26 | Session size | Omitted or zero creation size resolves the current `l3b.policy.session_default_slots` (initially 65536) once at creation; that default or explicit positive input 1..1073741824 rounds upward to a power of two with a minimum actual size of 16 (1 becomes 16, 1000 becomes 1024); the size is shared across the workers of one VS in one instance. Resize omission or zero is `InvalidArgument`; a raw target below the accepted size is `FailedPrecondition` before rounding; an exactly equal target is a no-op; only a larger target rounds. A later default change never resizes a live VS, and no resource-driven downsize exists. Bounds are not allocation or flow qualification. |
| F27 | Encapsulation and NAT | Typed VS mode IPIP or GRE with IPIP as default; unknown or explicitly unspecified values reject. Minimal GRE v0 with a four-byte header and no optional key, checksum or sequence. The existing IPIP and GRE helpers are reused with no new header builders and no required separate pipeline hop; MSS precedes encapsulation. NAT is outside the reviewed mode set and is rejected rather than mapped to GRE. |
| F28 | Mutation token, incarnation, real target | The VS token covers configuration, manual administrative and manual weight changes, deletion and accepted capacity growth; ModuleConfig has its own token; health, WLC, session traffic and automatic back retirement do not advance it. A required expected token is 1..256 bytes; a real target is the full F03 key plus a required incarnation of 1..256 bytes; a missing or malformed shape is `InvalidArgument`. A stale token precedes target lookup, an absent key is `NotFound`, a mismatched incarnation is `Aborted`. |
| F29 | Create preparation | `PrepareCreate` takes a kind of SERVICE or MODULE_CONFIG and an exact name, observes absence, and returns an opaque token of 1..256 bytes bound to exact kind/name/instance/owner context with a 300-second lifetime measured on authoritative server monotonic time; at most 16384 retained permit records per actual instance registry across both kinds and all issuers. It is not an upsert, not a name or resource reservation, not payload-bound and not exactly-once. A well-shaped unknown, expired, consumed or context-mismatched permit is `Aborted` for an absent target; an otherwise valid existing target is `AlreadyExists`. An absent, empty or oversized request token field or another malformed required field is `InvalidArgument`; an unknown registry entry is not an absent request field. Recovery is `Unavailable`. |
| F30 | Accepted configuration readback | A coherent snapshot with the object incarnation, accepted token, encoding version, total bytes, offset, chunk, completion and next cursor. The snapshot separates the replacement descriptor from read-only metadata such as the actual accepted capacity, administrative state and real incarnations. WLC result and provenance, health and drain observations stay outside the token-stable bytes. One token and encoding version identify stable bytes; map-like ordering is fixed by the encoding rules, not by a library's deterministic mode. Configuration and token are read from one coherent protected state, coordinated with the final acceptance section and the owner epoch, so a new configuration is never paired with an old token. |
| F31 | Name-list responses | ASCII lexical inventory with a membership-revision, instance, epoch and position cursor; names, revision, completion and next cursor come from one coherent protected membership state per RPC (section 6), not necessarily newest at reply. Create and delete invalidate subsequent continuation requests; content changes alone do not. Bounded output and work, and authoritative recovery gating; an empty response is not recovery success. |
| F32 | `ListSessions` request | VS name plus limit and opaque cursor; omitted or 0 limit uses the centralized default (initially 100), a positive value up to the maximum (initially 1000) is accepted, a larger value is `InvalidArgument` rather than clamped. An empty cursor starts; an opaque continuation binds purpose, instance, VS incarnation, owner epoch, observed chain generation, finite initial layer watermarks and position. No cursor registry, TTL or between-RPC pin. A changed record limit shapes the next page, not the identity of the query, so it does not invalidate an in-flight continuation. |
| F33 | `SessionRecord` and `ListSessions` response | Coherent physical records with the flow key, the stored real key and incarnation, the expiry and the layer identity and role; a portless session reports an absent port rather than a fabricated 0; the page reports the VS and chain identity and one page clock reading used for both the expiry predicate and the response. Cross-page duplicates are allowed and no unique-flow or snapshot guarantee is given; `complete` means the captured traversal end, and the next cursor is empty exactly when complete. A successful sparse page may return zero rows and still advance. |
| F34 | Network, range and transport presence | Decoded family identity, optional port and source rules, and explicit-empty top-level collections. F02/F03 bare hosts, F19-F21 raw sources and the native ACL networks and ranges of section 14 are deliberately different types; generic network normalization must not erase raw source-template bits. Section 16 carries the per-field binary, ProtoJSON and YAML null, empty and missing matrix. |
| F35 | Centralized operational policy | One authoritative location, the `l3b.policy` section of the control-plane configuration, for constants and suitable configurable limits; the CLI uses server defaults and lower layers receive the resolved budget. The initial read limits of 100 default records, 1000 maximum records, 4096 scan steps, a 256 KiB response and a 1024-byte cursor are provisional; the reviewed 4 MiB export cap and 16384 module-binding cap are unqualified. Policy changes are initially restart-required, hard API domains are not adjustable upward, no cap is bypassed automatically, ACL-owned limits are not duplicated in l3b, and lowering a limit must not make accepted configuration unreadable. |
| F36 | Service runtime status | Separate read-only `GetServiceStatus` observations: VS identity/configuration context, per-real health and published effective enablement/selector weight, WLC mode/freshness/applicability/provenance, scoped readiness and drain/cleanup phases. Bounded live pages, not an accepted export or cross-page snapshot; accepted-config changes abort continuation, runtime-only changes do not. Pending deleted-VS cleanup is addressed by incarnation, not a reused name; no infinite completed-history promise. Section 6 defines the surface. |

F35 configurable domains: `session_default_slots` is 1..1073741824 resolved
with F26 rounding; `list_default_records`, `list_max_records`,
`list_max_scan_steps`, `read_max_response_bytes`, `config_max_export_bytes`
and `module_max_bindings` are positive uint32 with defaults 100, 1000, 4096,
262144, 4194304 and 16384 respectively, and `session_default_slots` defaults
to 65536. Default records must not exceed max records; page framing and at
least a usable chunk or record must fit; incompatible reductions reject
activation. Cursor 1024, permit 256/300/16384, policy metadata 256, weight and
other hard domains cannot be raised through this section. Native ACL limits
are not configurable l3b quota copies. Rule and collection resource failure is
`ResourceExhausted` even below a representation ceiling; there is no
workload-fit guarantee.

`module_max_bindings` limits the complete candidate list of one ModuleConfig at
create or replacement, not the sum across an instance. Two ModuleConfigs with
10000 bindings each satisfy this cap alone. Retained older versions do not
consume this logical cap again, but remain physically accounted until reclaimed.
Other admission limits still apply; passing this object-size bound does not
guarantee that resources exist for publication.

## 3. Operation Inventory (O01-O16)

One public l3b service exposes sixteen operations; there is no second
session-table service. The names are contract inventory, not a protobuf
package, tag or C ABI freeze.

| ID | Operation | Selected semantics |
| --- | --- | --- |
| O01 | `PrepareCreate` | Explicit absence preparation, not a `Get` side effect; a distinct bounded grant, no name or resource reservation, no payload binding. An existing target is `AlreadyExists` without a new grant; fixed expiry and invalidation of all permits from the preceding absence interval apply. A lost response may retain a record; no deduplication and no invisible extension. |
| O02 | `CreateService` | Full configuration including the required F22 policy binding plus a create token, absent target only; an existing valid request is `AlreadyExists` even when equal; duplicates, families and limits are validated before acceptance; new reals are administratively disabled. The first mutation token and incarnation are returned; there is no upsert. A known pre-accept failure preserves state; an uncertain outcome uses readback, not retry. |
| O03 | `UpdateService` | Full replacement with the expected VS token; absent is `NotFound`; a stale token is `Aborted` even when equal; a normalized equality succeeds without publication or token change. Surviving real state, session deadlines and accepted capacity are preserved, and the VS network identity is immutable. The F22 boundary requires the complete policy binding on replacement, rejects changed content under the same identity/revision against the current binding or a retained shared revision the candidate would reuse, and treats changed identity or revision as a configuration change. The writable descriptor excludes resize-only and other read-only metadata. |
| O04 | `DeleteService` | The current VS token; absent is `NotFound`; accepted incoming links are `FailedPrecondition` with no cascade. Accepted deletion automatically owns session cleanup; old configurations and readers delay reclamation, and there is no operator state-delete command. An accepted deletion accepts no resurrection of the deleted VS through a delayed create or link, and a VS recreated under the same name receives fresh session state: old affinity is not resurrected by name reuse. |
| O05 | `ListServices` | Bounded authoritative lexical inventory with per-RPC membership coherence under section 6, membership-bound continuation, and `Unavailable` during recovery or bounded coherence failure; no usable partial page on failure and no permit or token issuance side effect. |
| O06 | `CreateModuleConfig` | A distinct create with the complete explicit members or empty binding collection and a create token, no upsert; every link is validated by target incarnation and any invalid link rejects the whole operation. No internal common-policy field. |
| O07 | `UpdateModuleConfig` | Full replacement with its own expected token; absent is `NotFound`, an equal state is a no-op, a stale token is `Aborted`. Unlink before VS delete; a delete-first link is `NotFound` or `Aborted` after recreation. Ordered exact bindings replace arbitrary destination rules; no internal common policy. |
| O08 | `ListModuleConfigs` | The same bounded lexical membership inventory and recovery semantics as `ListServices`; it is not a substitute for the full ModuleConfig snapshot. |
| O09 | `UpdateRealServerState` | A partial administrative mutation under the VS token that preserves other fields and the materialized VS source bases, and is not effective health control. Enabled presence is explicit, the target is the full key plus incarnation, the token is required, and the `NotFound`, `Aborted` and validation precedence is fixed. |
| O10 | `UpdateRealServerWeight` | A partial current configured-weight change under the VS token, preserved across a surviving-real replacement and a no-op when equal; a partial real update preserves the materialized VS source bases. Weight presence is explicit, the domain covers all four schedulers, the target is the full key plus incarnation, and no clamping applies. |
| O11 | `GetService` | A full coherent accepted snapshot and token using the byte-page contract; a fresh absent object is `NotFound`, a stale continuation is `Aborted`, and recovery is `Unavailable`. No permit allocation and no mixed accepted and runtime snapshot. A returned token may still be stale after the read, so the conditional write remains required. |
| O12 | `ListSessions` | Bounded physical-record inspection with no refresh and no logical-dedup promise, under the selected page, identity, clock and continuation rules. |
| O13 | `GetModuleConfig` | The full accepted typed snapshot including ordered identity-bound links and its own token, under the same versioned byte-page rules as `GetService`. The state must be recoverable rather than reconstructed from an empty local map. |
| O14 | `ResizeServiceSessions` | Name, the existing VS token and a required positive size. After domain, existence and token checks, a raw target below the accepted size is `FailedPrecondition` before rounding, an exactly equal target is a no-op even with a back, growth while a back exists is rejected, and a larger target without a back proceeds to preparation and admission. A stale token is `Aborted` before target checks. Accepted growth advances the VS token; traffic and back retirement do not. One recoverable capacity, chain, token and export outcome; no second independent acceptance and no public state token. |
| O15 | `DeleteModuleConfig` | Name and expected token under the existing-object ordering; absent is `NotFound` and any accepted incoming reference, including a detached function, is `FailedPrecondition`. No cascade and an empty success response; accepted deletion removes outgoing VS links while old readers delay physical reclamation. A late cleanup failure does not undo the accepted deletion. |
| O16 | `GetServiceStatus` | Read-only bounded live status, separate from O11's immutable accepted export. Observe the current VS or pending cleanup identified by VS incarnation, with the F36 groups and section 6 consistency rules. Runtime changes neither advance the mutation token nor invalidate a traversal bound to unchanged accepted configuration. |

Successful O02/O06 creation returns the created object's incarnation and first
mutation token. O03/O07 replacement returns that object's incarnation/token for
the accepted version; O09/O10/O14 returns the owning VS incarnation/token.
A normalized no-op returns the same pair without a new version. O04/O15 deletion
returns empty success, not a new token. The pair identifies this operation's
accepted result, not an arbitrary latest version read while sending the reply.
Another writer may already have made the token stale; conditional mutations still
validate it. Full configuration and actual session capacity use existing readback,
not a mandatory full snapshot in every mutation response.

For all existing-object mutations the order is: management recovery gate,
request-local structural and domain validation, owning-object lookup,
expected-token check, target and state-dependent preconditions, normalized
equality, resource admission, acceptance. Local text syntax errors can precede
a connection and are not proof that the server passed recovery.
Identity/revision conflict with retained state is a precondition comparison
after the expected token, not a reason to report `InvalidArgument` instead of
`Aborted` for a stale request. Compiler grammar errors, allocation exhaustion
and authoritative unavailability stay distinct. A no-op never waives
validation. Create, prepare and read continuations retain their separate
orders.

## 4. Cross-Cutting Obligations (C01-C08)

| ID | Obligation |
| --- | --- |
| C01 Presence and parsing | Optional text null or omission maps to absence before the field default; a required null or absent value rejects; a required boolean or weight retains false or 0; collections are explicit; read-only metadata is rejected as a writable field. Binary protobuf uses presence and oneof, not textual null. A full replacement omission is not KEEP. Section 16 carries the field-by-field matrix. |
| C02 Identity and equality | Exact names, decoded addresses, optional ports, incarnations, authored real order, ModuleConfig binding order, the raw requested power and accepted no-op equality agree across clients and C. The F22 equality compares source-scoped identity, revision and the native normalized accepted content including order and counter-relevant identity; same identity/revision with changed content rejects against the current binding or a retained shared revision the candidate would reuse, and changed identity or revision changes configuration even for equal behavior. Stale-token precedence survives, and semantic equality is not inferred from serialization bytes. |
| C03 Errors and uncertain outcomes | The existing-object order of section 3 with `InvalidArgument`, `NotFound`, `AlreadyExists`, `Aborted`, `FailedPrecondition`, `ResourceExhausted` and `Unavailable` kept distinct, and separate create, prepare and read rules. A timeout or lost reply is not non-application; there is no auto-renewal, deduplication or exactly-once promise. |
| C04 Deadline and publication boundary | Admission and preparation are cancellable; the final section is bounded with no mid-commit rollback; success need not await worker adoption or reclamation. |
| C05 Protobuf and C boundary | A separate versioned incompatible service and package keeps the product, module and CLI identity; there is no silent old-server fallback and no old mutation bypass. Removed fields and tags are reserved, the creation-envelope size is not the old VirtualService tag 6, and real and session identities and shared-memory changes need explicit ABI treatment. The common filter types (`Device`, `VlanRange`, `Fragment`) stay imported common definitions, never redeclared as l3b predicates. Exact package and tags, generated types, C widths, layout and attach migration stay downstream; no freeze, implicit legacy compatibility, guessed shared-memory layout, rolling upgrade or transparent downgrade promise. |
| C06 CLI and direct-client parity | Both use the authoritative permits, tokens, names, timeout and power semantics and the centralized defaults; a complete document or an error is returned, never a partial replacement. An external DSCP conversion must not mask invalid input or merge conflicting shared-real policies. |
| C07 Readback and query scope | Accepted, intended or effective, and active observations stay distinct; output and scan work are bounded; record lifetime is preserved. Administrative readiness, WLC freshness and worker convergence are different. There is no requirement to persist confirmation history or expose every internal version. |
| C08 Acceptance evidence | The service, CLI and conversion boundary cases and the failure, identity and readback examples of section 18 are required obligations, **NOT RUN**; unrelated executions do not verify this contract. Downstream work remains subject to section 19. |

## 5. Management Surface, Permits And Error Order

- Creation is prepared, not implied: `PrepareCreate` observes absence under the
  mutation ordering and issues a grant of 300 seconds on authoritative server
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
- The 300-second lifetime starts at the coordinated issuance decision. A permit
  is invalid at `now >= expiry` on authoritative server monotonic time. Expiry
  after acceptance does not roll back an accepted create.
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
- Cancellation and a deadline do not prove that a change was not accepted.
  There is no automatic create or update replay with a fresh token after an
  uncertain result, and no automatic permit renewal. An uncertain, cancelled
  or failed outcome never discards shared memory automatically and never
  bypasses a cap.
- An accepted deletion has one irreversible outcome. A lost response or a late
  cleanup failure does not roll it back, and memory of old versions stays
  accounted until it is really released.

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
- A configuration, manual or capacity change aborts the continuation with
  `Aborted`; a no-op, a WLC publication or a health publication does not.
  After an owner restart the old cursor is invalid.
- A client accepts only a contiguous sequence of one identity, token, encoding
  and total length, and the CLI returns the complete document or an error. A
  client may impose a stricter local memory limit, but it reports a local
  failure rather than a truncated configuration.
- The export is prepared and accounted before the change is accepted and is
  not a second authoritative copy. Each page RPC protects its memory until the
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
retained pending work, not an infinite ledger of completed deletions. Exact wire
schema, cursor encoding, observation/error representation and protected retained
cleanup lookup must be specified before implementation; no new retention TTL,
cross-page pin or completed-history guarantee is selected by this surface.

## 7. Session Ownership, Capacity And Inspection

- For a permitted stateful insert or refresh, TCP timeout classification uses
  the following precedence. It does not admit a packet or authorize
  rescheduling: in particular SYN+RST does not reschedule a live invalid pin.

  | TCP flags | Timeout class |
  | --- | --- |
  | SYN and ACK, regardless of FIN/RST | `tcp_syn_ack` |
  | SYN without ACK, regardless of FIN/RST | `tcp_syn` |
  | No SYN, FIN present, regardless of ACK/RST | `tcp_fin` |
  | No SYN and no FIN, including RST alone | `tcp` |

  UDP uses `udp`; a permitted other-protocol refresh uses `other`. Duration
  conversion and deadline addition use checked uint64 arithmetic. If
  `now > UINT64_MAX - ttl`, a required insert or refresh leaves the prior
  record unchanged and counted-drops the packet, with no wrap, saturation or
  infinite lifetime. OPS and read-only queries do not refresh deadlines or
  require the corresponding deadline-addition check; lookup still treats
  `deadline <= now` as expired.
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
  `now < retire_not_before` does not. Eligibility is not immediate detachment,
  free or growth: detach first, wait for reader observation safety (an RCU grace
  period), and only then free. Retained storage stays physically accounted until release,
  and growth remains rejected while a back physically exists. Fenced takeover may
  finish reclamation; its absence may delay reclamation, not invalidate
  sessions or expose a third table. Terminal VS deletion still waits for old
  reader rights but does not wait for session expiry as its cleanup criterion.
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
  uses one clock reading in the session deadline domain for both the expiry
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

## 8. WLC Contract

- Go computes WLC once per second from one complete all-worker active-session
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
  contains no WLC formula. A missing required real, a read failure or a
  checked sum, product or calculation overflow invalidates the whole sample;
  no partial calculation is published.
- The eligible set is the reals that are administratively enabled,
  health-eligible and configured with a positive weight, each contributing its
  original configured weight rather than a pre-clamped value. An empty
  eligible set yields an empty selector; it does not divide by zero and does
  not independently clear sessions.
- Each `c_i` counts logically live authoritative sessions, not physical records.
  Count a logical key once across front/back copies and worker observations,
  within its VS incarnation, preserving address family and the portless rule
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
  result; the invalid-sample count is scoped to the input generation, a valid
  sample breaks the sequence, and publication failures are tracked separately
  and neither increment nor reset that count.
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
  guarantee or additional non-WLC algorithm is implied.
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
  for a stale result. Sessions, deadlines, incarnations and surviving PURR
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
- The builder continuation state lives in the control-plane mutation owner's
  process heap, while the published dataplane state stays in surviving shared
  memory. The dataplane never depends on a pointer into the control-plane heap
  or into Go memory. Disposable freshness bookkeeping may live in the heap,
  but accepted-state and publication-provenance recovery cannot rely on it
  exclusively.
- Budgets: the logical lookup length is 1600*N over all configured unique real
  bindings including disabled and zero-weight members, with an explicit empty
  selector at N=0; the aggregate binding charge is at most 2097151 per actual
  dataplane-instance registry across all four schedulers and all agent owners,
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
  selector. This distinction selects no retry cadence, new threshold, mandatory
  fallback on activation failure or additional O16 readiness representation.

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
  route-announcement lease expires no later than health eligibility. Exact
  checker, heartbeat and lease deadlines, transport ownership and integration
  tests are mandatory health-integration prerequisites, not defaults supplied
  by this representation contract. They must be fixed before enabling that
  integration; administrative, health, WLC and serving readiness remain distinct.
- The public scheduler set is RR, WRR, WLC and PURR. There are no public
  user-controlled hash masks, index masks or scheduler flags that can redefine
  the selected scheduler, and the implementation derives its representation
  from the accepted mode. Required PURR counter state must be available before
  publication; a missing counter rejects the candidate and never silently
  switches PURR to hash scheduling.
- RR always supplies selection weight 1 for an eligible real, even when its
  stored configured weight is 0, and RR shares the WRR ring builder. WRR, WLC
  and PURR exclude a configured weight of 0 from new selection without
  disabling the real or killing a valid pin. Configured weights [0,3]
  therefore produce RR selection weights [1,1] and WRR or PURR weights [0,3];
  changing the scheduler does not change the stored values.
- OPS is orthogonal to the scheduler and UDP-scoped. Entering OPS retains live
  session hits without refreshing their deadlines, and misses select
  statelessly and create no session; leaving OPS atomically restores refresh
  on live hits and creation on misses. For a TCP service, omitted, false and
  true are equivalent and normalize to false in accepted state, so a TCP
  service with `true` is a no-op under a current token and the readback
  returns false. Clients compare normalized intent with accepted
  configuration, so a TCP true on input and false on readback is not drift.
  WLC observes session load, not new stateless-packet load.
  OPS belongs to VS configuration, not to a packet or session setting; entry
  and exit take effect through VS configuration publication. Switching alone
  neither deletes sessions nor rewrites their deadlines. Each packet uses its
  applicable coherent protected VS version under the existing publisher rules;
  this does not require every in-flight packet to adopt the newest configuration.
  OPS invalid-pin rebinding follows the rule below, without deadline refresh.
  OPS transitions do not relax disabled-real or invalid-pin dispositions.
- The PURR cursor belongs to each worker and VS pair, is shared by links to
  the same VS on that worker, and advances once after a successful coherent
  read of the selected slot from a nonempty ring. It does not advance for a
  live pinned hit, a policy rejection, an empty ring, a failed coherent read,
  Echo or a separate ICMP delivery, and a later readiness or packet-processing
  failure does not roll back the consumed position. It survives replacement, a
  selector rebuild, switching away from PURR and back, and a control-plane
  only restart; delete followed by create establishes new cursor state. It is
  internal runtime state, not a user-settable field, and advancement does not
  change the configuration token. Given a coherent nonempty ring of length `L`,
  compute `next = (cursor + 1) mod 2^64` and read `slot = next mod L` without
  narrowing the cursor. Commit `cursor = next` only if selection succeeds;
  computing `next` is not itself an advancement. The initial cursor position and
  first slot have no public guarantee. Wrap is ordinary arithmetic, including
  a discontinuity in cyclic slot order when `L` does not divide `2^64`.
- The VS identity is the destination address, transport protocol and optional
  destination port. An absent port is a wildcard within the protocol, an
  explicit zero matches port zero, duplicates reject, and for one address and
  protocol a portless and a port-specific VS cannot coexist. Distinct explicit
  ports may coexist. Conflict admission covers all accepted VSes in the actual
  dataplane instance, including unlinked VSes and different module/agent owners;
  a conflicting candidate is `InvalidArgument`, with no same-identity staging
  clone. The redundant
  `pure_l3` switch is removed and the portless mode is derived from
  destination port absence. In portless mode the session identity excludes
  both transport ports, so admitted packets from one client address differing
  only in source and destination port share the same live pin; the ports are
  not direct selection-hash inputs, and the exact hash encoding and label
  provenance are not frozen.
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
  address when present, otherwise the IPv4 base, and neither the inner source
  nor an IPv4 prefix changes it. Without an IPv6 override the IPv6 base is
  copied and bytes 8..12 are replaced by R, where R is the inner IPv4 source
  bytes for an inner IPv4 packet and the bytewise exclusive-or of the inner
  IPv6 source bytes 8..12 and 12..16 for an inner IPv6 packet. With an IPv6
  override the raw address is copied without normalizing it, then with prefix
  p and k = ceil(p/8), n = min(4, 16-k), the first n bytes of R replace bytes
  k..k+n, and for p <= 80 the last two bytes of the IPv6 base replace bytes
  14..16. The prefix domain is 0..128 including non-byte lengths, and no
  canonical network or byte alignment is required.
- The IPv4 override is represented as an optional native IPv4 host address
  with no prefix in accepted state, readback, equality or the source policy,
  and an address-valued text input must be a bare address without a CIDR
  suffix.
- Generated-output admission is part of normal validation, not of enable or
  traffic. For each outer family present among configured reals, including
  disabled, zero-weight and health-ineligible reals, the selected source
  branch is validated before publication: the IPv4 output must not be exactly
  0.0.0.0, and the IPv6 output must not be able to equal :: or belong to the
  mapped range for any derivation input. Structurally valid dormant-family
  inputs are stored but their output check is skipped while that family has no
  configured real. A failure rejects the complete candidate of one VS create
  or replacement with `InvalidArgument` and preserves the accepted and active
  state and its token; it does not rewrite a source, unmap it, choose an
  interface fallback or add a per-packet guard.

## 12. Packet Surfaces

- Bounded validation and real-fragment rejection precede dispatch. Ordinary
  TCP/UDP traffic alone follows VS lookup, bound service-policy admission,
  session resolution and eligible-real selection, MSS processing and
  encapsulation, then the required atomic session insert/refresh/copy-forward
  commit before handoff. Packet-preparation failure creates no session and
  extends no deadline. A failed required commit counted-drops the prepared packet
  without stateless fallback or partial authority/suppression changes. After a
  successful commit, later transmission loss does not roll the session back.
  Lookup/selection alone is not a session commit; the same late-commit boundary
  applies to permitted reverse-session delivery refresh. Echo and ICMP errors use the separate paths below,
  not ordinary TCP/UDP VS or service-policy admission.
- An active VIP with at least one configured real answers IPv4/v6 Echo
  Requests without session lookup or refresh. Supported ICMPv4 errors are
  Destination Unreachable, Time Exceeded, Redirect, Source Quench and Parameter
  Problem; supported ICMPv6 errors are Destination Unreachable, Packet Too Big,
  Time Exceeded and Parameter Problem. The bounded quote identifies the VIP as
  quoted source and exposes TCP/UDP ports. Quoted first fragments are usable
  when ports exist; quoted non-initial fragments counted-drop; IPv6 atomic
  fragments are ordinary. These quote rules do not admit fragmented incoming
  error packets through the real-fragment drop floor.
- An ICMP type outside the supported Echo Request and error paths, if delivered
  to l3b, counted-drops with a distinct unsupported-type reason. It invokes no
  session operation, real selection, encapsulation or error-distribution path.
  Traffic needing routing, Neighbor Discovery or local delivery must be directed
  to those paths before l3b. This is not a router-wide ICMP prohibition and does
  not alter supported-type, malformed-packet or fragment handling. Exact counter
  naming is part of the separate naming contract.
- Raw ICMP errors enter the separate error-distribution module for bounded
  parsing and offending-flow normalization. Its read-only l3b ownership query
  protects one coherent session-chain generation and returns only `owned`,
  `not-owned` or `indeterminate`, never real identity or table memory.
  `owned` permits a separate handoff of the unchanged error to l3b for
  reverse-session delivery to its enabled real; it is not a permanent delivery
  guarantee across expiry or state changes. Disabled, removed or reincarnated
  real hits counted-drop without rescheduling. Permitted delivery refreshes
  to now plus `other`, unless OPS suppresses refresh; it creates no session,
  selects no new real, rebinds no affinity and revives no expired record.
  Queries never mutate sessions. Query/delivery API placement, execution
  context, packet ownership and protected generation lifetime require
  downstream integration proof, not access to shared tables by the caller.
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
  integral capacity in clones. Refill uses elapsed time in one monotonic time
  domain for the shared bucket, retains fractional credit without floating
  point and caps total credit at burst; excess credit is discarded, not banked.
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
- The MSS policy is the typed boolean of F24 with the behavior recorded there.
  Option parsing stops at EOL, skips NOP, checks the other lengths, and the
  MSS option has length 4; the first valid MSS is used. A malformed
  enabled-policy packet, or failure to obtain packet-buffer space for an
  otherwise required MSS modification, is a counted drop with no partial
  modification. Insufficient logical TCP option space for the four-byte
  insertion is not such a failure: a valid header remains unchanged by the MSS
  step. A disabled policy adds no MSS-specific rejection. Lengths and the necessary checksums are
  corrected. The constants 1220 and 536 are not path-MTU guarantees.
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
  section 11, and the library builds the headers, checks the headroom and
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
- IPv6 processing walks at most eight bounds-checked Hop-by-Hop, Routing,
  Destination Options, Fragment and AH headers. AH is structurally skipped
  using its encoded length, requires at least 12 bytes and is not authenticated.
  ESP, No Next Header and unsupported upper protocols counted-drop. An atomic
  IPv6 fragment (offset zero, M clear) follows ordinary processing. Exact-port
  VS and ACL port predicates require an available transport header; missing
  fields fail closed. These transport admission checks do not precede Echo or
  the separate ICMP-error path; no second error parser is introduced in l3b.

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
accept true and false only. YAML names and opaque base64 must be strings. YAML
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
| F02 VS VIP, protocol, port | VIP R bare host, 4 or 16 decoded bytes; mapped IPv6 rejects. Protocol R TCP or UDP; unspecified and unknown reject. Port OPT uint32 domain 0..65535; absent wildcard, 0 is port zero. | Raw bytes plus family and a port-presence bit; no C zero sentinel. Identity immutable; conflict `InvalidArgument`; current-token identity change `FailedPrecondition`. |
| F03 real destination and port | Destination R native IPv4 or IPv6 bare host; mapped IPv6 rejects. Port OPT 0..65535; no inheritance or rewrite. Duplicate decoded keys reject. | Full family, bytes, presence and value key, not position. |
| F04 real collection | COL; order retained; no null elements. Empty keeps the VS classified and drops at selection. Aggregate bindings at most 2097151 across all owners and unlinked VSes, plus physical and export bounds. | Explicit choice, checked count and lengths. Surviving incarnations and state preserved; a readded key is a new disabled real. |
| F05 new-real configured weight; manual weight | New or readded real D(1); explicit 0 retained; 0..16777215 for all schedulers. A surviving real ignores a submitted initial weight as a setter: the current weight is preserved and the supplied domain is still validated. Manual weight R uint32, 0 valid, same domain; missing or null rejects. | Presence-safe manual field, no clamping; survivor behavior comes from the replacement rule, not a stale snapshot overwrite. |
| F06 manual enabled; administrative, effective, health observations | Manual enabled R bool; false valid. A new real is administratively disabled. Descriptor administrative, effective and health fields are RO. Effective is administrative AND health, never a default healthy. | Separate input and response types; no bool-zero absence sentinel. Health transport and freshness keep their own owner and are not an extra setter. |
| F07 scheduler | R enum RR, WRR, WLC or PURR; no default; unspecified and unknown reject; CHASH unsupported here. | Checked enum mapping; masks and flags are X. |
| F08 OPS; pure_l3 | OPS D(false); UDP true retained; all TCP values normalize to false. pure_l3 is X, including false and 0. | Typed bool; portless derived solely from F02 presence. |
| F09 requested WLC power; effective power; max_weight | Requested D(0), uint32 0..4294967295 retained. Effective 1..100 when requested is in range, otherwise 10; effective and applicability are RO. max_weight and default_wlc_power are X. | uint32 without premature narrowing; no upper-100 input clamp. |
| F10 WLC samples, results, freshness | RO; not fields of create, replace or manual requests. One complete sample and the inherited cadence and fallback; no caller default or zero setter. | Observations are separate from the token-stable descriptor; no float coercion of integer counters. |
| F11 selector geometry, N, charges | Derived and RO; 1600*N checked; N=0 is an explicit empty selector; binding charge cap 2097151; physical exhaustion is `ResourceExhausted`. | Checked multiplication and count conversion plus retained-memory accounting; no caller cell count and no 100000-real cap. |
| F12 PURR cursor | Internal and RO; no public setter; uint64 modulo advancement and lifetime unchanged. | uint64, not index or int32; source state independent of the configuration token. |
| F13 timeouts.tcp | D(60), uint32 0..4294967295 seconds, zero retained. | Optional scalar resolved to a concrete uint32 before C; checked deadline arithmetic. |
| F14 timeouts.tcp_syn | D(accepted tcp), same domain; zero retained. | Presence resolved before inheritance; classification follows section 7. |
| F15 timeouts.tcp_syn_ack | D(accepted tcp_syn), same domain; zero retained. | Presence resolved before inheritance. |
| F16 timeouts.tcp_fin | D(accepted tcp), same domain; zero retained. | Presence resolved before inheritance. |
| F17 timeouts.udp | D(60), same domain; zero retained. | No zero-to-default conversion. |
| F18 timeouts.other; timeout container | other D(60), same domain; zero retained. The container absent, null or `{}` means all children absent and resolves the defaults above; a scalar or list container rejects. | Query and Echo never refresh; a permitted error delivery refreshes to now plus other; OPS suppresses refresh; a replacement preserves old deadlines. |
| F19 source bases M4 and M6 | D(0.0.0.0) and D(::); bare host strings in YAML, raw family address bytes on the wire; an empty string or bytes when present rejects. A native v4 or v6 family is required; no prefix. Dormant values retained. | No host-bit masking. The selected-family generated-output admission of section 11 still applies. |
| F20 IPv4 source override | OPT bare native IPv4 host; a present 0.0.0.0 is structurally valid and the selected-family output admission rejects it. A prefix suffix or an empty wrapper rejects. | Presence bit plus 4 raw bytes; absent differs from zero. |
| F21 IPv6 source template | OPT wrapper; a present wrapper requires a raw IPv6 host address R and prefix D(128) in 0..128; explicit 0 retained; `{}` rejects. Non-byte prefixes accepted; host bits kept. | Raw 16 bytes plus the resolved prefix, not common network normalization. Dormant structural validity and the configured-family all-output admission are unchanged. |
| F22 policy envelope | Binding R; source, key and revision R bytes, each 1..256 decoded bytes, base64 in text; empty string or bytes rejects. Exactly one of native nonempty rules or the explicit empty choice; section 14 covers every nested native field. | Owned length-delimited bytes, checked counts, a protected immutable ACL reference and tuple equality. |
| F23 ModuleConfig bindings | COL; each member has R name and R VS incarnation bytes 1..256; duplicates reject; order retained; empty unlinks only. No VIP, protocol or range fields on a link. | One atomic all-target validation; name and incarnation are separate, not token parsing. |
| F24 mss_fix | D(false) bool; true is retained and dormant for UDP. A numeric MSS input is X. | Typed bool; no new clamp value. |
| F25 DSCP | All old packed or per-VS DSCP inputs are X even null or 0; the separate `dscp` module owns marking. | No field in the new descriptor or C config; no shared token. |
| F26 initial_session_slots; new_slots; actual_slots | Create only: D(current `l3b.policy.session_default_slots`, initially 65536); explicit 0 also means that default; the default or a positive value in 1..1073741824 rounds to the next power of two with minimum 16. Resize: R positive in the same domain; zero rejects. Actual slots RO. An initial size inside a replacement is X. | A creation envelope separate from the descriptor; the raw resize value is compared before rounding: shrink `FailedPrecondition`, equal no-op, back-blocked growth `FailedPrecondition`. |
| F27 encapsulation mode | D(IPIP) optional enum; explicit unspecified, unknown or NAT rejects; GRE is supported with the fixed minimal header; key, checksum and sequence inputs are excluded. | Presence is distinct from enum zero; checked C mode mapping. |
| F28 expected token, real target incarnation, returned identities | Expected token R bytes 1..256; real incarnation R bytes 1..256 with the full F03 key. Returned object incarnation and token are RO bytes 1..256; link-target incarnations use the same width. | Copied length plus bytes. Owner absence `NotFound`; a stale token `Aborted` before target lookup; a missing real `NotFound`; a wrong incarnation `Aborted`. |
| F29 prepare kind and name; create token; valid_for_seconds | Kind R SERVICE or MODULE_CONFIG; name per F01; create token R bytes 1..256 bound to exact kind/name/instance/owner context; valid_for_seconds RO uint32 (300 at issuance). The 16384 permit count is per registry, not per client. | A permit is distinct from a mutation token. An absent, empty or oversized request token field is `InvalidArgument`; a well-shaped unknown, expired, consumed or context-mismatched permit is `Aborted` for an absent target. Unknown means absent from the authoritative registry, not absent from the request. An otherwise valid existing target is `AlreadyExists`; recovery is `Unavailable`. |
| F30 GetService and GetModuleConfig request and snapshot | Request R name; cursor D(empty) bytes 0..1024. Response: incarnation and token R nonempty bytes; encoding version R positive uint32; total and offset uint64 with offset <= total; chunk bytes; complete bool; next cursor bytes 0..1024. All response fields are RO. An empty chunk is valid only at completion; an incomplete success must advance the offset. | total <= the configured export cap (initial 4194304); chunk plus envelope <= the response cap (262144); checked size_t copies. No mixed snapshot, gap, overlap or overflow. An unknown encoding rejects client assembly, never a partial replacement. |
| F31 list names request and response | No owner name; cursor D(empty); limit D(server default); 0 also means the default; a positive value at most the server maximum. Response names per F01 in ASCII lexical order; an empty page is permitted with an advancing cursor; complete if and only if the next cursor is empty. Names, membership revision, complete and next cursor obey section 6's per-RPC coherent-state rule. Membership identity, instance, epoch and position are RO and carried inside the opaque cursor, not a new revision field. | Uses the F32 bounds for lists; no caller position or revision setter; checked counts and bytes; membership invalidation only on create and delete. Bounded coherence failure is Unavailable with no usable partial page/cursor. |
| F32 ListSessions request | R VS name; cursor D(empty) bytes 0..1024, empty starts; limit D(100 initially); 0 means the default; 1..1000 initially; above the maximum rejects, no clamp. | An opaque cursor, not the old uint64 cursor. The server resolves one policy snapshot per page; an unchanged limit is not required on continuation. |
| F33 SessionRecord and response | The entire response is RO. Flow family, addresses and protocol (TCP or UDP) and the stored real key and incarnation 1..256 are required; optional ports preserve absence versus 0. expires_at and page_now are uint64 nanoseconds in the same identified dataplane clock domain, with no UTC claim; layer identity and front or back role are required; a zero numeric time is representable. Rows may be empty on an incomplete advancing page. | Complete if and only if the next cursor is empty; uint64 clocks and lossless typed identities; no public memory address. An unsafe read is `Unavailable`, an oversized record is `ResourceExhausted`, and a failure yields no usable partial page. |
| F34 networks, ranges, transport presence | F02 and F03 bare hosts, F19-F21 raw sources and the section 14 native networks and ranges are deliberately different types; no blanket normalization. | The per-field matrix above applies to every field rather than copying old l3b network semantics. |
| F35 l3b.policy configuration | Not an RPC mutation surface. A missing, null or `{}` section selects defaults; each optional setting missing or null selects its default; explicit zero is invalid for positive budgets. Settings and defaults are in section 2; unknown keys reject. | Checked integer arithmetic, restart-required changes, and the all-accepted-object readability check before a reduction. The CLI sends absence or 0 only on paging and initial-size request fields, not on policy configuration. |
| F36 GetServiceStatus | Read-only observation groups; current VS identity or incarnation-scoped pending cleanup; accepted-token context distinct from runtime provenance. | Section 6's bounded live pages and configuration-bound continuation; reuse existing applicable identity/read-limit domains. Exact schema and cursor/error representation must precede implementation; no fabricated observation defaults or infinite completed history. |

### Byte-Page Framing, Cursor Errors And Read-Only Identities

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

Service, CLI and direct-client tests must instantiate every matrix row with
omitted, null where expressible, explicit empty, explicit zero or false,
minimum, maximum, just-outside-domain and wrong-type forms. Binary absence is
not textual null. Include an unselected choice branch, empty members, null
elements and RO or X keys with null or zero, plus two equal decoded spellings
and genuinely distinct values.

Required discriminating vectors include:

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
- Six timeouts: container absent, null and `{}`; tcp absent versus 0 versus
  60; SYN and SYN-ACK inheritance through an explicit 0; uint32 maximum and
  overflow; all six classes; every SYN/ACK/FIN/RST combination under section 7;
  representable and overflowing deadline addition with unchanged prior state
  on failure.
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
- Clone bucket fractional refill/cap, shared-worker contention, new-state full
  versus surviving control-plane restart/takeover, old-rate update accrual and
  carry/clamp, unchanged policy and peer churn, zero/exact/insufficient funding,
  peers above burst, and no refund after reservation failures. Numeric/clock
  bounds and error handling must be specified before activation.
- Selected selector algorithms/ties/vectors before implementation; creation and
  membership versus unchanged-membership Update, empty selector and failure
  preservation, no silent full rebuild. These are algorithm-conformance vectors,
  not portable-placement guarantees; explicit recovery rebuilding is preserved.

- Late session commit: MSS/encapsulation preparation failures leave session
  state/deadlines unchanged; required commit failure drops the prepared packet
  without partial authority change; postcommit transmission loss does not roll
  back. Include existing-pin refresh, new insertion, copy-forward and zero TTL.
- ModuleConfig binding cap: two 10000-binding objects pass this cap alone;
  a 16385-binding candidate exceeds the initial per-object cap; retained old
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
  qualification keep their own scopes. NAT rejection is fixed by F27 and
  section 12, not an open mode-selection decision.
- Nothing in this document is an implementation, an ABI or compatibility
  freeze, a capacity qualification or a production-readiness claim.
