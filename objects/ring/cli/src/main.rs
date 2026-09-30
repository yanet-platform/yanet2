use std::borrow::Cow;

use args::{CreateCmd, DeleteCmd, ModeCmd, ShowCmd};
use bytesize::ByteSize;
use clap::{CommandFactory, Parser};
use clap_complete::engine::CompletionCandidate;
use ringpb::{
    CreateRingRequest, DeleteRingRequest, ListRingsRequest, RingInfo, ShowRingRequest,
    ring_service_client::RingServiceClient, ring_service_server::SERVICE_NAME,
};
use tabled::Tabled;
use tonic::codec::CompressionEncoding;
use ync::{
    GlobalArgs,
    client::{LayeredChannel, Service},
    completion, display,
    errors::{Error, ErrorKind},
    output,
};

mod args;

#[allow(clippy::std_instead_of_core, non_snake_case)]
pub mod ringpb {
    tonic::include_proto!("objects.ring.controlplane.ringpb.v1");
}

fn client(channel: LayeredChannel) -> RingServiceClient<LayeredChannel> {
    RingServiceClient::new(channel)
        .send_compressed(CompressionEncoding::Gzip)
        .accept_compressed(CompressionEncoding::Gzip)
}

/// Manages standalone ring objects that module configs can link by name.
#[derive(Debug, Clone, Parser)]
#[command(version = ync::version(), about)]
#[command(flatten_help = true)]
pub struct Cmd {
    #[clap(subcommand)]
    pub mode: ModeCmd,
    #[command(flatten)]
    pub globals: GlobalArgs,
}

type RingCliService = Service<RingServiceClient<LayeredChannel>>;

impl Tabled for RingInfo {
    const LENGTH: usize = 3;

    fn fields(&self) -> Vec<Cow<'_, str>> {
        vec![
            Cow::Borrowed(self.name.as_str()),
            Cow::Owned(ByteSize::b(self.capacity).to_string()),
            Cow::Owned(self.publish_batch.to_string()),
        ]
    }

    fn headers() -> Vec<Cow<'static, str>> {
        vec![
            Cow::Borrowed("NAME"),
            Cow::Borrowed("CAPACITY"),
            Cow::Borrowed("PUBLISH BATCH"),
        ]
    }
}

async fn ring_create(service: &mut RingCliService, cmd: CreateCmd) -> Result<(), Error> {
    let request = CreateRingRequest {
        name: cmd.name.clone(),
        capacity: cmd.capacity,
        publish_batch: cmd.publish_batch.unwrap_or(0),
    };

    service
        .unary("create", request, async |client, request| {
            client.create_ring(request).await
        })
        .await?;

    output::success("create", format_args!("Created ring '{}'.", cmd.name));

    Ok(())
}

async fn ring_list(service: &mut RingCliService) -> Result<(), Error> {
    let response = service
        .unary("list", ListRingsRequest {}, async |client, request| {
            client.list_rings(request).await
        })
        .await?;

    output::data(
        || &response.rings,
        || {
            if response.rings.is_empty() {
                output::empty_with_hint(
                    format_args!("No ring objects found."),
                    format_args!("create one with 'yanet-cli-ring create --name <name> --capacity <size>'"),
                );
                return;
            }

            let mut rows = response.rings.clone();
            rows.sort_by(|a, b| a.name.cmp(&b.name));

            display::print_table_from_entries(rows);
        },
    );

    Ok(())
}

async fn ring_show(service: &mut RingCliService, cmd: ShowCmd) -> Result<(), Error> {
    let request = ShowRingRequest { name: cmd.name.clone() };

    let response = service
        .unary_with(
            "show",
            request,
            service.not_found("show", &format!("ring '{}'", cmd.name)),
            async |client, request| client.show_ring(request).await,
        )
        .await?;

    let Some(ring) = &response.ring else {
        return Err(Error::new(
            ErrorKind::Rpc,
            "show",
            service.endpoint(),
            format!("the service answered without ring '{}'", cmd.name),
        ));
    };

    output::data(
        || &response,
        || {
            display::KeyValue::new()
                .row("name", &ring.name)
                .row("capacity", ByteSize::b(ring.capacity))
                .row("publish batch", ring.publish_batch)
                .print();
        },
    );

    Ok(())
}

async fn ring_delete(service: &mut RingCliService, cmd: DeleteCmd) -> Result<(), Error> {
    let request = DeleteRingRequest { name: cmd.name.clone() };

    service
        .unary_with(
            "delete",
            request,
            service.not_found("delete", &format!("ring '{}'", cmd.name)),
            async |client, request| client.delete_ring(request).await,
        )
        .await?;

    output::success("delete", format_args!("Deleted ring '{}'.", cmd.name));

    Ok(())
}

async fn run(cmd: Cmd) -> Result<(), Error> {
    let action = cmd.mode.action();
    let mut service = Service::connect_for(&cmd.globals.connection, action, SERVICE_NAME, client).await?;

    match cmd.mode {
        ModeCmd::Create(args) => ring_create(&mut service, args).await,
        ModeCmd::List => ring_list(&mut service).await,
        ModeCmd::Show(args) => ring_show(&mut service, args).await,
        ModeCmd::Delete(args) => ring_delete(&mut service, args).await,
    }
}

fn main() -> std::process::ExitCode {
    ync::entrypoint(|cmd: &Cmd| cmd.globals.options(), run)
}

fn ring_candidates() -> Vec<CompletionCandidate> {
    completion::candidates(Cmd::command, client, async move |mut client| {
        Ok(client
            .list_rings(ListRingsRequest {})
            .await?
            .into_inner()
            .rings
            .into_iter()
            .map(|ring| ring.name)
            .collect())
    })
}
