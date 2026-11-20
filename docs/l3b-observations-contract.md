# l3b External Observation Contract (Candidate)

Status: **not approved, not implemented, NOT RUN**. This public candidate
records the service-side requirements of [#2775](https://github.com/yanet-platform/yanet2/issues/2775).
It does not select a protobuf, an ABI, production principals, deployment
deadlines, or an external checker implementation. The representation contract
in [#2718](https://github.com/yanet-platform/yanet2/issues/2718), the publisher
in [#2725](https://github.com/yanet-platform/yanet2/issues/2725), publisher
applicability in [#2762](https://github.com/yanet-platform/yanet2/issues/2762),
and actual-hop trust in [#2735](https://github.com/yanet-platform/yanet2/issues/2735)
retain their own approvals. No integration may claim this draft as approval.

The handoff includes the complete initial view, ordered deltas, source/checker/
real identities, observation age, heartbeat, expiry ownership, bounded outcome
recovery, assignments and publication/readiness ACKs. It also includes quorum-
weight inputs required by the operator's retained Q/H calculation, including
dynamic source weights, distinct from scheduling weights.

## Ownership and eligibility

- The first-class Service group and its shared ACL are defined in the
  [representation contract](l3b-contract.md#service-membership-and-shared-policy).
  Each VS retains its own real/session/health identities; Service membership
  is not a merged checker set or a shared session owner. A Service ACL update
  changes neither VS/real incarnations nor accepted observation deadlines or
  pending-disable publication obligations. Transfer by delete/recreate follows
  the existing incarnation fencing and removal/recreation obligations; a new
  Service or VS name does not discharge old accepted obligations.
- An external source submits observations for assigned objects only. It cannot
  change administrative state, configured weight, effective state or sessions.
  The owning control plane validates and expires observations, combines trusted
  eligibility with administrative state, and uses the single approved publisher.
  An operator's cache or heartbeat timer is not another effective-state owner.
- A real is eligible only when it is administratively enabled **and** its
  required health evidence is trusted and current. An administrative disable or
  drain or health expiry cannot be overridden by an observation. Scheduling
  weight zero excludes new selection, not health eligibility or a valid stateful
  pin to an enabled/healthy real outside explicit initial-SYN/UDP-OPS bypass.
  Missing, unknown, unproven or stale required checker/stream state
  fails health closed. Unknown is not a completed failed probe. The result for
  an empty checker set is health-ineligible: administrative enablement alone
  cannot admit a real without any required health checker.
- Health freshness has a worker-enforced expiry cutoff. Before new selection or
  forwarding through an existing pin, the worker checks the protected required
  checker/stream deadlines against a proved comparable dataplane clock. At or
  after the earliest applicable deadline, the real is health-ineligible under
  existing invalid-pin/reschedule/drop rules, even if disable publication fails
  or the worker retains an older descriptor/selector. A retained enabled bit
  cannot extend this deadline. Clock-domain proof and numerical budgets remain
  prerequisites; this is a selected requirement, not delivered enforcement.
- Accepted observation, successfully published effective state and generation-
  scoped serving readiness are separate facts. A publication failure preserves
  the previous published state and reports incomplete application; preserving
  those bytes does not suspend the worker-enforced expiry cutoff. It cannot
  report the requested state as active. Existing invalid-pin and
  reschedule/drop rules apply to health-ineligible reals without adding a new
  graceful-drain mode.
- Ordinary withdrawal follows a published disable; recovery announcement
  follows publication and serving-readiness acknowledgement. A separate
  announcement lease must expire no later than health eligibility. These
  constraints do not choose a publication barrier for every delta.

Emergency withdrawal and permit expiry do not await an unavailable CP or a
successful disable publication; the disable-publication obligation remains.
Zero configured weight is separate and is not itself an invalid-pin reason.

## Emergency withdrawal and component restarts

l3b operator owns Q/H, hysteresis state and the contributing ports/VS. Monalive
supplies checker/real results, failure history and freshness, not authoritative
announcement quorum. The operator combines quorum with current serving/ACL
gates. Announcer/BIRD do not recalculate Q/H. Neither the formula nor quorum-
weight composition changes; scheduling weights and administrative state remain
separate. CP retains authoritative health expiry and effective-state publication;
no operator health timer or active-state cache is introduced.

Failure, hang or restart of Monalive, l3b operator or announcer must withdraw all
managed VIPs. This is an MVP obligation, not a deferred guarantee. The selected
mechanism is BIRD `protocol monalive`: periodic state requests and prefix
confirmation over Unix datagram sockets; BIRD independently removes unconfirmed
routes without relying on sender liveness. Announcer, not Monalive, is the sender
in the target integration: only announcer applies current final operator
readiness including health and required ACL.

On lost/stale Monalive or operator state, announcer stops confirming every managed
VIP and initiates withdrawal. On announcer failure, confirmations cease without
another process and BIRD removes routes on its own schedule. Old files, cached
Ready or live RPC without fresh results cannot renew permission. An include-only
backend without independent BIRD expiry does not meet this contract. Integration
is not implemented.

Even a restart shorter than TTL requires withdrawal before reannouncement.
Distinguish process incarnations, fence the old sender and prove
withdraw→reannounce ordering. BIRD's periodic timer alone does not prove it.
Fresh health/serving readiness and required ACL confirmation are mandatory after
restart. Revalidate those gates after confirmed BIRD removal and export; do not
carry old Ready over. Unknown withdrawal outcome blocks
reannouncement. The fencing/confirmation protocol remains engineering work, not
a new message schema selected here.

The node-local recovery barrier is confirmed removal from the managed BIRD table
and its export to all configured dependent BGP peers, preserving withdrawal
before fresh announcement. Enqueueing a datagram is not confirmation. BGP has no
per-withdraw peer-application ACK: observing removal at peers is qualification
evidence, not an already available synchronous online RPC. If independent removal
or export ordering cannot be proved, reannouncement stays closed. Delayed,
duplicate or reordered confirmations from an old incarnation cannot refresh or
restore a withdrawn route.

Permit expiry, withdrawal decision, BIRD removal and peer application are distinct
events. BGP hold time covers BGP-peer loss, not announcer failure with BIRD alive.
Observe backend errors and unconfirmed withdrawal separately; successful datagram
send does not prove removal. GR/LLGR may retain stale routes on session loss:
disable retention for managed VIPs or explicitly include it in the configured,
verified withdrawal budget. Hold time alone does not establish a bound. Current
plugin behavior and the exact removal bound still need integration proof.

## Epoch, identity and history

Stream epoch, checker continuity marker, configuration revision, real
incarnation, publisher identity and opaque operator metadata are distinct.
Payload-supplied source names cannot grant authority. A new epoch does not
expand object scope or displace another assigned source.

| Event | Required outcome |
| --- | --- |
| New stream epoch | Start with a complete initial view; reject deltas and heartbeats until that view is accepted. Initialization itself is not successful-probe evidence. |
| Next delta | Require the expected sequence and current authorized source, configuration and real identities. |
| Duplicate accepted message | Do not apply or refresh it again; permit outcome lookup within bounded retention. |
| Reused sequence with different content | Reject as a conflict without changing accepted history. |
| Sequence gap | Stop applying deltas; require a complete initial view in a new epoch. |
| Reconnect | Require a complete view and refresh no observation merely by reconnecting. Preserve streak/eligibility history only with the same trustworthy continuity marker, continuous sequence and complete authoritative history. |
| Changed marker, unproven history or restart | Initialize unknown/unhealthy with streak zero; do not fabricate success. |
| Old revision, marker or real incarnation | A delayed message cannot enable a current real or attach to an address reused by a new real. |
| Outcome outside retention | Report unknown outcome and require resynchronization; do not claim non-application or permit replay by evicting a result. |
| Quorum-weight inputs W | Each real has a separate static quorum weight or exactly one designated checker supplies its dynamic weight. Reject a configuration that declares both sources for one real. Ignore weight from undesignated checkers; they still participate in health AND. No runtime fallback between sources. Weight is nonnegative and bound to the current VS and real. Dynamic weight also requires provenance from the designated checker and its current revision. Omission is not zero and does not extend validity. A previous weight is usable only until its original expiry with proven continuity. Never substitute scheduling weight. Untrusted or incomplete inputs cannot establish readiness. Do not accept a partial or overflowing sum as full W. Encoding and numeric bounds are technical work. |

Before implementing [#2776](https://github.com/yanet-platform/yanet2/issues/2776),
the receiver must have an **approved validator-visible stale-exclusion proof**.
Exercise configuration A -> B -> A, checker restart, marker/sequence reset or
wrap, reconnect, delayed or in-flight delivery, obsolete validators, removed
and re-added reals, and reused addresses. Specify which provenance the receiver
actually validates, what authoritative history survives, and how obsolete
sources are fenced. Repeating a revision, cache expiry or assuming old messages
vanish is not a non-aliasing proof. Do not choose wire widths or a permanent
per-observation journal by implication.

## Checker configuration and freshness

Checker settings are external configuration/runtime provenance, not writable
VS fields, manual weights or administrative setters.

- `attempt_timeout_ms` is a uint32 millisecond duration in 1..4294967295;
  absent or textual null resolves to **500 ms**. Zero and out-of-domain values
  reject checker configuration. The **100 ms operational target** is not a
  different default or cap. Every authoritative view carries the effective
  timeout with immutable checker-configuration identity and revision. Missing
  or invalid effective timeout is untrusted and fails health closed.
- `retries` is an external-checker integer in 0..255 **without an implicit
  default**. An authoritative view carries its value, configuration identity
  and revision, streak, eligibility, continuity marker and sequence together.
  Missing or invalid retries fails health closed, not retries=0. A configuration
  change requires a coherent new-revision view; old observations cannot apply.
- Completed success resets the failure streak and establishes checker
  eligibility, subject to independent constraints. Completed failure disables
  when `streak > retries`, including failure 256 with retries=255. Preserve
  proven history when changing the threshold: lowering may disable at once;
  raising cannot revive an already unhealthy checker without a new success.
  Unproven history initializes unknown/unhealthy. A view claiming eligibility
  with `streak > retries` cannot be trusted; an ineligible view below threshold
  may represent hysteresis. Required checkers aggregate with AND; success of
  one cannot override another's unknown or failed state.
- Attempt timeout is for one probe, not retry cadence, observation age, stream
  lease or route lease. No deadline is inferred from 500 ms, 100 ms or the
  separate WLC sampling cadence.

Age is measured from the observation, not receipt, view creation, reconnect,
retransmission or acknowledgement. A complete view can carry stale data.
Allowed uncertainty, maximum age, future timestamps, clock jumps and restarts
need validator-visible rules. Unverifiable age or an impermissible future
timestamp rejects. After validation, fix the **remaining** lifetime against
the control-plane monotonic clock; receipt never grants a fresh full lifetime.
Only a genuinely newer observation may extend its deadline. A heartbeat
establishes source liveness but does not refresh checker evidence. Checker
expiry, source liveness, probe timeout and route lease retain separate owners.
All timeouts, intervals and freshness budgets are configurable; effective values
and configuration revisions are diagnostic observations. Defaults are allowed,
hidden immutable constants instead of settings are not. Session TTL is per VS;
attempt timeout belongs to checker configuration; maximum age, stream liveness
and clock uncertainty belong to existing health/activation context, not a new VS
field. See [deployment decisions](l3b-mvp-spec.md#path-to-mvp-completion).
Ready timeout/staleness, polling, hysteresis and lease belong to the appropriate
operator/announcer contexts; scan/check to BIRD and hold time to BGP. Each owner
validates settings; integration validates combinations before announcement.
The end-to-end budget includes maximum detection delay, BIRD checks, removal and
withdrawal application: lease alone is not an upper bound. Renewal and removal
must fit health/readiness budgets. Unproved bounds or invalid combinations fail
announcement closed; reducing a budget does not refresh old observations.
For a fixed checker/activation, each published checker deadline derives from
accepted probe evidence; descriptor replacement, takeover or worker restart does
not refresh it. Extending it requires a genuinely newer accepted observation,
not a new publication timestamp. Renewing stream liveness cannot extend a checker
deadline. Recovery must preserve these provenance and non-renewal invariants.

Configured durations use uint32 milliseconds; absolute monotonic timestamps
use uint64 milliseconds with identified clock domains. A source timestamp
cannot be compared directly to a control-plane timestamp. Widen durations
before checked deadline addition; neither wrap nor saturate. Keep same-domain
uint64 elapsed intervals without narrowing to uint32. An expired observation
is unknown even if an expiry publication fails; report incomplete publication
without claiming the new effective state is active.

## Acceptance, acknowledgement and cancellation

| Boundary | Required guarantee |
| --- | --- |
| Rejection before acceptance | This request changes no accepted state and starts no publication; independent concurrent work is unaffected. |
| Acceptance | Identify source/epoch/sequence in configuration/real context, without claiming publication or readiness. |
| Successful publication | Name the actual publisher generation and provenance; bind any readiness to the generation and scope it certifies, not the newest generation observed when replying. |
| Publication failure | Do not reject an already accepted observation retroactively or report its requested state as active. |
| Server-confirmed cancellation before acceptance | This request applied nothing; a client timeout alone cannot prove that. |
| Cancellation after acceptance | Stop waiting, not the accepted update; do not roll publication back. |
| Lost acknowledgement | Query outcome by source/epoch/sequence and validated context; repetition cannot reapply or refresh. |
| Evicted outcome | Report uncertainty and resynchronize; expiry never authorizes identity reuse or stale replay. |

The same before/after-acceptance cancellation boundary applies to complete
views, deltas and heartbeat liveness updates. A lost heartbeat reply is
uncertain; it cannot refresh checker observations. Cancelling a status read
stops that read, not the underlying observation or publication. There is no
implicit worker-adoption wait or separate publication barrier for each delta.

Outcome retention is bounded; recovery window, capacity, exhaustion and
eviction policy need approval. Stale/duplicate exclusion must survive result
cache eviction independently. Accepted failures and hysteresis must not be
erased by coalescing. Define whether a result was published, superseded before
publication, or published and later superseded, with truthful provenance;
do not invent a generation for an unpublished result. An equal-result
confirmation needs an actual publication/adoption provenance rule, not an
analogy to configuration CAS no-op. An accepted transition disabling a real's
health eligibility must have a successful disabled-state publication before a
subsequent recovery enable is published for that real/activation; recovery cannot
coalesce away that unpublished disable. Failed disable publication keeps this
obligation pending and blocks recovery-enable publication; it does not retroactively
reject accepted observations. New observation admission remains bounded by reserved
resources and must report exhaustion before acceptance. Worker adoption is separate:
this does not promise every worker saw
the intermediate state or a global traffic stop. Preserve the obligation across
takeover and outcome-cache eviction; bounded retention/admission is part of the
publisher proof, not permission to silently discard it. Other partial-batch
supersession/coalescing and equal-result provenance details remain to be specified.

### Disable Publication Before Structural Removal

An accepted health-disable obligation MUST have a successful disabled-state
publication before acceptance of a structural operation removing its real
incarnation or its owning VS. This includes real removal through full VS
replacement, explicit empty reals, a changed real key, VS deletion and subsequent
recreation or transfer to another Service. The identity is the full real key
and real incarnation within the VS incarnation and observation activation,
not an address, reused name or new incarnation. Structural removal is not an
alternative terminal discharge, even if it would make the real unreachable.

For O03/O04, a definitely pending obligation on any removed incarnation is
`FailedPrecondition` after the existing request-local, object, expected-token
and target checks, before resource admission and structural acceptance. Reject
the whole operation; do not remove other reals, change membership/tokens/sessions,
accept a queued deletion, or implicitly wait/retry until publication succeeds.
Independent lawful health/publication/expiry activity can still progress. An
unchanged full replacement or an operation preserving all affected incarnations
does not fail this removal precondition merely because an obligation is pending.
Unlinking a VS from ModuleConfig does not remove the VS or settle its obligations;
it cannot destroy the surviving publication target or its retained rights.

The final structural acceptance check is serialized with disable acceptance and
publication settlement. If the disable wins, removal must see its obligation;
if removal wins, subsequent observations targeting the removed incarnation are
rejected as stale/absent under the existing observation rules, not accepted
against a replacement identity. A prevalidated old validator cannot bypass this
final ordering. If authoritative obligation state or settlement provenance cannot
be established safely, removal returns `Unavailable`, never assumes an empty set.

Successful publication must be proved for the exact accepted obligation and
identity/activation by protected publisher outcome/provenance. An older disable
publication predating that transition, an accepted observation, attempted
publication, lost reply alone, outcome-cache eviction, deadline expiry, worker
pin invalidation, route withdrawal or a new real's disabled start is not that
proof. A lost publication reply is reconciled to the actual outcome; uncertainty
does not discharge the obligation or authorize destructive retry. Takeover
preserves pending obligations and established settlement evidence. Once covered
by a proven successful disable publication, the obligation does not require a
second publication merely for removal; a later independent transition can create
a new obligation. Existing CAS and other preconditions are still rechecked.

No all-worker adoption, reader drainage or global traffic-stop acknowledgement
is required between the successful disable publication and structural acceptance.
Retained readers and worker-enforced health expiry keep their existing separate
rules. Failed publication can block removal indefinitely; this is not permission
to invent a timeout exemption or silently discard accepted state. The concrete
bounded admission, retention, enrollment/settlement and crash-recovery proof
remains D01/D05 work; no unbounded per-address history or new public flag is selected.

## Capabilities and RPC hops

- Authenticate and authorize the operation and object scope **before**
  mutation or protected-status disclosure. Derive source identity from the
  authenticated principal plus approved assignment, not a submitted name.
- A source may initialize, submit deltas, heartbeat and query outcomes only
  for its assigned objects. A status reader can read authorized status but
  cannot write or inspect sessions. Operator authority to manage assignments
  and administrative state does not itself grant observation-source authority.
- Initial views and deltas must validate the **current** configuration and
  real incarnation. A new epoch or reused address grants no inherited rights.
  Outcome queries require source authority; unknown or unauthorized targets
  must not leak diagnostics through status or errors.
- Reuse platform authentication/authorization under #2735 and prove trusted
  principal delivery across actual Gateway/registration/backend RPC hops.
  Gateway-local identity alone is not evidence that the receiving service
  authenticates a caller. Fixture credentials are not production grants.

## Operator metadata boundary

The candidate [#2718 contract](https://github.com/yanet-platform/yanet2/pull/3007)
defines F39 optional opaque operator metadata on VS, real and ModuleConfig:
0..256 decoded UTF-8 bytes, valid UTF-8, no NUL. Omission, textual null and
empty denote empty; no trimming, Unicode/case normalization, ordering,
uniqueness or inheritance. Full create/replace writes submitted/default
strings for surviving reals while preserving manual weight, administrative
state, incarnation and pins. Equality includes strings; metadata-only changes
advance the owning CAS once (real metadata uses VS CAS); stale CAS is Aborted.
Partial/admin/weight/resize/runtime operations and recovery preserve strings.

F39 is **not** an epoch, checker revision, anti-ABA identity, freshness evidence,
authorization, publisher generation or adoption proof. Replacing the former
numeric-version proposal does not remove system fencing and incarnation.

## Accepted quorum and publication rules

Start down on startup, restart, contributing VS recreation or configured contributing membership change. Changes limited to non-contributing VSes, metadata, ordinary W changes and individual probes do not reset hysteresis. Require fresh evidence at the up threshold. Loss of the last contributing VS withdraws the VIP. |

Equal-result confirmation requires actual publication provenance and a current target. Coalescing must preserve history, freshness and required intermediate disable publication.

## Approval register (open)

The [health-expiry enforcement proof](l3b-mvp-spec.md#actual-hop-trust-and-routing)
must establish the selected hard dataplane cutoff: logical owner-side expiry,
failed publication and lagging workers are distinct. An unknown status or route
lease alone is not evidence that old-descriptor forwarding has stopped.

The [MVP engineering proposal](l3b-mvp-spec.md#observation-fencing-proposal)
supplies a concrete activation/stream fencing design and counterexamples.
It remains a proposal: neither its presence nor peer review satisfies this
register's accountable approval or the cross-process publisher proof.

| Owner / issue | Required decision before dependent implementation |
| --- | --- |
| Observation-contract owner / #2775 | Approve the receiver-visible stale-exclusion proof and bounded failure/recovery vectors, before #2776. |
| Contract and deployment owners / #2775 | Approve age evidence, uncertainty and clock-jump/restart rules, independent freshness/liveness/deadline bounds. |
| Resource owner / #2775 | Approve recovery-window capacity, exhaustion/eviction and stale exclusion after eviction. |
| Publication / #2725/#2762/#2775 | Specify the current target and prove publication, including intermediate disable. |
| Trust-policy owner / #2735/#2775 | Approve actual principals, assignments, trusted identity delivery and grants. |
| Health-integration owner / #2775 | Verify selected empty-checker ineligibility and validate timing/lease ownership while preserving AND and fail-closed semantics for required checkers. |
| Operator Q/H / #2775 | Weight encoding and numeric bounds; proof of freshness and quorum reset. |

These are unresolved; this draft picks no deployment numbers, named production
roles, codec, epoch width, new grace period, pending-disable flag, per-delta
barrier or independent observation journal. In particular, fail-closed unknown
eligibility **is already a requirement**, not a pending owner decision.

## Synthetic conformance obligations (NOT RUN)

| Group | Cases |
| --- | --- |
| Order and recovery | Delta/heartbeat before initialization, duplicate without refresh, conflicting sequence, gap, reconnect with and without proven history. |
| Fencing | A -> B -> A, restarts, marker/sequence reset/wrap, delayed/in-flight messages, obsolete validators and real/address reuse. |
| Checker settings | Omitted/null/zero/min/max timeout; absent/invalid effective timeout; revisions; retries 0/255 and rejection of 256 as a configured value, failure 256 at retries=255, threshold changes and hysteresis. |
| Freshness | Stale initial view; heartbeat/replay/reconnect/ack delay cannot extend evidence; newer same-value probe can; future timestamps, clock jumps and expiry. |
| Clock arithmetic | Allowed and rejected age/uncertainty, restart and incompatible clock domains. Widen uint32 durations before checked uint64 deadline addition without wrap or saturation. Same-domain elapsed time retains uint64 range. |
| Worker expiry | Equality with the earliest required checker/stream deadline rejects eligibility for both new selection and pin use. Repeat with failed disable publication, an old enabled descriptor, idle/resumed worker and unverifiable clock mapping. A later successful probe cannot extend the deadline in an immutable older descriptor. |
| Authority | Health cannot override admin disable/drain; weight0 excludes new choice but does not invalidate an enabled/healthy stateful pin outside bypass. One healthy checker cannot override another's unknown; empty required-checker set remains ineligible even if admin-enabled. |
| Q/H and weights | Test first startup and restart separately. Start the restart case with quorum up and the VIP advertised. Verify withdrawal and no reannouncement with fresh in-band W. Fresh W at the up threshold raises quorum, but reannouncement also requires the other readiness checks. Test prior up/down inside the band,W=0,Q−H,Q+H, and loss of the last contributor. Verify reset to down on contributing VS recreation or configured contributing-set change. A non-contributing VS change or a metadata-only change must not reset quorum. Verify dynamic weight expiry on omission with unchanged scheduling weight. Check provenance without inventing missing defaults. |
| Intermediate disable | Accept disable then recovery before publication; recovery-enable publication cannot overtake or absorb the successful disabled-state publication. Repeat with failed publication, lost acknowledgement, takeover and outcome-cache eviction; no implicit all-worker adoption wait. |
| Disable versus recreation | Accept disable for i1, fail publication, then attempt O03 real removal (including explicit empty and a changed port/key) or O04 VS deletion: FailedPrecondition, whole request unchanged, no queued deletion. Repeat transfer-by-recreation. After proven successful disabled-state publication, retry under valid CAS may remove; recreate as fresh i2/VS, never alias another port/incarnation. No all-worker adoption wait. |
| Removal ordering and recovery | Race accepted disable with final structural acceptance in both orders; delayed prevalidated observations cannot attach to removed/recreated identities. Inject lost publication reply, takeover, outcome-cache eviction and uncertain settlement state: pending remains FailedPrecondition, uncertain authority/provenance Unavailable, proven settlement permits normal removal checks. An old disable publication predating the transition is not coverage. Test stale CAS before pending-precondition failure and a valid no-op/survivor-preserving replacement while pending; unrelated reals never partially remove. Bound retained resources before admission, not by erasing an accepted obligation. |
| Recreation with worker lag | Satisfy any pending-disable obligation by successful disabled publication before removal; retain an earlier enabled i1 descriptor on a lagging worker while removal/readdition commits elsewhere. With other eligibility conditions unchanged, test both removal-adoption-before-expiry and retained-i1-deadline-before-adoption: i1 becomes invalid at the first boundary, including expiry before adoption, and follows existing invalid-pin disposition. A worker adopting removal cannot alias an i1 pin to i2. Include recently healthy i1 with substantial remaining lifetime within the approved age budget, not an impermissible future observation. Assert forwarding-decision eligibility, not a wall-time TX cutoff; i2's disabled start alone does not remove the old-worker exposure. |
| Admission exhaustion | Exhaust reserved observation/publication-obligation capacity before accepting a new transition: reject without new accepted state or publication effects; preserve all previously accepted disable obligations. Distinguish this from eviction of an already accepted result's outcome cache. |
| Publication | Accepted versus published versus adopted; failed and lost expiry acknowledgement; actual generation/scope; approved equal-result/supersession/coalescing. |
| Cancellation | Before/after acceptance for view, delta and heartbeat; response loss, bounded recovery, cache exhaustion and post-eviction uncertainty without replay. |
| Trust | Unauthorized writes leave state unchanged; forbidden status/error does not disclose protected data; scope and principal survive actual RPC hops. |
| Metadata | Equal, changed and reused F39 values never validate stale health or certify adoption. |

These vectors are approval and implementation obligations, not passing tests.
Closing #2775 requires accountable approval of this tracked contract and its
stale-exclusion proof before #2776; transport, scale and external-consumer
integration have separate outcomes.
