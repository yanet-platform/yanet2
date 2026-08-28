# Static module operator readiness

A static module operator exposes one readiness scope per configured gateway,
covering every module config and function pushed to that gateway.

- gRPC FQN: `operators.<name>.operatorpb.v1.ReadinessService`, where `<name>`
  is the operator's own name (`forward`, `decap`, ...). The service is the
  `controlplane.ynpb.v1.ReadinessService` contract registered under that name,
  so several operators can share one gateway.
- Service implementation and scope logic: `static.go`.

## Scopes

| Scope              | Meaning                                                    |
| ------------------ | ---------------------------------------------------------- |
| `config:<gateway>` | Per-gateway apply outcome of all module configs and functions. |

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
operator is shutting down, dead, or frozen. Each scope publishes
`expected_observation_interval = reconcile.interval`; consumers choose a small
multiplier and judge the scope stale when its age exceeds that product. Retry
backoff deliberately does not inflate the contract: a prolonged retry or slow
apply remains visible as freshness lag instead of being hidden by
`reconcile.max_backoff`.
