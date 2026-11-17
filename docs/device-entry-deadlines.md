# Device-entry deadline scheduling

Design note for #2813 (idle worker cost grows with the number of virtual
devices). Related: #1677 (worker budgets as time deadlines), #3492, #3493,
#3494 (write-stage costs proportional to the configured device count).

## Problem

Every worker round, `worker_pipeline_round()` drains the home list of device
entries onto the ready list and processes it, so each device's input and
output entries run unconditionally — cascading through pipelines, functions
and chains into module handlers — even when every packet front is empty.
The force-poll exists so modules with periodic work (aging, generation)
are invoked without traffic. With many virtual devices the idle round costs
O(devices x entries x chains x modules) iterations of pure overhead.

## Design

Replace the force-poll with a monotonic min-timer over device entries. The
scheduler mechanism is three rules; everything else is handler
responsibility.

### Scheduler contract

1. **Create/apply arms zero.** On configuration creation or reconfiguration
   every device entry of the new generation starts with deadline "now", so
   each entry fires exactly once immediately after the dataplane applies
   the configuration. Modules with periodic work (traffic generators, aging)
   boot from this invocation and re-arm at their own cadence; pure
   packet-driven entries fire once on an empty front and never re-arm.
   The framework learns nothing about module intentions — intent is
   expressed only by runtime re-arm behavior.
2. **Invocation disarms.** When an entry runs, its deadline resets to
   infinity ("never wake this entry again") *before* the handler executes,
   so a re-arm performed by the handler itself is just an ordinary
   schedule into the freshly reset value.
3. **Schedule only decreases.** Any schedule attempt — the entry's own
   handler, another module, a timeout arm — does
   `deadline = min(deadline, t)`. An entry may therefore be woken earlier
   than its handler asked (a packet arrived, or another module scheduled
   it): spurious wakeups are part of the contract.

All scheduling executes on the owning worker thread inside a round — each
worker owns its own generation execution context, and a module handler
scheduling a cross-device entry resolves it inside the same worker's
round — so these are plain stores: no atomics, no locking. The worker
keeps a cached `nearest_deadline`, lowered by the same monotonic min on
every arm; a stale-low cache after a deadline is consumed causes at most
one extra sweep that recomputes it. Locating due entries when the sweep
runs is deliberately left to the implementation (a lazy linear sweep over
armed entries suffices at first; a bucketed timing wheel if profiling
ever demands it).

### Handler contract

A handler that needs periodic invocation re-arms the entry whenever it
wants to be invoked again; the scheduler performs exactly what it asks,
each round. Any resulting re-arm pattern — a period that slides under
steady traffic, ticks skipped because a spurious wakeup reset the
deadline — is intended behavior: the framework adds no machinery to
track, diagnose or compensate for it. How a handler computes its next
arm time is entirely its own concern.

### Entry access from a module handler

Today a `module_ectx` exposes the registry device ID but no direction and
no back-reference to the device entry invoking it, so a module placed in
an output pipeline — or in both directions of one device — cannot resolve
which entry to arm: resolving by device alone leaves both the input and
the output entry as candidates. The design therefore requires the
execution context to carry a back-reference to the *currently executing*
device entry (set by the framework around each handler invocation,
restored afterwards), and the schedule API takes that reference as its
target. This back-reference is a prerequisite of step 1 in the rollout.

### Round shape

For the pipeline scheduling stage, an idle round becomes: read the clock,
compare against `nearest_deadline`, arm due entries onto the ready list,
poll RX — O(1) in the configured device count. `nearest_deadline` doubles
as the idle-sleep bound for #1677. The ready list remains the only
execution driver; the deadline pass only adds entries to it. The whole
idle round reaches O(1) only after the write-stage passes of #3492,
#3493 and #3494 are also gone.

A configuration apply costs one full forced invocation pass: every entry
fires once on an empty front, and each firing dispatches its configured
pipelines, functions, chains and modules, so the apply-time work is
proportional to the descendant tree beneath the entries, not merely to
the entry count (only the deadline enumeration is O(entries)). The pass
also serves as a natural "configuration applied" tick.

## Correctness notes

- **Counters.** Per-packet accounting is unaffected: counters only fire on
  nonzero deltas, so empty-front invocations add nothing. Only the
  force-poll's silent module invocations disappear.
- **Recirculation.** Unchanged: `module_ectx_route_input/output` already
  move a routed-to entry onto the ready list; a packet never waits for a
  deadline.
- **Testing.** The scheduler rules are checkable: a `dataplane_ut`
  harness arms an entry, verifies any invocation — deadline-driven or
  packet-driven before the deadline — consumes and resets the deadline,
  and verifies a schedule only decreases it.

## Rollout

1. Introduce the deadline field, the executing-entry back-reference and
   the three scheduler rules behind the existing force-poll behavior;
   keep the force-poll as the default.
2. Convert modules with periodic work to self re-arm (traffic generator,
   l3b session aging); audit remaining handlers for hidden empty-front
   work.
3. Drop the force-poll second pass; the pipeline scheduling stage of an
   idle round becomes O(1) in the configured device count.
4. Address the write-stage O(device_count) passes (#3492, #3493, #3494),
   after which the whole idle round is O(1).
