# Route operator readiness

The route operator exposes four readiness dimensions, each as its own scope:
FIB programming per gateway, kernel neighbour resolution, the RIB content, and
(optionally) the BIRD FeedRIB transport session.

- gRPC FQN: `operators.route.operatorpb.v1.ReadinessService`
- Service implementation: `readiness_service.go` (`ReadinessService`).
- Scope logic: `operator.go` (tracker construction and the FIB/neighbour
  wiring), `rib_readiness.go` (the RIB/BIRD helper).

## Scopes

| Scope                    | Meaning                                              |
| ------------------------ | --------------------------------------------------- |
| `fib:<gateway>:<module>` | Per-gateway FIB apply outcome into the dataplane module. |
| `neighbours`             | Kernel neighbour (ARP/ND) resolution state.          |
| `rib`                    | Routing information base content readiness.          |
| `bird-session`           | BIRD FeedRIB transport liveness (only when `readiness.expect_bird`). |

One `fib` scope per gateway, named `fib:<gateway.name>:<function.module>`.
`bird-session` is only registered when `readiness.expect_bird` is true
(default true).

### `fib:<gateway>:<module>`

Driven by the apply outcome recorded through `Observe` (via
`NewObservedActuator`):

- Apply succeeded: `STATE_READY`.
- Apply failed, previously READY or DEGRADED: `STATE_DEGRADED` (`APPLY_FAILED`).
- Apply failed, never applied: `STATE_NOT_READY` (`APPLY_FAILED`).

### `neighbours`

Driven by the netlink neighbour monitor. When the monitor is enabled
(`netlink_monitor.disabled` is false, the default):

- Before the first sync: `STATE_NOT_READY`, reason `SYNCING` (seeded at
  construction). Refresh errors are ignored until the first successful sync, so
  this state persists through bootstrap even when refreshes fail.
- Initial sync completes: `STATE_READY`.
- Resync after an error episode: `STATE_READY`.
- Refresh error: `STATE_DEGRADED`, reason `RESYNC`.

When the monitor is disabled (`netlink_monitor.disabled: true`), the scope is
latched to `STATE_READY` once at construction and never updated again.

### `rib`

Reflects route-content readiness. Two modes depending on `readiness.expect_bird`:

BIRD mode (`expect_bird` true, the default): gates READY on both a settled
update rate and a non-empty RIB. The bulk-settle gate clears once the update
rate stays below `readiness.rate_threshold` for `readiness.stability_window`.
States depend on whether a FeedRIB session is active:

- No session active (cold start before BIRD connects, or after a session ended):
  `STATE_NOT_READY` reason `NO_ROUTES` when the RIB is empty, or
  `STATE_DEGRADED` reason `SESSION_ENDED` when routes remain. The scope is seeded
  `STATE_NOT_READY` reason `SYNCING` at construction, but the first tick moves it
  into the no-session values above.
- Session active and bulk-loading (the settle gate has not cleared): reason
  `SYNCING` — `STATE_DEGRADED` when the RIB already has routes or
  `STATE_NOT_READY` when empty (the value tracks route presence, not prior
  readiness).
- Session active and settled: `STATE_READY` when non-empty, otherwise
  `STATE_NOT_READY` reason `NO_ROUTES`.

Static mode (`expect_bird` false): `STATE_READY` when routes are present,
otherwise `STATE_NOT_READY` (`NO_ROUTES`). Session events are ignored.

### `bird-session`

Reflects BIRD FeedRIB transport liveness. State and reason over the lifecycle:

- Before the first session: `STATE_DEGRADED`, reason `NO_SESSION` (seeded at
  construction).
- Session active: `STATE_READY`.
- Session ended, within `readiness.reconnect_grace` (default 15s):
  `STATE_DEGRADED`, reason `RECONNECTING`.
- Session ended, past `reconnect_grace` with no new session:
  `STATE_DEGRADED`, reason `DOWN`.
