use yanet_cp_sys::{
    DeviceBlock,
    fixture::{Borrowed, FakeShm},
};

use super::{Error, VNI_MAX, VxlanConfig, c_name, device_layout, validate_device, validate_tunnel};

fn tunnel() -> VxlanConfig {
    VxlanConfig {
        local_mac: [0x02, 0, 0, 0, 0, 0x01],
        remote_mac: [0x02, 0, 0, 0, 0, 0x02],
        local_ip: [192, 0, 2, 1],
        remote_ip: [198, 51, 100, 7],
        vni: 0x1234,
    }
}

/// A fixture device holding the given body, as the api builds it before
/// publish, at the returned offset of the fixture.
fn built_device(shm: &FakeShm, body: VxlanConfig) -> (usize, Borrowed<'_, DeviceBlock<'_>>) {
    let item = device_layout(&FakeShm::layout());
    let (offset, mut block) = shm.device(item.size);
    block.write(item.body_offset, body).expect("body fits");
    (offset, block)
}

#[test]
fn test_validate_tunnel_accepts_unicast_endpoints() {
    assert_eq!(Ok(()), validate_tunnel(&tunnel()));
}

#[test]
fn test_validate_tunnel_rejects_wide_vni() {
    let mut tunnel = tunnel();
    tunnel.vni = VNI_MAX + 1;

    assert!(matches!(validate_tunnel(&tunnel), Err(Error::InvalidArgument(_))));
}

#[test]
fn test_validate_tunnel_accepts_largest_vni() {
    let mut tunnel = tunnel();
    tunnel.vni = VNI_MAX;

    assert_eq!(Ok(()), validate_tunnel(&tunnel));
}

#[test]
fn test_validate_tunnel_rejects_unspecified_local_ip() {
    let mut tunnel = tunnel();
    tunnel.local_ip = [0; 4];

    assert!(matches!(validate_tunnel(&tunnel), Err(Error::InvalidArgument(_))));
}

#[test]
fn test_validate_tunnel_rejects_multicast_remote_ip() {
    let mut tunnel = tunnel();
    tunnel.remote_ip = [239, 1, 1, 1];

    assert!(matches!(validate_tunnel(&tunnel), Err(Error::InvalidArgument(_))));
}

#[test]
fn test_validate_tunnel_rejects_broadcast_remote_ip() {
    let mut tunnel = tunnel();
    tunnel.remote_ip = [255; 4];

    assert!(matches!(validate_tunnel(&tunnel), Err(Error::InvalidArgument(_))));
}

#[test]
fn test_validate_tunnel_rejects_multicast_mac() {
    let mut tunnel = tunnel();
    tunnel.remote_mac = [0x01, 0, 0x5e, 0, 0, 1];

    assert!(matches!(validate_tunnel(&tunnel), Err(Error::InvalidArgument(_))));
}

#[test]
fn test_validate_tunnel_rejects_zero_mac() {
    let mut tunnel = tunnel();
    tunnel.local_mac = [0; 6];

    assert!(matches!(validate_tunnel(&tunnel), Err(Error::InvalidArgument(_))));
}

#[test]
fn test_c_name_rejects_empty_long_and_nul() {
    assert!(c_name("name", "", 80).is_err());
    assert!(c_name("name", &"x".repeat(80), 80).is_err());
    assert!(c_name("name", "a\0b", 80).is_err());
    assert!(c_name("name", &"x".repeat(79), 80).is_ok());
}

#[test]
fn test_validate_device_accepts_built_device() {
    let shm = FakeShm::new();
    let (_, block) = built_device(&shm, tunnel());

    assert_eq!(Ok(tunnel()), validate_device(&shm.agent(), &block));
}

#[test]
fn test_validate_device_rejects_edge_outside_arenas() {
    let shm = FakeShm::new();
    let (offset, block) = built_device(&shm, tunnel());
    shm.write_u64(offset + 8, 1 << 40);

    assert!(matches!(validate_device(&shm.agent(), &block), Err(Error::Failed(_))));
}

#[test]
fn test_validate_device_rejects_missing_entry() {
    let shm = FakeShm::new();
    let (offset, block) = built_device(&shm, tunnel());
    shm.write_u64(offset + 16, 0);

    assert!(matches!(validate_device(&shm.agent(), &block), Err(Error::Failed(_))));
}

#[test]
fn test_validate_device_rejects_foreign_owner() {
    let shm = FakeShm::new();
    let (offset, block) = built_device(&shm, tunnel());
    shm.write_u64(offset, 64);

    assert!(matches!(validate_device(&shm.agent(), &block), Err(Error::Failed(_))));
}

#[test]
fn test_validate_device_rejects_out_of_range_body() {
    let mut body = tunnel();
    body.vni = VNI_MAX + 1;
    let shm = FakeShm::new();
    let (_, block) = built_device(&shm, body);

    assert!(matches!(
        validate_device(&shm.agent(), &block),
        Err(Error::InvalidArgument(_))
    ));
}
