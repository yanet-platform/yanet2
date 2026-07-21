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
    collections::{BTreeMap, HashMap, HashSet},
    sync::Arc,
};

use readinesspb::pb::{ReadyRequest, ReadyResponse, Scope};
use serde::Serialize;
use tokio::{sync::mpsc, task::AbortHandle};
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

    let mut supervisors: HashMap<String, AbortHandle> = HashMap::with_capacity(services.len());

    for service in &services {
        let alias = aliases.get(service).cloned().unwrap_or_else(|| service.clone());

        let handle = tokio::spawn(supervise(connection.clone(), service.clone(), alias, sender.clone()));
        supervisors.insert(service.clone(), handle.abort_handle());
    }

    tokio::spawn(rediscover(connection.clone(), supervisors, aliases, sender.clone()));

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

/// One update delivered to the render loop: either a change concerning one
/// already-known service, or one re-discovery sweep's whole membership
/// delta.
///
/// [`Self::Service`]'s `service` and `data` are the serialized payload's
/// source (via [`EventPayload`]); `alias` steers human rendering only and
/// is never serialized. [`Self::Membership`] cannot share that shape — a
/// sweep's delta can name any number of services on either side, not one —
/// so it carries its own `(service, alias)` pairs and serializes through
/// [`MembershipPayload`] instead. Both sides of the same sweep travel in
/// one event so the render loop can apply the whole delta and check for a
/// readiness crossing exactly once, against the final state, rather than
/// once per service.
enum Event {
    /// A supervisor's update about one already-known service.
    Service {
        service: String,
        alias: String,
        data: EventData,
    },
    /// One re-discovery sweep's whole membership delta: every service it
    /// found newly registered, and every one it found missing, as
    /// `(service, alias)` pairs on each side.
    Membership {
        discovered: Vec<(String, String)>,
        departed: Vec<(String, String)>,
    },
}

/// The event-specific data carried by an [`Event::Service`].
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
}

/// The wire discriminator shared by [`SnapshotPayload`]'s,
/// [`EventPayload`]'s, and [`MembershipPayload`]'s `event` field.
///
/// `Snapshot` is the stream's first line ([`SnapshotPayload`]); `Message`,
/// `Reattached`, and `Lost` each describe one [`EventPayload`] line;
/// `Membership` describes one [`MembershipPayload`] line.
#[derive(Debug, Clone, Copy, PartialEq, Eq, Serialize)]
#[serde(rename_all = "snake_case")]
enum EventKind {
    Snapshot,
    Message,
    Reattached,
    Lost,
    Membership,
}

/// The wire shape of the snapshot line an aggregate `--watch` JSON stream
/// opens with.
///
/// Most later lines are an [`EventPayload`], which always names one
/// `service`; this envelope instead lists every service probed before any
/// supervisor's stream opened, so it needs its own struct rather than
/// reusing that shape — [`MembershipPayload`] is the other exception, for
/// the same reason. Wrapping `services` behind the same `event`
/// discriminator means line 1 is a JSON object like every other line, not
/// a bare array a `jq -c 'select(.event == …)'` pipeline would choke on.
#[derive(Serialize)]
struct SnapshotPayload<'a> {
    event: EventKind,
    services: &'a [ServiceReport],
}

/// The self-describing JSON Lines wire shape of one aggregate `--watch`
/// per-service event: every [`Event::Service`] line after the stream's
/// first (see [`SnapshotPayload`]).
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

/// The wire shape of a `--watch` membership line: the one
/// [`Event::Membership`] line a re-discovery sweep emits when its delta is
/// non-empty on either side.
///
/// Needs its own struct rather than [`EventPayload`]'s single-`service`
/// shape for the same reason [`SnapshotPayload`] does: this event names as
/// many services as the sweep found on each side, not one. Lists only
/// service names — `alias` steers human rendering alone and is
/// deliberately never serialized.
#[derive(Serialize)]
struct MembershipPayload<'a> {
    event: EventKind,
    discovered: &'a [&'a str],
    departed: &'a [&'a str],
}

impl Event {
    fn message(service: &str, alias: &str, scopes: Vec<Scope>) -> Self {
        Self::Service {
            service: service.to_owned(),
            alias: alias.to_owned(),
            data: EventData::Message(scopes),
        }
    }

