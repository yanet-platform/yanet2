//! Generic metrics probe CLI.

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use commonpb::pb::{GetMetricsRequest, GetMetricsResponse, Histogram, Label, Metric, MetricTag, metric::Value};
use tabled::Tabled;
use ync::{
    client::{self, Connection, ConnectionArgs},
    completion,
    discovery::{self, Resolution},
    errors::{Error, ErrorKind},
    output::{self, CommonFormat},
};

/// Trailing segment of every metrics service's fully-qualified name.
const METRICS_SERVICE: &str = "MetricsService";

/// Caption of the hint that lists every discovered metrics service.
const AVAILABLE_SERVICES: &str = "available metrics services:";

/// Message used in place of the hint when no metrics service is registered.
const NO_SERVICES: &str = "no metrics services are registered with the gateway";

/// Generic metrics probe — calls `GetMetrics` on any `MetricsService`.
///
/// Connects to the gateway and invokes `/<FQN>/GetMetrics` using tonic's
/// low-level dynamic dispatcher with the shared `commonpb` message types. No
/// per-service generated client is needed. The service to probe is resolved
/// against the gateway registry. A single service's metrics can already be a
/// large payload, so naming none is a usage error whose hint lists the
/// available services rather than dumping them all.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    /// Metrics service to probe: either a fully-qualified gRPC service name
    /// (e.g. `operators.route.operatorpb.v1.MetricsService`) or a short
    /// alias matched against the discovered services (e.g. `route`).
    ///
    /// Omitting it lists the available services as a usage error.
    #[arg(value_name = "SERVICE", add = ArgValueCandidates::new(service_candidates))]
    pub name: Option<String>,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Server-side tag filter: `NAME=VALUE`, repeatable — a metric is
    /// returned only if it satisfies every tag (logical AND).
    ///
    /// An empty `VALUE` requires the label to be absent, `*` requires it to
    /// be present with any value, and any other string requires an exact
    /// value match. Shells typically expand `*`, so quote it, e.g.
    /// `--tag 'config=*'`.
    #[arg(long = "tag", short = 't', value_name = "NAME=VALUE", global = true)]
    pub tags: Vec<String>,
    /// Output format.
    #[arg(long, value_enum, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

pub fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

/// Run the metrics probe against the named service.
///
/// A service must be named: probing every one is never attempted, since a
/// single service can already emit an enormous number of metrics. Naming
/// none is therefore a usage error whose hint lists the services to choose
/// from.
async fn run(cmd: Cmd) -> Result<(), Error> {
    let Some(name) = cmd.name.clone() else {
        return Err(require_service(&cmd).await);
    };

    let endpoint = client::resolve_label(&cmd.connection, "metrics")?;

    if is_blank(&name) {
        return Err(Error::invalid_argument(
            "metrics",
            endpoint.clone(),
            "service name must not be empty",
        ));
    }

    let tags = cmd
        .tags
        .iter()
        .map(|entry| parse_tag(entry))
        .collect::<Result<Vec<_>, String>>()
        .map_err(|message| Error::invalid_argument("metrics", endpoint, message))?;

    let connection = Connection::connect_for(&cmd.connection, "metrics").await?;

    let name = if name.contains('.') {
        name
    } else {
        resolve_alias(&connection, &name).await?
    };

    run_probe(&connection, &name, tags).await
}

/// Parses a `NAME=VALUE` tag entry into a [`MetricTag`].
///
/// An entry without `=` is bad input — the message is turned into an
/// invalid-argument [`Error`] once at the call site.
fn parse_tag(entry: &str) -> Result<MetricTag, String> {
    let Some((name, value)) = entry.split_once('=') else {
        return Err(format!("invalid --tag \"{entry}\": expected NAME=VALUE"));
    };

    Ok(MetricTag {
        name: name.to_string(),
        value: value.to_string(),
    })
}

/// Whether a service name is empty or whitespace only.
///
/// An empty name is a substring of every service, so as an alias it matches
/// them all — and with a single metrics service registered it would resolve
/// to that one and dump its metrics for a caller who never asked for a probe.
/// `yanet-cli metrics "$SERVICE"` on an unset variable is bad input rather
/// than a registry condition, so it is rejected as such, and before anything
/// is discovered.
fn is_blank(name: &str) -> bool {
    name.trim().is_empty()
}

/// Builds the error shown when the command names no metrics service.
///
/// A service is required — probing every one is never attempted — so this is
/// an invalid-argument error, and the discovered services become its hint so
/// the caller can pick one. Discovery is best-effort: a gateway that is down
/// or slower than the budget simply leaves the error hintless, since the
/// usage mistake stands on its own.
async fn require_service(cmd: &Cmd) -> Error {
    let endpoint = match client::resolve_label(&cmd.connection, "metrics") {
        Ok(endpoint) => endpoint,
        Err(err) => return err,
    };
    let err = Error::invalid_argument("metrics", endpoint, "no metrics service specified");

    match discovery::discover_within(&cmd.connection, METRICS_SERVICE, discovery::DISCOVERY_TIMEOUT).await {
        Ok(services) => err.with_hint(discovery::services_hint(AVAILABLE_SERVICES, NO_SERVICES, &services)),
        Err(..) => err,
    }
}

