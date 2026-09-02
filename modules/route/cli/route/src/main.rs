//! CLI for YANET "route" module.

use core::error::Error as StdError;
use std::{
    fs::File,
    path::{Path, PathBuf},
};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::engine::{ArgValueCandidates, CompletionCandidate};
use tonic::codec::CompressionEncoding;
use yanet_cli_route::{
    fib::render::print_fib,
    routepb::{
        self, route_service_client::RouteServiceClient, DeleteConfigRequest, ListConfigsRequest, ShowFibRequest,
        UpdateFibRequest,
    },
};
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    completion,
    errors::{Error, NotFoundMapper},
    output::{self, CommonFormat},
};

/// Errors of the local YAML loader, reported to the user as invalid input.
type LoadError = Box<dyn StdError>;

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
/// required key simply missing (rather than stray) is a different
/// failure mode `deny_unknown_fields` does not cover -- see
/// [`FibConfig::validate`].
#[derive(Debug, serde::Deserialize)]
#[serde(deny_unknown_fields)]
struct FibConfig {
    #[serde(default)]
    entries: Vec<routepb::FibEntry>,
}

impl FibConfig {
    /// Loads and validates the file, naming it in every failure.
    fn load<P>(path: P) -> Result<Self, LoadError>
    where
        P: AsRef<Path>,
    {
        let path = path.as_ref();
        let at_path = |err: LoadError| -> LoadError { format!("{}: {err}", path.display()).into() };

        let file = File::open(path).map_err(|err| at_path(err.into()))?;
        let config: Self = serde_yaml::from_reader(file).map_err(|err| at_path(err.into()))?;
        config.validate().map_err(at_path)?;
        Ok(config)
    }

    /// Rejects an entry the server would otherwise bounce back as an
    /// opaque `Internal` RPC error with no file, line, or entry index.
    ///
    /// Every message field `FibEntry`/`FibNexthop` carry deserializes as
    /// an `Option` (`nexthops` defaults to empty instead -- see
    /// `build.rs`), so `deny_unknown_fields` alone does not catch a
    /// required one simply missing from the file; this fills that gap.
    /// It mirrors exactly what `modules/route/controlplane/service.go`'s
    /// `UpdateFIB` and `backend.go`'s `newHardwareRoute` already reject on
    /// the server -- range presence, and nexthop MAC/device presence --
    /// and leaves the range semantics the server owns (address family
    /// match, `start <= end` ordering) to the server's own error.
    fn validate(&self) -> Result<(), LoadError> {
        for (idx, entry) in self.entries.iter().enumerate() {
            let range = entry
                .range
                .as_ref()
                .ok_or_else(|| format!("entry {idx}: missing range"))?;
            if range.start.is_none() {
                return Err(format!("entry {idx}: range missing start address").into());
            }
            if range.end.is_none() {
                return Err(format!("entry {idx}: range missing end address").into());
            }

            for (nidx, nexthop) in entry.nexthops.iter().enumerate() {
                if nexthop.dst_mac.is_none() {
                    return Err(format!("entry {idx}: nexthop {nidx}: missing dst_mac").into());
                }
                if nexthop.src_mac.is_none() {
                    return Err(format!("entry {idx}: nexthop {nidx}: missing src_mac").into());
                }
                if nexthop.device.is_empty() {
                    return Err(format!("entry {idx}: nexthop {nidx}: empty device").into());
                }
            }
        }
        Ok(())
    }
}

/// Route module CLI.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
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
    /// Delete a route module config.
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

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.route.controlplane.routepb.v1.RouteService";

/// Rewrites a genuine missing-config `NotFound` into a friendly message.
const NOT_FOUND: NotFoundMapper = NotFoundMapper::new(SERVICE_NAME, "requested config");

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| (cmd.verbose, cmd.format), run)
}

/// Completion candidates for a `--name` argument: the route configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(
        Cmd::command,
        |channel| {
            RouteServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        },
        async move |mut client| Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs),
    )
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = RouteService::new(&cmd.connection, action).await?;

    match cmd.mode {
        ModeCmd::Fib(cmd) => match cmd.action {
            FibAction::List => service.list_fibs().await,
            FibAction::Show(cmd) => service.show_fib(cmd).await,
            FibAction::Update(cmd) => service.update_fib(cmd).await,
            FibAction::Delete(cmd) => service.delete_fib(cmd).await,
        },
    }
}

