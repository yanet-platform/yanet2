//! Aggregate `--watch`: one reconnect-with-backoff supervisor per
//! discovered service, a periodic re-discovery sweep for services that
//! register later, and the single render loop that serializes their
//! output.
//!
//! The initial probe (shared with the one-shot aggregate command) seeds the
//! merged transition snapshot before any supervisor's `Watch` stream opens,
//! so its first message — always a full snapshot, per the server's own
//! contract — diffs to nothing instead of reporting every scope as newly
//! discovered.

use core::time::Duration;
use std::{
    collections::{BTreeMap, HashSet},
    sync::Arc,
};

use readinesspb::pb::{ReadyRequest, ReadyResponse, Scope};
use serde::Serialize;
use tokio::sync::mpsc;
use ync::{client::Connection, discovery, errors::Error, output};

use crate::{
    Cmd, READINESS_SERVICE, ServiceReport, print_report, probe,
    render::{self, ServiceColumn, Transition},
};

/// Initial delay before a supervisor's first reconnect attempt, doubling on
/// each further failure up to [`MAX_BACKOFF`].
const INITIAL_BACKOFF: Duration = Duration::from_secs(1);

/// Ceiling every supervisor's reconnect backoff is clamped to.
const MAX_BACKOFF: Duration = Duration::from_secs(30);

/// How often the re-discovery sweep re-lists the gateway's registered
/// readiness services, to pick up ones not yet registered at startup.
///
/// Also doubles as that sweep's own per-attempt timeout budget: a
/// `list_services` call that has not answered by the time the next sweep is
/// due is stalled by definition, so the cadence is its own natural bound and
/// needs no separate magic number. The worst case is simply that the
/// effective sweep period doubles.
const REDISCOVER_INTERVAL: Duration = Duration::from_secs(10);

/// Budget for one service's initial `Ready` probe in aggregate `--watch`.
///
/// The probe only seeds the first rendered snapshot; a service that misses
/// this budget still gets its supervisor spawned on schedule, whose `Watch`
/// stream opens independently and always starts with a full snapshot (per
/// the server's own contract) that repopulates it as first sightings. So
/// this deliberately favours starting the watch promptly over a prettier
/// first screen, which is why it is its own constant rather than a reuse of
/// [`discovery::DISCOVERY_TIMEOUT`] — that one scopes only best-effort
/// enrichment such as hints and shell completions.
const PROBE_TIMEOUT: Duration = Duration::from_secs(5);

/// Runs aggregate `--watch`.
///
/// Probes every discovered service once — rendering the same blocks the
/// one-shot aggregate command does, plus a standalone `watching…` line —
/// then hands off to one supervisor task per service and a re-discovery
/// task, rendering every event they produce until the process is
/// interrupted.
///
/// Only ends on interruption: every supervisor loops forever and the
/// re-discovery task never exits, so the `Ok(true)` below is unreachable
/// in practice — it mirrors single-service `--watch`, whose clean stream
/// close likewise maps to `Ok(true)` rather than reporting the readiness
/// observed at startup.
pub async fn run(cmd: &Cmd) -> Result<bool, Error> {
    let connection = Arc::new(Connection::connect_for(&cmd.connection, "ready").await?);
    let services = discovery::list_services(&connection, READINESS_SERVICE).await?;

    let mut reports = Vec::with_capacity(services.len());
    for service in &services {
        reports.push(probe_bounded(&connection, service.clone()).await);
    }

    let aliases = discovery::alias_map(&services);
    let alias_width = render::name_width(aliases.values().map(String::as_str));
    let scope_width = render::name_width(
        reports
            .iter()
            .flat_map(|report| report.scopes.iter())
            .map(|scope| scope.name.as_str()),
    );

    // Always renders, even with no services discovered: the re-discovery
    // sweep below will pick services up as they register, so the wait
    // must stay visible instead of leaving a silent hang.
    let snapshot_payload = SnapshotPayload {
        event: EventKind::Snapshot,
        services: &reports,
    };

    let mut aggregate = Aggregate::seed(&reports);

    output::data(&snapshot_payload, false, format_args!(""), || {
        if reports.is_empty() {
            eprintln!("no readiness services registered");
        } else {
            for (idx, report) in reports.iter().enumerate() {
                if idx > 0 {
                    println!();
                }

                print_report(report, scope_width, cmd.stale_after);
            }
        }

        render::print_watching_line();

        if aggregate.all_ready {
            render::print_all_ready_line();
        }
    });

    let (sender, mut receiver) = mpsc::unbounded_channel::<Event>();

    for service in &services {
        let alias = aliases.get(service).cloned().unwrap_or_else(|| service.clone());

        tokio::spawn(supervise(connection.clone(), service.clone(), alias, sender.clone()));
    }

    let known: HashSet<String> = services.into_iter().collect();
    tokio::spawn(rediscover(connection.clone(), known, aliases, sender.clone()));

    drop(sender);

    while let Some(event) = receiver.recv().await {
        render_event(event, &mut aggregate, scope_width, alias_width);
    }

    Ok(true)
}