    fn reattached(service: &str, alias: &str) -> Self {
        Self::Service {
            service: service.to_owned(),
            alias: alias.to_owned(),
            data: EventData::Reattached,
        }
    }

    fn lost(service: &str, alias: &str, cause: &str, retry_after: Duration) -> Self {
        Self::Service {
            service: service.to_owned(),
            alias: alias.to_owned(),
            data: EventData::Lost { cause: cause.to_owned(), retry_after },
        }
    }

    /// Builds one re-discovery sweep's whole membership-delta event from
    /// the `discovered` and `departed` sides of that sweep.
    fn membership(discovered: Vec<(String, String)>, departed: Vec<(String, String)>) -> Self {
        Self::Membership { discovered, departed }
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
/// announces each newly discovered one to the render loop and spawns its
/// supervisor, and tears down every service that has since fallen out of
/// the list.
///
/// A failed or stalled sweep changes nothing: every existing supervisor
/// keeps running, and the next tick simply retries. Misreading it as
/// "every service disappeared" would not end the watch — this task holds
/// its own `sender` clone and is never aborted — but it would still abort
/// every supervisor over one network hiccup, flooding the render loop with
/// spurious departure lines, resetting every supervisor's backoff, and
/// risking a premature "all ready" banner once they all reappear. So
/// membership is only ever compared against a sweep that actually
/// completed. A genuinely shrunken registry — for example every backend
/// re-registering after a gateway restart — produces that same departure
/// and rediscovery churn legitimately and recovers on its own, so this
/// guard covers only the failed-or-stalled-sweep half, not membership
/// churn in general.
///
/// The `list_services` call is bounded by [`REDISCOVER_INTERVAL`] itself
/// rather than left to hang indefinitely, since a stuck gateway connection
/// must not park this task forever — it is the only path that picks up
/// both newly registering services and departed ones. `supervisors` and
/// `aliases` are owned exclusively by this task, so recording a joined or
/// departed service needs no shared or locked state, and never rewrites an
/// alias a running supervisor has already been handed.
///
/// Both sides are sent to the render loop together, as one
/// [`Event::Membership`], so the whole sweep is applied to the aggregate
/// atomically rather than one service at a time.
async fn rediscover(
    connection: Arc<Connection>,
    mut supervisors: HashMap<String, AbortHandle>,
    mut aliases: BTreeMap<String, String>,
    sender: mpsc::UnboundedSender<Event>,
) {
    loop {
        tokio::time::sleep(REDISCOVER_INTERVAL).await;

        let sweep = discovery::list_services(&connection, READINESS_SERVICE);
        let Ok(Ok(services)) = tokio::time::timeout(REDISCOVER_INTERVAL, sweep).await else {
            continue;
        };

        let listed: HashSet<&str> = services.iter().map(String::as_str).collect();

        let departed_services: Vec<String> = supervisors
            .keys()
            .filter(|service| !listed.contains(service.as_str()))
            .cloned()
            .collect();

        let discovered_services: Vec<String> = services
            .into_iter()
            .filter(|service| !supervisors.contains_key(service))
            .collect();

        // The invariant this bookkeeping upholds is that no two live services ever
        // share an alias — both halves of it, not just the departure half. A departed
        // service's alias is captured here for the membership event's departed side but
        // deliberately left in `aliases` until after every newcomer below has been
        // assigned, so a newcomer can never claim a still-nominally-live departing
        // service's alias out from under it. `assign_newcomer_aliases` covers the other
        // half: it inserts each newcomer as it goes, so two newcomers discovered in
        // this very sweep that derive the same short alias collide against each other
        // too, not only against services outside the sweep.
        let departed: Vec<(String, String)> = departed_services
            .into_iter()
            .map(|service| {
                let alias = aliases.get(&service).cloned().unwrap_or_else(|| service.clone());
                (service, alias)
            })
            .collect();

        let discovered = assign_newcomer_aliases(discovered_services, &mut aliases);

        // Only now is it safe to free a departed service's alias: every
        // newcomer this sweep found has already been assigned above, so
        // removing it here can no longer hand it to one.
        for (service, _) in &departed {
            aliases.remove(service);
        }

        for (service, _) in &departed {
            // `departed` was built from `supervisors`' own distinct keys,
            // so this loop can never call `remove` on the same key twice.
            let handle = supervisors
                .remove(service)
                .expect("`departed` was just collected from `supervisors`' own keys");

            // `abort` only requests cancellation, so it happens before the
            // membership event below: otherwise the departing service's
            // supervisor could queue a `Lost` event behind its own
            // departure. This binary's `current_thread` runtime makes that
            // free today; it only guards a hypothetical multi-thread one.
            handle.abort();
        }

        // Emitted only when this sweep actually changed something: a sweep
        // that finds the same membership as before has nothing worth
        // telling the operator, and an empty `Event::Membership` would
        // print no lines and never cross the aggregate either way.
        if !discovered.is_empty() || !departed.is_empty() {
            let _ = sender.send(Event::membership(discovered.clone(), departed));
        }

        // This sweep's whole membership delta is announced above before
        // any of its supervisors is spawned below, so it is always
        // enqueued ahead of any `Message` a sibling's supervisor could
        // emit — `tokio::spawn` only schedules the task and may run it
        // concurrently with the rest of this loop.
        for (service, alias) in discovered {
            let handle = tokio::spawn(supervise(connection.clone(), service.clone(), alias, sender.clone()));
            supervisors.insert(service, handle.abort_handle());
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

/// Assigns each newly discovered service in `discovered` its display alias
/// via [`assign_alias`], inserting each into `aliases` before the next is
/// considered.
///
/// This insert-as-you-go order is what lets two services discovered in the
/// very same sweep collide with each other: [`assign_alias`]'s collision
/// check only ever sees the map it is handed, so if both were assigned
/// against a snapshot of `aliases` taken before either ran, the second
/// would never see the first's freshly derived alias and both would end up
/// with it.
fn assign_newcomer_aliases(discovered: Vec<String>, aliases: &mut BTreeMap<String, String>) -> Vec<(String, String)> {
    discovered
        .into_iter()
        .map(|service| {
            let alias = assign_alias(&service, aliases);
            aliases.insert(service.clone(), alias.clone());
            (service, alias)
        })
        .collect()
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

    /// Forgets `service`: a re-discovery sweep found it missing from the
    /// gateway's registry.
    ///
    /// Removes every `snapshot` entry keyed by `service` — the snapshot is
    /// keyed by `(service, scope name)` precisely so this can target one
    /// service's entries without touching a sibling's same-named scope —
    /// and drops it from `blocking`, so a departed service stops holding
    /// stale scopes and stops blocking the banner on a supervisor that no
    /// longer exists. Unlike [`Self::block`], does not touch `all_ready`
    /// directly: a departure can move it in either direction, up by
    /// unblocking the last blocking service or down by emptying the
    /// snapshot entirely, so it cannot be forced here and must instead be
    /// recomputed by [`Self::refresh`].
    fn forget(&mut self, service: &str) {
        self.snapshot.retain(|(known, _), _| known != service);
        self.blocking.remove(service);
    }

    /// Applies one re-discovery sweep's whole membership delta as a single
    /// atomic update and reports whether it crossed the aggregate into
    /// fully ready.
    ///
    /// Records every arrival in `discovered` via [`Self::mark_discovered`]
    /// and forgets every departure in `departed` via [`Self::forget`], then
    /// calls [`Self::refresh`] exactly once against the resulting state. A
    /// sweep is one membership change, not a sequence of them, so this is
    /// the one place that decides its readiness crossing: checking after
    /// each side, or after each service within a side, could catch an
    /// intermediate state the sweep's delta never actually left the
    /// aggregate in — such as one that is briefly non-empty and all-ready
    /// between forgetting the only blocking service and forgetting the
    /// last remaining ready one.
    fn apply_membership(&mut self, discovered: &[(String, String)], departed: &[(String, String)]) -> bool {
        for (service, _) in discovered {
            self.mark_discovered(service);
        }

        for (service, _) in departed {
            self.forget(service);
        }

        self.refresh()
    }
}

/// Renders one event from a supervisor or the re-discovery sweep.
///
/// [`Event::Service`]'s `Message`, `Reattached`, and `Lost` are three of
/// the four observable events; [`Event::Membership`] is the fourth. Each
/// always calls `output::data` so JSON mode sees it exactly as received,
/// even when nothing is worth printing in human mode. A `Message` first
/// calls [`Aggregate::mark_reported`] once, unblocking `service` regardless
/// of how many scopes the message carries, then diffs its scopes against
/// the merged `aggregate` and prints one transition line per scope that
/// actually changed, then prints [`render::print_all_ready_line`] if that
/// message's transitions crossed the whole set into ready; `Reattached`
/// prints one dim [`render::print_lifecycle_line`] only — it deliberately
/// leaves `service` blocked, since a reattached service must stay blocked
/// until its own next `Message` clears it, so a message from another
/// supervisor cannot cross the aggregate into ready off this service's
/// stale pre-disconnect scopes; `Lost` composes its cause and retry delay
/// via [`render::print_lost_line`].
///
/// `Membership` is one re-discovery sweep's whole membership delta: every
/// service it found newly registered, and every one it found missing, on either
/// side. [`Aggregate::apply_membership`] applies the whole delta and checks for
/// a readiness crossing exactly once, against the final state, before either
/// loop runs. Every service on either side then renders through
/// [`render::print_membership_line`], one line each, so a joined and a departed
/// service can never drift apart from one another. The lines print arrivals
/// before departures purely because that reads better; what makes that order
/// unable to affect the crossing check is that there is exactly one recheck for
/// the whole delta, not one per side or per service, not merely that the
/// recheck now happens first.
fn render_event(event: Event, aggregate: &mut Aggregate, scope_width: usize, alias_width: usize) {
    match event {
        Event::Service { service, alias, data } => match data {
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
        },
        Event::Membership { discovered, departed } => {
            let discovered_names: Vec<&str> = discovered.iter().map(|(service, _)| service.as_str()).collect();
            let departed_names: Vec<&str> = departed.iter().map(|(service, _)| service.as_str()).collect();

            let payload = MembershipPayload {
                event: EventKind::Membership,
                discovered: &discovered_names,
                departed: &departed_names,
            };

            output::data(&payload, false, format_args!(""), || {
                let crossed = aggregate.apply_membership(&discovered, &departed);

                for (_, alias) in &discovered {
                    render::print_membership_line(alias, alias_width, render::Membership::Discovered);
                }

                for (_, alias) in &departed {
                    render::print_membership_line(alias, alias_width, render::Membership::Gone);
                }

                if crossed {
                    render::print_all_ready_line();
                }
            });
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
    fn assign_newcomer_aliases_resolves_two_same_sweep_collisions_to_different_aliases() {
        // Both of these real FQNs derive "route" — a regression test for a
        // sweep that discovers them together, rather than picking two
        // synthetic names.
        let discovered = vec![
            "operators.route.operatorpb.v1.ReadinessService".to_owned(),
            "operators.route.neighbourpb.v1.ReadinessService".to_owned(),
        ];
        let mut aliases = BTreeMap::new();

        let assigned = assign_newcomer_aliases(discovered, &mut aliases);

        assert_eq!(2, assigned.len());
        // Whichever fallback spelling `assign_alias` picks, the two
        // newcomers must not end up naming the same alias.
        assert_ne!(assigned[0].1, assigned[1].1);
    }

    #[test]
    fn lost_event_keeps_the_raw_cause_with_no_composed_wording() {
        let event = Event::lost("svc", "alias", "boom", Duration::from_secs(5));

        let Event::Service { data, .. } = event else {
            panic!("expected a Service event");
        };
        let EventData::Lost { cause, retry_after } = data else {
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

    #[test]
    fn aggregate_forget_the_blocking_service_crosses_into_ready() {
        let reports = vec![report("a", vec![scope("rib", State::Ready)]), failed_report("b")];
        let mut aggregate = Aggregate::seed(&reports);
        assert!(!aggregate.all_ready);

        aggregate.forget("b");
        assert!(aggregate.refresh());

        // Still fully ready, but this is not a new crossing.
        assert!(!aggregate.refresh());
    }

    #[test]
    fn aggregate_forget_removes_only_the_departed_services_scopes() {
        // Both services share a scope name on purpose — the snapshot is
        // keyed by `(service, scope name)` precisely because operators
        // share names like `reconcile`, and a naive `retain` keyed on scope
        // name alone would wipe both.
        let reports = vec![
            report("a", vec![scope("reconcile", State::Ready)]),
            report("b", vec![scope("reconcile", State::Ready)]),
        ];
        let mut aggregate = Aggregate::seed(&reports);
        assert!(aggregate.all_ready);

        aggregate.forget("b");

        assert!(
            aggregate
                .snapshot
                .contains_key(&("a".to_owned(), "reconcile".to_owned()))
        );
        assert!(
            !aggregate
                .snapshot
                .contains_key(&("b".to_owned(), "reconcile".to_owned()))
        );

        // `a`'s readiness survives untouched: no crossing since the
        // aggregate was already ready and still is.
        assert!(!aggregate.refresh());
        assert!(aggregate.all_ready);
    }

    #[test]
    fn aggregate_forget_a_blocking_service_with_no_scopes_still_unblocks_it() {
        let reports = vec![report("a", vec![scope("rib", State::Ready)])];
        let mut aggregate = Aggregate::seed(&reports);
        assert!(aggregate.all_ready);

        aggregate.mark_discovered("b");
        assert!(!aggregate.all_ready);
        assert!(aggregate.blocking.contains("b"));

        aggregate.forget("b");
        assert!(!aggregate.blocking.contains("b"));
        assert!(aggregate.refresh());
    }

    #[test]
    fn aggregate_forget_the_only_service_leaves_the_aggregate_not_ready() {
        let reports = vec![report("a", vec![scope("rib", State::Ready)])];
        let mut aggregate = Aggregate::seed(&reports);
        assert!(aggregate.all_ready);

        aggregate.forget("a");
        assert!(!aggregate.refresh());
        assert!(!aggregate.all_ready);
    }

    #[test]
    fn aggregate_apply_membership_departing_the_only_blocker_and_the_last_ready_service_together_does_not_cross() {
        // One sweep departing both the only blocking service and the last
        // remaining ready service must leave the aggregate empty, never
        // crossed. `b` (the blocker) is listed ahead of `a` (the last
        // ready service) on purpose — that is the order that would leave
        // a non-empty, all-ready snapshot if the readiness recheck ran
        // after `b`'s departure alone, so this must go red if
        // `apply_membership` is changed to recheck inside its departure
        // loop instead of once at the end.
        let reports = vec![report("a", vec![scope("rib", State::Ready)]), failed_report("b")];
        let mut aggregate = Aggregate::seed(&reports);
        assert!(!aggregate.all_ready);

        let departed = vec![("b".to_owned(), "b".to_owned()), ("a".to_owned(), "a".to_owned())];
        assert!(!aggregate.apply_membership(&[], &departed));
        assert!(!aggregate.all_ready);
    }

    #[test]
    fn aggregate_apply_membership_departing_the_blocker_while_discovering_a_newcomer_does_not_cross() {
        // A newcomer blocks the banner until its own first message, so a
        // sweep that both loses its only blocker and discovers a newcomer
        // must not cross — this must go red if `apply_membership` drops
        // the loop that records arrivals via `mark_discovered` before the
        // recheck.
        let reports = vec![report("a", vec![scope("rib", State::Ready)]), failed_report("b")];
        let mut aggregate = Aggregate::seed(&reports);

        let discovered = vec![("c".to_owned(), "c".to_owned())];
        let departed = vec![("b".to_owned(), "b".to_owned())];
        assert!(!aggregate.apply_membership(&discovered, &departed));
    }

    #[test]
    fn aggregate_apply_membership_departing_only_the_blocker_crosses() {
        // With nothing else arriving in the same sweep and a ready service
        // remaining, departing the only blocking service does cross the
        // aggregate into ready — the crossing is not simply suppressed
        // unconditionally.
        let reports = vec![report("a", vec![scope("rib", State::Ready)]), failed_report("b")];
        let mut aggregate = Aggregate::seed(&reports);

        let departed = vec![("b".to_owned(), "b".to_owned())];
        assert!(aggregate.apply_membership(&[], &departed));
    }
}