- Shutdown: `STATE_NOT_READY`, reason `SHUTTING_DOWN` (see below).

During normal operation it never reaches `STATE_NOT_READY`; only the shutdown
drain forces it there.

On shutdown `Drain` flips every route scope to `STATE_NOT_READY` with reason
`SHUTTING_DOWN`, including `fib`, `neighbours`, `rib`, and `bird-session`.
This is best-effort: the operator tracker is not drain-latched, so an apply in
flight when shutdown begins can overwrite the `SHUTTING_DOWN` value before the
reconciler stops.

## `observed_at` freshness

`observed_at` reflects how recently each scope's source was re-evaluated, not
whether the evaluation succeeded — read `state` for outcome. The four scope
families refresh on different clocks.

Freshness checks must poll `Ready`, not `Watch`. `Watch` emits only on
state/reason changes, so a pure `observed_at` refresh (a `Touch`, or a re-apply
that leaves state and reason unchanged) never streams — a `Watch`-based checker
would hold a stale `observed_at` while the service stays fresh. This matters
most for `rib` and `bird-session`, which refresh `observed_at` every tick
without changing state.

### `fib:<gateway>:<module>`

Advances on every reconcile apply attempt via `Observe`, regardless of success.

Config parameters (under `reconcile:`):

- `reconcile.interval` (default 30s): steady-state refresh cadence on a
  successful apply. One apply attempt is made per interval.
- `reconcile.initial_backoff` (default 500ms): first backoff sleep after a
  failed apply; grows exponentially with each retry.
- `reconcile.max_backoff` (default 30s): cap on the exponential backoff during
  persistent failure. `observed_at` still advances on each failed retry.

Each FIB scope publishes
`expected_observation_interval = reconcile.interval`. Consumers choose a small
multiplier and judge the scope stale when its age exceeds that product. Retry
backoff deliberately does not inflate the contract: a prolonged retry or slow
apply remains visible as freshness lag instead of being hidden by
`reconcile.max_backoff`.

### `neighbours`

When the monitor is enabled, `observed_at` advances on every successful
neighbour cache refresh, which happens in two ways:

- Event-driven: on each kernel `RTM_NEWNEIGH` update (near-real-time in an
  active network). Deletion events are deliberately not processed, so a
  delete-only network only refreshes on the periodic timer below.
- Periodic: a fixed 5min force-update timer. This interval is a built-in
  default of the neighbour monitor and is **not** exposed in the operator
  config, so it cannot be tuned per deployment.

Config parameters (under `netlink_monitor:`):

- `netlink_monitor.disabled` (bool, default false): when true the scope
  becomes a latch (see below) and `observed_at` stops advancing entirely. The
  event-driven and periodic refresh paths have no separate config knobs.

A quiet network therefore shows up to ~5min of age. The scope publishes that
5min periodic interval; a multiplier of 2-3 gives a 10-15min threshold. An
active network stays near-real-time.

When the monitor is disabled, the scope is set READY once and never refreshed,
so a staleness check is **not applicable** (treat READY as fresh regardless of
`observed_at`, as for the gateway scope).

### `rib` and `bird-session`

Both are driven by the same sampling ticker. Every tick re-evaluates `rib` and
touches `bird-session`, so `observed_at` for both advances once per tick
regardless of whether the state value changed.

Config parameters (under `readiness:`):

- `readiness.sample_interval` (default 1s): the sampling tick that drives
  `observed_at` for both scopes.
- `readiness.expect_bird` (bool, default true): selects BIRD vs static mode
  for `rib` and whether `bird-session` exists at all, but does **not** change
  the tick cadence — both modes tick at `sample_interval`.

Both scopes publish
`expected_observation_interval = readiness.sample_interval`, so a consumer's
multiplier scales automatically when the configured interval changes. Beyond
that scaled threshold the sampling ticker has stopped, which means the
operator is shutting down or the RIB readiness worker is no longer running.
