//! Gateway-driven discovery of registered gRPC services.
//!
//! The gateway registry holds one entry per registered gRPC service name, so
//! a family of services — readiness, metrics, and so on — is simply the
//! entries whose name ends in that family's own trailing segment (e.g.
//! `ReadinessService`, `MetricsService`): the built-in one plus one per
//! running operator. That list is what a CLI probes when no service is
//! named, what a short alias resolves against, and what an error hint
//! suggests.

use core::time::Duration;
use std::collections::BTreeMap;

use clap::Command;
use clap_complete::engine::CompletionCandidate;
use tonic::codec::CompressionEncoding;
use ynpb::pb::{ListServicesRequest, gateway_client::GatewayClient};

use crate::{
    client::{Connection, ConnectionArgs, LayeredChannel, Service},
    completion, config,
    errors::{Error, ErrorKind},
};

/// Fully-qualified name of the gateway registry service.
///
/// Taken from the `Gateway` service declaration in
/// `controlplane/ynpb/v1/gateway.proto`, the wire contract — not from the
/// generated tonic module name, which is a Rust-side artefact.
const GATEWAY_SERVICE: &str = "controlplane.ynpb.v1.Gateway";

/// Budget for a best-effort gateway lookup: an error hint, a shell completion.
///
/// Such a lookup only enriches something the CLI can do without, so it must
/// never be the thing that hangs: a flag-validation error or a tab press would
/// otherwise stall against a slow or blackholed endpoint until the transport
/// gives up. Exceeding the budget is simply a failed lookup — the hint or the
/// candidate list is dropped and the caller carries on. A probe the user did
/// ask for gets no such budget: there a slow gateway must surface as the error
/// it is.
pub const DISCOVERY_TIMEOUT: Duration = Duration::from_secs(1);

/// Outcome of resolving a short alias against the discovered services.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Resolution {
    /// Exactly one service matched the alias.
    Resolved(String),
    /// Several services matched; these are the candidates.
    Ambiguous(Vec<String>),
    /// No service matched.
    Unknown,
}

/// A family of gRPC services sharing one trailing name segment (readiness,
/// metrics, and so on), and the vocabulary a CLI needs to probe, resolve and
/// hint about it.
#[derive(Debug, Clone, Copy)]
pub struct Family {
    /// Trailing segment of every service's fully-qualified name (e.g.
    /// `ReadinessService`).
    suffix: &'static str,
    /// User-facing verb every error from this family carries (e.g.
    /// `"ready"`).
    action: &'static str,
    /// Noun used in captions and messages (e.g. `"readiness"`).
    noun: &'static str,
}

impl Family {
    pub const fn new(suffix: &'static str, action: &'static str, noun: &'static str) -> Self {
        Self { suffix, action, noun }
    }

    pub fn hint(&self, services: &[String]) -> String {
        services_hint(
            &format!("available {} services:", self.noun),
            &self.no_services(),
            services,
        )
    }

    fn no_services(&self) -> String {
        format!("no {} services are registered with the gateway", self.noun)
    }

    /// Rejects a blank service name before anything is discovered.
    ///
    /// An empty name is a substring of every service, so as an alias it
    /// matches them all — and with a single service of this family
    /// registered it would resolve to that one and hand its outcome to a
    /// caller who never named a service. That is bad input rather than a
    /// registry condition, so it is rejected as such, before any lookup.
    // The async siblings hide the same-sized `Result` inside their desugared
    // `Future` output, so only this synchronous method trips the lint.
    #[allow(clippy::result_large_err)]
    pub fn require_name(&self, endpoint: &str, name: &str) -> Result<(), Error> {
        if name.trim().is_empty() {
            return Err(Error::invalid_argument(
                self.action,
                endpoint,
                "service name must not be empty",
            ));
        }

        Ok(())
    }

    /// Lists the fully-qualified names of the registered services in this
    /// family, sorted.
    pub async fn list(&self, connection: &Connection) -> Result<Vec<String>, Error> {
        list_services(connection, self.suffix).await
    }

