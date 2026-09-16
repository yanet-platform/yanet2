use core::net::IpAddr;

use args::{DeleteCmd, ModeCmd, ShowCmd, UpdateCmd};
use clap::{CommandFactory, Parser};
use clap_complete::engine::CompletionCandidate;
use commonpb::pb::{IpAddress, MacAddress};
use fwstatepb::{
    DeleteConfigRequest, ListConfigsRequest, ShowConfigRequest, ShowConfigResponse, SyncConfig, UpdateConfigRequest,
    fw_state_service_client::FwStateServiceClient, fw_state_service_server::SERVICE_NAME,
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
    let dst_ether = sync_config
        .dst_ether
        .as_ref()
        .map_or_else(|| "-".to_string(), ToString::to_string);
    block = block
        .row("src addr", address(sync_config.src_addr.as_ref()))
        .row("dst ether", dst_ether)
        .row("dst addr multicast", address(sync_config.dst_addr_multicast.as_ref()))
        .row("port multicast", sync_config.port_multicast.unwrap_or_default())
        .row("dst addr unicast", address(sync_config.dst_addr_unicast.as_ref()))
        .row("port unicast", sync_config.port_unicast.unwrap_or_default());

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

    block
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
    if let Some(value) = cmd.src_addr {
        sync_config.src_addr = Some(IpAddress::from(IpAddr::V6(value)));
    }
    if let Some(value) = cmd.dst_ether {
        sync_config.dst_ether = Some(MacAddress::from(value));
    }
    update_sync_endpoints(&mut sync_config, cmd)?;
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
    request.sync_config = Some(sync_config);
    Ok(request)
}

/// Fills in the multicast and unicast endpoint fields the caller supplied.
///
/// `--no-multicast` and `--no-unicast` disable their endpoint by carrying a
/// zero port. The server clears the stored address of a disabled endpoint.
fn update_sync_endpoints(sync_config: &mut SyncConfig, cmd: &UpdateCmd) -> Result<(), &'static str> {
    if cmd.multicast.is_some_and(|endpoint| endpoint.scope_id() != 0) {
        return Err("--multicast does not support IPv6 scope IDs");
    }
    if cmd.unicast.is_some_and(|endpoint| endpoint.scope_id() != 0) {
        return Err("--unicast does not support IPv6 scope IDs");
    }

    if let Some(multicast) = cmd.multicast {
        sync_config.dst_addr_multicast = Some(IpAddress::from(IpAddr::V6(*multicast.ip())));
        sync_config.port_multicast = Some(u32::from(multicast.port()));
    }
    if let Some(dst_addr_multicast) = cmd.dst_addr_multicast {
        sync_config.dst_addr_multicast = Some(IpAddress::from(IpAddr::V6(dst_addr_multicast)));
    }
    if let Some(port_multicast) = cmd.port_multicast {
        sync_config.port_multicast = Some(u32::from(port_multicast));
    }

    if let Some(unicast) = cmd.unicast {
        sync_config.dst_addr_unicast = Some(IpAddress::from(IpAddr::V6(*unicast.ip())));
        sync_config.port_unicast = Some(u32::from(unicast.port()));
    }
    if let Some(dst_addr_unicast) = cmd.dst_addr_unicast {
        sync_config.dst_addr_unicast = Some(IpAddress::from(IpAddr::V6(dst_addr_unicast)));
    }
    if let Some(port_unicast) = cmd.port_unicast {
        sync_config.port_unicast = Some(u32::from(port_unicast));
    }

    if cmd.no_multicast {
        sync_config.port_multicast = Some(0);
    }
    if cmd.no_unicast {
        sync_config.port_unicast = Some(0);
    }
    Ok(())
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
    fn test_config_block_carries_endpoints_and_converted_timeouts() {
        let response = ShowConfigResponse {
            name: "fwstate0".to_string(),
            sync_config: Some(fwstatepb::SyncConfig {
                src_addr: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::LOCALHOST))),
                dst_ether: Some(MacAddress { addr: 0x3333_0000_0001 }),
                dst_addr_multicast: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::UNSPECIFIED))),
                port_multicast: Some(9999),
                dst_addr_unicast: Some(IpAddress::from(IpAddr::V6(core::net::Ipv6Addr::LOCALHOST))),
                port_unicast: Some(10000),
                tcp: Some(60_000_000_000),
                sync_suppress_timeout: Some(0),
                ..Default::default()
            }),
            ..Default::default()
        };

        let block = config_block(&response);
        let rows = block.entries();
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
    fn test_update_request_no_multicast_disables_port_without_setting_address() {
        let mut cmd = update_cmd("cfg", None, None);
        cmd.no_multicast = true;
        cmd.unicast = Some("[2001:db8::1]:10000".parse().unwrap());

        let request = update_request(&cmd).unwrap();

        let sync_config = request.sync_config.unwrap();
        assert_eq!(Some(0), sync_config.port_multicast);
        assert_eq!(None, sync_config.dst_addr_multicast);
        assert_eq!(Some(10000), sync_config.port_unicast);
        assert_eq!(
            Some(IpAddress::from(IpAddr::V6("2001:db8::1".parse().unwrap()))),
            sync_config.dst_addr_unicast
        );
    }

    #[test]
    fn test_update_request_split_endpoint_flag_sets_only_its_own_field() {
        let mut cmd = update_cmd("cfg", None, None);
        cmd.port_multicast = Some(9999);

        let request = update_request(&cmd).unwrap();

        let sync_config = request.sync_config.unwrap();
        assert_eq!(Some(9999), sync_config.port_multicast);
        assert_eq!(None, sync_config.dst_addr_multicast);
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
    fn test_update_sync_endpoints_combines_multicast_disable_with_unicast_endpoint() {
        let mut sync_config: SyncConfig = Default::default();
        let mut cmd = update_cmd("cfg", None, None);
        cmd.unicast = Some("[2001:db8::1]:10000".parse().unwrap());
        cmd.no_multicast = true;

        update_sync_endpoints(&mut sync_config, &cmd).unwrap();

        assert_eq!(None, sync_config.dst_addr_multicast);
        assert_eq!(Some(0), sync_config.port_multicast);
        assert_eq!(
            Some(IpAddress::from(IpAddr::V6("2001:db8::1".parse().unwrap()))),
            sync_config.dst_addr_unicast
        );
        assert_eq!(Some(10000), sync_config.port_unicast);
    }

    #[test]
    fn test_update_sync_endpoints_rejects_scoped_multicast() {
        let mut sync_config: SyncConfig = Default::default();
        let current = sync_config.clone();
        let mut cmd = update_cmd("cfg", None, None);
        cmd.multicast = Some(core::net::SocketAddrV6::new(core::net::Ipv6Addr::LOCALHOST, 9999, 0, 3));

        let err = update_sync_endpoints(&mut sync_config, &cmd).unwrap_err();

        assert_eq!("--multicast does not support IPv6 scope IDs", err);
        assert_eq!(current, sync_config);
    }

    #[test]
    fn test_update_sync_endpoints_rejects_scoped_unicast() {
        let mut sync_config: SyncConfig = Default::default();
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
}
