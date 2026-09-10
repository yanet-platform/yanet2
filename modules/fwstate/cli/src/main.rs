use core::net::IpAddr;

use args::{DeleteCmd, ModeCmd, ShowCmd, UpdateCmd};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::CompletionCandidate;
use commonpb::pb::{IpAddress, MacAddress};
use fwstatepb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, ShowConfigResponse, SyncConfig, UpdateConfigRequest,
    fw_state_service_client::FwStateServiceClient,
};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    client::{Connection, ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::Error,
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

fn client(channel: LayeredChannel) -> FwStateServiceClient<LayeredChannel> {
    FwStateServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Manages fwstate module configs.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose: shows debug log lines and raw gRPC error details.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

/// Makes text that came off the wire safe to hand a terminal.
///
/// A name is stored as it was given, so it can carry an escape sequence or
/// a newline. It is spelled out rather than dropped: two names that differ
/// only in one must not read alike.
fn escape_wire_text(value: &str) -> String {
    value.escape_debug().to_string()
}

/// Orders the stored configuration names for a human reader.
///
/// The service builds its reply by walking a map, so the order it answers
/// in changes between calls and would otherwise reshuffle the listing under
/// an operator watching it.
fn config_list_rows(configs: &[String]) -> Vec<ConfigRow> {
    let mut names: Vec<&String> = configs.iter().collect();
    names.sort();

    names
        .into_iter()
        .map(|name| ConfigRow { config: escape_wire_text(name) })
        .collect()
}

/// Renders a stored nanosecond timeout as the milliseconds an operator reads.
///
/// Zero is a meaningful setting here, so a value below a millisecond keeps
/// its remainder rather than truncating into one.
fn format_timeout_millis(nanos: u64) -> String {
    const NANOS_PER_MILLI: u64 = 1_000_000;

    let millis = nanos / NANOS_PER_MILLI;
    let remainder = nanos % NANOS_PER_MILLI;
    if remainder == 0 {
        return millis.to_string();
    }

    let fraction = format!("{remainder:06}");
    format!("{millis}.{}", fraction.trim_end_matches('0'))
}

#[derive(Tabled)]
struct ConfigRow {
    #[tabled(rename = "Config")]
    config: String,
}

#[derive(Tabled)]
struct SettingRow {
    #[tabled(rename = "Setting")]
    setting: String,
    #[tabled(rename = "Value")]
    value: String,
}

impl SettingRow {
    fn new(setting: &str, value: String) -> Self {
        Self { setting: setting.to_string(), value }
    }
}

/// Lays out one stored configuration for a human reader.
fn config_rows(response: &ShowConfigResponse) -> Vec<SettingRow> {
    let mut rows = vec![
        SettingRow::new("name", escape_wire_text(&response.name)),
        SettingRow::new("map name v4", escape_wire_text(&response.map_name_v4)),
        SettingRow::new("map name v6", escape_wire_text(&response.map_name_v6)),
    ];

    let Some(sync_config) = response.sync_config.as_ref() else {
        return rows;
    };

    let address = |addr: Option<&IpAddress>| addr.map_or_else(|| "-".to_string(), ToString::to_string);
    rows.push(SettingRow::new("src addr", address(sync_config.src_addr.as_ref())));
    rows.push(SettingRow::new(
        "dst ether",
        sync_config
            .dst_ether
            .as_ref()
            .map_or_else(|| "-".to_string(), ToString::to_string),
    ));
    rows.push(SettingRow::new(
        "dst addr multicast",
        address(sync_config.dst_addr_multicast.as_ref()),
    ));
    rows.push(SettingRow::new(
        "port multicast",
        sync_config.port_multicast.to_string(),
    ));
    rows.push(SettingRow::new(
        "dst addr unicast",
        address(sync_config.dst_addr_unicast.as_ref()),
    ));
    rows.push(SettingRow::new("port unicast", sync_config.port_unicast.to_string()));

    for (setting, nanos) in [
        ("tcp syn-ack timeout", sync_config.tcp_syn_ack),
        ("tcp syn timeout", sync_config.tcp_syn),
        ("tcp fin timeout", sync_config.tcp_fin),
        ("tcp timeout", sync_config.tcp),
        ("udp timeout", sync_config.udp),
        ("default timeout", sync_config.default),
        ("sync suppress timeout", sync_config.sync_suppress_timeout),
    ] {
        rows.push(SettingRow::new(setting, format!("{} ms", format_timeout_millis(nanos))));
    }

    rows
}

/// Builds a partial update from explicitly supplied flags only.
fn update_request(cmd: &UpdateCmd) -> Result<UpdateConfigRequest, &'static str> {
    let mut request = UpdateConfigRequest {
        name: cmd.config_name.clone(),
        ..Default::default()
    };
    let mut paths = Vec::new();
    let mut sync_config = fwstatepb::SyncConfig::default();
    if let Some(value) = &cmd.map_name_v4 {
        request.map_name_v4 = value.clone();
        paths.push("map_name_v4".to_string());
    }
    if let Some(value) = &cmd.map_name_v6 {
        request.map_name_v6 = value.clone();
        paths.push("map_name_v6".to_string());
    }
    if let Some(value) = cmd.src_addr {
        sync_config.src_addr = Some(IpAddress::from(IpAddr::V6(value)));
        paths.push("sync_config.src_addr".to_string());
    }
    if let Some(value) = cmd.dst_ether {
        sync_config.dst_ether = Some(MacAddress::from(value));
        paths.push("sync_config.dst_ether".to_string());
    }
    update_sync_endpoints(&mut sync_config, cmd)?;
    if cmd.multicast.is_some() || cmd.no_multicast || cmd.dst_addr_multicast.is_some() {
        paths.push("sync_config.dst_addr_multicast".to_string());
    }
    if cmd.multicast.is_some() || cmd.no_multicast || cmd.port_multicast.is_some() {
        paths.push("sync_config.port_multicast".to_string());
    }
    if cmd.unicast.is_some() || cmd.no_unicast || cmd.dst_addr_unicast.is_some() {
        paths.push("sync_config.dst_addr_unicast".to_string());
    }
    if cmd.unicast.is_some() || cmd.no_unicast || cmd.port_unicast.is_some() {
        paths.push("sync_config.port_unicast".to_string());
    }
    if let Some(value) = cmd.tcp_syn_ack {
        sync_config.tcp_syn_ack = value.as_nanos() as u64;
        paths.push("sync_config.tcp_syn_ack".to_string());
    }
    if let Some(value) = cmd.tcp_syn {
        sync_config.tcp_syn = value.as_nanos() as u64;
        paths.push("sync_config.tcp_syn".to_string());
    }
    if let Some(value) = cmd.tcp_fin {
        sync_config.tcp_fin = value.as_nanos() as u64;
        paths.push("sync_config.tcp_fin".to_string());
    }
    if let Some(value) = cmd.tcp {
        sync_config.tcp = value.as_nanos() as u64;
        paths.push("sync_config.tcp".to_string());
    }
    if let Some(value) = cmd.udp {
        sync_config.udp = value.as_nanos() as u64;
        paths.push("sync_config.udp".to_string());
    }
    if let Some(value) = cmd.default {
        sync_config.default = value.as_nanos() as u64;
        paths.push("sync_config.default".to_string());
    }
    if let Some(value) = cmd.sync_suppress_timeout {
        sync_config.sync_suppress_timeout = value.as_nanos() as u64;
        paths.push("sync_config.sync_suppress_timeout".to_string());
    }
    request.sync_config = Some(sync_config);
    request.update_mask = Some(fwstatepb::FieldMask { paths });
    Ok(request)
}

fn remove_multicast_endpoint(sync_config: &mut SyncConfig) {
    sync_config.dst_addr_multicast = None;
    sync_config.port_multicast = 0;
}

fn remove_unicast_endpoint(sync_config: &mut SyncConfig) {
    sync_config.dst_addr_unicast = None;
    sync_config.port_unicast = 0;
}

fn update_sync_endpoints(sync_config: &mut SyncConfig, cmd: &UpdateCmd) -> Result<(), &'static str> {
    if cmd.multicast.is_some_and(|endpoint| endpoint.scope_id() != 0) {
        return Err("--multicast does not support IPv6 scope IDs");
    }
    if cmd.unicast.is_some_and(|endpoint| endpoint.scope_id() != 0) {
        return Err("--unicast does not support IPv6 scope IDs");
    }

    if let Some(multicast) = cmd.multicast {
        sync_config.dst_addr_multicast = Some(IpAddress::from(IpAddr::V6(*multicast.ip())));
        sync_config.port_multicast = u32::from(multicast.port());
    }
    if let Some(dst_addr_multicast) = cmd.dst_addr_multicast {
        sync_config.dst_addr_multicast = Some(IpAddress::from(IpAddr::V6(dst_addr_multicast)));
    }
    if let Some(port_multicast) = cmd.port_multicast {
        sync_config.port_multicast = u32::from(port_multicast);
    }

    if let Some(unicast) = cmd.unicast {
        sync_config.dst_addr_unicast = Some(IpAddress::from(IpAddr::V6(*unicast.ip())));
        sync_config.port_unicast = u32::from(unicast.port());
    }
    if let Some(dst_addr_unicast) = cmd.dst_addr_unicast {
        sync_config.dst_addr_unicast = Some(IpAddress::from(IpAddr::V6(dst_addr_unicast)));
    }
    if let Some(port_unicast) = cmd.port_unicast {
        sync_config.port_unicast = u32::from(port_unicast);
    }

    if cmd.no_multicast {
        remove_multicast_endpoint(sync_config);
    }
    if cmd.no_unicast {
        remove_unicast_endpoint(sync_config);
    }
    Ok(())
}