    /// Resolves a short alias against `services`.
    ///
    /// A name containing `.` is assumed already fully qualified and returned
    /// unchanged. An alias that matches nothing describes the same
    /// operational condition as a fully-qualified name the gateway does not
    /// know — that service is not registered, its operator down or not yet
    /// up — because the alias is resolved against the live registry, so it
    /// carries the same kind the gateway's own answer would map to. An
    /// ambiguous alias, in contrast, really is bad input.
    // The async siblings hide the same-sized `Result` inside their desugared
    // `Future` output, so only this synchronous method trips the lint.
    #[allow(clippy::result_large_err)]
    pub fn resolve_among(&self, endpoint: &str, name: &str, services: &[String]) -> Result<String, Error> {
        if name.contains('.') {
            return Ok(name.to_owned());
        }

        match resolve_alias(name, services) {
            Resolution::Resolved(resolved) => Ok(resolved),
            Resolution::Ambiguous(candidates) => {
                let message = format!("service name \"{name}\" is ambiguous");
                let hint = services_hint(
                    &format!("matching {} services:", self.noun),
                    &self.no_services(),
                    &candidates,
                );

                Err(Error::invalid_argument(self.action, endpoint, message).with_hint(hint))
            }
            Resolution::Unknown => {
                let message = format!("unknown {} service \"{name}\"", self.noun);

                Err(
                    Error::new(ErrorKind::ServiceUnregistered, self.action, endpoint, message)
                        .with_hint(self.hint(services)),
                )
            }
        }
    }

    /// Resolves `name` against the services discovered over `connection`.
    ///
    /// A name containing `.` is assumed already fully qualified and returned
    /// without any lookup.
    pub async fn resolve(&self, connection: &Connection, name: &str) -> Result<String, Error> {
        if name.contains('.') {
            return Ok(name.to_owned());
        }

        let services = self.list(connection).await?;

        self.resolve_among(connection.endpoint(), name, &services)
    }

    /// Adds the discovered services to `err`'s hint when it reads like the
    /// named service is simply not registered.
    ///
    /// Best-effort: a failed discovery leaves `err` exactly as it was, so a
    /// gateway that is down still surfaces the original probe error rather
    /// than a second, more confusing one.
    pub async fn suggest(&self, connection: &Connection, err: Error) -> Error {
        if !matches!(err.kind(), ErrorKind::ServiceUnregistered | ErrorKind::NotFound) {
            return err;
        }

        match self.list(connection).await {
            Ok(services) => err.with_hint(self.hint(&services)),
            Err(..) => err,
        }
    }

    /// Adds the discovered services to `err`'s hint, discovering them fresh
    /// over their own best-effort, budgeted connection.
    ///
    /// For an error raised before any [`Connection`] exists — such as no
    /// service named at all — where [`Family::suggest`] has nothing to reuse.
    pub async fn suggest_within(&self, args: &ConnectionArgs, err: Error) -> Error {
        match discover_within(args, self.suffix, DISCOVERY_TIMEOUT).await {
            Ok(services) => err.with_hint(self.hint(&services)),
            Err(..) => err,
        }
    }

    /// Completion candidates for a positional naming a service of this
    /// family: the services the gateway currently knows.
    ///
    /// Strictly best-effort — a tab-completion must never print an error nor
    /// hang — so a gateway that is down, slow or refusing us auth yields no
    /// candidates at all, [`DISCOVERY_TIMEOUT`] covering the slow case. The
    /// endpoint is recovered from the connection flags the user has actually
    /// typed so far, `YANET_ENDPOINT` included as the fallback.
    pub fn candidates(&self, command: impl FnOnce() -> Command) -> Vec<CompletionCandidate> {
        let args = completion::connection_args(command);

        candidates(&args, self.suffix, DISCOVERY_TIMEOUT)
            .into_iter()
            .map(CompletionCandidate::new)
            .collect()
    }
}

/// Lists the fully-qualified names of the services registered with the
/// gateway whose last dot-separated segment is `suffix`, sorted.
///
/// The sort makes probing order — and hence rendered blocks — stable across
/// runs, since the registry itself is unordered.
pub async fn list_services(connection: &Connection, suffix: &str) -> Result<Vec<String>, Error> {
    let mut service = Service::new(connection, GATEWAY_SERVICE, |channel: LayeredChannel| {
        GatewayClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip)
    });

    let response = service
        .client()
        .list_services(ListServicesRequest {})
        .await
        .map_err(service.status("discover"))?
        .into_inner();

    let mut services: Vec<String> = response
        .services
        .iter()
        .filter_map(|registered| registered.backend.as_ref())
        .map(|backend| backend.name.as_str())
        .filter(|name| has_suffix(name, suffix))
        .map(str::to_owned)
        .collect();

    services.sort();
    services.dedup();

    Ok(services)
}

/// Reports whether `name`'s last dot-separated segment is exactly `suffix`,
/// so that a service merely mentioning it elsewhere in its name is not
/// mistaken for a match.
fn has_suffix(name: &str, suffix: &str) -> bool {
    name.rsplit('.').next() == Some(suffix)
}

