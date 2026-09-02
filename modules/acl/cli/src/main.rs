use core::net::IpAddr;
use std::{collections::HashMap, fs::File, path::Path};

use aclpb::{
    DeleteConfigRequest, GetMetricsRulesRequest, GetRulesCountersRequest, ListConfigsRequest, ShowConfigRequest,
    UpdateConfigRequest, acl_service_client::AclServiceClient, metrics_service_client::MetricsServiceClient,
};
use args::{DeleteCmd, MetricsRulesCmd, ModeCmd, RuleCountersCmd, ShowCmd, UpdateCmd};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::CompletionCandidate;
use serde::{Deserialize, Serialize};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{Connection, ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::Error,
    metrics,
    output::{self, CommonFormat},
};

mod args;

use ::commonpb::pb as commonpb;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod aclpb {
    tonic::include_proto!("modules.acl.controlplane.aclpb.v1");
}

pub(crate) mod action_kind {
    use serde::{Deserialize, Deserializer, Serializer, de};

    use super::aclpb;

    pub fn serialize<S: Serializer>(kind: &i32, s: S) -> Result<S::Ok, S::Error> {
        let action_kind = aclpb::ActionKind::try_from(*kind)
            .map_err(|_| serde::ser::Error::custom(format!("unknown ActionKind value {kind}")))?;
        s.serialize_str(action_kind.as_str_name())
    }

