//! CLI for YANET "route" module.

use core::error::Error;
use std::{
    fs::File,
    path::{Path, PathBuf},
};

use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use commonpb::pb::MacAddress;
use netip::MacAddr;
use serde::{Deserialize, Serialize};
use tabled::{
    settings::{
        object::{Columns, Rows},
        style::{BorderColor, HorizontalLine},
        Color, Style,
    },
    Table, Tabled,
};
use tonic::codec::CompressionEncoding;
use yanet_cli_route::{
    routepb::{route_service_client::RouteServiceClient, ListConfigsRequest, ShowFibRequest, UpdateFibRequest},
    FibDisplayEntry,
};
use ync::{
    client::{ConnectionArgs, LayeredChannel},
    logging,
};

#[derive(Debug, Serialize, Deserialize)]
struct FibNexthop {
    dst_mac: String,
    src_mac: String,
    device: String,
}

#[derive(Debug, Serialize, Deserialize)]
struct FibEntry {
    prefix: String,
    #[serde(default)]
    nexthops: Vec<FibNexthop>,
}

#[derive(Debug, Serialize, Deserialize)]
struct FibConfig {
    #[serde(default)]
    entries: Vec<FibEntry>,
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
    Ok(MacAddress { addr: mac.as_u64() })
}

impl TryFrom<FibNexthop> for yanet_cli_route::routepb::FibNexthop {
    type Error = Box<dyn Error>;

    fn try_from(nh: FibNexthop) -> Result<Self, Self::Error> {
        Ok(Self {
            dst_mac: Some(parse_mac(&nh.dst_mac)?),
            src_mac: Some(parse_mac(&nh.src_mac)?),
            device: nh.device,
        })
    }
}

impl TryFrom<FibEntry> for yanet_cli_route::routepb::FibEntry {
    type Error = Box<dyn Error>;

    fn try_from(entry: FibEntry) -> Result<Self, Self::Error> {
        let nexthops = entry
            .nexthops
            .into_iter()
            .map(yanet_cli_route::routepb::FibNexthop::try_from)
            .collect::<Result<Vec<_>, _>>()?;
        Ok(Self { prefix: entry.prefix, nexthops })
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
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
    /// Path to the FIB YAML file.
    #[arg(required = true, long = "rules", value_name = "PATH")]
    pub rules: PathBuf,
}

#[derive(Debug, Clone, Parser)]
pub struct FibShowCmd {
    /// Show only IPv4 FIB entries.
    #[arg(long)]
    pub ipv4: bool,
    /// Show only IPv6 FIB entries.
    #[arg(long)]
    pub ipv6: bool,
    /// Route config name.
    #[arg(long = "name", short = 'n')]
    pub config_name: String,
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();

    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("no error expected");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
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
            .map(yanet_cli_route::routepb::FibEntry::try_from)
            .collect::<Result<Vec<_>, _>>()?;
        let request = UpdateFibRequest {
            module_name: cmd.config_name,
            entries,
        };
        self.client.update_fib(request).await?;

        println!("OK");
        Ok(())
    }

    pub async fn list_fibs(&mut self) -> Result<(), Box<dyn Error>> {
        let response = self.client.list_configs(ListConfigsRequest {}).await?.into_inner();

        for name in response.configs {
            println!("{name}");
        }
        Ok(())
    }

    pub async fn show_fib(&mut self, cmd: FibShowCmd) -> Result<(), Box<dyn Error>> {
        let request = ShowFibRequest {
            name: cmd.config_name.clone(),
            ipv4_only: cmd.ipv4,
            ipv6_only: cmd.ipv6,
        };

        let response = self.client.show_fib(request).await?.into_inner();

        let entries: Vec<FibDisplayEntry> = response
            .entries
            .into_iter()
            .flat_map(FibDisplayEntry::from_range_entry)
            .collect();

        if entries.is_empty() {
            log::info!("No FIB entries found for {}", cmd.config_name);
            return Ok(());
        }

        print_table(entries);

        Ok(())
    }
}

fn print_table<I, T>(entries: I)
where
    I: IntoIterator<Item = T>,
    T: Tabled,
{
    let mut table = Table::new(entries);
    table.with(
        Style::modern()
            .horizontals([(1, HorizontalLine::inherit(Style::modern()))])
            .remove_horizontal(),
    );
    table.modify(Columns::new(..), BorderColor::filled(Color::rgb_fg(0x4e, 0x4e, 0x4e)));
    table.modify(Rows::first(), Color::BOLD);

    println!("{table}");
}
