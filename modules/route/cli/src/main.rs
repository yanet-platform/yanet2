//! CLI for YANET "route" module.

mod fib;

use std::path::PathBuf;

use clap::{CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    completion, display,
    errors::Error,
    output, yaml,
};

use crate::{
    fib::render::print_fib,
    routepb::{
        DeleteConfigRequest, ListConfigsRequest, ShowFibRequest, UpdateFibRequest,
        route_service_client::RouteServiceClient, route_service_server::SERVICE_NAME,
    },
};

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod routepb {
    tonic::include_proto!("modules.route.controlplane.routepb.v1");
}

/// The FIB rules file, deserialized straight into the wire's
/// [`routepb::FibEntry`] -- `range` is range-native, matching the wire, so
/// there is no CIDR-to-range conversion here either.
///
/// A minimal wrapper around `entries` rather than [`routepb::UpdateFibRequest`]
/// itself: `module_name` comes from `--name`, not the file. Deserializing
/// straight into `UpdateFibRequest` would leave a `module_name` key written
/// into the file either silently discarded (if overwritten after loading)
/// or silently conflicting with `--name` (if kept) -- neither reports
/// anything to the caller. `#[serde(deny_unknown_fields)]` instead turns
/// that key, or any other stray one at this level, into a load error.
/// `FIBEntry`/`FIBNexthop` carry the same attribute (see `build.rs`), so a
/// stray or retired key inside an entry fails to load the same way. A
/// required key missing from the file is left to the server.
#[derive(Debug, serde::Deserialize)]
#[serde(deny_unknown_fields)]
struct FibConfig {
    #[serde(default)]
    entries: Vec<routepb::FibEntry>,
}

/// Manages route module configs.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// FIB (Forwarding Information Base) operations.
    Fib(FibCmd),
}

impl ModeCmd {
    fn action(&self) -> &'static str {
        match self {
            Self::Fib(cmd) => match &cmd.action {
                FibAction::List => "list",
                FibAction::Show(..) => "show",
                FibAction::Update(..) => "update",
                FibAction::Delete(..) => "delete",
            },
        }
    }
}

#[derive(Debug, Clone, Parser)]
pub struct FibCmd {
    #[clap(subcommand)]
    pub action: FibAction,
}