/// Probes one service for the initial snapshot, bounded by [`PROBE_TIMEOUT`].
///
/// A service that is registered but never answers `Ready` — a wedged
/// component, a half-open socket — must not stall every other service's
/// probe, nor the supervisors and rediscovery task spawned once probing
/// finishes. A timeout is reported the same way [`probe`] already reports a
/// failed one: an empty `scopes` and a populated `error`, so it renders as
/// that service's error block and its supervisor still starts normally.
async fn probe_bounded(connection: &Connection, service: String) -> ServiceReport {
    match tokio::time::timeout(PROBE_TIMEOUT, probe(connection, service.clone())).await {
        Ok(report) => report,
        Err(..) => ServiceReport {
            service,
            scopes: Vec::new(),
            error: Some(format!("probe timed out after {PROBE_TIMEOUT:?}")),
        },
    }
}

/// One update delivered by a supervisor to the render loop.
///
/// `service` and `data` are the serialized payload's source (via
/// [`EventPayload`]); `alias` steers human rendering only and is never
/// serialized.
struct Event {
    service: String,
    alias: String,
    data: EventData,
}

/// The event-specific data carried by an [`Event`].
///
/// A [`Lost`](Self::Lost) event keeps its disconnect `cause` and its
/// `retry_after` backoff separate rather than pre-composed into one
/// string: the human renderer composes them into one line at render time,
/// and the serialized payload's `error` is `cause` alone.
enum EventData {
    /// A `Watch` message arrived, carrying the scopes it changed (or, for
    /// the first message after a stream opens, the full snapshot).
    Message(Vec<Scope>),
    /// The stream (re)established after a previous disconnect.
    Reattached,
    /// The stream ended or errored; the supervisor is retrying after
    /// `retry_after`.
    Lost { cause: String, retry_after: Duration },
    /// A re-discovery sweep just found this service and is about to spawn
    /// its supervisor.
    ///
    /// Carries no data — it exists purely to tell the render loop's
    /// `Aggregate` that this service now exists, before its supervisor's
    /// first `Message` can arrive, so the banner blocks on it like any
    /// other unreported service. Never reaches `output::data`.
    Discovered,
}

/// The wire discriminator shared by [`SnapshotPayload`]'s and
/// [`EventPayload`]'s `event` field.
///
/// `Snapshot` is the stream's first line ([`SnapshotPayload`]); the other
/// three each describe one [`EventPayload`] line that follows it.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
enum EventKind {
    Snapshot,
    Message,
    Reattached,
    Lost,
}

/// The wire shape of the snapshot line an aggregate `--watch` JSON stream
/// opens with.
///
/// Every later line is an [`EventPayload`], which always names one
/// `service`; this envelope instead lists every service probed before any
/// supervisor's stream opened, so it needs its own struct rather than
/// reusing that shape. Wrapping `services` behind the same `event`
/// discriminator means line 1 is a JSON object like every other line, not
/// a bare array a `jq -c 'select(.event == …)'` pipeline would choke on.
#[derive(Serialize)]
struct SnapshotPayload<'a> {
    event: EventKind,
    services: &'a [ServiceReport],
}

