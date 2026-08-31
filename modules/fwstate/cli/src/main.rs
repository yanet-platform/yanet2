use core::net::IpAddr;
use std::collections::HashMap;

use args::{DeleteCmd, MetricsCmd, ModeCmd, ShowCmd, UpdateCmd};
use clap::{ArgAction, CommandFactory, Parser, ValueEnum};
use clap_complete::{CompleteEnv, engine::CompletionCandidate};
use commonpb::pb::{GetMetricsRequest, IpAddress, Metric as ProtoMetric};
use fwstatepb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, ShowConfigResponse, UpdateConfigRequest,
    fw_state_service_client::FwStateServiceClient, metrics_service_client::MetricsServiceClient,
};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{Connection, ConnectionArgs, LayeredChannel, Service},
    completion,
    display::print_table_from_entries,
    errors::Error,
    metrics::{self, Kind, Metric},
    output::{self, CommonFormat},
};

mod args;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod fwstatepb {
    use serde::Serialize;

    tonic::include_proto!("modules.fwstate.controlplane.fwstatepb.v1");
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.fwstate.controlplane.fwstatepb.v1.FWStateService";

/// The fully-qualified gRPC service name for the metrics service.
const METRICS_SERVICE_NAME: &str = "modules.fwstate.controlplane.fwstatepb.v1.MetricsService";

/// FWState module CLI.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

/// Merges the linked map object names an update should carry.
///
/// The pre-flight lookup answers an unknown name with an empty message,
/// while a stored config always echoes the requested one — an empty name
/// in the reply therefore marks the create case. A create has no stored
/// names to merge from and the server rejects empty ones, so both
/// map-name flags are required then; otherwise each flag, when present,
/// overrides the stored value.
fn merged_map_names(current: &ShowConfigResponse, cmd: &UpdateCmd) -> Result<(String, String), String> {
    if current.name.is_empty() && (cmd.map_name_v4.is_none() || cmd.map_name_v6.is_none()) {
        return Err(format!(
            "creating config '{}' requires --map-name-v4 and --map-name-v6",
            cmd.config_name
        ));
    }
    Ok((
        cmd.map_name_v4.clone().unwrap_or_else(|| current.map_name_v4.clone()),
        cmd.map_name_v6.clone().unwrap_or_else(|| current.map_name_v6.clone()),
    ))
}

pub struct FWStateService {
    service: Service<FwStateServiceClient<LayeredChannel>>,
    metrics: Service<MetricsServiceClient<LayeredChannel>>,
}

impl FWStateService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let conn = Connection::connect(connection).await?;
        let service = Service::new(&conn, SERVICE_NAME, |channel| {
            FwStateServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });
        let metrics = Service::new(&conn, METRICS_SERVICE_NAME, |channel| {
            MetricsServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        });
        Ok(Self { service, metrics })
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let request = ListConfigsRequest {};
        let response = self
            .service
            .client()
            .list_configs(request)
            .await
            .map_err(self.service.status("list"))?
            .into_inner();

        output::data(
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty_with_hint(
                        format_args!("No FWState configurations found."),
                        format_args!(
                            "provision maps with 'yanet-cli-fwstatemap create --name <name> --kind <v4|v6>', then create a config with 'yanet-cli-fwstate update --name <name> --map-name-v4 <map> --map-name-v6 <map> --src-addr <addr> --dst-addr-multicast <addr> --port-multicast <port>'"
                        ),
                    );
                    return;
                }