/// Resolves a short alias (e.g. `route`) against the discovered `services`.
///
/// A service matches when its name contains the alias as a case-insensitive
/// substring, which lets both an operator name (`forward`) and a package
/// segment (`controlplane`) select their service without spelling out the
/// whole FQN.
pub fn resolve_alias(alias: &str, services: &[String]) -> Resolution {
    let alias = alias.to_lowercase();

    let mut matched: Vec<String> = services
        .iter()
        .filter(|service| service.to_lowercase().contains(&alias))
        .cloned()
        .collect();

    match matched.len() {
        0 => Resolution::Unknown,
        1 => Resolution::Resolved(matched.remove(0)),
        _ => Resolution::Ambiguous(matched),
    }
}

/// Derives a short display alias from a service FQN — the inverse of
/// [`resolve_alias`].
///
/// Drops the trailing segment (the service name itself, e.g.
/// `ReadinessService`), then discards any remaining segment that is a bare
/// version marker (`v1`, `v2`, …) or ends in `pb` (`operatorpb`, `ynpb`,
/// …), and takes the last segment left. `controlplane.ynpb.v1.ReadinessService`
/// becomes `controlplane`; `operators.route.operatorpb.v1.ReadinessService`
/// becomes `route`. Falls back to the full `fqn` when nothing remains, e.g.
/// a bare `ReadinessService` with no package at all.
pub fn derive_alias(fqn: &str) -> String {
    let segments: Vec<&str> = fqn.split('.').collect();
    let package = &segments[..segments.len().saturating_sub(1)];

    package
        .iter()
        .rev()
        .find(|segment| !is_version_segment(segment) && !segment.ends_with("pb"))
        .map(|segment| (*segment).to_owned())
        .unwrap_or_else(|| fqn.to_owned())
}

/// Reports whether `segment` is a bare version marker: `v` followed by one
/// or more ASCII digits and nothing else.
fn is_version_segment(segment: &str) -> bool {
    let Some(digits) = segment.strip_prefix('v') else {
        return false;
    };

    !digits.is_empty() && digits.bytes().all(|byte| byte.is_ascii_digit())
}

/// Derives a display alias for every service in `services`, keyed by the
/// service's own FQN.
///
/// Two services whose derived alias collides both fall back to their full
/// FQN — a short alias is only useful when it is unambiguous.
pub fn alias_map(services: &[String]) -> BTreeMap<String, String> {
    let mut aliases: BTreeMap<String, String> = services
        .iter()
        .map(|service| (service.clone(), derive_alias(service)))
        .collect();

    let mut counts: BTreeMap<String, usize> = BTreeMap::new();
    for alias in aliases.values() {
        *counts.entry(alias.clone()).or_insert(0) += 1;
    }

    for (service, alias) in aliases.iter_mut() {
        if counts.get(alias.as_str()).copied().unwrap_or(0) > 1 {
            *alias = service.clone();
        }
    }

    aliases
}

/// Connects to the gateway afresh and lists the services whose last segment
/// is `suffix`, within `budget`.
///
/// This is the best-effort half of discovery, and the only half that has to
/// establish its own connection: every caller reaches an endpoint nothing has
/// spoken to yet, purely to enrich something — a hint, a completion — which is
/// why the budget is a parameter here rather than fixed at the call sites.
/// Discovery the user asked for runs over the [`Connection`] the probe
/// already established and is deliberately unbounded.
pub async fn discover_within(args: &ConnectionArgs, suffix: &str, budget: Duration) -> Result<Vec<String>, Error> {
    // Resolved once, up front, and reused by both the connect attempt and
    // the timeout arm: a bad file or an unknown alias is a config error
    // reported as such, and the label must not shift, or the file be read
    // twice, between the attempt and a timeout.
    let settings = config::resolve(args, &config::Sources::from_process_env())
        .map_err(|err| Error::from_config(err, "discover", args.endpoint.clone()))?;
    let endpoint = settings.label();

    let lookup = async {
        let connection = Connection::connect_settings(&settings, "discover").await?;

        list_services(&connection, suffix).await
    };

    match tokio::time::timeout(budget, lookup).await {
        Ok(services) => services,
        Err(..) => {
            let formatted_budget = humantime::format_duration(budget);
            let message = format!("gateway did not answer within {formatted_budget}");

            Err(Error::new(ErrorKind::Unavailable, "discover", endpoint, message))
        }
    }
}

/// Formats a hint listing `services` one per line under a greyed `caption`.
///
/// The caption is dimmed so the fully-qualified names it introduces stay the
/// prominent part, and `output::failure` aligns every line after the first
/// under it, so the names come out as a column. When no service is
/// registered the plain `empty_message` is returned in place of the list.
pub fn services_hint(caption: &str, empty_message: &str, services: &[String]) -> String {
    if services.is_empty() {
        return empty_message.to_owned();
    }

    let mut hint = crate::output::dim(caption);

    for service in services {
        hint.push('\n');
        hint.push_str(service);
    }

    hint
}

