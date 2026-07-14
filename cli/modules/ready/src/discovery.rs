//! Gateway-driven discovery of readiness services.
//!
//! The gateway registry holds one entry per registered gRPC service name, so
//! the readiness services are simply the entries whose name ends in
//! `ReadinessService`: the built-in one plus one per running operator. That
//! list is what the CLI probes when no service is named, what a short alias
//! resolves against, and what an error hint suggests.

use tonic::codec::CompressionEncoding;
use ync::{
    client::{Connection, LayeredChannel, Service},
    errors::Error,
};
use ynpb::pb::{ListServicesRequest, gateway_client::GatewayClient};

/// Fully-qualified name of the gateway registry service.
///
/// Taken from the `Gateway` service declaration in
/// `controlplane/ynpb/v1/gateway.proto`, the wire contract — not from the
/// generated tonic module name, which is a Rust-side artefact.
const GATEWAY_SERVICE: &str = "controlplane.ynpb.v1.Gateway";

/// Trailing segment of every readiness service's fully-qualified name.
const READINESS_SERVICE: &str = "ReadinessService";

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

/// Lists the fully-qualified names of the readiness services registered with
/// the gateway, sorted.
///
/// The sort makes the aggregate probe order — and hence the rendered blocks —
/// stable across runs, since the registry itself is unordered.
pub async fn list_readiness_services(connection: &Connection) -> Result<Vec<String>, Error> {
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
        .filter(|name| is_readiness_service(name))
        .map(str::to_owned)
        .collect();

    services.sort();
    services.dedup();

    Ok(services)
}

/// Reports whether `name` is a readiness service's fully-qualified name.
///
/// The last dot-separated segment must be exactly `ReadinessService`, so a
/// service that merely mentions readiness elsewhere in its name is not
/// mistaken for one.
fn is_readiness_service(name: &str) -> bool {
    name.rsplit('.').next() == Some(READINESS_SERVICE)
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

#[cfg(test)]
mod test {
    use super::*;

    fn services() -> Vec<String> {
        vec![
            "controlplane.ynpb.v1.ReadinessService".to_owned(),
            "operators.decap.operatorpb.v1.ReadinessService".to_owned(),
            "operators.forward.operatorpb.v1.ReadinessService".to_owned(),
            "operators.pipeline.operatorpb.v1.ReadinessService".to_owned(),
            "operators.route.operatorpb.v1.ReadinessService".to_owned(),
        ]
    }

    #[test]
    fn readiness_service_is_recognised_by_its_last_segment() {
        assert!(is_readiness_service("controlplane.ynpb.v1.ReadinessService"));
        assert!(is_readiness_service("operators.route.operatorpb.v1.ReadinessService"));
    }

    #[test]
    fn other_services_are_not_readiness_services() {
        assert!(!is_readiness_service("controlplane.ynpb.v1.Gateway"));
        assert!(!is_readiness_service("operators.route.operatorpb.v1.MetricsService"));
        assert!(!is_readiness_service(
            "operators.route.operatorpb.v1.ReadinessServiceV2"
        ));
        assert!(!is_readiness_service("ReadinessService.operators.route"));
    }

    #[test]
    fn alias_resolves_to_the_only_match() {
        assert_eq!(
            Resolution::Resolved("operators.route.operatorpb.v1.ReadinessService".to_owned()),
            resolve_alias("route", &services())
        );
    }

    #[test]
    fn alias_resolution_ignores_case() {
        assert_eq!(
            Resolution::Resolved("operators.decap.operatorpb.v1.ReadinessService".to_owned()),
            resolve_alias("DeCap", &services())
        );
    }

    #[test]
    fn package_segment_resolves_the_built_in_service() {
        assert_eq!(
            Resolution::Resolved("controlplane.ynpb.v1.ReadinessService".to_owned()),
            resolve_alias("controlplane", &services())
        );
    }

    #[test]
    fn alias_matching_several_services_is_ambiguous() {
        assert_eq!(
            Resolution::Ambiguous(vec![
                "operators.decap.operatorpb.v1.ReadinessService".to_owned(),
                "operators.forward.operatorpb.v1.ReadinessService".to_owned(),
                "operators.pipeline.operatorpb.v1.ReadinessService".to_owned(),
                "operators.route.operatorpb.v1.ReadinessService".to_owned(),
            ]),
            resolve_alias("operatorpb", &services())
        );
    }

    #[test]
    fn unmatched_alias_is_unknown() {
        assert_eq!(Resolution::Unknown, resolve_alias("balancer", &services()));
    }

    #[test]
    fn any_alias_is_unknown_without_discovered_services() {
        assert_eq!(Resolution::Unknown, resolve_alias("route", &[]));
    }
}