pub struct FWStateService {
    service: Service<FwStateServiceClient<LayeredChannel>>,
}

impl FWStateService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let conn = Connection::connect_for(connection, action).await?;
        let service = Service::new(&conn, SERVICE_NAME, client);
        Ok(Self { service })
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
                            "provision maps with 'yanet-cli-fwstatemap create --name <map> --kind <v4|v6>', then create a config with 'yanet-cli-fwstate update --name <name> --map-name-v4 <map> --map-name-v6 <map>'"
                        ),
                    );
                    return;
                }

                ync::display::print_table_from_entries(config_list_rows(&response.configs));
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
            || ync::display::print_table_from_entries(config_rows(&response)),
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

        output::success("delete", format_args!("Deleted config '{}'.", cmd.config_name));

        Ok(())
    }

    pub async fn update_config(&mut self, cmd: UpdateCmd) -> Result<(), Error> {
        let request = update_request(&cmd).map_err(|err| self.service.invalid("update", err))?;
        log::trace!("UpdateConfigRequest: {request:?}");
        self.service
            .client()
            .update_config(request)
            .await
            .map_err(self.service.status("update"))?;

        output::success("update", format_args!("Updated config '{}'.", cmd.config_name));

        Ok(())
    }
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = FWStateService::new(&cmd.connection, action).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Update(cmd) => service.update_config(cmd).await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

