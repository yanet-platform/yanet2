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

use tonic::codec::CompressionEncoding;
use ynpb::pb::{gateway_client::GatewayClient, ListServicesRequest};

use crate::{
    client::{Connection, ConnectionArgs, LayeredChannel, Service},
    config,
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
}