#[derive(Debug, Clone, Parser)]
pub enum FibAction {
    /// List route module config names known to the route module shim.
    List,
    /// Dump FIB entries.
    Show(FibShowCmd),
    /// Replace the FIB atomically with entries from a YAML file.
    Update(FibUpdateCmd),
    /// Delete a config.
    Delete(FibDeleteCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct FibUpdateCmd {
    /// Route module config name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Path to the FIB YAML file.
    #[arg(value_name = "PATH")]
    pub file: PathBuf,
}

#[derive(Debug, Clone, Parser)]
pub struct FibDeleteCmd {
    /// Route module config name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

#[derive(Debug, Clone, Parser)]
pub struct FibShowCmd {
    /// Show only IPv4 FIB entries.
    #[arg(long, short = '4', conflicts_with = "ipv6")]
    pub ipv4: bool,
    /// Show only IPv6 FIB entries.
    #[arg(long, short = '6')]
    pub ipv6: bool,
    /// Route config name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
}

fn client(channel: LayeredChannel) -> RouteServiceClient<LayeredChannel> {
    RouteServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    match cmd.mode {
        ModeCmd::Fib(cmd) => match cmd.action {
            FibAction::List => list_fibs(&mut service).await,
            FibAction::Show(cmd) => show_fib(&mut service, cmd).await,
            FibAction::Update(cmd) => update_fib(&mut service, cmd).await,
            FibAction::Delete(cmd) => delete_fib(&mut service, cmd).await,
        },
    }
}

type RouteService = Service<RouteServiceClient<LayeredChannel>>;

async fn update_fib(service: &mut RouteService, cmd: FibUpdateCmd) -> Result<(), Error> {
    let config: FibConfig = yaml::load(&cmd.file).map_err(|err| service.invalid("update", err.to_string()))?;
    let entry_count = config.entries.len();
    let request = UpdateFibRequest {
        module_name: cmd.config_name.clone(),
        entries: config.entries,
    };
    service
        .unary("update", request, async |client, request| {
            client.update_fib(request).await
        })
        .await?;

    output::success(
        "update",
        format_args!("Updated config '{}' ({} entries).", cmd.config_name, entry_count),
    );
    Ok(())
}

async fn delete_fib(service: &mut RouteService, cmd: FibDeleteCmd) -> Result<(), Error> {
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

async fn list_fibs(service: &mut RouteService) -> Result<(), Error> {
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
                format_args!("No FIB configurations found."),
                format_args!("create one with 'yanet-cli-route fib update --name <name> <path>'"),
            )
        },
    );
    Ok(())
}

async fn show_fib(service: &mut RouteService, cmd: FibShowCmd) -> Result<(), Error> {
    let request = ShowFibRequest {
        name: cmd.config_name.clone(),
        ipv4_only: cmd.ipv4,
        ipv6_only: cmd.ipv6,
    };

    let response = service
        .unary_with(
            "show",
            request,
            service.not_found("show", &format!("config '{}'", cmd.config_name)),
            async |client, request| client.show_fib(request).await,
        )
        .await?;
    let entries = response.entries;

    output::data(
        || &entries,
        || {
            if entries.is_empty() {
                output::empty(format_args!("No FIB entries found for '{}'.", cmd.config_name));
                return;
            }

            print_fib(&entries);
        },
    );

    Ok(())
}

#[cfg(test)]
mod test {
    use core::net::IpAddr;

    use commonpb::pb::{IpRange, MacAddress};
    use netip::MacAddr;

    use super::*;

    fn ip_range(start: &str, end: &str) -> IpRange {
        IpRange::from((start.parse::<IpAddr>().unwrap(), end.parse::<IpAddr>().unwrap()))
    }

    fn mac(s: &str) -> MacAddress {
        MacAddress::from(s.parse::<MacAddr>().unwrap())
    }

    /// Pins `--format json`'s wire shape byte-for-byte: `range` and
    /// `nexthops` come straight from the derived `routepb::FibEntry`/
    /// `FibNexthop` impls (see `build.rs`), nested rather than flattened.
    #[test]
    fn fib_entry_json_matches_wire_shape() {
        let entry = routepb::FibEntry {
            range: Some(ip_range("10.0.0.0", "10.0.0.255")),
            nexthops: vec![routepb::FibNexthop {
                dst_mac: Some(mac("aa:bb:cc:dd:ee:ff")),
                src_mac: Some(mac("11:22:33:44:55:66")),
                device: "vlan100".to_owned(),
                counter: "nexthop_custom-counter".to_owned(),
            }],
        };

        let json = serde_json::to_string(&entry).unwrap();

        assert_eq!(
            r#"{"range":{"start":"10.0.0.0","end":"10.0.0.255"},"nexthops":[{"dst_mac":"aa:bb:cc:dd:ee:ff","src_mac":"11:22:33:44:55:66","device":"vlan100","counter":"nexthop_custom-counter"}]}"#,
            json
        );
    }

