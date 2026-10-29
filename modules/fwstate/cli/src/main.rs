use core::net::IpAddr;

use args::{DeleteCmd, ModeCmd, ShowCmd, UpdateCmd};
use clap::{CommandFactory, Parser};
use clap_complete::engine::CompletionCandidate;
use commonpb::pb::IpAddress;
use fwstatepb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, ShowConfigResponse, SyncConfig, SyncEndpoint,
    UpdateConfigRequest, fw_state_service_client::FwStateServiceClient, fw_state_service_server::SERVICE_NAME,
};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    completion, display,
    errors::Error,
    output,
};

mod args;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod fwstatepb {
    tonic::include_proto!("modules.fwstate.controlplane.fwstatepb.v1");
}

fn client(channel: LayeredChannel) -> FwStateServiceClient<LayeredChannel> {
    FwStateServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Zstd)
        .accept_compressed(CompressionEncoding::Zstd)
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
    pub globals: GlobalArgs,
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

/// Lays out one stored configuration for a human reader.
fn config_block(response: &ShowConfigResponse) -> display::KeyValue {
    let mut block = display::KeyValue::new()
        .row("name", &response.name)
        .row("map name v4", &response.map_name_v4)
        .row("map name v6", &response.map_name_v6);

    let Some(sync_config) = response.sync_config.as_ref() else {
        return block;
    };

    let address = |addr: Option<&IpAddress>| addr.map_or_else(|| "-".to_string(), ToString::to_string);
    for (idx, endpoint) in sync_config.endpoints.iter().enumerate() {
        block = block
            .row(&format!("endpoint {idx} src addr"), address(endpoint.src_addr.as_ref()))
            .row(&format!("endpoint {idx} dst addr"), address(endpoint.dst_addr.as_ref()))
            .row(&format!("endpoint {idx} port"), endpoint.port.unwrap_or_default());
    }

    for (setting, nanos) in [
        ("tcp syn-ack timeout", sync_config.tcp_syn_ack),
        ("tcp syn timeout", sync_config.tcp_syn),
        ("tcp fin timeout", sync_config.tcp_fin),
        ("tcp timeout", sync_config.tcp),
        ("udp timeout", sync_config.udp),
        ("default timeout", sync_config.default),
        ("sync suppress timeout", sync_config.sync_suppress_timeout),
    ] {
        block = block.row(
            setting,
            format!("{} ms", format_timeout_millis(nanos.unwrap_or_default())),
        );
    }

    block.row("sync mtu", effective_sync_mtu(sync_config.sync_mtu))
}

/// Sync MTU the dataplane applies: an absent or zero value selects 1500.
fn effective_sync_mtu(sync_mtu: Option<u32>) -> u32 {
    const DEFAULT_SYNC_MTU: u32 = 1500;

    match sync_mtu {
        None | Some(0) => DEFAULT_SYNC_MTU,
        Some(mtu) => mtu,
    }
}

/// Builds a partial update carrying only the fields the caller supplied.
fn update_request(cmd: &UpdateCmd) -> Result<UpdateConfigRequest, &'static str> {
    let mut request = UpdateConfigRequest {
        name: cmd.config_name.clone(),
        map_name_v4: cmd.map_name_v4.clone(),
        map_name_v6: cmd.map_name_v6.clone(),
        ..Default::default()
    };
    // The getter generated for the optional default timeout shadows the
    // associated constructor, so the trait method is reached through inference.
    let mut sync_config: fwstatepb::SyncConfig = Default::default();
    update_sync_endpoints(&mut sync_config, cmd);
    if let Some(value) = cmd.tcp_syn_ack {
        sync_config.tcp_syn_ack = Some(value.as_nanos() as u64);
    }
    if let Some(value) = cmd.tcp_syn {
        sync_config.tcp_syn = Some(value.as_nanos() as u64);
    }
    if let Some(value) = cmd.tcp_fin {
        sync_config.tcp_fin = Some(value.as_nanos() as u64);
    }
    if let Some(value) = cmd.tcp {
        sync_config.tcp = Some(value.as_nanos() as u64);
    }
    if let Some(value) = cmd.udp {
        sync_config.udp = Some(value.as_nanos() as u64);
    }
    if let Some(value) = cmd.default {
        sync_config.default = Some(value.as_nanos() as u64);
    }
    if let Some(value) = cmd.sync_suppress_timeout {
        sync_config.sync_suppress_timeout = Some(value.as_nanos() as u64);
    }
    if let Some(value) = cmd.sync_mtu {
        sync_config.sync_mtu = Some(u32::from(value));
    }
    request.sync_config = Some(sync_config);
    Ok(request)
}

