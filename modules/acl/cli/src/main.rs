use core::fmt::Write;
use std::collections::HashMap;

use aclpb::{
    DeleteConfigRequest, GetMetricsRulesRequest, GetRulesCountersRequest, ListConfigsRequest, Rule, RuleCounter,
    ShowConfigRequest, UpdateConfigRequest, acl_service_client::AclServiceClient,
    acl_service_server::SERVICE_NAME as ACL_SERVICE_NAME, metrics_service_client::MetricsServiceClient,
    metrics_service_server::SERVICE_NAME as METRICS_SERVICE_NAME,
};
use args::{CountersMode, DeleteCmd, MetricsRulesCmd, ModeCmd, RuleCountersCmd, ShowCmd, UpdateCmd};
use clap::{CommandFactory, Parser, error::ErrorKind};
use clap_complete::engine::CompletionCandidate;
use ipfw::RuleLine;
use serde::{Deserialize, Deserializer, Serialize, Serializer, de};
use tabled::Tabled;
use tokio::try_join;
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{Connection, ConnectionArgs, LayeredChannel, Service, resolve_label},
    completion, display,
    errors::Error,
    metrics,
    output::{self, CommonFormat, Paint, Painted},
    yaml,
};

mod args;
mod ipfw;

use ::commonpb::{pb as commonpb, serde_with};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod aclpb {
    tonic::include_proto!("modules.acl.controlplane.aclpb.v1");
}

/// Serializes an action kind as its declared name, an undeclared number as
/// the number itself, so one unknown kind cannot hide the rest of a ruleset.
fn serialize_action_kind<S: Serializer>(kind: &i32, serializer: S) -> Result<S::Ok, S::Error> {
    serde_with::declared_name(kind, serializer, aclpb::ActionKind::as_str_name)
}

/// Deserializes an action kind from its declared name only: a null or a
/// number is refused, because the zero kind would silently read as PASS.
fn deserialize_action_kind<'de, D: Deserializer<'de>>(deserializer: D) -> Result<i32, D::Error> {
    let name = String::deserialize(deserializer)?;
    let kind = aclpb::ActionKind::from_str_name(&name)
        .ok_or_else(|| de::Error::custom(format!("unknown ActionKind name `{name}`")))?;

    Ok(kind as i32)
}

#[derive(Tabled)]
struct CounterRow {
    #[tabled(rename = "Counter")]
    counter: String,
    #[tabled(rename = "Packets")]
    packets: String,
    #[tabled(rename = "Bytes")]
    bytes: String,
}

fn print_counter_table(rows: Vec<CounterRow>) {
    let show_packets = rows.iter().any(|r| r.packets != "-");
    let show_bytes = rows.iter().any(|r| r.bytes != "-");

    if !show_packets && !show_bytes {
        return;
    }

    let mut builder = tabled::builder::Builder::new();
    let mut header = vec!["Counter".to_string()];
    if show_packets {
        header.push("Packets".to_string());
    }
    if show_bytes {
        header.push("Bytes".to_string());
    }
    builder.push_record(header);

    for r in rows {
        let mut row = vec![r.counter];
        if show_packets {
            row.push(r.packets);
        }
        if show_bytes {
            row.push(r.bytes);
        }
        builder.push_record(row);
    }

    ync::display::print_table(builder.build());
}

/// A rule counter's location and its packet and byte totals.
struct RuleCounterRow {
    location: RuleLocation,
    counter: String,
    packets: Option<u64>,
    bytes: Option<u64>,
}

/// The position a rule counter belongs to, kept as raw field values.
type RuleLocation = (String, String, String, String, String);

fn rule_label(metric: &commonpb::Metric, name: &str) -> String {
    metric
        .labels
        .iter()
        .find(|label| label.name == name)
        .map_or_else(String::new, |label| label.value.clone())
}