    pub fn deserialize<'de, D: Deserializer<'de>>(d: D) -> Result<i32, D::Error> {
        let s = String::deserialize(d)?;
        let action_kind = aclpb::ActionKind::from_str_name(&s)
            .ok_or_else(|| de::Error::custom(format!("unknown ActionKind name `{s}`")))?;
        Ok(action_kind as i32)
    }
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

fn print_rule_metrics_table(metrics: &[commonpb::Metric]) {
    let rows = rule_counter_rows(metrics);

    let mut current: Option<&RuleLocation> = None;
    let mut pending: Vec<CounterRow> = Vec::new();

    for row in &rows {
        if current != Some(&row.location) {
            if current.is_some() {
                print_counter_table(core::mem::take(&mut pending));
                println!();
            }
            let (config, device, pipeline, function, chain) = &row.location;
            println!(
                "ACL RULE COUNTERS  config={config} device={device} pipeline={pipeline} function={function} chain={chain}"
            );
            println!();
            current = Some(&row.location);
        }

        pending.push(CounterRow {
            counter: row.counter.clone(),
            packets: row.packets.map_or_else(|| "-".to_string(), metrics::format_number),
            bytes: row.bytes.map_or_else(|| "-".to_string(), metrics::format_number),
        });
    }

    if !pending.is_empty() {
        print_counter_table(pending);
        println!();
    }
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ACLConfig {
    rules: Vec<aclpb::Rule>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    fwtable_name_v4: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    fwtable_name_v6: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    sync_config: Option<aclpb::SyncConfig>,
}

impl ACLConfig {
    pub fn load<P>(path: P) -> Result<Self, Box<dyn core::error::Error>>
    where
        P: AsRef<Path>,
    {
        let file = File::open(path)?;
        let config = serde_yaml::from_reader(file)?;

        Ok(config)
    }
}

/// Display view of an ACL config returned by the show command.
///
/// `fwtable_name_v4`, `fwtable_name_v6`, and `sync_config` are omitted
/// when absent.
#[derive(Debug, Serialize)]
struct ShowConfig {
    rules: Vec<aclpb::Rule>,
    #[serde(skip_serializing_if = "Option::is_none")]
    fwtable_name_v4: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    fwtable_name_v6: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    sync_config: Option<aclpb::SyncConfig>,
}

/// ACL module CLI.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.acl.controlplane.aclpb.v1.ACLService";
const METRICS_SERVICE_NAME: &str = "modules.acl.controlplane.aclpb.v1.MetricsService";

pub struct ACLService {
    service: Service<AclServiceClient<LayeredChannel>>,
    metrics: Service<MetricsServiceClient<LayeredChannel>>,
}

impl ACLService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let conn = Connection::connect_for(connection, action).await?;
        let service = Service::new(&conn, SERVICE_NAME, |channel| {
            AclServiceClient::new(channel)
                .max_decoding_message_size(256 * 1024 * 1024)
                .max_encoding_message_size(256 * 1024 * 1024)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });
        let metrics = Service::new(&conn, METRICS_SERVICE_NAME, |channel| {
            MetricsServiceClient::new(channel)
                .max_decoding_message_size(256 * 1024 * 1024)
                .max_encoding_message_size(256 * 1024 * 1024)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });

        Ok(Self { service, metrics })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let response = self
            .service
            .client()
            .list_configs(ListConfigsRequest {})
            .await
            .map_err(self.service.status("list"))?
            .into_inner();

        output::data(
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty_with_hint(
                        format_args!("No ACL configurations found."),
                        format_args!("create one with 'yanet-cli-acl update --name <name> <path>'"),
                    );
                    return;
                }

                for name in &response.configs {
                    println!("{name}");
                }
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Error> {
        let request = ShowConfigRequest { name: cmd.config_name.clone() };
        let response = self
            .service
            .client()
            .show_config(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();

        output::data(
            || &response,
            || {
                let display = ShowConfig {
                    rules: response.rules.clone(),
                    fwtable_name_v4: if response.fwtable_name_v4.is_empty() {
                        None
                    } else {
                        Some(response.fwtable_name_v4.clone())
                    },
                    fwtable_name_v6: if response.fwtable_name_v6.is_empty() {
                        None
                    } else {
                        Some(response.fwtable_name_v6.clone())
                    },
                    sync_config: response.sync_config.clone(),
                };
                print!(
                    "{}",
                    serde_yaml::to_string(&display).expect("ACL config YAML serialization must not fail")
                );

                if response.rules.is_empty() {
                    output::empty_with_hint(
                        format_args!("No ACL rules found for '{}'.", cmd.config_name),
                        format_args!("create one with 'yanet-cli-acl update --name <name> <path>'"),
                    );
                }
            },
        );

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.service
            .client()
            .delete_config(request)
            .await
            .map_err(self.service.status("delete"))?
            .into_inner();

        output::success("delete", format_args!("Deleted {}.", cmd.config_name));

        Ok(())
    }

    /// Merge the emission sync config from the YAML file and the flags.
    ///
    /// The YAML section is the base; every flag that was passed overrides
    /// its field. A config with neither source carries no sync config.
    fn merge_sync_config(&self, base: Option<aclpb::SyncConfig>, cmd: &UpdateCmd) -> Option<aclpb::SyncConfig> {
        let mut sync = match base {
            Some(base) => base,
            None => {
                let no_flags = cmd.dst_ether.is_none()
                    && cmd.dst_addr_multicast.is_none()
                    && cmd.port_multicast.is_none()
                    && cmd.dst_addr_unicast.is_none()
                    && cmd.port_unicast.is_none();
                if no_flags {
                    return None;
                }
                aclpb::SyncConfig::default()
            }
        };

        if let Some(dst_ether) = cmd.dst_ether {
            sync.dst_ether = Some(commonpb::MacAddress::from(dst_ether));
        }
        if let Some(dst_addr_multicast) = cmd.dst_addr_multicast {
            sync.dst_addr_multicast = Some(commonpb::IpAddress::from(IpAddr::V6(dst_addr_multicast)));
        }
        if let Some(port_multicast) = cmd.port_multicast {
            sync.port_multicast = u32::from(port_multicast);
        }
        if let Some(dst_addr_unicast) = cmd.dst_addr_unicast {
            sync.dst_addr_unicast = Some(commonpb::IpAddress::from(IpAddr::V6(dst_addr_unicast)));
        }
        if let Some(port_unicast) = cmd.port_unicast {
            sync.port_unicast = u32::from(port_unicast);
        }

        Some(sync)
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let config = ACLConfig::load(&cmd.file).map_err(|err| {
            self.service.invalid(
                "update",
                format!("failed to load rules from {}: {err}", cmd.file.display()),
            )
        })?;
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
        let sync_config = self.merge_sync_config(config.sync_config, &cmd);

        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            rules: config.rules,
            fwtable_name_v4,
            fwtable_name_v6,
            sync_config,
        };
        log::trace!("UpdateConfigRequest: {request:?}");
        let response = self
            .service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?
            .into_inner();
        log::debug!("UpdateConfigResponse: {response:?}");

        output::success(
            "update",
            format_args!("Updated {} ({} rules).", cmd.config_name, rule_count),
        );

        Ok(())
    }

    pub async fn rule_counters(&mut self, cmd: RuleCountersCmd) -> Result<(), Error> {
        let request = GetRulesCountersRequest {
            name: cmd.config_name.clone().unwrap_or_default(),
        };
        let response = self
            .service
            .client()
            .get_rules_counters(request)
            .await
            .map_err(self.service.status("rule-counters"))?
            .into_inner();

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

                let mut location_keys: Vec<String> = Vec::new();
                let mut location_map: HashMap<String, Vec<&aclpb::RuleCounter>> = HashMap::new();
                for entry in &response.counters {
                    let key = format!(
                        "{}\0{}\0{}\0{}\0{}",
                        entry.config, entry.device, entry.pipeline, entry.function, entry.chain,
                    );
                    if !location_map.contains_key(&key) {
                        location_keys.push(key.clone());
                    }
                    location_map.entry(key).or_default().push(entry);
                }

                for (loc_idx, key) in location_keys.iter().enumerate() {
                    if loc_idx > 0 {
                        println!();
                    }
                    let entries = &location_map[key];
                    let parts: Vec<&str> = key.split('\0').collect();
                    let (cfg, device, pipeline, function, chain) = (parts[0], parts[1], parts[2], parts[3], parts[4]);
                    println!(
                        "ACL RULE COUNTERS  config={cfg} device={device} pipeline={pipeline} function={function} chain={chain}"
                    );
                    println!();

                    let rows: Vec<CounterRow> = entries
                        .iter()
                        .map(|entry| CounterRow {
                            counter: entry.counter.clone(),
                            packets: metrics::format_number(entry.packets),
                            bytes: metrics::format_number(entry.bytes),
                        })
                        .collect();
                    print_counter_table(rows);
                    println!();
                }
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
            .client()
            .get_metrics_rules(request)
            .await
            .map_err(self.metrics.status("metrics-rules"))?
            .into_inner();

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

                print_rule_metrics_table(&metrics)
            },
        );

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = ACLService::new(&cmd.connection, action).await?;
    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::MetricsRules(cmd) => service.metrics_rules(cmd).await,
        ModeCmd::RuleCounters(cmd) => service.rule_counters(cmd).await,
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

/// Completion candidates for a `--name` argument: the ACL configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            AclServiceClient::new(channel)
                .max_decoding_message_size(256 * 1024 * 1024)
                .max_encoding_message_size(256 * 1024 * 1024)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
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