/// Probes one metrics service's `GetMetrics` over the shared connection, and
/// suggests the services that do exist when the probe finds none under that
/// name.
async fn run_probe(connection: &Connection, name: &str, tags: Vec<MetricTag>) -> Result<(), Error> {
    let result = connection
        .invoke_unary::<_, GetMetricsResponse>("metrics", name, "GetMetrics", GetMetricsRequest { tags })
        .await;

    let response = match result {
        Ok(response) => response,
        Err(err) => return Err(suggest_services(connection, err).await),
    };

    let total = response.metrics.len();

    output::data(
        || &response.metrics,
        || {
            if response.metrics.is_empty() {
                output::empty(format_args!("No metrics found for {name}."));
                return;
            }

            let mut scalars: Vec<&Metric> = response
                .metrics
                .iter()
                .filter(|m| !matches!(&m.value, Some(Value::Histogram(_))))
                .collect();
            scalars.sort_by(|a, b| a.name.cmp(&b.name));

            let mut histograms: Vec<&Metric> = response
                .metrics
                .iter()
                .filter(|m| matches!(&m.value, Some(Value::Histogram(_))))
                .collect();
            histograms.sort_by(|a, b| a.name.cmp(&b.name));

            if !scalars.is_empty() {
                let rows: Vec<MetricRow> = scalars.iter().map(|m| MetricRow::from(*m)).collect();
                ync::display::print_table_from_entries(rows);
            }

            if !histograms.is_empty() {
                println!();
                println!("Histograms");
                println!();

                for metric in &histograms {
                    if let Some(Value::Histogram(h)) = &metric.value {
                        print_histogram(&metric.name, &metric.labels, h);
                    }
                }
            }

            println!("summary: {total} metrics");
        },
    );

    Ok(())
}

/// Resolves a short alias against the services the gateway knows.
///
/// An alias that matches nothing describes the same operational condition as
/// a fully-qualified name the gateway does not know — that metrics service is
/// not registered, its operator down or not yet up — because the alias is
/// resolved against the live registry. It therefore carries the same kind the
/// gateway's own answer would map to, so that a monitoring script gets one
/// exit code for both spellings of the condition. An ambiguous alias, in
/// contrast, really is bad input.
async fn resolve_alias(connection: &Connection, alias: &str) -> Result<String, Error> {
    let services = discovery::list_services(connection, METRICS_SERVICE).await?;
    let endpoint = connection.endpoint();

    match discovery::resolve_alias(alias, &services) {
        Resolution::Resolved(name) => Ok(name),
        Resolution::Ambiguous(candidates) => {
            let message = format!("service name \"{alias}\" is ambiguous");

            Err(
                Error::invalid_argument("metrics", endpoint, message).with_hint(discovery::services_hint(
                    "matching metrics services:",
                    NO_SERVICES,
                    &candidates,
                )),
            )
        }
        Resolution::Unknown => {
            let message = format!("unknown metrics service \"{alias}\"");

            Err(Error::new(ErrorKind::ServiceUnregistered, "metrics", endpoint, message)
                .with_hint(discovery::services_hint(AVAILABLE_SERVICES, NO_SERVICES, &services)))
        }
    }
}

/// Adds the discovered services to `err`'s hint when it reads like the named
/// service is simply not registered.
///
/// Best-effort: a failed discovery leaves `err` exactly as it was, so a
/// gateway that is down still surfaces the original probe error rather than a
/// second, more confusing one.
async fn suggest_services(connection: &Connection, err: Error) -> Error {
    if !matches!(err.kind(), ErrorKind::ServiceUnregistered | ErrorKind::NotFound) {
        return err;
    }

    match discovery::list_services(connection, METRICS_SERVICE).await {
        Ok(services) => err.with_hint(discovery::services_hint(AVAILABLE_SERVICES, NO_SERVICES, &services)),
        Err(..) => err,
    }
}

/// Completion candidates for the service positional: the metrics services
/// the gateway currently knows.
///
/// Strictly best-effort — a tab-completion must never print an error nor hang
/// — so a gateway that is down, slow or refusing us auth yields no candidates
/// at all, `discovery::DISCOVERY_TIMEOUT` covering the slow case. The
/// endpoint is recovered from the connection flags the user has actually
/// typed so far, `YANET_ENDPOINT` included as the fallback.
fn service_candidates() -> Vec<CompletionCandidate> {
    let connection = completion::connection_args(Cmd::command);

    discovery::candidates(&connection, METRICS_SERVICE, discovery::DISCOVERY_TIMEOUT)
        .into_iter()
        .map(CompletionCandidate::new)
        .collect()
}