                println!(
                    "{}",
                    serde_json::to_string_pretty(&response.configs)
                        .expect("fwstate config list JSON serialization must not fail")
                );
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowCmd) -> Result<(), Error> {
        let request = ShowConfigRequest {
            name: cmd.config_name.clone(),
            ok_if_not_found: false,
        };
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
                println!(
                    "{}",
                    serde_json::to_string_pretty(&response).expect("fwstate config JSON serialization must not fail")
                );
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
            .map_err(self.service.status("delete"))?;

        output::success("delete", format_args!("Deleted fwstate config {}.", cmd.config_name));

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        // First, fetch the current config to merge with new values
        let current_request = ShowConfigRequest {
            name: cmd.config_name.clone(),
            ok_if_not_found: true,
        };
        let current_response = self.service.client().show_config(current_request).await;
        let (map_name_v4, map_name_v6, mut sync_config) = match current_response {
            Ok(resp) => {
                let msg = resp.into_inner();
                let (map_name_v4, map_name_v6) =
                    merged_map_names(&msg, &cmd).map_err(|err| self.service.invalid("update", err))?;
                (map_name_v4, map_name_v6, msg.sync_config.unwrap_or_default())
            }
            _ => (
                cmd.map_name_v4.clone().unwrap_or_default(),
                cmd.map_name_v6.clone().unwrap_or_default(),
                Default::default(),
            ),
        };

        // Update only the fields that were provided
        if let Some(src_addr) = cmd.src_addr {
            sync_config.src_addr = Some(IpAddress::from(IpAddr::V6(src_addr)));
        }

        if let Some(dst_addr_multicast) = cmd.dst_addr_multicast {
            sync_config.dst_addr_multicast = Some(IpAddress::from(IpAddr::V6(dst_addr_multicast)));
        }

        if let Some(port_multicast) = cmd.port_multicast {
            sync_config.port_multicast = u32::from(port_multicast);
        }

        // Convert timeouts from Duration to nanoseconds if provided
        if let Some(tcp_syn_ack) = cmd.tcp_syn_ack {
            sync_config.tcp_syn_ack = tcp_syn_ack.as_nanos() as u64;
        }

        if let Some(tcp_syn) = cmd.tcp_syn {
            sync_config.tcp_syn = tcp_syn.as_nanos() as u64;
        }

        if let Some(tcp_fin) = cmd.tcp_fin {
            sync_config.tcp_fin = tcp_fin.as_nanos() as u64;
        }

        if let Some(tcp) = cmd.tcp {
            sync_config.tcp = tcp.as_nanos() as u64;
        }

        if let Some(udp) = cmd.udp {
            sync_config.udp = udp.as_nanos() as u64;
        }

        if let Some(default) = cmd.default {
            sync_config.default = default.as_nanos() as u64;
        }

        if let Some(suppress) = cmd.sync_suppress_timeout {
            sync_config.sync_suppress_timeout = suppress.as_nanos() as u64;
        }

        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            map_name_v4,
            map_name_v6,
            sync_config: Some(sync_config),
        };
        log::trace!("UpdateConfigRequest: {request:?}");
        self.service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?;

        output::success("update", format_args!("Updated fwstate config {}.", cmd.config_name));

        Ok(())
    }

    pub async fn metrics(&mut self, cmd: MetricsCmd) -> Result<(), Error> {
        let response = self
            .metrics
            .client()
            .get_metrics(GetMetricsRequest::default())
            .await
            .map_err(self.metrics.status("metrics"))?
            .into_inner();
        let mut label_filters: Vec<(&str, &str)> = Vec::with_capacity(cmd.labels.len());
        for s in &cmd.labels {
            match s.split_once('=') {
                Some(kv) => label_filters.push(kv),
                None => {
                    return Err(self
                        .metrics
                        .invalid("metrics", format!("invalid label filter {s:?}, expected KEY=VALUE")));
                }
            }
        }

        let metrics: Vec<ProtoMetric> = response
            .metrics
            .into_iter()
            .filter(|m| {
                if let Some(ref f) = cmd.name
                    && !f.matches(m)
                {
                    return false;
                }
                label_filters
                    .iter()
                    .all(|(k, v)| metrics::proto_label_value(m, k) == Some(v))
            })
            .collect();

        output::data(
            || &metrics,
            || {
                if metrics.is_empty() {
                    match cmd.name.as_ref().and_then(ValueEnum::to_possible_value) {
                        Some(name) => {
                            output::empty(format_args!("No FWState metrics found for '{}'.", name.get_name()))
                        }
                        None => output::empty(format_args!("No FWState metrics found.")),
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

#[derive(Tabled)]
struct CounterRow {
    #[tabled(rename = "Counter")]
    counter: String,
    #[tabled(rename = "Packets")]
    packets: String,
    #[tabled(rename = "Bytes")]
    bytes: String,
    #[tabled(rename = "Entries")]
    entries: String,
}

#[derive(Tabled)]
struct GaugeRow {
    #[tabled(rename = "Map")]
    map: String,
    #[tabled(rename = "AF")]
    af: String,
    #[tabled(rename = "Index size")]
    index_size: String,
    #[tabled(rename = "Extra buckets")]
    extra_buckets: String,
    #[tabled(rename = "Max chain")]
    max_chain: String,
    #[tabled(rename = "Layers")]
    layers: String,
    #[tabled(rename = "Elements")]
    elements: String,
    #[tabled(rename = "Max deadline TTL")]
    max_deadline_ttl: String,
    #[tabled(rename = "Memory")]
    memory: String,
}

fn print_metrics_table(metrics: &[Metric]) {
    struct CounterPair {
        display: String,
        packets: Option<u64>,
        bytes: Option<u64>,
        entries: Option<u64>,
    }

    let mut counter_keys: Vec<String> = Vec::new();
    let mut counter_map: HashMap<String, Vec<&Metric>> = HashMap::new();
    let mut map_gauges: HashMap<(String, String), HashMap<String, u64>> = HashMap::new();
    let mut map_gauge_order: Vec<(String, String)> = Vec::new();
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

        // State-map gauges (fwstate_total_elements and siblings) carry a
        // map name and an address family; collect them per map for the
        // gauge section below.
        if m.kind == Kind::Gauge && m.label_value("map").is_some() {
            let key = (
                m.label_value("map").unwrap_or_default().to_string(),
                m.label_value("af").unwrap_or_default().to_string(),
            );
            let entry = map_gauges.entry(key.clone()).or_insert_with(|| {
                map_gauge_order.push(key);
                HashMap::new()
            });
            entry.insert(m.name.clone(), m.value.unwrap_or(0.0) as u64);
            continue;
        }

        if m.kind == Kind::Counter {
            let key = format!(
                "{}\0{}\0{}\0{}\0{}",
                m.label_value("config").unwrap_or(""),
                m.label_value("device").unwrap_or(""),
                m.label_value("pipeline").unwrap_or(""),
                m.label_value("function").unwrap_or(""),
                m.label_value("chain").unwrap_or(""),
            );
            if !counter_map.contains_key(&key) {
                counter_keys.push(key.clone());
            }
            counter_map.entry(key).or_default().push(m);
        }
    }

    for key in &counter_keys {
        let counters = &counter_map[key];
        let parts: Vec<&str> = key.split('\0').collect();
        let (config, device, pipeline, function, chain) = (parts[0], parts[1], parts[2], parts[3], parts[4]);
        println!(
            "FWSTATE COUNTERS  config={config} device={device} pipeline={pipeline} function={function} chain={chain}"
        );
        println!();

        let mut pair_order: Vec<String> = Vec::new();
        let mut pair_map: HashMap<String, CounterPair> = HashMap::new();

        // Generic counters (the default arm of emitCounterMetrics) are all
        // exported under the shared names fwstate_counter_packets /
        // fwstate_counter_bytes and distinguished only by the "counter" label.
        // After stripping the fwstate_ prefix and the _packets/_bytes suffix
        // they all collapse to the same base "counter", so the pair key must
        // include the "counter" label value to keep them as separate rows
        // instead of overwriting each other (last write wins). Dedicated
        // counters carry no "counter" label, so the suffix is empty and their
        // base is unaffected.
        for m in counters {
            let val = m.value.unwrap_or(0.0) as u64;
            let stripped = m.name.strip_prefix("fwstate_").unwrap_or(&m.name);
            let counter_label = m.label_value("counter").unwrap_or("");

            // Determine the semantic suffix and the base name shared by the
            // packets/bytes series of the same counter. The pair key and the
            // display name must be built from the base (suffix stripped),
            // otherwise fwstate_rx_packets and fwstate_rx_bytes get different
            // keys and end up on two separate rows instead of one.
            let (base, is_packets, is_bytes) = if let Some(b) = stripped.strip_suffix("_packets") {
                (b, true, false)
            } else if let Some(b) = stripped.strip_suffix("_bytes") {
                (b, false, true)
            } else {
                (stripped, false, false)
            };

            let display = if counter_label.is_empty() {
                metrics::metric_display_name(base, "fwstate_")
            } else {
                metrics::metric_display_name(counter_label, "fwstate_")
            };
            let pair_key = format!("{base}\0{counter_label}");

            let pair = pair_map.entry(pair_key.clone()).or_insert_with(|| {
                pair_order.push(pair_key.clone());
                CounterPair {
                    display,
                    packets: None,
                    bytes: None,
                    entries: None,
                }
            });

            if is_packets {
                pair.packets = Some(val);
            } else if is_bytes {
                pair.bytes = Some(val);
            } else {
                // State-table entry counters (e.g. sync insert counters) and
                // any unsuffixed counter count frames/items, not
                // packets/bytes; render under Entries.
                pair.entries = Some(val);
            }
        }

        let rows: Vec<CounterRow> = pair_order
            .iter()
            .map(|pair_key| {
                let p = &pair_map[pair_key];
                CounterRow {
                    counter: p.display.clone(),
                    packets: p.packets.map(metrics::format_number).unwrap_or_else(|| "-".into()),
                    bytes: p.bytes.map(metrics::format_number).unwrap_or_else(|| "-".into()),
                    entries: p.entries.map(metrics::format_number).unwrap_or_else(|| "-".into()),
                }
            })
            .collect();
        print_table_from_entries(rows);
        println!();
    }

    if !map_gauge_order.is_empty() {
        let rows: Vec<GaugeRow> = map_gauge_order
            .into_iter()
            .map(|(map, af)| {
                let gauges = &map_gauges[&(map.clone(), af.clone())];
                let get = |name: &str| gauges.get(name).copied();
                GaugeRow {
                    map,
                    af,
                    index_size: get("fwstate_index_size")
                        .map(metrics::format_number)
                        .unwrap_or_else(|| "-".into()),
                    extra_buckets: get("fwstate_extra_bucket_count")
                        .map(metrics::format_number)
                        .unwrap_or_else(|| "-".into()),
                    max_chain: get("fwstate_max_chain_length")
                        .map(metrics::format_number)
                        .unwrap_or_else(|| "-".into()),
                    layers: get("fwstate_layer_count")
                        .map(metrics::format_number)
                        .unwrap_or_else(|| "-".into()),
                    elements: get("fwstate_total_elements")
                        .map(metrics::format_number)
                        .unwrap_or_else(|| "-".into()),
                    max_deadline_ttl: get("fwstate_max_deadline_ns")
                        .map(metrics::format_number)
                        .unwrap_or_else(|| "-".into()),
                    memory: get("fwstate_memory_bytes")
                        .map(metrics::format_number)
                        .unwrap_or_else(|| "-".into()),
                }
            })
            .collect();
        println!("FWSTATE STATE MAPS");
        println!();
        print_table_from_entries(rows);
        println!();
    }

    metrics::print_grpc_metrics(&grpc_counters, &grpc_histograms);
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = FWStateService::new(&cmd.connection).await?;

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

/// Completion candidates for a `--name` argument: the fwstate configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            FwStateServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cmd_is_valid() {
        Cmd::command().debug_assert();
    }

    /// Update command carrying only the config name and optional map names.
    fn update_cmd(config_name: &str, map_name_v4: Option<&str>, map_name_v6: Option<&str>) -> UpdateCmd {
        UpdateCmd {
            config_name: config_name.to_string(),
            map_name_v4: map_name_v4.map(str::to_string),
            map_name_v6: map_name_v6.map(str::to_string),
            src_addr: None,
            dst_addr_multicast: None,
            port_multicast: None,
            tcp_syn_ack: None,
            tcp_syn: None,
            tcp_fin: None,
            tcp: None,
            udp: None,
            default: None,
            sync_suppress_timeout: None,
        }
    }

    /// Reply for the given config name and its two linked map objects.
    fn show_response(name: &str, map_name_v4: &str, map_name_v6: &str) -> ShowConfigResponse {
        ShowConfigResponse {
            name: name.to_string(),
            map_name_v4: map_name_v4.to_string(),
            map_name_v6: map_name_v6.to_string(),
            sync_config: None,
        }
    }

    #[test]
    fn test_merged_map_names_create_without_map_names_is_rejected() {
        let cmd = update_cmd("cfg", None, None);
        let err = merged_map_names(&show_response("", "", ""), &cmd).unwrap_err();

        assert_eq!("creating config 'cfg' requires --map-name-v4 and --map-name-v6", err);
    }

    #[test]
    fn test_merged_map_names_create_with_one_map_name_is_rejected() {
        let empty_reply = show_response("", "", "");
        assert!(merged_map_names(&empty_reply, &update_cmd("cfg", Some("v4"), None)).is_err());
        assert!(merged_map_names(&empty_reply, &update_cmd("cfg", None, Some("v6"))).is_err());
    }

    #[test]
    fn test_merged_map_names_create_with_both_map_names_uses_flags() {
        let cmd = update_cmd("cfg", Some("map4"), Some("map6"));
        let (map_name_v4, map_name_v6) = merged_map_names(&show_response("", "", ""), &cmd).unwrap();

        assert_eq!(("map4", "map6"), (map_name_v4.as_str(), map_name_v6.as_str()));
    }

    #[test]
    fn test_merged_map_names_existing_config_keeps_stored_names_without_flags() {
        let cmd = update_cmd("cfg", None, None);
        let (map_name_v4, map_name_v6) = merged_map_names(&show_response("cfg", "stored4", "stored6"), &cmd).unwrap();

        assert_eq!(("stored4", "stored6"), (map_name_v4.as_str(), map_name_v6.as_str()));
    }

    #[test]
    fn test_merged_map_names_existing_config_flag_overrides_stored_name() {
        let reply = show_response("cfg", "stored4", "stored6");
        let (map_name_v4, map_name_v6) = merged_map_names(&reply, &update_cmd("cfg", Some("new4"), None)).unwrap();

        assert_eq!(("new4", "stored6"), (map_name_v4.as_str(), map_name_v6.as_str()));
    }
}
