use std::{collections::HashMap, fs::File, path::Path};

use aclpb::{
    DeleteConfigRequest, GetMetricsRequest, ListConfigsRequest, ShowConfigRequest, UpdateConfigRequest,
    acl_service_client::AclServiceClient, metrics_service_client::MetricsServiceClient,
};
use args::{DeleteCmd, MetricsCmd, ModeCmd, ShowCmd, UpdateCmd};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use metric::Metric;
use serde::{Deserialize, Serialize};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    display::print_table_from_entries,
    errors::Error,
    output::{self, CommonFormat},
};

mod args;
mod metric;

use ::commonpb::pb as commonpb;

#[allow(non_snake_case)]
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

#[derive(Tabled)]
struct GaugeRow {
    #[tabled(rename = "Metric")]
    metric: String,
    #[tabled(rename = "Value")]
    value: String,
}

#[derive(Tabled)]
struct GrpcCallRow {
    #[tabled(rename = "Method")]
    method: String,
    #[tabled(rename = "Code")]
    code: String,
    #[tabled(rename = "Handled")]
    handled: String,
}

#[derive(Tabled)]
struct GrpcLatRow {
    #[tabled(rename = "Method")]
    method: String,
    #[tabled(rename = "Total Calls")]
    total: String,
    #[tabled(rename = "P50")]
    p50: String,
    #[tabled(rename = "P95")]
    p95: String,
    #[tabled(rename = "P99")]
    p99: String,
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

fn format_number(n: u64) -> String {
    let s = n.to_string();
    let mut result = String::new();
    for (i, c) in s.chars().rev().enumerate() {
        if i > 0 && i % 3 == 0 {
            result.push(',');
        }
        result.push(c);
    }
    result.chars().rev().collect()
}

fn format_gauge_value(name: &str, value: f64) -> String {
    if name.ends_with("_ns") {
        if value < 1_000.0 {
            format!("{:.0}ns", value)
        } else if value < 1_000_000.0 {
            format!("{:.2}µs", value / 1_000.0)
        } else if value < 1_000_000_000.0 {
            format!("{:.2}ms", value / 1_000_000.0)
        } else {
            format!("{:.2}s", value / 1_000_000_000.0)
        }
    } else if name.ends_with("_bytes") {
        if value < 1024.0 {
            format!("{:.0} B", value)
        } else if value < 1024.0 * 1024.0 {
            format!("{:.2} KB", value / 1024.0)
        } else if value < 1024.0 * 1024.0 * 1024.0 {
            format!("{:.2} MB", value / (1024.0 * 1024.0))
        } else {
            format!("{:.2} GB", value / (1024.0 * 1024.0 * 1024.0))
        }
    } else {
        format_number(value as u64)
    }
}

fn metric_display_name(name: &str) -> String {
    let stripped = name.strip_prefix("acl_").unwrap_or(name);
    stripped
        .split('_')
        .map(|word| {
            let mut c = word.chars();
            match c.next() {
                None => String::new(),
                Some(first) => first.to_uppercase().collect::<String>() + c.as_str(),
            }
        })
        .collect::<Vec<_>>()
        .join(" ")
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
                metric::Kind::Counter => grpc_counters.push(m),
                metric::Kind::Histogram => grpc_histograms.push(m),
                _ => {}
            }
            continue;
        }

        match m.kind {
            metric::Kind::Histogram => {}
            metric::Kind::Gauge => {
                let cfg = m.label_value("config").unwrap_or("global").to_string();
                if !gauge_map.contains_key(&cfg) {
                    gauge_keys.push(cfg.clone());
                }
                gauge_map.entry(cfg).or_default().push(m);
            }
            metric::Kind::Counter => {
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
            metric::Kind::Unknown => {}
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
                        display: metric_display_name(base),
                        packets: None,
                        bytes: None,
                    }
                });
                pair.packets = Some(val);
            } else if let Some(base) = stripped.strip_suffix("_bytes") {
                let pair = pair_map.entry(base.to_string()).or_insert_with(|| {
                    pair_order.push(base.to_string());
                    CounterPair {
                        display: metric_display_name(base),
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
                        packets: p.packets.map(format_number).unwrap_or_else(|| "-".into()),
                        bytes: p.bytes.map(format_number).unwrap_or_else(|| "-".into()),
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
                        packets: pkts.map(format_number).unwrap_or_else(|| "-".into()),
                        bytes: b.map(format_number).unwrap_or_else(|| "-".into()),
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
                metric: metric_display_name(&m.name),
                value: format_gauge_value(&m.name, m.value.unwrap_or(0.0)),
            })
            .collect();
        print_table_from_entries(rows);
        println!();
    }

    if !grpc_counters.is_empty() {
        // Collect started counts keyed by grpc_method.
        let mut started: HashMap<String, u64> = HashMap::new();
        // Collect handled counts keyed by (grpc_method, grpc_code), preserving order.
        let mut handled_keys: Vec<(String, String)> = Vec::new();
        let mut handled: HashMap<(String, String), u64> = HashMap::new();

        for m in &grpc_counters {
            let method = m.label_value("grpc_method").unwrap_or("").to_string();
            if m.name == "grpc_server_started_total" {
                let count = m.value.unwrap_or(0.0) as u64;
                *started.entry(method).or_default() += count;
            } else if m.name == "grpc_server_handled_total" {
                let code = m.label_value("grpc_code").unwrap_or("").to_string();
                let key = (method, code);
                if !handled.contains_key(&key) {
                    handled_keys.push(key.clone());
                }
                *handled.entry(key).or_default() += m.value.unwrap_or(0.0) as u64;
            }
        }

        if !handled_keys.is_empty() || !started.is_empty() {
            println!();
            println!("GRPC CALLS");
            println!();
        }

        if !handled_keys.is_empty() {
            let rows: Vec<GrpcCallRow> = handled_keys
                .iter()
                .map(|(method, code)| GrpcCallRow {
                    method: method.clone(),
                    code: code.clone(),
                    handled: format_number(handled[&(method.clone(), code.clone())]),
                })
                .collect();
            print_table_from_entries(rows);
        }

        if !started.is_empty() {
            println!();
            let mut started_methods: Vec<&String> = started.keys().collect();
            started_methods.sort();
            for method in started_methods {
                println!("  started  {method}: {}", format_number(started[method]));
            }
        }
    }

    if !grpc_histograms.is_empty() {
        println!();
        println!("GRPC HANDLING LATENCIES");
        println!();
        let rows: Vec<GrpcLatRow> = grpc_histograms
            .iter()
            .map(|m| {
                let method = m.label_value("grpc_method").unwrap_or("unknown").to_string();
                match &m.histogram {
                    Some(h) => GrpcLatRow {
                        method,
                        total: format_number(h.total_count),
                        p50: metric::histogram_percentile(&h.buckets, h.total_count, 50.0),
                        p95: metric::histogram_percentile(&h.buckets, h.total_count, 95.0),
                        p99: metric::histogram_percentile(&h.buckets, h.total_count, 99.0),
                    },
                    None => GrpcLatRow {
                        method,
                        total: "-".into(),
                        p50: "-".into(),
                        p95: "-".into(),
                        p99: "-".into(),
                    },
                }
            })
            .collect();
        print_table_from_entries(rows);
    }
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

pub struct ACLService {
    client: AclServiceClient<LayeredChannel>,
    metrics_client: MetricsServiceClient<LayeredChannel>,
    endpoint: String,
}

impl ACLService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let channel = ync::client::connect(connection)
            .await
            .map_err(|e| Error::from_connection(e, "connect", &connection.endpoint))?;
        let client = AclServiceClient::new(channel.clone())
            .max_decoding_message_size(256 * 1024 * 1024)
            .max_encoding_message_size(256 * 1024 * 1024)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        let metrics_client = MetricsServiceClient::new(channel)
            .max_decoding_message_size(256 * 1024 * 1024)
            .max_encoding_message_size(256 * 1024 * 1024)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);

        Ok(Self {
            client,
            metrics_client,
            endpoint: connection.endpoint.clone(),
        })
    }

    fn map_err<'a>(&'a self, action: &'a str) -> impl FnOnce(tonic::Status) -> Error + 'a {
        let endpoint = self.endpoint.clone();
        move |status| Error::from_status(status, action, endpoint, SERVICE_NAME)
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let response = self
            .client
            .list_configs(ListConfigsRequest {})
            .await
            .map_err(self.map_err("list"))?
            .into_inner();

        output::data(
            &response.configs,
            response.configs.is_empty(),
            format_args!("no configurations"),
            || {
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
            .client
            .show_config(request)
            .await
            .map_err(self.map_err("show"))?
            .into_inner();

        output::data(&response, false, format_args!(""), || {
            let config = ACLConfig { rules: response.rules.clone() };
            print!(
                "{}",
                serde_yaml::to_string(&config).expect("ACL config YAML serialization must not fail")
            );
        });

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        self.client
            .delete_config(request)
            .await
            .map_err(self.map_err("delete"))?
            .into_inner();

        output::success("delete", format_args!("Deleted {}.", cmd.config_name));

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let config = ACLConfig::load(&cmd.rules).map_err(|err| {
            Error::from_status(
                tonic::Status::invalid_argument(format!("failed to load rules from {}: {err}", cmd.rules.display())),
                "update",
                self.endpoint.clone(),
                SERVICE_NAME,
            )
        })?;
        let rule_count = config.rules.len();
        let request = UpdateConfigRequest {
            name: cmd.config_name.clone(),
            rules: config.rules,
        };
        log::trace!("UpdateConfigRequest: {request:?}");
        let response = self
            .client
            .update_config(request)
            .await
            .map_err(self.map_err("update"))?
            .into_inner();
        log::debug!("UpdateConfigResponse: {response:?}");

        output::success(
            "update",
            format_args!("Updated {} ({} rules).", cmd.config_name, rule_count),
        );

        Ok(())
    }

    pub async fn metrics(&mut self, cmd: MetricsCmd) -> Result<(), Error> {
        let response = self
            .metrics_client
            .get_metrics(GetMetricsRequest {})
            .await
            .map_err(self.map_err("metrics"))?
            .into_inner();
        let label_filters: Vec<(&str, &str)> = cmd
            .labels
            .iter()
            .filter_map(|s| {
                let mut it = s.splitn(2, '=');
                Some((it.next()?, it.next()?))
            })
            .collect();

        let metrics: Vec<Metric> = response
            .metrics
            .into_iter()
            .map(Metric::from_proto)
            .filter(|m| {
                if let Some(ref f) = cmd.name {
                    if !m.name.contains(f.as_filter()) {
                        return false;
                    }
                }
                label_filters.iter().all(|(k, v)| m.label_value(k) == Some(v))
            })
            .collect();

        output::data(&metrics, metrics.is_empty(), format_args!("no metrics"), || {
            print_metrics_table(&metrics)
        });

        Ok(())
    }
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

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
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
}
