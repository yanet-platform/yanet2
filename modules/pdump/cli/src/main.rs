use clap::{ArgAction, CommandFactory, Parser};
use clap_complete::CompleteEnv;
use core::error::Error;
use ptree::TreeBuilder;
use std::io::ErrorKind;

use tokio::signal::{unix, unix::SignalKind};
use tokio::task::JoinSet;
use tokio_util::sync::CancellationToken;
use tonic::transport::Channel;

use commonpb::TargetModule;
use pdumppb::{
    ListConfigsRequest, ReadDumpRequest, SetDumpModeRequest, SetFilterRequest, SetSnapLenRequest,
    SetWorkerRingSizeRequest, ShowConfigRequest, ShowConfigResponse, pdump_service_client::PdumpServiceClient,
};
use ync::logging;

use args::{
    ConfigOutputFormat, ModeCmd, ReadCmd, SetDumpModeCmd, SetFilterCmd, SetRingSizeCmd, SetSnapLenCmd, ShowConfigCmd,
};

mod args;
mod printer;
mod writer;

#[allow(non_snake_case)]
pub mod pdumppb {
    use serde::Serialize;

    tonic::include_proto!("pdumppb");
}

#[allow(non_snake_case)]
pub mod commonpb {
    use serde::Serialize;

    tonic::include_proto!("commonpb");
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
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::SetFilter(cmd) => service.set_filter(cmd).await,
        ModeCmd::SetDumpMode(cmd) => service.set_dump_mode(cmd).await,
        ModeCmd::SetSnapLen(cmd) => service.set_snap_len(cmd).await,
        ModeCmd::SetRingSize(cmd) => service.set_ring_size(cmd).await,
        ModeCmd::Read(cmd) => service.read_dump(cmd).await,
    }
}

pub struct PdumpService {
    client: PdumpServiceClient<Channel>,
}

impl PdumpService {
    pub async fn new(endpoint: String) -> Result<Self, Box<dyn Error>> {
        let client = PdumpServiceClient::connect(endpoint).await?;
        Ok(Self { client })
    }