/// Fills in the endpoint list the caller supplied.
///
/// `--sync-endpoint` replaces the whole list on the server;
/// `--no-sync-endpoints` asks the server to remove it.
fn update_sync_endpoints(sync_config: &mut SyncConfig, cmd: &UpdateCmd) {
    sync_config.endpoints = cmd
        .sync_endpoint
        .iter()
        .map(|endpoint| SyncEndpoint {
            src_addr: Some(IpAddress::from(IpAddr::V6(endpoint.src))),
            dst_addr: Some(IpAddress::from(IpAddr::V6(endpoint.dst))),
            port: Some(u32::from(endpoint.port)),
        })
        .collect();
    if cmd.no_sync_endpoints {
        sync_config.no_endpoints = Some(true);
    }
}

type FWStateService = Service<FwStateServiceClient<LayeredChannel>>;

async fn list_configs(service: &mut FWStateService) -> Result<(), Error> {
    let response = service
        .unary("list", ListConfigsRequest {}, async |client, request| {
            client.list_configs(request).await
        })
        .await?;

    output::data(
        || &response.configs,
        || {
            display::print_names_with_hint(
                &response.configs,
                format_args!("No FWState configurations found."),
                format_args!(
                    "provision maps with 'yanet-cli-fwstatemap create --name <map> --kind <v4|v6>', then create a config with 'yanet-cli-fwstate update --name <name> --map-name-v4 <map> --map-name-v6 <map>'"
                ),
            )
        },
    );

    Ok(())
}

async fn show_config(service: &mut FWStateService, cmd: ShowCmd) -> Result<(), Error> {
    let request = ShowConfigRequest {
        name: cmd.config_name.clone(),
        ok_if_not_found: false,
    };
    let response = service
        .unary_with(
            "show",
            request,
            service.not_found("show", &format!("config '{}'", cmd.config_name)),
            async |client, request| client.show_config(request).await,
        )
        .await?;

    output::data(|| &response, || config_block(&response).print());

    Ok(())
}

async fn delete_config(service: &mut FWStateService, cmd: DeleteCmd) -> Result<(), Error> {
    let request = DeleteConfigRequest { name: cmd.config_name.clone() };
    service
        .unary_with(
            "delete",
            request,
            service.not_found("delete", &format!("config '{}'", cmd.config_name)),
            async |client, request| client.delete_config(request).await,
        )
        .await?;

    output::success("delete", format_args!("Deleted config '{}'.", cmd.config_name));

    Ok(())
}

