use core::net::Ipv6Addr;
use std::{collections::HashMap, fs::File, path::Path};

use aclpb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    acl_service_client::AclServiceClient, metrics_service_client::MetricsServiceClient,
};
use args::{DeleteCmd, MetricsCmd, ModeCmd, ShowCmd, UpdateCmd};
use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::{CompleteEnv, engine::CompletionCandidate};
use serde::{Deserialize, Serialize};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{Connection, ConnectionArgs, LayeredChannel, Service},
    completion,
    display::print_table_from_entries,
    errors::Error,
    metrics::{self, GaugeRow, Kind, Metric},
    output::{self, CommonFormat},
};

mod args;

use ::commonpb::pb as commonpb;
use commonpb::{GetMetricsRequest, MetricTag};

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

fn print_metrics_table(metrics: &[Metric]) {
    struct CounterPair {
        display: String,
        packets: Option<u64>,
        bytes: Option<u64>,
    }

    let mut location_keys: Vec<String> = Vec::new();
    let mut location_map: HashMap<String, Vec<&Metric>> = HashMap::new();
    let mut gauge_keys: Vec<String> = Vec::new();
    let mut gauge_map: HashMap<String, Vec<&Metric>> = HashMap::new();
    let mut grpc_counters: Vec<&Metric> = Vec::new();
    let mut grpc_histograms: Vec<&Metric> = Vec::new();

    for m in metrics {
        if m.name.starts_with("grpc_") {
            match m.kind {
                Kind::Counter => grpc_counters.push(m),
                Kind::Histogram => grpc_histograms.push(m),
                _ => {}
            }
            continue;
        }

        match m.kind {
            Kind::Histogram => {}
            Kind::Gauge => {
                let cfg = m.label_value("config").unwrap_or("global").to_string();
                if !gauge_map.contains_key(&cfg) {
                    gauge_keys.push(cfg.clone());
                }
                gauge_map.entry(cfg).or_default().push(m);
            }
            Kind::Counter => {
                let key = format!(
                    "{}\0{}\0{}\0{}\0{}",
                    m.label_value("config").unwrap_or(""),
                    m.label_value("device").unwrap_or(""),
                    m.label_value("pipeline").unwrap_or(""),
                    m.label_value("function").unwrap_or(""),
                    m.label_value("chain").unwrap_or(""),
                );
                if !location_map.contains_key(&key) {
                    location_keys.push(key.clone());
                }
                location_map.entry(key).or_default().push(m);
            }
            Kind::Unknown => {}
        }
    }

    for (loc_idx, key) in location_keys.iter().enumerate() {
        if loc_idx > 0 {
            println!();
        }
        let counters = &location_map[key];
        let parts: Vec<&str> = key.split('\0').collect();
        let (cfg, device, pipeline, function, chain) = (parts[0], parts[1], parts[2], parts[3], parts[4]);
        println!("ACL COUNTERS  config={cfg} device={device} pipeline={pipeline} function={function} chain={chain}");
        println!();

        let std_counters: Vec<&&Metric> = counters.iter().filter(|m| m.label_value("counter").is_none()).collect();
        let rule_counters: Vec<&&Metric> = counters.iter().filter(|m| m.label_value("counter").is_some()).collect();

        let mut pair_order: Vec<String> = Vec::new();
        let mut pair_map: HashMap<String, CounterPair> = HashMap::new();

        for m in &std_counters {
            let val = m.value.unwrap_or(0.0) as u64;
            let stripped = m.name.strip_prefix("acl_").unwrap_or(&m.name);
            if let Some(base) = stripped.strip_suffix("_packets") {
                let pair = pair_map.entry(base.to_string()).or_insert_with(|| {
                    pair_order.push(base.to_string());
                    CounterPair {
                        display: metrics::metric_display_name(base, "acl_"),
                        packets: None,
                        bytes: None,
                    }
                });
                pair.packets = Some(val);
            } else if let Some(base) = stripped.strip_suffix("_bytes") {
                let pair = pair_map.entry(base.to_string()).or_insert_with(|| {
                    pair_order.push(base.to_string());
                    CounterPair {
                        display: metrics::metric_display_name(base, "acl_"),
                        packets: None,
                        bytes: None,
                    }
                });
                pair.bytes = Some(val);
            }
        }

        if !pair_order.is_empty() {
            let rows: Vec<CounterRow> = pair_order
                .iter()
                .map(|k| {
                    let p = &pair_map[k];
                    CounterRow {
                        counter: p.display.clone(),
                        packets: p.packets.map(metrics::format_number).unwrap_or_else(|| "-".into()),
                        bytes: p.bytes.map(metrics::format_number).unwrap_or_else(|| "-".into()),
                    }
                })
                .collect();
            print_counter_table(rows);
        }

        if !rule_counters.is_empty() {
            println!();
            println!("Per-Rule Counters:");

            let mut rule_order: Vec<String> = Vec::new();
            let mut rule_map_inner: HashMap<String, (Option<u64>, Option<u64>)> = HashMap::new();

            for m in &rule_counters {
                let rule_name = m.label_value("counter").unwrap_or("unknown").to_string();
                let val = m.value.unwrap_or(0.0) as u64;
                if !rule_map_inner.contains_key(&rule_name) {
                    rule_order.push(rule_name.clone());
                    rule_map_inner.insert(rule_name.clone(), (None, None));
                }
                let entry = rule_map_inner.get_mut(&rule_name).unwrap();
                if m.name.ends_with("_packets") {
                    entry.0 = Some(val);
                } else if m.name.ends_with("_bytes") {
                    entry.1 = Some(val);
                }
            }

            let rows: Vec<CounterRow> = rule_order
                .iter()
                .map(|name| {
                    let (pkts, b) = rule_map_inner[name];
                    CounterRow {
                        counter: name.clone(),
                        packets: pkts.map(metrics::format_number).unwrap_or_else(|| "-".into()),
                        bytes: b.map(metrics::format_number).unwrap_or_else(|| "-".into()),
                    }
                })
                .collect();
            print_counter_table(rows);
        }

        println!();
    }

    for cfg in &gauge_keys {
        let gauges = &gauge_map[cfg];
        println!("ACL CONFIG INFO  config={cfg}");
        println!();
        let rows: Vec<GaugeRow> = gauges
            .iter()
            .map(|m| GaugeRow {
                metric: metrics::metric_display_name(&m.name, "acl_"),
                value: metrics::format_gauge_value(&m.name, m.value.unwrap_or(0.0)),
            })
            .collect();
        print_table_from_entries(rows);
        println!();
    }

    metrics::print_grpc_metrics(&grpc_counters, &grpc_histograms);
}