/// Best-effort discovery for shell completion: the services whose last
/// segment is `suffix`, or an empty list on any failure.
///
/// Strictly best-effort — a tab-completion must never print an error nor hang
/// — so a gateway that is down, slow or refusing us auth yields no candidates
/// at all, `budget` covering the slow case.
pub fn candidates(args: &ConnectionArgs, suffix: &str, budget: Duration) -> Vec<String> {
    let args = args.clone();
    let suffix = suffix.to_owned();

    // Isolate discovery from any runtime the caller may already own.
    //
    // Direct lifecycle callers may request completion from Tokio. Blocking
    // another runtime there would panic; a worker thread also turns such a
    // panic into an empty candidate list like every other failure.
    std::thread::spawn(move || {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .enable_all()
            .build()
            .ok()?;

        runtime.block_on(discover_within(&args, &suffix, budget)).ok()
    })
    .join()
    .ok()
    .flatten()
    .unwrap_or_default()
}

#[cfg(test)]
mod test {
    use super::*;

    fn services(suffix: &str) -> Vec<String> {
        vec![
            format!("controlplane.ynpb.v1.{suffix}"),
            format!("operators.decap.operatorpb.v1.{suffix}"),
            format!("operators.forward.operatorpb.v1.{suffix}"),
            format!("operators.pipeline.operatorpb.v1.{suffix}"),
            format!("operators.route.operatorpb.v1.{suffix}"),
        ]
    }

    #[test]
    fn service_is_recognised_by_its_last_segment() {
        assert!(has_suffix("controlplane.ynpb.v1.ReadinessService", "ReadinessService"));
        assert!(has_suffix(
            "operators.route.operatorpb.v1.MetricsService",
            "MetricsService"
        ));
    }

    #[test]
    fn other_services_do_not_match() {
        assert!(!has_suffix("controlplane.ynpb.v1.Gateway", "ReadinessService"));
        assert!(!has_suffix(
            "operators.route.operatorpb.v1.MetricsService",
            "ReadinessService"
        ));
        assert!(!has_suffix(
            "operators.route.operatorpb.v1.ReadinessServiceV2",
            "ReadinessService"
        ));
        assert!(!has_suffix("ReadinessService.operators.route", "ReadinessService"));
    }

    #[test]
    fn alias_resolves_to_the_only_match() {
        let services = services("ReadinessService");

        assert_eq!(
            Resolution::Resolved("operators.route.operatorpb.v1.ReadinessService".to_owned()),
            resolve_alias("route", &services)
        );
    }

    #[test]
    fn alias_resolution_ignores_case() {
        let services = services("MetricsService");

        assert_eq!(
            Resolution::Resolved("operators.decap.operatorpb.v1.MetricsService".to_owned()),
            resolve_alias("DeCap", &services)
        );
    }

    #[test]
    fn package_segment_resolves_the_built_in_service() {
        let services = services("ReadinessService");

        assert_eq!(
            Resolution::Resolved("controlplane.ynpb.v1.ReadinessService".to_owned()),
            resolve_alias("controlplane", &services)
        );
    }

    #[test]
    fn alias_matching_several_services_is_ambiguous() {
        let services = services("MetricsService");

        assert_eq!(
            Resolution::Ambiguous(vec![
                "operators.decap.operatorpb.v1.MetricsService".to_owned(),
                "operators.forward.operatorpb.v1.MetricsService".to_owned(),
                "operators.pipeline.operatorpb.v1.MetricsService".to_owned(),
                "operators.route.operatorpb.v1.MetricsService".to_owned(),
            ]),
            resolve_alias("operatorpb", &services)
        );
    }

    #[test]
    fn unmatched_alias_is_unknown() {
        let services = services("ReadinessService");

        assert_eq!(Resolution::Unknown, resolve_alias("balancer", &services));
    }

    #[test]
    fn any_alias_is_unknown_without_discovered_services() {
        assert_eq!(Resolution::Unknown, resolve_alias("route", &[]));
    }

    #[test]
    fn services_hint_lists_each_service_after_the_caption() {
        let services = vec!["a.ReadinessService".to_owned(), "b.ReadinessService".to_owned()];

        let hint = services_hint("available:", "no services registered", &services);
        let lines: Vec<&str> = hint.lines().collect();

        assert_eq!(3, lines.len());
        assert!(lines[0].contains("available:"));
        assert_eq!("a.ReadinessService", lines[1]);
        assert_eq!("b.ReadinessService", lines[2]);
    }