/// Completion candidates for a `--name` argument: the fwstate configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Update command carrying only the config name and optional map names.
    fn update_cmd(config_name: &str, map_name_v4: Option<&str>, map_name_v6: Option<&str>) -> UpdateCmd {
        UpdateCmd {
            config_name: config_name.to_string(),
            map_name_v4: map_name_v4.map(str::to_string),
            map_name_v6: map_name_v6.map(str::to_string),
            src_addr: None,
            dst_ether: None,
            multicast: None,
            unicast: None,
            dst_addr_multicast: None,
            port_multicast: None,
            dst_addr_unicast: None,
            port_unicast: None,
            no_multicast: false,
            no_unicast: false,
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
    fn test_format_timeout_millis_whole_milliseconds() {
        assert_eq!("120000", format_timeout_millis(120_000_000_000));
        assert_eq!("16000", format_timeout_millis(16_000_000_000));
        assert_eq!("1", format_timeout_millis(1_000_000));
    }

    #[test]
    fn test_format_timeout_millis_zero_stays_zero() {
        assert_eq!("0", format_timeout_millis(0));
    }

    #[test]
    fn test_format_timeout_millis_below_a_millisecond_keeps_its_remainder() {
        assert_eq!("0.5", format_timeout_millis(500_000));
        assert_eq!("0.001", format_timeout_millis(1_000));
        assert_eq!("0.000001", format_timeout_millis(1));
    }

    #[test]
    fn test_format_timeout_millis_fractional_milliseconds() {
        assert_eq!("1.5", format_timeout_millis(1_500_000));
        assert_eq!("60000.1", format_timeout_millis(60_000_100_000));
    }

    #[test]
    fn test_config_rows_carry_endpoints_and_converted_timeouts() {
        let response = ShowConfigResponse {
            name: "fwstate0".to_string(),
            sync_config: Some(fwstatepb::SyncConfig {
                src_addr: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::LOCALHOST))),
                dst_ether: Some(MacAddress { addr: 0x3333_0000_0001 }),
                dst_addr_multicast: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::UNSPECIFIED))),
                port_multicast: 9999,
                dst_addr_unicast: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::LOCALHOST))),
                port_unicast: 10000,
                tcp: 60_000_000_000,
                sync_suppress_timeout: 0,
                ..Default::default()
            }),
            ..Default::default()
        };

        let rows: Vec<(String, String)> = config_rows(&response)
            .into_iter()
            .map(|row| (row.setting, row.value))
            .collect();
        for expected in [
            ("src addr", "::1"),
            ("dst ether", "33:33:00:00:00:01"),
            ("dst addr multicast", "::"),
            ("port multicast", "9999"),
            ("dst addr unicast", "::1"),
            ("port unicast", "10000"),
            ("tcp timeout", "60000 ms"),
            ("sync suppress timeout", "0 ms"),
        ] {
            assert!(
                rows.iter().any(|row| row.0 == expected.0 && row.1 == expected.1),
                "missing {expected:?} in {rows:?}"
            );
        }
        assert!(
            !rows.iter().any(|row| row.1.contains("60000000000")),
            "no row may carry the stored nanoseconds: {rows:?}"
        );
    }

    #[test]
    fn test_config_rows_without_sync_config_omit_timeouts() {
        let response = show_response("fwstate0", "map4", "map6");

        let settings: Vec<String> = config_rows(&response).into_iter().map(|row| row.setting).collect();
        assert_eq!(vec!["name", "map name v4", "map name v6"], settings);
    }

    #[test]
    fn test_escape_wire_text_spells_out_control_characters() {
        assert_eq!("fwstate0", escape_wire_text("fwstate0"));
        assert_eq!("a\\nb", escape_wire_text("a\nb"));
        assert_eq!("\\u{1b}\\r[2J", escape_wire_text("\u{1b}\r[2J"));
    }

    #[test]
    fn test_config_list_rows_are_ordered() {
        let configs = ["fwstate2".to_string(), "fwstate0".to_string(), "fwstate1".to_string()];

        let names: Vec<String> = config_list_rows(&configs).into_iter().map(|row| row.config).collect();
        assert_eq!(vec!["fwstate0", "fwstate1", "fwstate2"], names);
    }

    #[test]
    fn test_config_rows_escape_names() {
        let response = show_response("fw\nstate0", "map\u{1b}4", "map6");

        let values: Vec<String> = config_rows(&response).into_iter().map(|row| row.value).collect();
        assert_eq!(vec!["fw\\nstate0", "map\\u{1b}4", "map6"], values);
    }

    #[test]
    fn test_update_request_endpoint_flags_select_pairs_and_clear_without_legacy_flags() {
        let mut cmd = update_cmd("cfg", None, None);
        cmd.no_multicast = true;
        cmd.unicast = Some("[2001:db8::1]:10000".parse().unwrap());
        let request = update_request(&cmd).unwrap();
        assert_eq!(
            vec![
                "sync_config.dst_addr_multicast",
                "sync_config.port_multicast",
                "sync_config.dst_addr_unicast",
                "sync_config.port_unicast",
            ],
            request.update_mask.unwrap().paths
        );
        assert!(!request.clear_multicast);
        assert!(!request.clear_unicast);
        let sync_config = request.sync_config.unwrap();
        assert_eq!(None, sync_config.dst_addr_multicast);
        assert_eq!(0, sync_config.port_multicast);
        assert_eq!(10000, sync_config.port_unicast);
        assert_eq!(
            Some(IpAddress::from(IpAddr::V6("2001:db8::1".parse().unwrap()))),
            sync_config.dst_addr_unicast
        );
    }

    #[test]
    fn test_update_request_split_endpoint_flag_selects_only_explicit_field() {
        let mut cmd = update_cmd("cfg", None, None);
        cmd.port_multicast = Some(9999);
        let request = update_request(&cmd).unwrap();
        assert_eq!(vec!["sync_config.port_multicast"], request.update_mask.unwrap().paths);
        let sync_config = request.sync_config.unwrap();
        assert_eq!(9999, sync_config.port_multicast);
        assert_eq!(None, sync_config.dst_addr_multicast);
    }

    #[test]
    fn test_update_request_empty_mask_preserves_existing_fields() {
        let request = update_request(&update_cmd("cfg", None, None)).unwrap();
        assert!(request.update_mask.unwrap().paths.is_empty());
    }

    #[test]
    fn test_update_request_explicit_zero_and_empty_are_selected() {
        let mut cmd = update_cmd("cfg", Some(""), None);
        cmd.sync_suppress_timeout = Some(core::time::Duration::ZERO);
        let request = update_request(&cmd).unwrap();
        assert_eq!(
            vec!["map_name_v4", "sync_config.sync_suppress_timeout"],
            request.update_mask.unwrap().paths
        );
        assert_eq!("", request.map_name_v4);
        assert_eq!(0, request.sync_config.unwrap().sync_suppress_timeout);
    }

    #[test]
    fn test_update_request_independent_flags_select_only_their_fields() {
        let mut cmd = update_cmd("cfg", None, Some("map6"));
        cmd.udp = Some(core::time::Duration::from_secs(45));
        let request = update_request(&cmd).unwrap();
        assert_eq!(
            vec!["map_name_v6", "sync_config.udp"],
            request.update_mask.unwrap().paths
        );
        assert_eq!("map6", request.map_name_v6);
        assert_eq!(45_000_000_000, request.sync_config.unwrap().udp);
    }

    #[test]
    fn test_update_sync_endpoints_switches_to_unicast_only() {
        let mut sync_config = SyncConfig {
            dst_addr_multicast: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::LOCALHOST))),
            port_multicast: 9999,
            ..Default::default()
        };
        let mut cmd = update_cmd("cfg", None, None);
        cmd.unicast = Some("[2001:db8::1]:10000".parse().unwrap());
        cmd.no_multicast = true;

        update_sync_endpoints(&mut sync_config, &cmd).unwrap();

        assert_eq!(None, sync_config.dst_addr_multicast);
        assert_eq!(0, sync_config.port_multicast);
        assert_eq!(
            Some(IpAddress::from(IpAddr::V6("2001:db8::1".parse().unwrap()))),
            sync_config.dst_addr_unicast
        );
        assert_eq!(10000, sync_config.port_unicast);
    }

    #[test]
    fn test_update_sync_endpoints_rejects_scoped_multicast() {
        let mut sync_config = SyncConfig::default();
        let current = sync_config.clone();
        let mut cmd = update_cmd("cfg", None, None);
        cmd.multicast = Some(core::net::SocketAddrV6::new(core::net::Ipv6Addr::LOCALHOST, 9999, 0, 3));

        let err = update_sync_endpoints(&mut sync_config, &cmd).unwrap_err();

        assert_eq!("--multicast does not support IPv6 scope IDs", err);
        assert_eq!(current, sync_config);
    }

    #[test]
    fn test_update_sync_endpoints_rejects_scoped_unicast() {
        let mut sync_config = SyncConfig::default();
        let current = sync_config.clone();
        let mut cmd = update_cmd("cfg", None, None);
        cmd.unicast = Some(core::net::SocketAddrV6::new(
            core::net::Ipv6Addr::LOCALHOST,
            10000,
            0,
            3,
        ));

        let err = update_sync_endpoints(&mut sync_config, &cmd).unwrap_err();

        assert_eq!("--unicast does not support IPv6 scope IDs", err);
        assert_eq!(current, sync_config);
    }

    #[test]
    fn test_remove_multicast_endpoint_clears_last_destination() {
        let mut sync_config = SyncConfig {
            dst_addr_multicast: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::LOCALHOST))),
            port_multicast: 9999,
            ..Default::default()
        };

        let mut cmd = update_cmd("cfg", None, None);
        cmd.no_multicast = true;

        update_sync_endpoints(&mut sync_config, &cmd).unwrap();

        assert_eq!(None, sync_config.dst_addr_multicast);
        assert_eq!(0, sync_config.port_multicast);
        assert_eq!(None, sync_config.dst_addr_unicast);
        assert_eq!(0, sync_config.port_unicast);
    }

    #[test]
    fn test_update_sync_endpoints_switches_to_multicast_only() {
        let mut sync_config = SyncConfig {
            dst_addr_unicast: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::LOCALHOST))),
            port_unicast: 10000,
            ..Default::default()
        };
        let mut cmd = update_cmd("cfg", None, None);
        cmd.multicast = Some("[ff02::1]:9999".parse().unwrap());
        cmd.no_unicast = true;

        update_sync_endpoints(&mut sync_config, &cmd).unwrap();

        assert_eq!(None, sync_config.dst_addr_unicast);
        assert_eq!(0, sync_config.port_unicast);
        assert_eq!(
            Some(IpAddress::from(IpAddr::V6("ff02::1".parse().unwrap()))),
            sync_config.dst_addr_multicast
        );
        assert_eq!(9999, sync_config.port_multicast);
    }

    #[test]
    fn test_remove_unicast_endpoint_clears_last_destination() {
        let mut sync_config = SyncConfig {
            dst_addr_unicast: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::LOCALHOST))),
            port_unicast: 10000,
            ..Default::default()
        };

        let mut cmd = update_cmd("cfg", None, None);
        cmd.no_unicast = true;

        update_sync_endpoints(&mut sync_config, &cmd).unwrap();

        assert_eq!(None, sync_config.dst_addr_unicast);
        assert_eq!(0, sync_config.port_unicast);
        assert_eq!(None, sync_config.dst_addr_multicast);
        assert_eq!(0, sync_config.port_multicast);
    }
}
