use core::net::{IpAddr, Ipv4Addr};

use prost::Message;
use tokio::{net::TcpListener, sync::oneshot};
use tokio_stream::wrappers::TcpListenerStream;
use tonic::{Request, Response, Status, Streaming, transport::Server};
use ync::auth::{AuthArgs, AuthMethod};

use super::{operatorpb::*, *};

#[test]
fn test_neighbour_wire_json_preserves_identity_and_named_state() {
    let entry = ProtoNeighbourEntry {
        next_hop: Some(IpAddress::from("192.0.2.1".parse::<IpAddr>().unwrap())),
        link_addr: Some(MacAddress::from("02:00:00:00:00:01".parse::<MacAddr>().unwrap())),
        hardware_addr: Some(MacAddress::from("02:00:00:00:00:02".parse::<MacAddr>().unwrap())),
        state: NeighbourState::NudStale.into(),
        updated_at: 123,
        source: "static".to_owned(),
        priority: 100,
        device: "kni1".to_owned(),
        ifindex: 17,
    };
    let value = serde_json::to_value(entry).unwrap();
    assert_eq!("192.0.2.1", value["next_hop"]);
    assert_eq!("02:00:00:00:00:01", value["link_addr"]);
    assert_eq!("02:00:00:00:00:02", value["hardware_addr"]);
    assert_eq!("STALE", value["state"]);
    assert_eq!(123, value["updated_at"]);
    assert_eq!("static", value["source"]);
    assert_eq!(100, value["priority"]);
    assert_eq!("kni1", value["device"]);
    assert_eq!(17, value["ifindex"]);
}

/// Serves a complete large table through a real unary transport.
struct LargeList {
    response: ListNeighboursResponse,
}

#[tonic::async_trait]
impl neighbour_service_server::NeighbourService for LargeList {
    async fn list(&self, request: Request<ListNeighboursRequest>) -> Result<Response<ListNeighboursResponse>, Status> {
        assert_eq!("netlink-dataplane-large", request.into_inner().table);
        Ok(Response::new(self.response.clone()))
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
async fn test_official_client_lists_response_larger_than_default_decode_limit() {
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
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let (stop, stopped) = oneshot::channel();
    let server = tokio::spawn(async move {
        Server::builder()
            .add_service(
                neighbour_service_server::NeighbourServiceServer::new(LargeList { response })
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
    let mut service = NeighbourService::new(&connection, "show").await.unwrap();
    let received = service
        .service
        .client()
        .list(ListNeighboursRequest {
            table: "netlink-dataplane-large".to_owned(),
        })
        .await
        .unwrap()
        .into_inner();
    assert_eq!(20_000, received.neighbours.len());
    assert_eq!(1, received.neighbours[0].ifindex);
    assert_eq!(20_000, received.neighbours[19_999].ifindex);
    stop.send(()).unwrap();
    server.await.unwrap();
}
