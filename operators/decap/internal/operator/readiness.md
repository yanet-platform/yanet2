# Decap operator readiness

The decap operator exposes one readiness scope per configured gateway,
covering all decap configs pushed to that gateway.

- gRPC FQN: `operators.decap.operatorpb.v1.ReadinessService`
- Service implementation: `readiness_service.go` (`ReadinessService`).
- Scope logic: `operator.go` (the observed actuator wiring).

## Scopes

| Scope              | Meaning                                       |
| ------------------ | -------------------------------------------- |
| `config:<gateway>` | Per-gateway apply outcome of all decap configs. |

One scope per entry in `gateways`, named `config:<gateway.name>`. Each scope
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
means the reconcile loop is alive — it does **not** mean the config applied
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

A stale `observed_at` means the reconcile loop has stopped refreshing — the
operator is shutting down, dead, or frozen. Recommended staleness threshold:
base it on `max(reconcile.interval, reconcile.max_backoff)` — the largest gap
between apply attempts in success or retry. At the defaults (both 30s) a
threshold around twice that (~60s) covers jitter and slow applies. If
`max_backoff` is raised above `interval`, size the threshold off `max_backoff`
or a live, retrying operator will false-alarm as stale.