/// A displayable row for the metrics table.
#[derive(Debug, Tabled)]
pub struct MetricRow {
    #[tabled(rename = "Name")]
    pub name: String,
    #[tabled(rename = "Labels")]
    pub labels: String,
    #[tabled(rename = "Type")]
    pub kind: String,
    #[tabled(rename = "Value")]
    pub value: String,
}

impl From<&Metric> for MetricRow {
    fn from(m: &Metric) -> Self {
        let labels = {
            let s = format_labels(&m.labels);
            if s.is_empty() { "-".to_string() } else { s }
        };

        let (kind, value) = match &m.value {
            Some(Value::Counter(c)) => ("counter".to_string(), c.to_string()),
            Some(Value::Gauge(g)) => ("gauge".to_string(), g.to_string()),
            Some(Value::Histogram(h)) => ("histogram".to_string(), format!("count={}", h.total_count)),
            None => ("unknown".to_string(), "-".to_string()),
        };

        Self {
            name: m.name.clone(),
            labels,
            kind,
            value,
        }
    }
}

/// Returns the `k=v, k=v` join of `labels`, or an empty string when `labels`
/// is empty.
fn format_labels(labels: &[Label]) -> String {
    labels
        .iter()
        .map(|l| format!("{}={}", l.name, l.value))
        .collect::<Vec<_>>()
        .join(", ")
}

/// Formats `value` as a human-readable bound string.
///
/// `+Inf`/`-Inf` become `"inf"`/`"-inf"`, whole numbers become their integer
/// form, and all other values use the default `f64` display.
fn format_bound(value: f64) -> String {
    if value.is_infinite() {
        if value.is_sign_negative() {
            return "-inf".to_string();
        }

        return "inf".to_string();
    }

    if value.fract() == 0.0 {
        return format!("{}", value as i64);
    }

    format!("{value}")
}

/// Prints a single histogram block to stdout.
fn print_histogram(name: &str, labels: &[Label], histogram: &Histogram) {
    let label_str = format_labels(labels);
    if label_str.is_empty() {
        println!("{name}");
    } else {
        println!("{name} {{{label_str}}}");
    }

    let buckets = &histogram.buckets;

    if buckets.is_empty() {
        println!("  count = {}", histogram.total_count);
        println!();
        return;
    }

    let max_count = buckets.iter().map(|b| b.count).max().unwrap_or(0);

    let bounds: Vec<(String, String)> = buckets
        .iter()
        .enumerate()
        .map(|(idx, bucket)| {
            let lower = if idx == 0 { 0.0 } else { buckets[idx - 1].upper_bound };
            (format_bound(lower), format_bound(bucket.upper_bound))
        })
        .collect();

    let wl = bounds.iter().map(|(l, _)| l.len()).max().unwrap_or(0);
    let wu = bounds.iter().map(|(_, u)| u.len()).max().unwrap_or(0);
    let wc = buckets.iter().map(|b| b.count.to_string().len()).max().unwrap_or(0);

    for (bucket, (lower, upper)) in buckets.iter().zip(bounds.iter()) {
        let bars = "∎".repeat(ync::display::bar_len(bucket.count, max_count));
        let count = bucket.count;
        println!("  {lower:>wl$} .. {upper:>wu$} [ {count:>wc$} ] {bars}");
    }

    println!("  count = {}", histogram.total_count);
    println!();
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn an_empty_service_name_is_blank() {
        assert!(is_blank(""));
    }

    #[test]
    fn a_whitespace_only_service_name_is_blank() {
        assert!(is_blank("  \t "));
    }

    #[test]
    fn a_named_service_is_not_blank() {
        assert!(!is_blank("route"));
    }

    #[test]
    fn no_arguments_leaves_the_service_unset() {
        let cmd = Cmd::try_parse_from(["yanet-cli-metrics"]).expect("no arguments must parse");

        assert!(cmd.name.is_none());
    }

    #[test]
    fn naming_a_service_sets_the_service() {
        let cmd = Cmd::try_parse_from(["yanet-cli-metrics", "route"]).expect("a service name must parse");

        assert_eq!(Some("route".to_owned()), cmd.name);
    }

    #[test]
    fn a_tag_entry_splits_on_the_first_equals() {
        let tag = parse_tag("config=my-acl").expect("a well-formed tag must parse");

        assert_eq!("config", tag.name);
        assert_eq!("my-acl", tag.value);
    }

    #[test]
    fn an_empty_tag_value_requires_the_label_absent() {
        let tag = parse_tag("config=").expect("an empty value must parse");

        assert_eq!("config", tag.name);
        assert_eq!("", tag.value);
    }

    #[test]
    fn a_tag_entry_without_equals_is_rejected() {
        let err = parse_tag("config").expect_err("a bare tag name must be rejected");

        assert!(err.contains("NAME=VALUE"));
    }
}