/// The self-describing JSON Lines wire shape of one aggregate `--watch`
/// per-service event: every line after the stream's first (see
/// [`SnapshotPayload`]).
///
/// `event` is the discriminator a monitoring script switches on; `error`
/// means an error and nothing else — it is set only when `event` is
/// `"lost"`, and holds the raw disconnect cause alone, never the composed
/// "reconnecting in …" wording that the human renderer adds. A successful
/// reconnect (`"reattached"`) therefore always serializes with
/// `error: null`, never mistakeable for a failure.
#[derive(Serialize)]
struct EventPayload<'a> {
    service: &'a str,
    event: EventKind,
    scopes: &'a [Scope],
    error: Option<&'a str>,
}

impl Event {
    fn message(service: &str, alias: &str, scopes: Vec<Scope>) -> Self {
        Self {
            service: service.to_owned(),
            alias: alias.to_owned(),
            data: EventData::Message(scopes),
        }
    }

    fn reattached(service: &str, alias: &str) -> Self {
        Self {
            service: service.to_owned(),
            alias: alias.to_owned(),
            data: EventData::Reattached,
        }
    }

    fn lost(service: &str, alias: &str, cause: &str, retry_after: Duration) -> Self {
        Self {
            service: service.to_owned(),
            alias: alias.to_owned(),
            data: EventData::Lost { cause: cause.to_owned(), retry_after },
        }
    }

    fn discovered(service: &str, alias: &str) -> Self {
        Self {
            service: service.to_owned(),
            alias: alias.to_owned(),
            data: EventData::Discovered,
        }
    }
}

/// The reconnect-lifecycle state one `supervise` loop threads across
/// attempts.
///
/// Kept as its own type, independent of the live stream, so the rules
/// around when a `Lost` is due and when it is closed out by a `Reattached`
/// are testable without a server to drive `supervise`'s stream.
/// `last_unopened_message` dedupes repeated `Lost` events across
/// consecutive attempts that never open, all reporting the same error;
/// `lost_outstanding` is the single fact both event kinds key off — a
/// `Lost` has been emitted since the last successful open and not yet
/// closed out by one.
#[derive(Debug, Default)]
struct SuperviseState {
    lost_outstanding: bool,
    last_unopened_message: Option<String>,
}

impl SuperviseState {
    /// Records that this attempt's stream opened successfully.
    ///
    /// Returns whether a `Reattached` event is due — exactly when a `Lost`
    /// was outstanding from a previous attempt — and always clears the
    /// outstanding flag and the failure-dedupe key, since a live stream
    /// closes both out.
    fn opened(&mut self) -> bool {
        let reattached = self.lost_outstanding;
        self.lost_outstanding = false;
        self.last_unopened_message = None;

        reattached
    }

    /// Records that this attempt's stream opened at least once and then
    /// ended.
    ///
    /// Always due: a stream that was open and is now gone is always worth
    /// reporting, unlike a repeat of the same never-opened failure.
    fn lost_after_open(&mut self) -> bool {
        self.lost_outstanding = true;

        true
    }

    /// Records that this attempt ended without the stream ever opening,
    /// with the given error `message`.
    ///
    /// Returns whether a `Lost` event is due: a repeat of the same message
    /// across consecutive never-opened attempts is deduped, but the first
    /// is always reported.
    fn failed_before_open(&mut self, message: &str) -> bool {
        let should_emit = self.last_unopened_message.as_deref() != Some(message);
        self.last_unopened_message = Some(message.to_owned());

        if should_emit {
            self.lost_outstanding = true;
        }

        should_emit
    }
}