/// Groups `acl_rule_packets` and `acl_rule_bytes` into one row per rule
/// counter, keyed by the raw location fields and the `counter` label.
///
/// The metrics table renderer drops counter-labelled metrics, so rule
/// metrics need their own pairing. Totals are read from the protobuf
/// `uint64` directly, since routing them through `f64` would round a
/// counter past 2^53.
fn rule_counter_rows(metrics: &[commonpb::Metric]) -> Vec<RuleCounterRow> {
    let mut order: Vec<(RuleLocation, String)> = Vec::new();
    let mut rows: HashMap<(RuleLocation, String), RuleCounterRow> = HashMap::new();

    for metric in metrics {
        let counter = rule_label(metric, "counter");
        if counter.is_empty() {
            continue;
        }

        let location: RuleLocation = (
            rule_label(metric, "config"),
            rule_label(metric, "device"),
            rule_label(metric, "pipeline"),
            rule_label(metric, "function"),
            rule_label(metric, "chain"),
        );
        let key = (location.clone(), counter.clone());

        let row = rows.entry(key.clone()).or_insert_with(|| {
            order.push(key);
            RuleCounterRow {
                location,
                counter,
                packets: None,
                bytes: None,
            }
        });

        let Some(commonpb::metric::Value::Counter(value)) = metric.value else {
            continue;
        };

        if metric.name.ends_with("_packets") {
            row.packets = Some(value);
        } else if metric.name.ends_with("_bytes") {
            row.bytes = Some(value);
        }
    }

    order.into_iter().filter_map(|key| rows.remove(&key)).collect()
}

/// Prints the rows as one titled counter table per location, the
/// locations in order of first appearance.
fn print_rule_counter_groups(rows: &[RuleCounterRow]) {
    let mut groups: Vec<(&RuleLocation, Vec<&RuleCounterRow>)> = Vec::new();
    let mut index: HashMap<&RuleLocation, usize> = HashMap::new();
    for row in rows {
        let group = *index.entry(&row.location).or_insert_with(|| {
            groups.push((&row.location, Vec::new()));
            groups.len() - 1
        });
        groups[group].1.push(row);
    }

    for (location, rows) in groups {
        let (config, device, pipeline, function, chain) = location;
        println!(
            "ACL RULE COUNTERS  config={config} device={device} pipeline={pipeline} function={function} chain={chain}"
        );
        println!();

        let table = rows
            .iter()
            .map(|row| CounterRow {
                counter: row.counter.clone(),
                packets: row.packets.map_or_else(|| "-".to_string(), metrics::format_number),
                bytes: row.bytes.map_or_else(|| "-".to_string(), metrics::format_number),
            })
            .collect();
        print_counter_table(table);
        println!();
    }
}

#[derive(Debug, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ACLConfig {
    #[serde(default)]
    name: String,
    rules: Vec<aclpb::Rule>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    fwtable_name_v4: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    fwtable_name_v6: Option<String>,
}

/// Prints numbered `add` lines, with counters summed over positions: `-` for
/// a rule no count action moves, only counted rules in the nonzero mode.
fn print_rules(rules: &[Rule], counters: Option<(&[RuleCounter], CountersMode)>) {
    let colored = output::is_colored() && output::stdout_is_terminal();
    let width = digits(rules.len().saturating_sub(1) as u64);

    let Some((counters, mode)) = counters else {
        for (idx, rule) in rules.iter().enumerate() {
            println!(
                "{:>width$}  {}",
                Paint::Dim.when(colored, idx),
                RuleLine::new(rule, colored)
            );
        }
        return;
    };

    let mut totals: HashMap<&str, (u64, u64)> = HashMap::new();
    for counter in counters {
        let total = totals.entry(counter.counter.as_str()).or_default();
        total.0 = total.0.saturating_add(counter.packets);
        total.1 = total.1.saturating_add(counter.bytes);
    }

    let mut implicit = String::new();
    let cells = rules
        .iter()
        .enumerate()
        .map(|(idx, rule)| {
            let cell = RuleLine::new(rule, colored).counts().then(|| {
                let name = if rule.counter.is_empty() {
                    implicit.clear();
                    write!(implicit, "rule {idx}").expect("writing to a string does not fail");
                    implicit.as_str()
                } else {
                    rule.counter.as_str()
                };
                totals.get(name).copied().unwrap_or_default()
            });
            (idx, rule, cell)
        })
        .filter(|(_, _, cell)| mode == CountersMode::All || cell.is_some_and(|(packets, _)| packets > 0))
        .collect::<Vec<_>>();

    if cells.is_empty() {
        output::empty(format_args!("No rule has counted a packet."));
        return;
    }

    let packets_width = cells
        .iter()
        .filter_map(|(_, _, cell)| *cell)
        .map(|(packets, _)| digits(packets))
        .fold("packets".len(), usize::max);
    let bytes_width = cells
        .iter()
        .filter_map(|(_, _, cell)| *cell)
        .map(|(_, bytes)| digits(bytes))
        .fold("bytes".len(), usize::max);

    println!(
        "{:>width$}  {:>packets_width$}  {:>bytes_width$}  {}",
        Paint::Dim.when(colored, "#"),
        Paint::Dim.when(colored, "packets"),
        Paint::Dim.when(colored, "bytes"),
        Paint::Dim.when(colored, "rule"),
    );
    for (idx, rule, cell) in cells {
        let line = RuleLine::new(rule, colored);
        match cell {
            None => println!(
                "{:>width$}  {:>packets_width$}  {:>bytes_width$}  {line}",
                Paint::Dim.when(colored, idx),
                Paint::Dim.when(colored, "-"),
                Paint::Dim.when(colored, "-"),
            ),
            Some((packets, bytes)) => {
                let paint = (colored && packets == 0).then_some(Paint::Dim);
                println!(
                    "{:>width$}  {:>packets_width$}  {:>bytes_width$}  {line}",
                    Paint::Dim.when(colored, idx),
                    Painted::new(paint, packets),
                    Painted::new(paint, bytes),
                );
            }
        }
    }
}

