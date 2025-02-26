//! CLI for YANET "route" module.

use core::{error::Error, net::IpAddr};

use clap::{ArgAction, Parser};
use code::{route_client::RouteClient, InsertRouteRequest};
use ipnet::IpNet;
use tonic::transport::Channel;
use ync::logging;

#[allow(non_snake_case)]
pub mod code {
    tonic::include_proto!("routepb");
}

/// Route module.
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Be verbose in terms of logging.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

#[derive(Debug, Clone, Parser)]
pub enum ModeCmd {
    /// Inserts a unicast static route.
    Insert(RouteInsertCmd),
}

#[derive(Debug, Clone, Parser)]
pub struct RouteInsertCmd {
    /// The destination prefix of the route.
    ///
    /// The prefix must be an IPv4 or IPv6 address followed by "/" and the
    /// length of the prefix.
    pub prefix: IpNet,
    /// Route module name.
    #[arg(long = "mod")]
    pub module_name: String,
    /// The IP address of the nexthop router.
    #[arg(long = "via")]
    pub nexthop_addr: IpAddr,
    /// NUMA node index where changes should be applied, optionally repeated.
    ///
    /// If not specified, the route will be applied to all NUMA nodes.
    #[arg(long)]
    pub numa: Option<Vec<u32>>,
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("no error expected");

    if let Err(err) = run(cmd).await {
        log::error!("ERROR: {err}");
        std::process::exit(1);
    }
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = RouteService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::Insert(cmd) => service.insert_route(cmd).await,
    }
}

pub struct RouteService {
    client: RouteClient<Channel>,
}

impl RouteService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = RouteClient::connect(endpoint).await?;
        let m = Self { client };

        Ok(m)
    }

    pub async fn insert_route(&mut self, cmd: RouteInsertCmd) -> Result<(), Box<dyn Error>> {
        let request = InsertRouteRequest {
            module_name: cmd.module_name,
            prefix: cmd.prefix.to_string(),
            nexthop_addr: cmd.nexthop_addr.to_string(),
            numa: cmd.numa.unwrap_or_default(),
        };

        let resp = self.client.insert_route(request).await?;

        log::debug!("InsertRouteResponse: {:?}", resp);
        Ok(())
    }
}