/// Watches one service forever, reconnecting with backoff on every
/// disconnect and forwarding every message and lifecycle event to `sender`.
///
/// Never returns on its own: a service that disappears from the registry,
/// or never answers at all, just keeps retrying and reporting `Lost`
/// events rather than being torn down — re-registration then works with no
/// special handling.
async fn supervise(connection: Arc<Connection>, service: String, alias: String, sender: mpsc::UnboundedSender<Event>) {
    let mut backoff = INITIAL_BACKOFF;
    let mut state = SuperviseState::default();

    loop {
        let mut opened_this_attempt = false;

        let result = connection
            .invoke_server_stream::<ReadyRequest, ReadyResponse, _>(
                "ready",
                &service,
                "Watch",
                ReadyRequest { scopes: Vec::new() },
                |resp: ReadyResponse| {
                    if !opened_this_attempt {
                        opened_this_attempt = true;
                        backoff = INITIAL_BACKOFF;

                        if state.opened() {
                            let _ = sender.send(Event::reattached(&service, &alias));
                        }
                    }

                    let _ = sender.send(Event::message(&service, &alias, resp.scopes));
                },
            )
            .await;

        let message = stream_error_message(&result);
        let should_emit = if opened_this_attempt {
            state.lost_after_open()
        } else {
            state.failed_before_open(&message)
        };

        if should_emit {
            let _ = sender.send(Event::lost(&service, &alias, &message, backoff));
        }

        tokio::time::sleep(backoff).await;
        backoff = next_backoff(backoff);
    }
}

/// Renders the outcome of one `Watch` attempt as a `Lost` event's message
/// text: the RPC error's own message, or a fixed wording when the server
/// simply closed the stream.
fn stream_error_message(result: &Result<(), Error>) -> String {
    match result {
        Ok(()) => "stream closed by server".to_owned(),
        Err(err) => err.message().to_owned(),
    }
}

/// Doubles `backoff`, clamped to [`MAX_BACKOFF`].
fn next_backoff(backoff: Duration) -> Duration {
    (backoff * 2).min(MAX_BACKOFF)
}

/// Periodically re-lists the gateway's registered readiness services,
/// announces each newly discovered one to the render loop, and spawns its
/// supervisor.
///
/// A failed or stalled sweep is silent: the next tick retries, and a service
/// that really is unreachable already has its own supervisor saying so via
/// `Lost` events. The `list_services` call is bounded by
/// [`REDISCOVER_INTERVAL`] rather than left to hang indefinitely — a stuck
/// gateway connection must not park this task forever, since it is the only
/// path that picks up services registering after startup. `known` and
/// `aliases` are owned exclusively by this task from the moment it is
/// spawned, so assigning a newly discovered service's alias needs no shared
/// or locked state, and never rewrites an alias a running supervisor has
/// already been handed.
async fn rediscover(
    connection: Arc<Connection>,
    mut known: HashSet<String>,
    mut aliases: BTreeMap<String, String>,
    sender: mpsc::UnboundedSender<Event>,
) {
    loop {
        tokio::time::sleep(REDISCOVER_INTERVAL).await;

        let sweep = discovery::list_services(&connection, READINESS_SERVICE);
        let Ok(Ok(services)) = tokio::time::timeout(REDISCOVER_INTERVAL, sweep).await else {
            continue;
        };

        let mut discovered = Vec::new();

        for service in services {
            if !known.insert(service.clone()) {
                continue;
            }

            let alias = assign_alias(&service, &aliases);
            aliases.insert(service.clone(), alias.clone());

            let _ = sender.send(Event::discovered(&service, &alias));
            discovered.push((service, alias));
        }

        // Every discovery from this sweep is announced above before any of
        // its supervisors is spawned below, so `Discovered` for a service
        // is always enqueued ahead of any `Message` a sibling's supervisor
        // could emit — `tokio::spawn` only schedules the task and may run
        // it concurrently with the rest of this loop.
        for (service, alias) in discovered {
            tokio::spawn(supervise(connection.clone(), service, alias, sender.clone()));
        }
    }
}

