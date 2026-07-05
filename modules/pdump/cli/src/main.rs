use args::{DeleteCmd, ModeCmd, ReadCmd, SetConfigCmd, ShowConfigCmd};
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
use tonic::{Status, codec::CompressionEncoding};
use ync::{
    client::{ConnectionArgs, LayeredChannel, Service},
    errors::Error,
    output::{self, CommonFormat},
};

use crate::pdumppb::SetConfigRequest;

mod args;
mod dump_mode;
mod printer;
mod writer;

#[allow(non_snake_case)]
pub mod pdumppb {
    use serde::Serialize;

    tonic::include_proto!("modules.pdump.controlplane.pdumppb.v1");
}

/// Pdump - packet dump module
#[derive(Debug, Clone, Parser)]
#[command(version, about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub connection: ConnectionArgs,
    /// Output format.
    #[arg(long, default_value = "human", global = true)]
    pub format: CommonFormat,
    /// Log verbosity level.
    #[clap(short, action = ArgAction::Count, global = true)]
    pub verbose: u8,
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let mut service = PdumpService::new(&cmd.connection).await?;

    match cmd.mode {
        ModeCmd::List => service.list_configs().await,
        ModeCmd::Show(cmd) => service.show_config(cmd).await,
        ModeCmd::Set(cmd) => service.set_config(cmd).await,
        ModeCmd::Delete(cmd) => service.delete_config(cmd).await,
        ModeCmd::Read(cmd) => service.read_dump(cmd).await,
    }
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.pdump.controlplane.pdumppb.v1.PdumpService";

pub struct PdumpService {
    service: Service<PdumpServiceClient<LayeredChannel>>,
}

impl PdumpService {
    pub async fn new(connection: &ConnectionArgs) -> Result<Self, Error> {
        let service = Service::connect(connection, SERVICE_NAME, |channel| {
            PdumpServiceClient::new(channel)
                .send_compressed(CompressionEncoding::Gzip)
                .accept_compressed(CompressionEncoding::Gzip)
        })
        .await?;

        Ok(Self { service })
    }

    async fn get_config(&mut self, name: &str) -> Result<ShowConfigResponse, Error> {
        let request = ShowConfigRequest { name: name.to_owned() };
        log::trace!("show config request: {request:?}");
        let response = self
            .service
            .client()
            .show_config(request)
            .await
            .map_err(self.service.status("show"))?
            .into_inner();
        log::debug!("show config response: {response:?}");
        Ok(response)
    }

    pub async fn list_configs(&mut self) -> Result<(), Error> {
        let request = ListConfigsRequest {};
        log::trace!("list configs request: {request:?}");
        let response = self
            .service
            .client()
            .list_configs(request)
            .await
            .map_err(self.service.status("list"))?
            .into_inner();
        log::debug!("list configs response: {response:?}");

        output::data(
            &response.configs,
            response.configs.is_empty(),
            format_args!("no pdump configs"),
            || {
                let mut tree = TreeBuilder::new("List Pdump Configs".to_owned());
                for config in &response.configs {
                    tree.add_empty_child(config.clone());
                }
                let _ = ptree::print_tree(&tree.build());
            },
        );

        Ok(())
    }

    pub async fn show_config(&mut self, cmd: ShowConfigCmd) -> Result<(), Error> {
        let response = self.get_config(&cmd.config_name).await?;

        output::data(&response, false, format_args!(""), || print_tree(&response));

        Ok(())
    }

    pub async fn set_config(&mut self, cmd: SetConfigCmd) -> Result<(), Error> {
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
            cfg.ring_size = ring_size.get();
            mask.paths.push("ring_size".to_string());
        }

        request.config = Some(cfg);
        request.update_mask = Some(mask);
        log::trace!("set config request: {request:?}");
        self.service
            .client()
            .set_config(request)
            .await
            .map_err(self.service.status("set"))?;

        output::success("set", format_args!("Set pdump config {}.", cmd.config_name));

        Ok(())
    }

    pub async fn delete_config(&mut self, cmd: DeleteCmd) -> Result<(), Error> {
        let request = DeleteConfigRequest { name: cmd.config_name.clone() };
        log::trace!("delete config request: {request:?}");
        self.service
            .client()
            .delete_config(request)
            .await
            .map_err(self.service.status("delete"))?;

        output::success("delete", format_args!("Deleted pdump config {}.", cmd.config_name));

        Ok(())
    }

    pub async fn read_dump(&mut self, cmd: ReadCmd) -> Result<(), Error> {
        let cancellation_token = CancellationToken::new();
        let done = cancellation_token.clone();

        let mut reader_set = JoinSet::new();
        let (tx, rx) = tokio::sync::mpsc::channel::<pdumppb::Record>(16);

        log::debug!("request current pdump configuration");
        let config = self.get_config(&cmd.config_name).await?;
        let Some(config) = config.config else {
            return Err(Error::from_status(
                Status::not_found(format!("config '{}' not found", cmd.config_name)),
                "read",
                self.service.endpoint(),
                SERVICE_NAME,
            ));
        };

        let request = ReadDumpRequest { name: cmd.config_name.clone() };
        log::trace!("read_data request: {request:?}");
        let stream = self
            .service
            .client()
            .read_dump(request)
            .await
            .map_err(self.service.status("read"))?
            .into_inner();
        log::debug!("read_data successfully acquired data stream for {}", cmd.config_name,);

        reader_set.spawn(writer::pdump_stream_reader(stream, tx.clone(), done.clone()));
        drop(tx);

        // Spawn outside the reader_set to get unpinable join handler.
        let mut write_jh = tokio::task::spawn_blocking(move || {
            let output = cmd.output.unwrap_or("-".to_string());
            writer::pdump_write(vec![config], rx, cmd.num, cmd.dump_format, &output)
        });

        let mut sig_pipe = unix::signal(SignalKind::pipe()).expect("failed to register SIGPIPE handler");

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

fn print_tree(resp: &ShowConfigResponse) {
    let mut tree = TreeBuilder::new("Pdump Config".to_owned());

    if let Some(config) = &resp.config {
        tree.add_empty_child(format!("Filter: {}", config.filter));
        tree.add_empty_child(format!("Mode: {}", dump_mode::to_str(config.mode)));
        tree.add_empty_child(format!("Snaplen: {}", config.snaplen));
        tree.add_empty_child(format!("PerWorkerRingSize: {}", config.ring_size));
    }

    let _ = ptree::print_tree(&tree.build());
}

#[tokio::main(flavor = "current_thread")]
pub async fn main() {
    CompleteEnv::with_factory(Cmd::command).complete();
    let cmd = Cmd::parse();
    ync::init(cmd.verbose, cmd.format);

    if let Err(err) = run(cmd).await {
        output::failure(&err);
        std::process::exit(err.exit_code());
    }
}