    async fn get_configs(
        &mut self,
        name: &str,
        numa_indices: Vec<u32>,
    ) -> Result<Vec<ShowConfigResponse>, Box<dyn Error>> {
        let mut responses = Vec::new();
        for numa in numa_indices {
            let request = ShowConfigRequest {
                target: Some(TargetModule { config_name: name.to_owned(), numa }),
            };
            log::trace!("show config request on NUMA {numa}: {request:?}");
            let response = self.client.show_config(request).await?.into_inner();
            log::debug!("show config response on NUMA {numa}: {response:?}");
            responses.push(response);
        }
        Ok(responses)
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Box<dyn Error>> {
        let Some(name) = cmd.config_name else {
            self.print_config_list().await?;
            return Ok(());
        };

        let mut numa_indices = cmd.numa;
        if numa_indices.is_empty() {
            numa_indices = self.get_numa_indices().await?;
        }
        let configs = self.get_configs(&name, numa_indices).await?;

        match cmd.format {
            ConfigOutputFormat::Json => print_json(configs)?,
            ConfigOutputFormat::Tree => print_tree(configs)?,
        }

        Ok(())
    }

    pub async fn set_filter(&mut self, cmd: SetFilterCmd) -> Result<(), Box<dyn Error>> {
        for numa in cmd.numa {
            let request = SetFilterRequest {
                target: Some(TargetModule {
                    config_name: cmd.config_name.clone(),
                    numa,
                }),
                filter: cmd.filter.to_string(),
            };
            log::trace!("set filter request on NUMA {numa}: {request:?}");
            let response = self.client.set_filter(request).await?.into_inner();
            log::debug!("set filter response on NUMA {numa}: {response:?}");
        }
        Ok(())
    }

    pub async fn set_dump_mode(&mut self, cmd: SetDumpModeCmd) -> Result<(), Box<dyn Error>> {
        for numa in cmd.numa {
            let request = SetDumpModeRequest {
                target: Some(TargetModule {
                    config_name: cmd.config_name.clone(),
                    numa,
                }),
                mode: cmd.mode.into(),
            };
            log::trace!("set dump mode request on NUMA {numa}: {request:?}");
            let response = self.client.set_dump_mode(request).await?.into_inner();
            log::debug!("set dump mode response on NUMA {numa}: {response:?}");
        }
        Ok(())
    }

    pub async fn set_snap_len(&mut self, cmd: SetSnapLenCmd) -> Result<(), Box<dyn Error>> {
        for numa in cmd.numa {
            let request = SetSnapLenRequest {
                target: Some(TargetModule {
                    config_name: cmd.config_name.clone(),
                    numa,
                }),
                snaplen: cmd.snaplen,
            };
            log::trace!("set snap len request on NUMA {numa}: {request:?}");
            let response = self.client.set_snap_len(request).await?.into_inner();
            log::debug!("set snap len response on NUMA {numa}: {response:?}");
        }
        Ok(())
    }

    pub async fn set_ring_size(&mut self, cmd: SetRingSizeCmd) -> Result<(), Box<dyn Error>> {
        for numa in cmd.numa {
            let request = SetWorkerRingSizeRequest {
                target: Some(TargetModule {
                    config_name: cmd.config_name.clone(),
                    numa,
                }),
                ring_size: cmd.ring_size,
            };
            log::trace!("set per worker ring size request on NUMA {numa}: {request:?}");
            let response = self.client.set_worker_ring_size(request).await?.into_inner();
            log::debug!("set per worker ring size response on NUMA {numa}: {response:?}");
        }
        Ok(())
    }

    pub async fn read_dump(&mut self, cmd: ReadCmd) -> Result<(), Box<dyn Error>> {
        let cancellation_token = CancellationToken::new();
        let done = cancellation_token.clone();

        let mut reader_set = JoinSet::new();
        let (tx, rx) = tokio::sync::mpsc::channel::<pdumppb::Record>(16);

        log::debug!("request current pdump configuration for numa: {:?}", cmd.numa);
        let configs: Vec<_> = self
            .get_configs(&cmd.config_name, cmd.numa.clone())
            .await?
            .into_iter()
            .filter_map(|c| c.config)
            .collect();

        if configs.is_empty() {
            return Err(Box::new(std::io::Error::new(
                ErrorKind::NotFound,
                format!("Configuration {} not found on NUMA {:?}", cmd.config_name, cmd.numa),
            )));
        }

        for numa in cmd.numa {
            let request = ReadDumpRequest {
                target: Some(TargetModule {
                    config_name: cmd.config_name.clone(),
                    numa,
                }),
            };
            log::trace!("read_data request on NUMA {numa}: {request:?}");
            let stream = self.client.read_dump(request).await?.into_inner();
            log::debug!(
                "read_data successfully acquired data stream on NUMA {numa} for {}",
                cmd.config_name,
            );

            reader_set.spawn(writer::pdump_stream_reader(stream, tx.clone(), done.clone()));
        }
        drop(tx);

        // Spawn outside the reader_set to get unpinable join handler.
        let mut write_jh = tokio::task::spawn_blocking(move || {
            let output = cmd.output.unwrap_or("-".to_string());
            writer::pdump_write(configs, rx, cmd.num, cmd.format, &output)
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
                    Err(e) => log::warn!("writer task failed: {}", e),
                }
                cancellation_token.cancel();
            }
        }

        // Wait for all reader tasks to gracefully finish.
        while let Some(res) = reader_set.join_next().await {
            if let Err(e) = res {
                log::warn!("reader task failed during shutdown: {}", e);
            }
        }

        Ok(())
    }

    async fn get_numa_indices(&mut self) -> Result<Vec<u32>, Box<dyn Error>> {
        let request = ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();
        Ok(response.numa_configs.iter().map(|c| c.numa).collect())
    }

    async fn print_config_list(&mut self) -> Result<(), Box<dyn Error>> {
        let request = ListConfigsRequest {};
        let response = self.client.list_configs(request).await?.into_inner();
        let mut tree = TreeBuilder::new("Pdump Configs".to_string());
        for numa in response.numa_configs {
            tree.begin_child(format!("NUMA {}", numa.numa));
            for config in numa.configs {
                tree.add_empty_child(config);
            }
        }
        let tree = tree.build();
        ptree::print_tree(&tree)?;
        Ok(())
    }
}

pub fn print_json(resp: Vec<ShowConfigResponse>) -> Result<(), Box<dyn Error>> {
    println!("{}", serde_json::to_string(&resp)?);
    Ok(())
}

pub fn print_tree(configs: Vec<ShowConfigResponse>) -> Result<(), Box<dyn Error>> {
    let mut tree = TreeBuilder::new("Pdump Configs".to_string());

    for config in &configs {
        tree.begin_child(format!("NUMA {}", config.numa));

        if let Some(config) = &config.config {
            tree.add_empty_child(format!("Filter: {}", config.filter));
            tree.add_empty_child(format!(
                "Mode: {}",
                config.mode().as_str_name().replace("PDUMP_DUMP_", "")
            ));
            tree.add_empty_child(format!("Snaplen: {}", config.snaplen));
            tree.add_empty_child(format!("PerWorkerRingSize: {}", config.ring_size));
        }

        tree.end_child();
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