/// Assigns a display alias for a newly discovered `service`, falling back
/// to its full name when the derived alias collides with one already
/// assigned.
///
/// Compares against every known service's own *derived* alias
/// (`discovery::derive_alias`), not against `aliases`' assigned values: a
/// pair of services that already collided has had both of its values
/// rewritten to their FQNs, so comparing against values alone would miss a
/// third service later deriving that same alias.
fn assign_alias(service: &str, aliases: &BTreeMap<String, String>) -> String {
    let candidate = discovery::derive_alias(service);

    let collides = aliases.keys().any(|known| discovery::derive_alias(known) == candidate);

    if collides { service.to_owned() } else { candidate }
}

/// The merged readiness state aggregate `--watch` tracks across every
/// supervisor, used solely to detect the whole set crossing into fully
/// ready and print [`render::print_all_ready_line`] exactly once per
/// crossing.
///
/// Kept separate from the per-scope `snapshot` diffing
/// [`render::record_transition`] already does, because "all ready" needs one
/// more fact `snapshot` alone cannot answer: whether any service is currently
/// unreachable. `blocking` is that set of service names — a service that
/// errored at its initial probe and has not since been observed, or whose
/// stream is currently [`EventData::Lost`] — mirroring the one-shot
/// [`all_ready`]'s own rule that an unreachable service is never ready.
struct Aggregate {
    snapshot: BTreeMap<(String, String), Scope>,
    blocking: HashSet<String>,
    all_ready: bool,
}

impl Aggregate {
    /// Builds the initial aggregate from the services' probe `reports`.
    ///
    /// `snapshot` starts from every OK report's scopes, exactly as the old
    /// startup seeding loop did. `blocking` starts as the set of services
    /// whose initial probe errored.
    fn seed(reports: &[ServiceReport]) -> Self {
        let mut snapshot = BTreeMap::new();
        let mut blocking = HashSet::new();

        for report in reports {
            for scope in &report.scopes {
                snapshot.insert((report.service.clone(), scope.name.clone()), scope.clone());
            }

            if report.error.is_some() {
                blocking.insert(report.service.clone());
            }
        }

        let mut aggregate = Self { snapshot, blocking, all_ready: false };
        aggregate.all_ready = aggregate.compute();

        aggregate
    }

    /// Computes whether the aggregate is currently fully ready.
    ///
    /// `all_ready` per the task's definition: the snapshot is non-empty, no
    /// service is blocking, and every scope currently in the snapshot is
    /// [`crate::is_ready`].
    fn compute(&self) -> bool {
        !self.snapshot.is_empty() && self.blocking.is_empty() && self.snapshot.values().all(crate::is_ready)
    }

    /// Records one `Message` event's `scope` from `service` and classifies
    /// its transition.
    ///
    /// Diffs the scope into `snapshot` only — it does not touch `blocking`,
    /// since a message may carry any number of scopes (including zero) and
    /// `blocking` must be cleared exactly once per message regardless. See
    /// [`Self::mark_reported`] for that.
    fn observe(&mut self, service: &str, scope: &Scope) -> Transition {
        render::record_transition(&mut self.snapshot, service, scope)
    }

    /// Recomputes [`Self::all_ready`] and reports whether it just crossed
    /// from not-ready into ready.
    ///
    /// A crossing prints the banner exactly once: a second `refresh` call
    /// while already ready recomputes the same `true` but returns `false`,
    /// since `all_ready` was already `true` going in.
    fn refresh(&mut self) -> bool {
        let now = self.compute();
        let crossed = now && !self.all_ready;
        self.all_ready = now;

        crossed
    }

    /// Inserts `service` into `blocking` and forces `all_ready` false
    /// immediately, without waiting for the next recompute.
    ///
    /// Shared by [`Self::mark_lost`] and [`Self::mark_discovered`], which
    /// differ only in why `service` is currently unreported.
    fn block(&mut self, service: &str) {
        self.blocking.insert(service.to_owned());
        self.all_ready = false;
    }