    /// An absent `range` serializes as JSON `null`: there is no view left
    /// to substitute a fallback string for it.
    #[test]
    fn fib_entry_json_absent_range_is_null() {
        let entry = routepb::FibEntry { range: None, nexthops: Vec::new() };

        let json = serde_json::to_string(&entry).unwrap();

        assert_eq!(r#"{"range":null,"nexthops":[]}"#, json);
    }

    /// An absent MAC serializes as JSON `null`. `counter` is also empty
    /// here, so the key is absent entirely.
    #[test]
    fn fib_nexthop_json_absent_mac_is_null() {
        let nexthop = routepb::FibNexthop {
            dst_mac: None,
            src_mac: None,
            device: "vlan100".to_owned(),
            counter: String::new(),
        };

        let json = serde_json::to_string(&nexthop).unwrap();

        assert_eq!(r#"{"dst_mac":null,"src_mac":null,"device":"vlan100"}"#, json);
    }

    /// A malformed MAC (upper 16 bits set) serializes as the literal
    /// `"invalid"` -- `commonpb::pb::MacAddress`'s own `Serialize` impl
    /// falls back to that string for a value `MacAddr` itself rejects, the
    /// same fallback it gives everywhere else this type appears.
    #[test]
    fn fib_nexthop_json_malformed_mac_is_invalid_literal() {
        let nexthop = routepb::FibNexthop {
            dst_mac: Some(MacAddress { addr: 0x1_0000_0000_0000 }),
            src_mac: None,
            device: "vlan100".to_owned(),
            counter: String::new(),
        };

        let json = serde_json::to_string(&nexthop).unwrap();

        assert_eq!(r#"{"dst_mac":"invalid","src_mac":null,"device":"vlan100"}"#, json);
    }

    /// The `counter` key, omitted by `skip_serializing_if` when empty (see
    /// `fib_nexthop_json_absent_mac_is_null`), still loads back: the paired
    /// `#[serde(default)]` fills a missing `counter` in as empty.
    #[test]
    fn fib_nexthop_json_missing_counter_key_defaults_to_empty() {
        let nexthop: routepb::FibNexthop =
            serde_json::from_str(r#"{"dst_mac":null,"src_mac":null,"device":"vlan100"}"#).unwrap();

        assert_eq!("", nexthop.counter);
    }

    #[test]
    fn fib_config_yaml_round_trips_v4() {
        let yaml = "
entries:
  - range:
      start: 10.0.0.0
      end: 10.0.0.255
    nexthops:
      - dst_mac: aa:bb:cc:dd:ee:ff
        src_mac: 11:22:33:44:55:66
        device: eth0
";
        let config: FibConfig = serde_yaml::from_str(yaml).unwrap();

        assert_eq!(
            vec![routepb::FibEntry {
                range: Some(ip_range("10.0.0.0", "10.0.0.255")),
                nexthops: vec![routepb::FibNexthop {
                    dst_mac: Some(mac("aa:bb:cc:dd:ee:ff")),
                    src_mac: Some(mac("11:22:33:44:55:66")),
                    device: "eth0".to_owned(),
                    counter: String::new(),
                }],
            }],
            config.entries
        );
    }

    /// Only `start` needs quoting here: it is a plain YAML scalar ending in
    /// a bare `::` (as any IPv6 address abbreviated down to its `::` form
    /// is), which YAML parses as a nested mapping-value indicator, not
    /// string content -- a plain YAML quirk, not something this loader can
    /// special-case away. `end` doesn't end in `::`, so it would parse
    /// unquoted too; it's quoted here only to match.
    #[test]
    fn fib_config_yaml_round_trips_v6() {
        let yaml = r#"
entries:
  - range:
      start: "2001:db8::"
      end: "2001:db8::ff"
    nexthops:
      - dst_mac: aa:bb:cc:dd:ee:ff
        src_mac: 11:22:33:44:55:66
        device: eth0
"#;
        let config: FibConfig = serde_yaml::from_str(yaml).unwrap();

        assert_eq!(
            vec![routepb::FibEntry {
                range: Some(ip_range("2001:db8::", "2001:db8::ff")),
                nexthops: vec![routepb::FibNexthop {
                    dst_mac: Some(mac("aa:bb:cc:dd:ee:ff")),
                    src_mac: Some(mac("11:22:33:44:55:66")),
                    device: "eth0".to_owned(),
                    counter: String::new(),
                }],
            }],
            config.entries
        );
    }

    /// An entry can omit `nexthops` entirely -- `UpdateFibRequest`'s proto
    /// doc comment calls that a legitimate way to skip a range without
    /// displacing an earlier entry -- and still load, thanks to
    /// `build.rs`'s `#[serde(default)]` on that field.
    #[test]
    fn fib_config_yaml_entry_without_nexthops_defaults_to_empty() {
        let yaml = "
entries:
  - range:
      start: 10.0.0.0
      end: 10.0.0.255
";
        let config: FibConfig = serde_yaml::from_str(yaml).unwrap();

        assert_eq!(1, config.entries.len());
        assert!(config.entries[0].nexthops.is_empty());
    }

    /// A nexthop can omit `counter` entirely -- `FIBNexthop.counter`'s proto
    /// doc calls that the way to ask the server to generate the name itself
    /// -- and still load, thanks to `build.rs`'s `#[serde(default)]` on that
    /// field (the same treatment `FIBEntry.nexthops` gets above, needed here
    /// too since `counter` is a plain `String` rather than a message field
    /// that would default on its own).
    #[test]
    fn fib_config_yaml_nexthop_without_counter_defaults_to_empty() {
        let yaml = "
entries:
  - range:
      start: 10.0.0.0
      end: 10.0.0.255
    nexthops:
      - dst_mac: aa:bb:cc:dd:ee:ff
        src_mac: 11:22:33:44:55:66
        device: eth0
";
        let config: FibConfig = serde_yaml::from_str(yaml).unwrap();

        assert_eq!(1, config.entries[0].nexthops.len());
        assert_eq!("", config.entries[0].nexthops[0].counter);
    }

    /// A nexthop's `counter` key, when present, loads verbatim.
    #[test]
    fn fib_config_yaml_nexthop_with_counter_loads_given_name() {
        let yaml = "
entries:
  - range:
      start: 10.0.0.0
      end: 10.0.0.255
    nexthops:
      - dst_mac: aa:bb:cc:dd:ee:ff
        src_mac: 11:22:33:44:55:66
        device: eth0
        counter: nexthop_my-counter
";
        let config: FibConfig = serde_yaml::from_str(yaml).unwrap();

        assert_eq!("nexthop_my-counter", config.entries[0].nexthops[0].counter);
    }

    /// A malformed address in the file fails to load rather than silently
    /// producing a garbage `IpAddress` -- `commonpb`'s hand-written
    /// `Deserialize` impl rejects anything `FromStr` rejects.
    #[test]
    fn fib_config_yaml_malformed_address_fails_loudly() {
        let yaml = "
entries:
  - range:
      start: not-an-ip
      end: 10.0.0.255
";
        assert!(serde_yaml::from_str::<FibConfig>(yaml).is_err());
    }

    /// `module_name` belongs to `--name`, not the file: writing it into the
    /// file is a load error, not a silently ignored or silently
    /// overridden key -- see [`FibConfig`]'s doc.
    #[test]
    fn fib_config_yaml_rejects_module_name_field() {
        let yaml = "
module_name: foo
entries: []
";
        assert!(serde_yaml::from_str::<FibConfig>(yaml).is_err());
    }

    /// The old CIDR-keyed rules format's `prefix` key is retired, not
    /// renamed: `FIBEntry` has no such field, so loading a file still
    /// written in that shape fails to load here rather than silently
    /// deserializing with `range: None` -- see `build.rs`'s
    /// `deny_unknown_fields` on `FIBEntry`.
    #[test]
    fn fib_config_yaml_rejects_retired_prefix_field() {
        let yaml = r#"
entries:
  - prefix: "10.0.0.0/24"
    nexthops: []
"#;
        let err = serde_yaml::from_str::<FibConfig>(yaml).unwrap_err();
        assert!(err.to_string().contains("prefix"), "unexpected error: {err}");
    }
}
