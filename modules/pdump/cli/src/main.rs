use args::{DeleteCmd, ModeCmd, ReadCmd, SetConfigCmd, ShowConfigCmd};
use clap::{CommandFactory, Parser};
use clap_complete::engine::CompletionCandidate;
use pdumppb::{
    DeleteConfigRequest, ListConfigsRequest, ReadDumpRequest, ShowConfigRequest, ShowConfigResponse,
    pdump_service_client::PdumpServiceClient,
};
use tokio::{
    signal::{unix, unix::SignalKind},
    task::JoinSet,
};
use tokio_util::sync::CancellationToken;
use tonic::{Status, codec::CompressionEncoding};
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    completion, display,
    errors::{Error, ErrorKind},
    output,
};

use crate::{pdumppb::SetConfigRequest, writer::PdumpWriter};

mod args;
mod dump_mode;
mod printer;
mod writer;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod pdumppb {
    tonic::include_proto!("modules.pdump.controlplane.pdumppb.v1");
}

/// Manages pdump module configs.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    match cmd.mode {
        ModeCmd::List => list_configs(&mut service).await,
        ModeCmd::Show(cmd) => show_config(&mut service, cmd).await,
        ModeCmd::Set(cmd) => set_config(&mut service, cmd).await,
        ModeCmd::Delete(cmd) => delete_config(&mut service, cmd).await,
        ModeCmd::Read(cmd) => read_dump(&mut service, cmd).await,
    }
}

/// The fully-qualified gRPC service name used in error messages.
const SERVICE_NAME: &str = "modules.pdump.controlplane.pdumppb.v1.PdumpService";

fn client(channel: LayeredChannel) -> PdumpServiceClient<LayeredChannel> {
    PdumpServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

type PdumpService = Service<PdumpServiceClient<LayeredChannel>>;

async fn get_config(service: &mut PdumpService, name: &str, action: &'static str) -> Result<ShowConfigResponse, Error> {
    let request = ShowConfigRequest { name: name.to_owned() };
    service
        .unary_with(
            action,
            request,
            service.not_found(action, &format!("config '{name}'")),
            async |client, request| client.show_config(request).await,
        )
        .await
}

async fn list_configs(service: &mut PdumpService) -> Result<(), Error> {
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
                format_args!("No pdump configurations found."),
                format_args!("create one with 'yanet-cli-pdump set --name <name>'"),
            )
        },
    );

    Ok(())
}

async fn show_config(service: &mut PdumpService, cmd: ShowConfigCmd) -> Result<(), Error> {
    let response = get_config(service, &cmd.config_name, "show").await?;

    output::data(
        || &response,
        || {
            let Some(config) = &response.config else {
                output::empty_with_hint(
                    format_args!("No pdump configuration found for '{}'.", cmd.config_name),
                    format_args!("create one with 'yanet-cli-pdump set --name <name>'"),
                );
                return;
            };

            config_block(config).print();
        },
    );

    Ok(())
}

async fn set_config(service: &mut PdumpService, cmd: SetConfigCmd) -> Result<(), Error> {
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
    service
        .unary("set", request, async |client, request| client.set_config(request).await)
        .await?;

    output::success("set", format_args!("Updated config '{}'.", cmd.config_name));

    Ok(())
}

async fn delete_config(service: &mut PdumpService, cmd: DeleteCmd) -> Result<(), Error> {
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

async fn read_dump(service: &mut PdumpService, cmd: ReadCmd) -> Result<(), Error> {
    let cancellation_token = CancellationToken::new();
    let done = cancellation_token.clone();

    let mut reader_set = JoinSet::new();
    let (tx, rx) = tokio::sync::mpsc::channel::<pdumppb::Record>(16);

    log::debug!("request current pdump configuration");
    let config = get_config(service, &cmd.config_name, "read").await?;
    let Some(config) = config.config else {
        return Err(Error::from_status(
            Status::not_found(format!("config '{}' not found", cmd.config_name)),
            "read",
            service.endpoint(),
            SERVICE_NAME,
        ));
    };

    let request = ReadDumpRequest { name: cmd.config_name.clone() };
    log::trace!("read_data request: {request:?}");
    let stream = service
        .client()
        .read_dump(request)
        .await
        .map_err(service.status("read"))?
        .into_inner();
    log::debug!("read_data successfully acquired data stream for {}", cmd.config_name,);

    // Register before the first byte is written, so that a closed output
    // reaches the writer instead of the default SIGPIPE disposition.
    let mut sig_pipe = unix::signal(SignalKind::pipe()).expect("failed to register SIGPIPE handler");

    // Opened once the capture is granted, so that a rejected request
    // leaves an existing file alone.
    let output = cmd.output.clone().unwrap_or_else(|| "-".to_owned());
    let dump_writer = PdumpWriter::new(cmd.dump_format, &output, config.snaplen)
        .map_err(|err| service.invalid("read", format!("cannot write to '{output}': {err}")))?;

    reader_set.spawn(writer::pdump_stream_reader(stream, tx.clone(), done.clone()));
    drop(tx);

    // Spawn outside the reader_set to get unpinable join handler.
    let mut write_jh = tokio::task::spawn_blocking(move || writer::pdump_write(dump_writer, rx, cmd.num));

    let mut finished = None;
    tokio::select! {
        _ = sig_pipe.recv() => {
            log::debug!("the output pipe is closed, stopping the capture");
            cancellation_token.cancel();
        }
        _ = tokio::signal::ctrl_c() => {
            log::debug!("interrupted, stopping the capture");
            cancellation_token.cancel();
        }
        res = &mut write_jh => {
            log::debug!("the writer finished, stopping the capture");
            cancellation_token.cancel();
            finished = Some(res);
        }
    }

    // A capture stopped by a signal leaves the writer running, so wait for
    // it to drain the channel and flush before reporting anything.
    let written = match finished {
        Some(res) => res,
        None => (&mut write_jh).await,
    };

    let mut result = match written {
        Ok(Ok(())) => Ok(()),
        Ok(Err(err)) => Err(write_failed(service, err.to_string())),
        Err(err) => Err(write_failed(service, format!("the writer task failed: {err}"))),
    };

    while let Some(res) = reader_set.join_next().await {
        let failure = match res {
            Ok(Ok(())) => continue,
            Ok(Err(status)) => (service.status("read"))(status),
            Err(err) => write_failed(service, format!("the reader task failed: {err}")),
        };

        if result.is_ok() {
            result = Err(failure);
        } else {
            log::debug!("the capture also failed to read the stream: {}", failure.message());
        }
    }

    result
}

/// Reports a failure of the local capture pipeline, which has no gRPC
/// status of its own.
fn write_failed(service: &PdumpService, message: String) -> Error {
    Error::new(ErrorKind::Rpc, "read", service.endpoint(), message)
}

fn config_block(config: &pdumppb::Config) -> display::KeyValue {
    display::KeyValue::new()
        .row("filter", &config.filter)
        .row("mode", dump_mode::to_str(config.mode))
        .row("snaplen", config.snaplen)
        .row("ring size", config.ring_size)
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

/// Completion candidates for a `--name` argument: the pdump configs the
/// module currently knows.
///
/// Strictly best-effort — see [`completion::candidates`].
fn config_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client.list_configs(ListConfigsRequest {}).await?.into_inner().configs)
    })
}