    /// Records that `service`'s stream was lost.
    ///
    /// Forces `all_ready` false immediately, without waiting for the next
    /// `Message`: an unreachable service must stop the aggregate from
    /// reading as ready the instant it is known, not merely once the loss
    /// happens to be recomputed.
    fn mark_lost(&mut self, service: &str) {
        self.block(service);
    }

    /// Records that a re-discovery sweep just found `service`.
    ///
    /// A newly discovered service has not reported anything yet, so it
    /// must hold the "all ready" banner back exactly like an unreachable
    /// one until its first `Message` clears it from `blocking` via
    /// [`Self::observe`] — otherwise the aggregate could read ready before
    /// a service that registers later than the others has had a chance to
    /// report in.
    fn mark_discovered(&mut self, service: &str) {
        self.block(service);
    }

    /// Records that `service` has sent a `Watch` message, so it has
    /// reported its state and no longer blocks the banner.
    ///
    /// Called once per `Message` event, regardless of how many scopes it
    /// carries — even a message carrying zero scopes clears the block,
    /// unlike the per-scope [`Self::observe`].
    fn mark_reported(&mut self, service: &str) {
        self.blocking.remove(service);
    }
}

/// Renders one event from a supervisor.
///
/// `Message`, `Reattached`, and `Lost` are the three observable events:
/// each always calls `output::data` so JSON mode sees it exactly as
/// received, even when nothing is worth printing in human mode. A
/// `Message` first calls [`Aggregate::mark_reported`] once, unblocking
/// `service` regardless of how many scopes the message carries, then diffs
/// its scopes against the merged `aggregate` and prints one transition line
/// per scope that actually changed, then prints
/// [`render::print_all_ready_line`] if that message's transitions crossed
/// the whole set into ready; `Reattached` prints one dim
/// [`render::print_lifecycle_line`] only — it deliberately leaves `service`
/// blocked, since a reattached service must stay blocked until its own
/// next `Message` clears it, so a message from another supervisor cannot
/// cross the aggregate into ready off this service's stale pre-disconnect
/// scopes; `Lost` composes its cause and retry delay via
/// [`render::print_lost_line`].
///
/// `Discovered` is internal bookkeeping only: it updates `aggregate`'s
/// blocking set so the "all ready" banner waits for this service's first
/// message, and never reaches `output::data` — it renders neither a
/// human line nor a JSON line.
fn render_event(event: Event, aggregate: &mut Aggregate, scope_width: usize, alias_width: usize) {
    let Event { service, alias, data } = event;

    match data {
        EventData::Message(scopes) => {
            let payload = EventPayload {
                service: &service,
                event: EventKind::Message,
                scopes: &scopes,
                error: None,
            };

            output::data(&payload, false, format_args!(""), || {
                aggregate.mark_reported(&service);

                let mut scopes = scopes.clone();
                scopes.sort_by(|a, b| a.name.cmp(&b.name));

                for scope in &scopes {
                    let transition = aggregate.observe(&service, scope);

                    if transition != Transition::Unchanged {
                        render::print_transition_line(
                            ServiceColumn::Named { alias: &alias, width: alias_width },
                            scope,
                            scope_width,
                            transition,
                        );
                    }
                }

                if aggregate.refresh() {
                    render::print_all_ready_line();
                }
            });
        }
        EventData::Reattached => {
            let payload = EventPayload {
                service: &service,
                event: EventKind::Reattached,
                scopes: &[],
                error: None,
            };

            output::data(&payload, false, format_args!(""), || {
                render::print_lifecycle_line(&alias, alias_width, "stream reattached");
            });
        }
        EventData::Lost { cause, retry_after } => {
            let payload = EventPayload {
                service: &service,
                event: EventKind::Lost,
                scopes: &[],
                error: Some(&cause),
            };

            output::data(&payload, false, format_args!(""), || {
                aggregate.mark_lost(&service);
                render::print_lost_line(&alias, alias_width, &cause, retry_after);
            });
        }
        EventData::Discovered => {
            aggregate.mark_discovered(&service);
        }
    }
}

