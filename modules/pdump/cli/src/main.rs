use core::error::Error;
use std::io::ErrorKind;

use args::{ConfigOutputFormat, DeleteCmd, ModeCmd, ReadCmd, SetConfigCmd, ShowConfigCmd};
use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use pdumppb::{
    DeleteConfigRequest, ListConfigsRequest, ReadDumpRequest, ShowConfigRequest, ShowConfigResponse,
    pdump_service_client::PdumpServiceClient,
};
use ptree::TreeBuilder;
use tokio::{
    signal::{unix, unix::SignalKind},
    task::JoinSet,
};
use tokio_util::sync::CancellationToken;
use tonic::{codec::CompressionEncoding, transport::Channel};
use ync::logging;

use crate::pdumppb::SetConfigRequest;

mod args;
mod dump_mode;
mod printer;
mod writer;

#[allow(non_snake_case)]
pub mod pdumppb {
    use serde::Serialize;

    tonic::include_proto!("pdumppb");
}

/// Pdump - packet dump module
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    /// Gateway endpoint.
    #[clap(long, default_value = "grpc://[::1]:8080", global = true)]
    pub endpoint: String,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

async fn run(cmd: Cmd) -> Result<(), Box<dyn Error>> {
    let mut service = PdumpService::new(cmd.endpoint).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Set(cmd) => service.set_config(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Read(cmd) => service.read_dump(cmd).await,
    }
}

pub struct PdumpService {
    client: PdumpServiceClient<Channel>,
}

impl PdumpService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = PdumpServiceClient::connect(endpoint).await?;
        let client = client
            .send_compressed(CompressionEncoding::Gzip)
            .accept_compressed(CompressionEncoding::Gzip);
        Ok(Self { client })
    }

    async fn get_config(&mut self, name: &str) -> Result<ShowConfigResponse, Box<dyn Error>> {
        let request = ShowConfigRequest { name: name.to_owned() };
        log::trace!("show config request: {request:?}");
        let response = self.client.show_config(request).await?.into_inner();
        log::debug!("show config response: {response:?}");
        Ok(response)
    }

    pub async fn list_configs(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        log::trace!("list configs request: {request:?}");
        let response = self.client.list_configs(request).await?.into_inner();
        log::debug!("list configs response: {response:?}");

        let mut tree = TreeBuilder::new("List Pdump Configs".to_string());
        for config in response.configs {
            tree.add_empty_child(config);
        }
        let tree = tree.build();
        ptree::print_tree(&tree)?;
        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Box<dyn Error>> {
        let config = self.get_config(&cmd.config_name).await?;

        match cmd.format {
            ConfigOutputFormat::Json => print_json(&config)?,
            ConfigOutputFormat::Tree => print_tree(&config)?,
        }

        Ok(())
    }

    pub async fn set_config(&mut self, cmd: SetConfigCmd) -> Result<(), Box<dyn Error>> {
        let mut request = SetConfigRequest {
            name: cmd.config_name.clone(),
            ..Default::default()
        };
        let mut cfg = request.config.unwrap_or_default();
        let mut mask = request.update_mask.unwrap_or_default();

        if let Some(filter) = &cmd.filter {
            cfg.filter = filter.to_string();
            mask.paths.push("filter".to_string());
        }

        if let Some(mode) = cmd.mode {
            cfg.mode = mode.into();
            mask.paths.push("mode".to_string());
        }

        if let Some(snaplen) = cmd.snaplen {
            cfg.snaplen = snaplen;
            mask.paths.push("snaplen".to_string());
        }

        if let Some(ring_size) = cmd.ring_size {
            cfg.ring_size = ring_size;
            mask.paths.push("ring_size".to_string());
        }

        request.config = Some(cfg);
        request.update_mask = Some(mask);
        log::trace!("set config request: {request:?}");
        let response = self.client.set_config(request).await?.into_inner();
        log::debug!("set config response: {response:?}");
        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Box<dyn Error>> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        log::trace!("delete config request: {request:?}");
        self.client.delete_config(request).await?.into_inner();
        log::info!("config {} deleted", cmd.config_name);
        Ok(())
    }

    pub async fn read_dump(&mut self, cmd: ReadCmd) -> Result<(), Box<dyn Error>> {
        let cancellation_token = CancellationToken::new();
        let done = cancellation_token.clone();

        let mut reader_set = JoinSet::new();
        let (tx, rx) = tokio::sync::mpsc::channel::<pdumppb::Record>(16);

        log::debug!("request current pdump configuration");
        let config = self.get_config(&cmd.config_name).await?;
        let Some(config) = config.config else {
            return Err(Box::new(std::io::Error::new(
                ErrorKind::NotFound,
                format!("Configuration {} not found", cmd.config_name),
            )));
        };

        let request = ReadDumpRequest { name: cmd.config_name.clone() };
        log::trace!("read_data request: {request:?}");
        let stream = self.client.read_dump(request).await?.into_inner();
        log::debug!("read_data successfully acquired data stream for {}", cmd.config_name,);

        reader_set.spawn(writer::pdump_stream_reader(stream, tx.clone(), done.clone()));
        drop(tx);

        // Spawn outside the reader_set to get unpinable join handler.
        let mut write_jh = tokio::task::spawn_blocking(move || {
            let output = cmd.output.unwrap_or("-".to_string());
            writer::pdump_write(vec![config], rx, cmd.num, cmd.format, &output)
        });

        let mut sig_pipe = unix::signal(SignalKind::pipe())?;

        tokio::select! {
            _ = sig_pipe.recv() => {
                log::warn!("writer pipe closed; initiating shutdown...");
                cancellation_token.cancel();
            }
            _ = tokio::signal::ctrl_c() => {
                log::warn!("interrupted...");
                cancellation_token.cancel();
            }
            res = &mut write_jh => {
                log::warn!("writer task finished, initiating shutdown...");
                match res {
                    Ok(()) => log::debug!("writer task completed successfully."),
                    Err(e) => log::warn!("writer task failed: {e}"),
                }
                cancellation_token.cancel();
            }
        }

        // Wait for all reader tasks to gracefully finish.
        while let Some(res) = reader_set.join_next().await {
            if let Err(e) = res {
                log::warn!("reader task failed during shutdown: {e}");
            }
        }

        Ok(())
    }
}

pub fn print_json(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    println!("{}", serde_json::to_string(resp)?);
    Ok(())
}

pub fn print_tree(resp: &ShowConfigResponse) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Pdump Config".to_string());

    if let Some(config) = &resp.config {
        tree.add_empty_child(format!("Filter: {}", config.filter));
        tree.add_empty_child(format!("Mode: {}", dump_mode::to_str(config.mode)));
        tree.add_empty_child(format!("Snaplen: {}", config.snaplen));
        tree.add_empty_child(format!("PerWorkerRingSize: {}", config.ring_size));
    }

    let tree = tree.build();
    ptree::print_tree(&tree)?;

    Ok(())
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    logging::init(cmd.verbose as usize).expect("initialize logging");

    if let Err(err) = run(cmd).await {
        log::error!("run failed: {err}");
        std::process::exit(1);
    }
}
