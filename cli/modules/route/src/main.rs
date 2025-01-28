//! CLI for YANET "route" module.

use core::error::Error;

use clap::{ArgAction, Parser};
use code::{route_client::RouteClient, InsertRouteRequest};
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
    /// Insert a route.
    Insert,
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
    match cmd.mode {
        ModeCmd::Insert => route_insert(cmd.endpoint).await,
    }
}

async fn route_insert(endpoint: String) -> Result<(), Box<dyn Error>> {
    let mut client = RouteClient::connect(endpoint).await?;
    let resp = client.insert_route(InsertRouteRequest {}).await?;

    log::info!("LOL KEK: {:?}", resp);
    Ok(())
}