#[cfg(test)]
mod test {
    use readinesspb::pb::State;

    use super::*;

    fn scope(name: &str, state: State) -> Scope {
        Scope {
            name: name.to_owned(),
            state: state as i32,
            reasons: Vec::new(),
            observed_at: None,
            last_transition_time: None,
        }
    }

    fn report(service: &str, scopes: Vec<Scope>) -> ServiceReport {
        ServiceReport {
            service: service.to_owned(),
            scopes,
            error: None,
        }
    }

    fn failed_report(service: &str) -> ServiceReport {
        ServiceReport {
            service: service.to_owned(),
            scopes: Vec::new(),
            error: Some("unknown service".to_owned()),
        }
    }

    #[test]
    fn backoff_doubles_up_to_the_ceiling() {
        let mut backoff = INITIAL_BACKOFF;
        let mut steps = Vec::new();

        for _ in 0..7 {
            steps.push(backoff);
            backoff = next_backoff(backoff);
        }

        assert_eq!(
            vec![
                Duration::from_secs(1),
                Duration::from_secs(2),
                Duration::from_secs(4),
                Duration::from_secs(8),
                Duration::from_secs(16),
                Duration::from_secs(30),
                Duration::from_secs(30),
            ],
            steps
        );
    }

    #[test]
    fn supervise_state_first_attempt_opens_without_a_reattached() {
        let mut state = SuperviseState::default();

        assert!(!state.opened());
    }

    #[test]
    fn supervise_state_opens_dies_reopens_emits_lost_then_reattached() {
        let mut state = SuperviseState::default();

        assert!(!state.opened());
        assert!(state.lost_after_open());
        assert!(state.opened());
    }

    #[test]
    fn supervise_state_failed_before_first_open_still_reattaches() {
        // The bug case: a first attempt that never opens must not leave
        // the eventual successful open silent.
        let mut state = SuperviseState::default();

        assert!(state.failed_before_open("boom"));
        assert!(state.opened());
    }

    #[test]
    fn supervise_state_dedupes_a_repeated_failure_but_keeps_the_lost_outstanding() {
        let mut state = SuperviseState::default();

        assert!(state.failed_before_open("boom"));
        assert!(!state.failed_before_open("boom"));
        assert!(state.opened());
    }

    #[test]
    fn assign_alias_uses_the_derived_alias_when_no_collision() {
        let aliases = BTreeMap::new();

        let alias = assign_alias("operators.forward.operatorpb.v1.ReadinessService", &aliases);

        assert_eq!("forward", alias);
    }

    #[test]
    fn assign_alias_falls_back_to_the_fqn_on_collision() {
        let mut aliases = BTreeMap::new();
        aliases.insert(
            "operators.route.operatorpb.v1.ReadinessService".to_owned(),
            "route".to_owned(),
        );

        let alias = assign_alias("operators.route.operatorpb.v2.ReadinessService", &aliases);

        assert_eq!("operators.route.operatorpb.v2.ReadinessService", alias);
    }

    #[test]
    fn assign_alias_detects_a_collision_even_after_earlier_ones_were_rewritten_to_fqns() {
        // Both entries already collided with each other, so their values
        // are FQNs rather than "route" — a third service deriving "route"
        // must still be caught by comparing against derived aliases, not
        // against these already-rewritten values.
        let mut aliases = BTreeMap::new();
        aliases.insert(
            "operators.route.operatorpb.v1.ReadinessService".to_owned(),
            "operators.route.operatorpb.v1.ReadinessService".to_owned(),
        );
        aliases.insert(
            "operators.route.operatorpb.v2.ReadinessService".to_owned(),
            "operators.route.operatorpb.v2.ReadinessService".to_owned(),
        );

        let alias = assign_alias("operators.route.operatorpb.v3.ReadinessService", &aliases);

        assert_eq!("operators.route.operatorpb.v3.ReadinessService", alias);
    }