    #[test]
    fn services_hint_states_the_registry_is_empty() {
        assert_eq!(
            "no services registered",
            services_hint("available:", "no services registered", &[])
        );
    }

    #[test]
    fn derive_alias_drops_version_and_pb_segments() {
        assert_eq!("controlplane", derive_alias("controlplane.ynpb.v1.ReadinessService"));
        assert_eq!("route", derive_alias("operators.route.operatorpb.v1.ReadinessService"));
    }

    #[test]
    fn derive_alias_handles_double_digit_versions() {
        assert_eq!("route", derive_alias("operators.route.operatorpb.v12.ReadinessService"));
    }

    #[test]
    fn derive_alias_falls_back_to_the_full_name_when_nothing_remains() {
        assert_eq!("ReadinessService", derive_alias("ReadinessService"));
        assert_eq!("v1.pb.ReadinessService", derive_alias("v1.pb.ReadinessService"));
    }

    #[test]
    fn alias_map_uses_the_derived_alias_when_unique() {
        let services = vec![
            "controlplane.ynpb.v1.ReadinessService".to_owned(),
            "operators.route.operatorpb.v1.ReadinessService".to_owned(),
        ];

        let aliases = alias_map(&services);

        assert_eq!(Some(&"controlplane".to_owned()), aliases.get(&services[0]));
        assert_eq!(Some(&"route".to_owned()), aliases.get(&services[1]));
    }

    #[test]
    fn alias_map_falls_back_to_the_fqn_on_collision() {
        let services = vec![
            "a.route.v1.ReadinessService".to_owned(),
            "b.route.v2.ReadinessService".to_owned(),
        ];

        let aliases = alias_map(&services);

        assert_eq!(Some(&services[0]), aliases.get(&services[0]));
        assert_eq!(Some(&services[1]), aliases.get(&services[1]));
    }

    const READINESS: Family = Family::new("ReadinessService", "ready", "readiness");

    #[test]
    fn test_resolve_among_fully_qualified_name_skips_lookup() {
        let resolved = READINESS
            .resolve_among("grpc://[::1]:8080", "already.qualified.ReadinessService", &[])
            .expect("a fully-qualified name never fails to resolve");

        assert_eq!("already.qualified.ReadinessService", resolved);
    }

    #[test]
    fn test_resolve_among_single_match_resolves() {
        let services = services("ReadinessService");

        let resolved = READINESS
            .resolve_among("grpc://[::1]:8080", "route", &services)
            .expect("a single match resolves");

        assert_eq!("operators.route.operatorpb.v1.ReadinessService", resolved);
    }

    #[test]
    fn test_resolve_among_ambiguous_alias_lists_every_candidate() {
        let services = services("ReadinessService");

        let err = READINESS
            .resolve_among("grpc://[::1]:8080", "operatorpb", &services)
            .expect_err("several services share the operatorpb segment");

        assert_eq!(ErrorKind::InvalidArgument, err.kind());
        let hint = err.hint().expect("an ambiguous alias carries a hint");
        for service in &services[1..] {
            assert!(hint.contains(service.as_str()), "hint missing {service}: {hint}");
        }
    }

    #[test]
    fn test_resolve_among_unknown_alias_hints_available_services() {
        let services = services("ReadinessService");

        let err = READINESS
            .resolve_among("grpc://[::1]:8080", "balancer", &services)
            .expect_err("no service matches the balancer alias");

        assert_eq!(ErrorKind::ServiceUnregistered, err.kind());
        let hint = err.hint().expect("an unknown alias carries the available services");
        for service in &services {
            assert!(hint.contains(service.as_str()), "hint missing {service}: {hint}");
        }
    }

    #[test]
    fn test_hint_on_empty_services_states_the_registry_is_empty() {
        assert_eq!(
            "no readiness services are registered with the gateway",
            READINESS.hint(&[])
        );
    }

    #[test]
    fn test_require_name_rejects_empty_and_whitespace() {
        assert_eq!(
            ErrorKind::InvalidArgument,
            READINESS
                .require_name("grpc://[::1]:8080", "")
                .expect_err("an empty name is rejected")
                .kind()
        );
        assert_eq!(
            ErrorKind::InvalidArgument,
            READINESS
                .require_name("grpc://[::1]:8080", "   ")
                .expect_err("a whitespace-only name is rejected")
                .kind()
        );
    }

    #[test]
    fn test_require_name_accepts_a_real_name() {
        READINESS
            .require_name("grpc://[::1]:8080", "route")
            .expect("a non-blank name is accepted");
    }
}