/// Returns the number of decimal digits of a value.
fn digits(value: u64) -> usize {
    value.checked_ilog10().map_or(1, |log| log as usize + 1)
}

/// Manages acl module configs.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

fn client(channel: LayeredChannel) -> AclServiceClient<LayeredChannel> {
    AclServiceClient::new(channel)
        .max_decoding_message_size(256 * 1024 * 1024)
        .max_encoding_message_size(256 * 1024 * 1024)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

fn metrics_client(channel: LayeredChannel) -> MetricsServiceClient<LayeredChannel> {
    MetricsServiceClient::new(channel)
        .max_decoding_message_size(256 * 1024 * 1024)
        .max_encoding_message_size(256 * 1024 * 1024)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

pub struct ACLService {
    service: Service<AclServiceClient<LayeredChannel>>,
    /// A second client over the same channel, for a call running concurrently
    /// with the first.
    concurrent: Service<AclServiceClient<LayeredChannel>>,
    metrics: Service<MetricsServiceClient<LayeredChannel>>,
}

impl ACLService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let conn = Connection::connect_for(connection, action).await?;
        let service = Service::new(&conn, ACL_SERVICE_NAME, client);
        let concurrent = Service::new(&conn, ACL_SERVICE_NAME, client);
        let metrics = Service::new(&conn, METRICS_SERVICE_NAME, metrics_client);

        Ok(Self { service, concurrent, metrics })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let response = self
            .service
            .unary("list", ListConfigsRequest {}, async |client, request| {
                client.list_configs(request).await
            })
            .await?;

        output::data(
            || &response.configs,
            || {
                display::print_names_with_hint(
                    &response.configs,
                    format_args!("No ACL configurations found."),
                    format_args!("create one with 'yanet-cli-acl update --name <name> <path>'"),
                )
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Error> {
        let progress = if cmd.counters.is_some() {
            output::progress(format_args!(
                "Loading config '{}' and its rule counters",
                cmd.config_name
            ))
        } else {
            output::progress(format_args!("Loading config '{}'", cmd.config_name))
        };

        // The rules and their counters load at once, as a large config
        // takes seconds for each.
        let (service, concurrent) = (&mut self.service, &mut self.concurrent);
        let not_found = service.not_found("show", &format!("config '{}'", cmd.config_name));
        let config = service.unary_with(
            "show",
            ShowConfigRequest { name: cmd.config_name.clone() },
            not_found,
            async |client, request| client.show_config(request).await,
        );
        let counters = async {
            if cmd.counters.is_none() {
                return Ok(None);
            }

            let request = GetRulesCountersRequest { name: cmd.config_name.clone() };
            let not_found = concurrent.not_found("show", &format!("config '{}'", cmd.config_name));
            concurrent
                .unary_with("show", request, not_found, async |client, request| {
                    client.get_rules_counters(request).await
                })
                .await
                .map(|response| Some(response.counters))
        };

        let loaded = try_join!(config, counters);
        drop(progress);
        let (response, counters) = loaded?;

        output::paged(
            || &response,
            || {
                if response.rules.is_empty() {
                    output::empty_with_hint(
                        format_args!("No ACL rules found for '{}'.", cmd.config_name),
                        format_args!("create one with 'yanet-cli-acl update --name <name> <path>'"),
                    );
                    return;
                }

                print_rules(&response.rules, counters.as_deref().zip(cmd.counters));
            },
        );

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.service
            .unary_with(
                "delete",
                request,
                self.service
                    .not_found("delete", &format!("config '{}'", cmd.config_name)),
                async |client, request| client.delete_config(request).await,
            )
            .await?;

        output::success("delete", format_args!("Deleted config '{}'.", cmd.config_name));

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd, config: ACLConfig) -> Result<(), Error> {
        let rule_count = config.rules.len();

        // A flag wins for that field whenever it is passed, even as an
        // explicit empty value: an empty map name declares no link, so
        // `--map-name-v4 ''` clears a name the YAML carries. Absent
        // flags fall back to the YAML fields.
        let fwtable_name_v4 = cmd
            .map_name_v4
            .clone()
            .or(config.fwtable_name_v4.clone())
            .unwrap_or_default();
        let fwtable_name_v6 = cmd
            .map_name_v6
            .clone()
            .or(config.fwtable_name_v6.clone())
            .unwrap_or_default();
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            rules: config.rules,
            fwtable_name_v4,
            fwtable_name_v6,
            ..Default::default()
        };
        self.service
            .unary("update", request, async |client, request| {
                client.update_config(request).await
            })
            .await?;

        output::success(
            "update",
            format_args!("Updated config '{}' ({} rules).", cmd.config_name, rule_count),
        );

        Ok(())
    }

    pub async fn rule_counters(&mut self, cmd: RuleCountersCmd) -> Result<(), Error> {
        let request = GetRulesCountersRequest {
            name: cmd.config_name.clone().unwrap_or_default(),
        };
        let response = self
            .service
            .unary("rule-counters", request, async |client, request| {
                client.get_rules_counters(request).await
            })
            .await?;

        output::data(
            || &response.counters,
            || {
                if response.counters.is_empty() {
                    match cmd.config_name.as_deref() {
                        Some(name) => output::empty(format_args!("No rule counters found for '{name}'.")),
                        None => output::empty(format_args!("No rule counters found.")),
                    }
                    return;
                }

                let rows: Vec<RuleCounterRow> = response
                    .counters
                    .iter()
                    .map(|entry| RuleCounterRow {
                        location: (
                            entry.config.clone(),
                            entry.device.clone(),
                            entry.pipeline.clone(),
                            entry.function.clone(),
                            entry.chain.clone(),
                        ),
                        counter: entry.counter.clone(),
                        packets: Some(entry.packets),
                        bytes: Some(entry.bytes),
                    })
                    .collect();
                print_rule_counter_groups(&rows);
            },
        );

        Ok(())
    }

    pub async fn metrics_rules(&mut self, cmd: MetricsRulesCmd) -> Result<(), Error> {
        let request = GetMetricsRulesRequest {
            config: cmd.config_name.clone().unwrap_or_default(),
            device: cmd.device.clone().unwrap_or_default(),
            pipeline: cmd.pipeline.clone().unwrap_or_default(),
            function: cmd.function.clone().unwrap_or_default(),
            chain: cmd.chain.clone().unwrap_or_default(),
        };

        let response = self
            .metrics
            .unary("metrics-rules", request, async |client, request| {
                client.get_metrics_rules(request).await
            })
            .await?;

        let metrics = response.metrics;

        output::data(
            || &metrics,
            || {
                if metrics.is_empty() {
                    match cmd.config_name.as_deref() {
                        Some(name) => output::empty(format_args!("No ACL rule metrics found for '{name}'.")),
                        None => output::empty(format_args!("No ACL rule metrics found.")),
                    }
                    return;
                }

                print_rule_counter_groups(&rule_counter_rows(&metrics))
            },
        );

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();

    // Counters are columns of the human rules, the JSON of show stays the
    // config document update reads.
    if let ModeCmd::Show(show) = &cmd.mode
        && show.counters.is_some()
        && cmd.globals.format == CommonFormat::Json
    {
        Cmd::command()
            .error(
                ErrorKind::ArgumentConflict,
                "--counters cannot be used with '--format json', use 'yanet-cli-acl rule-counters --format json'",
            )
            .exit();
    }

    // The update file is read and bound before the connection, so bad local
    // input fails the same with or without a reachable gateway.
    let update = match &cmd.mode {
        ModeCmd::Update(update) => {
            let endpoint = resolve_label(&cmd.globals.connection, action)?;
            let mut config: ACLConfig = yaml::load(&update.file)
                .map_err(|err| Error::invalid_argument("update", endpoint.clone(), err.to_string()))?;
            yaml::bind_name(&mut config.name, &update.config_name)
                .map_err(|err| Error::invalid_argument("update", endpoint, err))?;
            Some(config)
        }
        _ => None,
    };

    let mut service = ACLService::new(&cmd.globals.connection, action).await?;
    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => {
            let config = update.expect("prepared for the update mode");
            service.update_config(cmd, config).await
        }
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::MetricsRules(cmd) => service.metrics_rules(cmd).await,
        ModeCmd::RuleCounters(cmd) => service.rule_counters(cmd).await,
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}

#[cfg(test)]
mod test {
    use super::*;

    fn rule_metric(name: &str, config: &str, counter: &str, value: u64) -> commonpb::Metric {
        commonpb::Metric {
            name: name.to_string(),
            value: Some(commonpb::metric::Value::Counter(value)),
            labels: [
                ("config", config),
                ("device", "port0"),
                ("pipeline", "p"),
                ("function", "f"),
                ("chain", "c"),
                ("counter", counter),
            ]
            .into_iter()
            .map(|(name, value)| commonpb::Label {
                name: name.to_string(),
                value: value.to_string(),
            })
            .collect(),
        }
    }

    #[test]
    fn pairs_rule_counters_the_metrics_table_would_drop() {
        let metrics = vec![
            rule_metric("acl_rule_packets", "test", "svc_counter", 3),
            rule_metric("acl_rule_bytes", "test", "svc_counter", 300),
            rule_metric("acl_rule_packets", "test", "other", 1),
        ];

        let rows = rule_counter_rows(&metrics);

        assert_eq!(rows.len(), 2);
        assert_eq!(rows[0].counter, "svc_counter");
        assert_eq!(rows[0].packets, Some(3));
        assert_eq!(rows[0].bytes, Some(300));
        assert_eq!(rows[1].counter, "other");
        assert_eq!(rows[1].packets, Some(1));
        assert_eq!(rows[1].bytes, None);
    }

    #[test]
    fn keeps_totals_beyond_f64_precision() {
        let exact = (1_u64 << 53) + 1;
        let metrics = vec![rule_metric("acl_rule_bytes", "test", "svc_counter", exact)];

        let rows = rule_counter_rows(&metrics);

        assert_eq!(rows[0].bytes, Some(exact));
    }

    #[test]
    fn keeps_positions_whose_names_carry_field_markers() {
        let metrics = vec![
            rule_metric("acl_rule_packets", "a device=b", "svc_counter", 1),
            rule_metric("acl_rule_packets", "a", "svc_counter", 2),
        ];

        let rows = rule_counter_rows(&metrics);

        assert_eq!(rows.len(), 2, "two positions must not collapse into one row");
        assert_eq!(rows[0].packets, Some(1));
        assert_eq!(rows[1].packets, Some(2));
    }

    #[test]
    fn deserialize_fixture_acl_yaml() {
        let content = include_str!("../../../../tests/functional/testdata/acl.yaml");
        let config: ACLConfig = serde_yaml::from_str(content).expect("acl.yaml fixture must deserialize");
        assert!(!config.rules.is_empty());
    }

    #[test]
    fn deserialize_fixture_acl_fwstate_yaml() {
        let content = include_str!("../../../../tests/functional/testdata/acl+fwstate.yaml");
        let config: ACLConfig = serde_yaml::from_str(content).expect("acl+fwstate.yaml fixture must deserialize");
        assert!(!config.rules.is_empty());
    }

    #[test]
    fn test_acl_config_rejects_legacy_sync_config() {
        let err = serde_yaml::from_str::<ACLConfig>("rules: []\nsync_config: {}\n").unwrap_err();

        assert!(err.to_string().contains("unknown field `sync_config`"));
    }

    #[test]
    fn test_rules_yaml_typed_network_lists_parse() {
        let yaml = r#"
rules:
  - actions:
      - kind: ACTION_KIND_PASS
    sources4:
      - 192.0.2.0/24
      - 192.0.3.1/255.255.255.255
    sources6:
      - 2001:db8::/32
      - "2001:db8::/ffff:ffff:ffff:0:ffff::"
    destinations4:
      - 0.0.0.0/0
    destinations6:
      - "::/::"
"#;
        let config: ACLConfig = serde_yaml::from_str(yaml).expect("typed network lists must parse");

        let rule = &config.rules[0];
        assert_eq!(2, rule.sources4.len());
        assert_eq!(2, rule.sources6.len());
        assert_eq!(1, rule.destinations4.len());
        assert_eq!(1, rule.destinations6.len());
    }
}