    #[test]
    fn lost_event_keeps_the_raw_cause_with_no_composed_wording() {
        let event = Event::lost("svc", "alias", "boom", Duration::from_secs(5));

        let EventData::Lost { cause, retry_after } = event.data else {
            panic!("expected a Lost event");
        };

        // The cause must stay exactly what the RPC reported: no
        // `reconnecting in …` suffix baked in, since that composition is
        // the human renderer's job, done only at render time.
        assert_eq!("boom", cause);
        assert_eq!(Duration::from_secs(5), retry_after);
    }

    #[test]
    fn aggregate_seed_is_ready_when_every_scope_of_every_service_is_ready() {
        let reports = vec![
            report("a", vec![scope("rib", State::Ready)]),
            report("b", vec![scope("fib", State::Ready)]),
        ];

        let aggregate = Aggregate::seed(&reports);

        assert!(aggregate.all_ready);
    }

    #[test]
    fn aggregate_seed_is_not_ready_when_one_service_errored() {
        let reports = vec![report("a", vec![scope("rib", State::Ready)]), failed_report("b")];

        let aggregate = Aggregate::seed(&reports);

        assert!(!aggregate.all_ready);
    }

    #[test]
    fn aggregate_seed_is_not_ready_when_empty() {
        let aggregate = Aggregate::seed(&[]);

        assert!(!aggregate.all_ready);
    }

    #[test]
    fn aggregate_refresh_crosses_into_ready_exactly_once() {
        let reports = vec![report("a", vec![scope("rib", State::NotReady)])];
        let mut aggregate = Aggregate::seed(&reports);
        assert!(!aggregate.all_ready);

        aggregate.observe("a", &scope("rib", State::Ready));
        assert!(aggregate.refresh());

        // Still fully ready, but this is not a new crossing.
        assert!(!aggregate.refresh());
    }

    #[test]
    fn aggregate_mark_lost_drops_ready_and_a_later_message_re_crosses() {
        let reports = vec![report("a", vec![scope("rib", State::Ready)])];
        let mut aggregate = Aggregate::seed(&reports);
        assert!(aggregate.all_ready);

        aggregate.mark_lost("a");
        assert!(!aggregate.all_ready);

        aggregate.mark_reported("a");
        aggregate.observe("a", &scope("rib", State::Ready));
        assert!(aggregate.refresh());
    }

    #[test]
    fn aggregate_mark_discovered_blocks_the_banner_until_its_first_message() {
        let reports = vec![report("a", vec![scope("rib", State::Ready)])];
        let mut aggregate = Aggregate::seed(&reports);
        assert!(aggregate.all_ready);

        aggregate.mark_discovered("b");
        assert!(!aggregate.all_ready);
        assert!(aggregate.blocking.contains("b"));

        aggregate.mark_reported("b");
        aggregate.observe("b", &scope("fib", State::Ready));
        assert!(aggregate.refresh());
    }

    #[test]
    fn aggregate_ready_message_does_not_cross_while_a_discovered_service_still_blocks() {
        let reports = vec![report("a", vec![scope("rib", State::Ready)])];
        let mut aggregate = Aggregate::seed(&reports);

        aggregate.mark_discovered("b");

        aggregate.observe("a", &scope("rib", State::Ready));
        assert!(!aggregate.refresh());
    }

    #[test]
    fn aggregate_mark_reported_clears_a_block_even_with_no_scopes() {
        let reports = vec![report("a", vec![scope("rib", State::Ready)])];
        let mut aggregate = Aggregate::seed(&reports);
        assert!(aggregate.all_ready);

        aggregate.mark_discovered("b");
        assert!(!aggregate.all_ready);
        assert!(aggregate.blocking.contains("b"));

        // A message carrying zero scopes never reaches `observe`, but it
        // must still clear the block and let the aggregate re-cross.
        aggregate.mark_reported("b");
        assert!(aggregate.refresh());
    }
}