pub struct RouteService {
    service: Service<RouteServiceClient<LayeredChannel>>,
}

impl RouteService {
    pub async fn new(connection: &ConnectionArgs, action: &'static str) -> Result<Self, Error> {
        let service = Service::connect_for(connection, action, SERVICE_NAME, |channel| {
            RouteServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    pub async fn update_fib(&mut self, cmd: FibUpdateCmd) -> Result<(), Error> {
        let config = FibConfig::load(&cmd.file).map_err(|err| self.service.invalid("update", err.to_string()))?;
        let entry_count = config.entries.len();
        let request = UpdateFibRequest {
            module_name: cmd.config_name.clone(),
            entries: config.entries,
        };
        self.service
            .client()
            .update_fib(request)
            .await
            .map_err(self.service.status("update"))?;

        output::success(
            "update",
            format_args!("Updated FIB '{}' ({} entries).", cmd.config_name, entry_count),
        );
        Ok(())
    }

    pub async fn delete_fib(&mut self, cmd: FibDeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };

        self.service.client().delete_config(request).await.map_err(|status| {
            NOT_FOUND.map(
                status,
                "delete",
                self.service.endpoint(),
                Some(&format!("config '{}'", cmd.config_name)),
            )
        })?;

        output::success("delete", format_args!("Deleted FIB '{}'.", cmd.config_name));
        Ok(())
    }

    pub async fn list_fibs(&mut self) -> Result<(), Error> {
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
                        format_args!("No FIB configurations found."),
                        format_args!("create one with 'yanet-cli-route fib update --name <name> <path>'"),
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

    pub async fn show_fib(&mut self, cmd: FibShowCmd) -> Result<(), Error> {
        let request = ShowFibRequest {
            name: cmd.config_name.clone(),
            ipv4_only: cmd.ipv4,
            ipv6_only: cmd.ipv6,
        };

        let response = self
            .service
            .client()
            .show_fib(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();
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
}

#[cfg(test)]
mod test {
    use core::net::IpAddr;
    use std::{env, fs, net::TcpListener};

    use commonpb::pb::{IpRange, MacAddress};
    use netip::MacAddr;
    use ync::{
        auth::{AuthArgs, AuthMethod},
        client::TlsArgs,
        errors::ErrorKind,
    };

    use super::*;

    #[test]
    fn cmd_is_valid() {
        Cmd::command().debug_assert();
    }

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

    /// A rules file omitting `range` entirely still deserializes -- `range`
    /// is `Option<routepb::IpRange>`, and serde treats a missing key for an
    /// `Option` field as `None` rather than a load error -- so catching
    /// this is `FibConfig::validate`'s job, not `deny_unknown_fields`'s.
    #[test]
    fn fib_config_validate_rejects_missing_range() {
        let yaml = "
entries:
  - nexthops: []
";
        let config: FibConfig = serde_yaml::from_str(yaml).unwrap();
        let err = config.validate().unwrap_err();
        assert_eq!("entry 0: missing range", err.to_string());
    }

    #[test]
    fn fib_config_validate_rejects_range_missing_start() {
        let yaml = "
entries:
  - range:
      end: 10.0.0.255
";
        let config: FibConfig = serde_yaml::from_str(yaml).unwrap();
        let err = config.validate().unwrap_err();
        assert_eq!("entry 0: range missing start address", err.to_string());
    }

    #[test]
    fn fib_config_validate_rejects_range_missing_end() {
        let yaml = "
entries:
  - range:
      start: 10.0.0.0
";
        let config: FibConfig = serde_yaml::from_str(yaml).unwrap();
        let err = config.validate().unwrap_err();
        assert_eq!("entry 0: range missing end address", err.to_string());
    }

    #[test]
    fn fib_config_validate_rejects_nexthop_missing_dst_mac() {
        let yaml = "
entries:
  - range:
      start: 10.0.0.0
      end: 10.0.0.255
    nexthops:
      - src_mac: 11:22:33:44:55:66
        device: eth0
";
        let config: FibConfig = serde_yaml::from_str(yaml).unwrap();
        let err = config.validate().unwrap_err();
        assert_eq!("entry 0: nexthop 0: missing dst_mac", err.to_string());
    }

    #[test]
    fn fib_config_validate_rejects_nexthop_missing_src_mac() {
        let yaml = "
entries:
  - range:
      start: 10.0.0.0
      end: 10.0.0.255
    nexthops:
      - dst_mac: aa:bb:cc:dd:ee:ff
        device: eth0
";
        let config: FibConfig = serde_yaml::from_str(yaml).unwrap();
        let err = config.validate().unwrap_err();
        assert_eq!("entry 0: nexthop 0: missing src_mac", err.to_string());
    }

    #[test]
    fn fib_config_validate_rejects_nexthop_empty_device() {
        let yaml = r#"
entries:
  - range:
      start: 10.0.0.0
      end: 10.0.0.255
    nexthops:
      - dst_mac: aa:bb:cc:dd:ee:ff
        src_mac: 11:22:33:44:55:66
        device: ""
"#;
        let config: FibConfig = serde_yaml::from_str(yaml).unwrap();
        let err = config.validate().unwrap_err();
        assert_eq!("entry 0: nexthop 0: empty device", err.to_string());
    }

    /// A fully-specified entry passes validation untouched.
    #[test]
    fn fib_config_validate_accepts_fully_specified_entry() {
        let config = FibConfig {
            entries: vec![routepb::FibEntry {
                range: Some(ip_range("10.0.0.0", "10.0.0.255")),
                nexthops: vec![routepb::FibNexthop {
                    dst_mac: Some(mac("aa:bb:cc:dd:ee:ff")),
                    src_mac: Some(mac("11:22:33:44:55:66")),
                    device: "eth0".to_owned(),
                    counter: "nexthop_custom-counter".to_owned(),
                }],
            }],
        };
        assert!(config.validate().is_ok());
    }

    /// An entry with no nexthops at all is a legitimate no-op entry -- see
    /// `UpdateFibRequest`'s proto doc comment -- and still passes
    /// validation.
    #[test]
    fn fib_config_validate_accepts_entry_without_nexthops() {
        let config = FibConfig {
            entries: vec![routepb::FibEntry {
                range: Some(ip_range("10.0.0.0", "10.0.0.255")),
                nexthops: Vec::new(),
            }],
        };
        assert!(config.validate().is_ok());
    }

    #[test]
    fn test_fib_config_load_missing_file_names_the_path() {
        let path = env::temp_dir().join(format!("yanet-cli-route-missing-fib-{}.yaml", std::process::id()));

        let err = FibConfig::load(&path).unwrap_err();

        assert!(err.to_string().starts_with(&path.display().to_string()), "{err}");
    }

    #[test]
    fn test_fib_config_load_invalid_entry_names_the_path() {
        let path = env::temp_dir().join(format!("yanet-cli-route-invalid-fib-{}.yaml", std::process::id()));
        fs::write(&path, "entries:\n  - nexthops: []\n").unwrap();

        let err = FibConfig::load(&path).unwrap_err();
        fs::remove_file(&path).unwrap();

        assert_eq!(format!("{}: entry 0: missing range", path.display()), err.to_string());
    }

    /// Verifies that a refused connection is reported through the shared
    /// error contract as a connection failure, exit code 4.
    #[tokio::test]
    async fn test_route_service_new_connection_refused_reports_connection_error() {
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let port = listener.local_addr().unwrap().port();
        drop(listener);
        let connection = ConnectionArgs {
            endpoint: Some(format!("grpc://127.0.0.1:{port}")),
            auth: AuthArgs {
                auth: Some(AuthMethod::None),
                cert_tag: None,
            },
            tls: TlsArgs::default(),
            timeout: None,
        };

        let err = RouteService::new(&connection, "list").await.err().unwrap();

        assert_eq!(ErrorKind::Connection, err.kind());
        assert_eq!(4, err.exit_code());
    }
}