async fn update_config(service: &mut FWStateService, cmd: UpdateCmd) -> Result<(), Error> {
    let request = update_request(&cmd).map_err(|err| service.invalid("update", err))?;
    service
        .unary("update", request, async |client, request| {
            client.update_config(request).await
        })
        .await?;

    output::success("update", format_args!("Updated config '{}'.", cmd.config_name));

    Ok(())
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    match cmd.mode {
        ModeCmd::List => list_configs(&mut service).await,
        ModeCmd::Delete(cmd) => delete_config(&mut service, cmd).await,
        ModeCmd::Update(cmd) => update_config(&mut service, cmd).await,
        ModeCmd::Show(cmd) => show_config(&mut service, cmd).await,
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
mod tests {
    use super::*;

    /// Update command carrying only the config name and optional map names.
    fn update_cmd(config_name: &str, map_name_v4: Option<&str>, map_name_v6: Option<&str>) -> UpdateCmd {
        UpdateCmd {
            config_name: config_name.to_string(),
            map_name_v4: map_name_v4.map(str::to_string),
            map_name_v6: map_name_v6.map(str::to_string),
            sync_endpoint: Vec::new(),
            no_sync_endpoints: false,
            tcp_syn_ack: None,
            tcp_syn: None,
            tcp_fin: None,
            tcp: None,
            udp: None,
            default: None,
            sync_suppress_timeout: None,
            sync_mtu: None,
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
    fn test_config_block_carries_endpoints_and_converted_timeouts() {
        let response = ShowConfigResponse {
            name: "fwstate0".to_string(),
            sync_config: Some(fwstatepb::SyncConfig {
                endpoints: vec![
                    SyncEndpoint {
                        src_addr: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::LOCALHOST))),
                        dst_addr: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::UNSPECIFIED))),
                        port: Some(9999),
                    },
                    SyncEndpoint {
                        src_addr: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::LOCALHOST))),
                        dst_addr: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::LOCALHOST))),
                        port: Some(10000),
                    },
                ],
                tcp: Some(60_000_000_000),
                sync_suppress_timeout: Some(0),
                ..Default::default()
            }),
            ..Default::default()
        };

        let block = config_block(&response);
        let rows = block.entries();
        for expected in [
            ("endpoint 0 src addr", "::1"),
            ("endpoint 0 dst addr", "::"),
            ("endpoint 0 port", "9999"),
            ("endpoint 1 src addr", "::1"),
            ("endpoint 1 dst addr", "::1"),
            ("endpoint 1 port", "10000"),
            ("tcp timeout", "60000 ms"),
            ("sync suppress timeout", "0 ms"),
        ] {
            assert!(
                rows.iter()
                    .any(|(key, lines)| key == expected.0 && *lines == [expected.1]),
                "missing {expected:?} in {rows:?}"
            );
        }
        assert!(
            !rows.iter().any(|(_, lines)| lines[0].contains("60000000000")),
            "no row may carry the stored nanoseconds: {rows:?}"
        );
    }

    #[test]
    fn test_config_block_without_sync_config_omits_timeouts() {
        let response = show_response("fwstate0", "map4", "map6");

        let block = config_block(&response);
        let settings: Vec<&str> = block.entries().iter().map(|(key, _)| key.as_str()).collect();
        assert_eq!(vec!["name", "map name v4", "map name v6"], settings);
    }

    #[test]
    fn test_config_block_escapes_names() {
        let response = show_response("fw\nstate0", "map\u{1b}4", "map6");

        let block = config_block(&response);
        let values: Vec<&str> = block.entries().iter().map(|(_, lines)| lines[0].as_str()).collect();
        assert_eq!(vec!["fw\\nstate0", "map\\u{1b}4", "map6"], values);
    }

    #[test]
    fn test_update_request_no_flags_leaves_every_optional_field_absent() {
        let request = update_request(&update_cmd("cfg", None, None)).unwrap();

        assert_eq!(None, request.map_name_v4);
        assert_eq!(None, request.map_name_v6);
        let sync_config = request.sync_config.unwrap();
        assert_eq!(SyncConfig { ..Default::default() }, sync_config);
    }

    #[test]
    fn test_update_request_endpoints_replace_the_list() {
        let mut cmd = update_cmd("cfg", None, None);
        cmd.sync_endpoint = vec![
            args::SyncEndpoint {
                src: "fd00::1".parse().unwrap(),
                dst: "ff02::1".parse().unwrap(),
                port: 9999,
            },
            args::SyncEndpoint {
                src: "fd00::1".parse().unwrap(),
                dst: "2001:db8::2".parse().unwrap(),
                port: 10000,
            },
        ];

        let request = update_request(&cmd).unwrap();

        let sync_config = request.sync_config.unwrap();
        assert_eq!(
            vec![
                SyncEndpoint {
                    src_addr: Some(IpAddress::from(IpAddr::V6("fd00::1".parse().unwrap()))),
                    dst_addr: Some(IpAddress::from(IpAddr::V6("ff02::1".parse().unwrap()))),
                    port: Some(9999),
                },
                SyncEndpoint {
                    src_addr: Some(IpAddress::from(IpAddr::V6("fd00::1".parse().unwrap()))),
                    dst_addr: Some(IpAddress::from(IpAddr::V6("2001:db8::2".parse().unwrap()))),
                    port: Some(10000),
                },
            ],
            sync_config.endpoints
        );
        assert_eq!(None, sync_config.no_endpoints);
    }

    #[test]
    fn test_update_request_no_endpoints_removes_the_list() {
        let mut cmd = update_cmd("cfg", None, None);
        cmd.no_sync_endpoints = true;

        let request = update_request(&cmd).unwrap();

        let sync_config = request.sync_config.unwrap();
        assert_eq!(Some(true), sync_config.no_endpoints);
        assert!(sync_config.endpoints.is_empty());
    }

    #[test]
    fn test_update_request_explicit_zero_and_empty_are_carried_as_present() {
        let mut cmd = update_cmd("cfg", Some(""), None);
        cmd.sync_suppress_timeout = Some(core::time::Duration::ZERO);

        let request = update_request(&cmd).unwrap();

        assert_eq!(Some(String::new()), request.map_name_v4);
        assert_eq!(None, request.map_name_v6);
        assert_eq!(Some(0), request.sync_config.unwrap().sync_suppress_timeout);
    }

    #[test]
    fn test_effective_sync_mtu_defaults_absent_and_zero_to_1500() {
        assert_eq!(1500, effective_sync_mtu(None));
        assert_eq!(1500, effective_sync_mtu(Some(0)));
        assert_eq!(9000, effective_sync_mtu(Some(9000)));
    }

    #[test]
    fn test_update_request_sync_mtu_is_carried() {
        let mut cmd = update_cmd("cfg", None, None);
        cmd.sync_mtu = Some(9000);

        let request = update_request(&cmd).unwrap();

        assert_eq!(Some(9000), request.sync_config.unwrap().sync_mtu);
    }

    #[test]
    fn test_update_request_independent_flags_set_only_their_own_fields() {
        let mut cmd = update_cmd("cfg", None, Some("map6"));
        cmd.udp = Some(core::time::Duration::from_secs(45));

        let request = update_request(&cmd).unwrap();

        assert_eq!(None, request.map_name_v4);
        assert_eq!(Some("map6".to_string()), request.map_name_v6);
        let sync_config = request.sync_config.unwrap();
        assert_eq!(Some(45_000_000_000), sync_config.udp);
        assert_eq!(None, sync_config.tcp);
    }

    #[test]
    fn test_parse_sync_endpoint_accepts_bracketed_and_bare_destinations() {
        use args::SyncEndpoint as Endpoint;

        let expected = Endpoint {
            src: "fd00::1".parse().unwrap(),
            dst: "ff02::1".parse().unwrap(),
            port: 9999,
        };
        assert_eq!(
            Ok(expected.clone()),
            args::parse_sync_endpoint("fd00::1@[ff02::1]:9999")
        );
        assert_eq!(Ok(expected), args::parse_sync_endpoint("fd00::1@ff02::1:9999"));
    }

    #[test]
    fn test_parse_sync_endpoint_rejects_incomplete_values() {
        assert!(args::parse_sync_endpoint("ff02::1:9999").is_err(), "missing source");
        assert!(args::parse_sync_endpoint("fd00::1@ff02::1").is_err(), "missing port");
        assert!(args::parse_sync_endpoint("fd00::1@ff02::1:0").is_err(), "zero port");
        assert!(
            args::parse_sync_endpoint("fd00::1@ff02::1:99999").is_err(),
            "port above 65535"
        );
        assert!(
            args::parse_sync_endpoint("fd00::1@not-an-addr:9999").is_err(),
            "bad destination"
        );
    }
}
