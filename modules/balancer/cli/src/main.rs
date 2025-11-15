mod cfg;
mod cmd;
mod info;
mod rpc;
mod service;

use clap::{CommandFactory, Parser};
use clap_complete::CompleteEnv;
use core::error::Error;
use ync::logging;

use crate::{cmd::Cmd, service::BalancerService};

////////////////////////////////////////////////////////////////////////////////

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = BalancerService::connect(cmd.endpoint).await?;
    service.handle_cmd(cmd.mode).await
}

////////////////////////////////////////////////////////////////////////////////

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();

    logging::init(cmd.verbosity as usize).expect("failed to initialize logging");

    if let Err(err) = run(cmd).await {
        log::error!("{err}");
        std::process::exit(1);
    }
}