#[derive(Debug, Serialize, Deserialize)]
pub struct ACLConfig {
    rules: Vec<aclpb::Rule>,
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
/// `fwtable_name_v4`, `fwtable_name_v6`, and `sync_config` are omitted when
/// absent.
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
#[command(version, about)]
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

/// Parse IPv6 address string into an `IpAddress` proto message.
fn parse_ipv6(s: &str) -> Result<commonpb::IpAddress, String> {
    let addr = s.parse::<Ipv6Addr>().map_err(|err| err.to_string())?;
    Ok(commonpb::IpAddress { addr: addr.octets().to_vec() })
}

/// Parse a MAC address string into a `MACAddress` proto message.
fn parse_mac(s: &str) -> Result<commonpb::MacAddress, String> {
    s.parse::<commonpb::MacAddress>().map_err(|err| err.to_string())
}

pub struct ACLService {
    service: Service<AclServiceClient<LayeredChannel>>,
    metrics: Service<MetricsServiceClient<LayeredChannel>>,
}

impl ACLService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let conn = Connection::connect(connection).await?;
        let service = Service::new(&conn, SERVICE_NAME, |channel| {
            AclServiceClient::new(channel)
                .max_decoding_message_size(256 * 1024 * 1024)
                .max_encoding_message_size(256 * 1024 * 1024)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });
        let metrics = Service::new(&conn, SERVICE_NAME, |channel| {
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
                        format_args!("create one with 'yanet-cli-acl update --name <name> --rules <path>'"),
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
                        format_args!("create one with 'yanet-cli-acl update --name <name> --rules <path>'"),
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

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let config = ACLConfig::load(&cmd.rules).map_err(|err| {
            self.service.invalid(
                "update",
                format!("failed to load rules from {}: {err}", cmd.rules.display()),
            )
        })?;
        let rule_count = config.rules.len();

        // CREATE_STATE populates a borrowed state map and therefore needs a
        // v4 and v6 fwstate-map plus a SyncConfig. Any other ruleset
        // (including CHECK_STATE, which only reads state) forwards whatever
        // the caller provided without error: a stateful ruleset may reference
        // a map it does not populate.
        let needs_create_state = config.rules.iter().any(|rule| {
            rule.actions
                .iter()
                .any(|action| action.kind == aclpb::ActionKind::CreateState as i32)
        });
        let map_name_v4 = cmd.map_name_v4.clone().filter(|name| !name.is_empty());
        let map_name_v6 = cmd.map_name_v6.clone().filter(|name| !name.is_empty());
        let has_sync = cmd.has_sync_flags();

        let (fwtable_name_v4, fwtable_name_v6, sync_config) = if needs_create_state {
            let fwtable_name_v4 = map_name_v4.ok_or_else(|| {
                self.service.invalid(
                    "update",
                    "--map-name-v4 is required when the ruleset uses ACTION_KIND_CREATE_STATE",
                )
            })?;
            let fwtable_name_v6 = map_name_v6.ok_or_else(|| {
                self.service.invalid(
                    "update",
                    "--map-name-v6 is required when the ruleset uses ACTION_KIND_CREATE_STATE",
                )
            })?;
            if !has_sync {
                return Err(self.service.invalid(
                    "update",
                    "the sync flags are required when the ruleset uses ACTION_KIND_CREATE_STATE",
                ));
            }
            let sync = self
                .build_sync_config(&cmd)
                .map_err(|err| self.service.invalid("update", err))?;
            (fwtable_name_v4, fwtable_name_v6, Some(sync))
        } else {
            let fwtable_name_v4 = map_name_v4.unwrap_or_default();
            let fwtable_name_v6 = map_name_v6.unwrap_or_default();
            let sync_config = if has_sync {
                Some(
                    self.build_sync_config(&cmd)
                        .map_err(|err| self.service.invalid("update", err))?,
                )
            } else {
                None
            };
            (fwtable_name_v4, fwtable_name_v6, sync_config)
        };

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

    /// Build a SyncConfig from the synchronization flags of an update command.
    ///
    /// Returns the underlying parse error so the caller can attach the service
    /// error context; this also keeps the `Err` variant smaller than `Ok`.
    fn build_sync_config(&self, cmd: &UpdateCmd) -> Result<aclpb::SyncConfig, String> {
        // Only provided fields are set; unset fields keep proto defaults.
        let mut sync = aclpb::SyncConfig::default();
        if let Some(ref src_addr) = cmd.src_addr {
            sync.src_addr = Some(parse_ipv6(src_addr)?);
        }
        if let Some(ref dst_ether) = cmd.dst_ether {
            sync.dst_ether = Some(parse_mac(dst_ether)?);
        }
        if let Some(ref dst_addr_multicast) = cmd.dst_addr_multicast {
            sync.dst_addr_multicast = Some(parse_ipv6(dst_addr_multicast)?);
        }
        if let Some(port_multicast) = cmd.port_multicast {
            sync.port_multicast = port_multicast;
        }
        if let Some(ref dst_addr_unicast) = cmd.dst_addr_unicast {
            sync.dst_addr_unicast = Some(parse_ipv6(dst_addr_unicast)?);
        }
        if let Some(port_unicast) = cmd.port_unicast {
            sync.port_unicast = port_unicast;
        }
        if let Some(tcp_syn_ack) = cmd.tcp_syn_ack {
            sync.tcp_syn_ack = tcp_syn_ack.as_nanos() as u64;
        }
        if let Some(tcp_syn) = cmd.tcp_syn {
            sync.tcp_syn = tcp_syn.as_nanos() as u64;
        }
        if let Some(tcp_fin) = cmd.tcp_fin {
            sync.tcp_fin = tcp_fin.as_nanos() as u64;
        }
        if let Some(tcp) = cmd.tcp {
            sync.tcp = tcp.as_nanos() as u64;
        }
        if let Some(udp) = cmd.udp {
            sync.udp = udp.as_nanos() as u64;
        }
        if let Some(default) = cmd.default {
            sync.default = default.as_nanos() as u64;
        }
        Ok(sync)
    }

    pub async fn metrics(&mut self, cmd: MetricsCmd) -> Result<(), Error> {
        let tags = cmd
            .tags
            .iter()
            .map(|entry| parse_tag(entry))
            .collect::<Result<Vec<_>, String>>()
            .map_err(|message| Error::invalid_argument("metrics", self.metrics.endpoint(), message))?;

        let response = self
            .metrics
            .client()
            .get_metrics(GetMetricsRequest { tags })
            .await
            .map_err(self.metrics.status("metrics"))?
            .into_inner();

        let metrics: Vec<commonpb::Metric> = response
            .metrics
            .into_iter()
            .filter(|m| cmd.name.as_ref().is_none_or(|f| m.name.contains(f.as_filter())))
            .collect();

        output::data(
            || &metrics,
            || {
                if metrics.is_empty() {
                    match cmd.name.as_ref().and_then(ValueEnum::to_possible_value) {
                        Some(name) => output::empty(format_args!("No ACL metrics found for '{}'.", name.get_name())),
                        None => output::empty(format_args!("No ACL metrics found.")),
                    }
                    return;
                }

                let metrics: Vec<Metric> = metrics.iter().cloned().map(Metric::from_proto).collect();
                print_metrics_table(&metrics)
            },
        );

        Ok(())
    }
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

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = ACLService::new(&cmd.connection).await?;
    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Metrics(cmd) => service.metrics(cmd).await,
    }
}

fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    start();
}

#[tokio::main(flavor = "current_thread")]
async fn start() {
    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
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
