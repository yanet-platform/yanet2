# Pipeline operator readiness

The pipeline operator exposes one readiness scope per configured gateway,
covering all stages pushed to that gateway.

- gRPC FQN: `operators.pipeline.operatorpb.v1.ReadinessService`
- Service implementation: `readiness_service.go` (`ReadinessService`).
- Scope logic: `operator.go` (the observed actuator wiring).

## Scopes

| Scope                | Meaning                                          |
| -------------------- | ----------------------------------------------- |
| `pipeline:<gateway>` | Per-gateway apply outcome of all pipeline stages. |

One scope per entry in `gateways`, named `pipeline:<gateway.name>`. Each scope
starts at `STATE_UNKNOWN`.

State is driven by the apply outcome recorded through `Observe` (via
`NewObservedActuator`):

- Apply succeeded: `STATE_READY`.
- Apply failed, scope previously READY or DEGRADED: `STATE_DEGRADED`, reason
  `APPLY_FAILED`.
- Apply failed, scope never applied: `STATE_NOT_READY`, reason `APPLY_FAILED`.

On shutdown `Drain` flips every scope to `STATE_NOT_READY` (`SHUTTING_DOWN`).
This is best-effort: operator trackers are not drain-latched, so an apply in
flight when shutdown begins can overwrite the `SHUTTING_DOWN` value before the
reconciler stops.

## `observed_at` freshness

`observed_at` advances on every reconcile apply attempt, regardless of
success, because `Observe` fires inside each `Apply`. Advancing `observed_at`
means the reconcile loop is alive — it does **not** mean the stages applied
correctly (read `state` for that).

Config parameters (under `reconcile:`):

- `reconcile.interval` (default 30s): steady-state refresh cadence on a
  successful apply. One apply attempt is made per interval.
- `reconcile.initial_backoff` (default 500ms): first backoff sleep after a
  failed apply; grows exponentially with each retry.
- `reconcile.max_backoff` (default 30s): cap on the exponential backoff during
  persistent failure. `observed_at` still advances on each failed retry.

Freshness checks must poll `Ready`, not `Watch`. `Watch` emits only on
state/reason changes, so a successful re-apply that leaves the state unchanged
never streams — a `Watch`-based checker would hold a stale `observed_at` while
the service stays fresh.

### Idle (no stages)

Unlike the other operators, the pipeline source has no work to apply until
stages are queued. When `stages` is empty at startup (which `Validate`
permits — only `gateways` is required), no stages are seeded, the reconcile
source reports no target, and the reconciler idles without ever calling
`Apply`/`Observe`. The `pipeline:<gateway>` scopes stay at `STATE_UNKNOWN` and
`observed_at` is never set (the field is omitted from the response).

This is a healthy idle state, not a stalled loop. An external checker must not
treat the missing `observed_at` (or `STATE_UNKNOWN`) as staleness or failure
for a pipeline deployed without stages. Once at least one stage is queued, the
tail stage is retained as a steady-state target and `observed_at` advances on
every apply as described above.

Recommended staleness threshold (when stages are present): base it on
`max(reconcile.interval, reconcile.max_backoff)` — the largest gap between
apply attempts in success or retry. At the defaults (both 30s) a threshold
around twice that (~60s) covers jitter and slow applies. If `max_backoff` is
raised above `interval`, size the threshold off `max_backoff` or a live,
retrying operator will false-alarm as stale.
