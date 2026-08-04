//! CLI for YANET "route" module.

use core::error::Error;
use std::{
    fs::File,
    path::{Path, PathBuf},
};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::{
    engine::{ArgValueCandidates, CompletionCandidate},
    CompleteEnv,
};
use commonpb::pb::{IpRange, MacAddress};
use netip::{Contiguous, IpNetwork, MacAddr};
use serde::{Deserialize, Serialize};
use tonic::codec::CompressionEncoding;
use yanet_cli_route::{
    fib::{json::FibEntryJson, render::print_fib},
    routepb::{self, route_service_client::RouteServiceClient, ListConfigsRequest, ShowFibRequest, UpdateFibRequest},
};
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    completion,
    output::{self, CommonFormat},
};

#[derive(Debug, Serialize, Deserialize)]
struct FibNexthop {
    dst_mac: String,
    src_mac: String,
    device: String,
}

/// A FIB entry as written in the YAML config.
///
/// Named `Config` rather than `Entry` to stay visually distinct from the
/// wire's [`routepb::FibEntry`], which this type converts into.
#[derive(Debug, Serialize, Deserialize)]
struct FibEntryConfig {
    prefix: String,
    #[serde(default)]
    nexthops: Vec<FibNexthop>,
}

#[derive(Debug, Serialize, Deserialize)]
struct FibConfig {
    #[serde(default)]
    entries: Vec<FibEntryConfig>,
}

impl FibConfig {
    fn load<P>(path: P) -> Result<Self, Box<dyn Error>>
    where
        P: AsRef<Path>,
    {
        let file = File::open(path)?;
        let config = serde_yaml::from_reader(file)?;
        Ok(config)
    }
}

fn parse_mac(s: &str) -> Result<MacAddress, Box<dyn Error>> {
    let mac: MacAddr = s.parse()?;
    Ok(mac.into())
}

impl TryFrom<FibNexthop> for routepb::FibNexthop {
    type Error = Box<dyn Error>;

    fn try_from(nh: FibNexthop) -> Result<Self, Self::Error> {
        Ok(Self {
            dst_mac: Some(parse_mac(&nh.dst_mac)?),
            src_mac: Some(parse_mac(&nh.src_mac)?),
            device: nh.device,
        })
    }
}

impl TryFrom<FibEntryConfig> for routepb::FibEntry {
    type Error = Box<dyn Error>;

    fn try_from(entry: FibEntryConfig) -> Result<Self, Self::Error> {
        let network = Contiguous::<IpNetwork>::parse(&entry.prefix)?;
        // `addr()` is this network's base address rather than the address as
        // typed: `IpNetwork` always normalizes to the mask on construction, so
        // the range below denotes the whole network the prefix names.
        let range = IpRange::from((network.addr(), network.last_addr()));
        let nexthops = entry
            .nexthops
            .into_iter()
            .map(routepb::FibNexthop::try_from)
            .collect::<Result<Vec<_>, _>>()?;
        Ok(Self { range: Some(range), nexthops })
    }
}

/// Route module CLI.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
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
}

#[derive(Debug, Clone, Parser)]
pub struct FibUpdateCmd {
    /// Route module config name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
    /// Path to the FIB YAML file.
    #[arg(required = true, long = "rules", value_name = "PATH")]
    pub rules: PathBuf,
}

#[derive(Debug, Clone, Parser)]
pub struct FibShowCmd {
    /// Show only IPv4 FIB entries.
    #[arg(long, short = '4')]
    pub ipv4: bool,
    /// Show only IPv6 FIB entries.
    #[arg(long, short = '6')]
    pub ipv6: bool,
    /// Route config name.
    #[arg(long = "name", short = 'n', add = ArgValueCandidates::new(config_candidates))]
    pub config_name: String,
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
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
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

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = RouteService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::Fib(cmd) => match cmd.action {
            FibAction::List => service.list_fibs().await,
            FibAction::Show(cmd) => service.show_fib(cmd).await,
            FibAction::Update(cmd) => service.update_fib(cmd).await,
        },
    }
}

pub struct RouteService {
    client: RouteServiceClient<LayeredChannel>,
}

impl RouteService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Box<dyn Error>> {
        let channel = ync::client::connect(connection).await?;
        let client = RouteServiceClient::new(channel)
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    pub async fn update_fib(&mut self, cmd: FibUpdateCmd) -> Result<(), Box<dyn Error>> {
        let config = FibConfig::load(&cmd.rules)?;
        let entries = config
            .entries
            .into_iter()
            .map(routepb::FibEntry::try_from)
            .collect::<Result<Vec<_>, _>>()?;
        let entry_count = entries.len();
        let request = UpdateFibRequest {
            module_name: cmd.config_name.clone(),
            entries,
        };
        self.client.update_fib(request).await?;

        output::success(
            "update",
            format_args!("Updated FIB '{}' ({} entries).", cmd.config_name, entry_count),
        );
        Ok(())
    }

    pub async fn list_fibs(&mut self) -> Result<(), Box<dyn Error>> {
        let response = self.client.list_configs(ListConfigsRequest {}).await?.into_inner();

        output::data(
            || &response.configs,
            || {
                if response.configs.is_empty() {
                    output::empty_with_hint(
                        format_args!("No FIB configurations found."),
                        format_args!("create one with 'yanet-cli-route fib update --name <name> --rules <path>'"),
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

    pub async fn show_fib(&mut self, cmd: FibShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowFibRequest {
            name: cmd.config_name.clone(),
            ipv4_only: cmd.ipv4,
            ipv6_only: cmd.ipv6,
        };

        let response = self.client.show_fib(request).await?.into_inner();
        let entries = response.entries;

        output::data(
            || entries.iter().map(FibEntryJson::from).collect::<Vec<_>>(),
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

    use super::*;

    #[test]
    fn cmd_is_valid() {
        Cmd::command().debug_assert();
    }

    fn ip_range(start: &str, end: &str) -> IpRange {
        IpRange::from((start.parse::<IpAddr>().unwrap(), end.parse::<IpAddr>().unwrap()))
    }

    fn entry_range(prefix: &str) -> IpRange {
        let entry = FibEntryConfig {
            prefix: prefix.to_string(),
            nexthops: Vec::new(),
        };
        routepb::FibEntry::try_from(entry).unwrap().range.unwrap()
    }

    #[test]
    fn fib_entry_config_prefix_converts_to_expected_range() {
        assert_eq!(ip_range("10.0.0.0", "10.0.0.255"), entry_range("10.0.0.0/24"));
    }

    #[test]
    fn fib_entry_config_prefix_masks_host_bits_v4() {
        assert_eq!(ip_range("10.0.0.0", "10.0.0.255"), entry_range("10.0.0.5/24"));
    }

    #[test]
    fn fib_entry_config_prefix_masks_host_bits_v6() {
        assert_eq!(ip_range("2001:db8::", "2001:db8::ff"), entry_range("2001:db8::5/120"));
    }

    #[test]
    fn fib_entry_config_prefix_slash_32_is_noop() {
        assert_eq!(ip_range("10.0.0.5", "10.0.0.5"), entry_range("10.0.0.5/32"));
    }

    #[test]
    fn fib_entry_config_prefix_slash_128_is_noop() {
        assert_eq!(ip_range("2001:db8::5", "2001:db8::5"), entry_range("2001:db8::5/128"));
    }
}
