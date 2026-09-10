use core::net::{IpAddr, Ipv4Addr};
use std::vec::IntoIter;

use prost::Message;
use tokio::{net::TcpListener, sync::oneshot};
use tokio_stream::wrappers::TcpListenerStream;
use tonic::{Request, Response, Status, Streaming, transport::Server};
use ync::{
    auth::{AuthArgs, AuthMethod},
    client::ConnectionArgs,
};

use super::{operatorpb::*, *};

/// Serves bounded chunks with an independently controlled terminal status.
struct LargeList {
    table: String,
    chunks: Vec<ListNeighboursResponse>,
    fail: bool,
}

#[tonic::async_trait]
impl neighbour_service_server::NeighbourService for LargeList {
    async fn list(&self, _: Request<ListNeighboursRequest>) -> Result<Response<ListNeighboursResponse>, Status> {
        Err(Status::unimplemented("unary listing is not used"))
    }

    type ListStreamStream = tokio_stream::Iter<IntoIter<Result<ListNeighboursResponse, Status>>>;

    async fn list_stream(
        &self,
        request: Request<ListNeighboursRequest>,
    ) -> Result<Response<Self::ListStreamStream>, Status> {
        assert_eq!(self.table, request.into_inner().table);
        let mut chunks: Vec<_> = self.chunks.iter().cloned().map(Ok).collect();
        if self.fail {
            chunks.push(Err(Status::data_loss("incomplete snapshot")));
        }
        Ok(Response::new(tokio_stream::iter(chunks)))
    }

    async fn create_table(
        &self,
        _: Request<CreateNeighbourTableRequest>,
    ) -> Result<Response<CreateNeighbourTableResponse>, Status> {
        Err(Status::unimplemented("unused operation"))
    }

    async fn update_table(
        &self,
        _: Request<UpdateNeighbourTableRequest>,
    ) -> Result<Response<UpdateNeighbourTableResponse>, Status> {
        Err(Status::unimplemented("unused operation"))
    }

    async fn remove_table(
        &self,
        _: Request<RemoveNeighbourTableRequest>,
    ) -> Result<Response<RemoveNeighbourTableResponse>, Status> {
        Err(Status::unimplemented("unused operation"))
    }

    async fn list_tables(
        &self,
        _: Request<ListNeighbourTablesRequest>,
    ) -> Result<Response<ListNeighbourTablesResponse>, Status> {
        Err(Status::unimplemented("unused operation"))
    }

    async fn update_neighbours(
        &self,
        _: Request<UpdateNeighboursRequest>,
    ) -> Result<Response<UpdateNeighboursResponse>, Status> {
        Err(Status::unimplemented("unused operation"))
    }

    async fn remove_neighbours(
        &self,
        _: Request<RemoveNeighboursRequest>,
    ) -> Result<Response<RemoveNeighboursResponse>, Status> {
        Err(Status::unimplemented("unused operation"))
    }

    async fn replace_neighbours(
        &self,
        _: Request<Streaming<ReplaceNeighboursRequest>>,
    ) -> Result<Response<ReplaceNeighboursResponse>, Status> {
        Err(Status::unimplemented("unused operation"))
    }
}

#[tokio::test]
async fn test_official_client_lists_complete_merged_and_named_streams() {
    for table in [None, Some("netlink-dataplane-large")] {
        run_list_stream(table, false).await;
    }
}

#[tokio::test]
async fn test_official_client_rejects_data_followed_by_a_stream_error() {
    run_list_stream(None, true).await;
}

/// Drives the production collector over TCP without enabling unary listing.
async fn run_list_stream(table: Option<&str>, fail: bool) {
    let entries = (0..20_000)
        .map(|index| ProtoNeighbourEntry {
            next_hop: Some(IpAddress::from(IpAddr::V4(Ipv4Addr::from(0xc000_0000_u32 + index)))),
            link_addr: Some(MacAddress::from("02:00:00:00:00:01".parse::<MacAddr>().unwrap())),
            hardware_addr: Some(MacAddress::from("02:00:00:00:00:02".parse::<MacAddr>().unwrap())),
            state: NeighbourState::NudPermanent.into(),
            updated_at: 1_700_000_000,
            source: "s".repeat(128),
            priority: 100,
            device: "d".repeat(79),
            ifindex: index + 1,
        })
        .collect();
    let response = ListNeighboursResponse { neighbours: entries };
    assert!(response.encoded_len() > 4 * 1024 * 1024);
    let chunks: Vec<_> = response
        .neighbours
        .chunks(500)
        .map(|entries| {
            let chunk = ListNeighboursResponse { neighbours: entries.to_vec() };
            assert!(chunk.encoded_len() <= 256 * 1024);
            chunk
        })
        .collect();
    let server_table = table.unwrap_or_default().to_owned();
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let (stop, stopped) = oneshot::channel();
    let server = tokio::spawn(async move {
        Server::builder()
            .add_service(
                neighbour_service_server::NeighbourServiceServer::new(LargeList { table: server_table, chunks, fail })
                    .accept_compressed(CompressionEncoding::Gzip),
            )
            .serve_with_incoming_shutdown(TcpListenerStream::new(listener), async {
                let _ = stopped.await;
            })
            .await
            .unwrap();
    });
    let connection = ConnectionArgs {
        endpoint: Some(format!("grpc://{address}")),
        auth: AuthArgs {
            auth: Some(AuthMethod::None),
            cert_tag: None,
        },
        tls: Default::default(),
        timeout: Some(Duration::from_secs(5)),
    };
    let mut service = Service::connect_for(&connection, "show", SERVICE_NAME, client)
        .await
        .unwrap();
    let result = list_neighbours(&mut service, table).await;
    if fail {
        assert!(result.is_err());
    } else {
        let received = result.unwrap();
        assert_eq!(20_000, received.neighbours.len());
        for (index, entry) in received.neighbours.iter().enumerate() {
            assert_eq!(index as u32 + 1, entry.ifindex);
            assert_eq!(
                Some(IpAddress::from(IpAddr::V4(Ipv4Addr::from(
                    0xc000_0000_u32 + index as u32
                )))),
                entry.next_hop
            );
        }
    }
    stop.send(()).unwrap();
    server.await.unwrap();
}
