use crate::pb::Device;

impl From<String> for Device {
    fn from(name: String) -> Self {
        Self { name }
    }
}

#[cfg(test)]
mod test {
    use super::*;

    #[test]
    fn device_object_roundtrip() {
        let device: Device = serde_json::from_str(r#"{"name":"eth0"}"#).expect("Device parse must not fail");
        assert_eq!("eth0", device.name);
        let serialized = serde_json::to_string(&device).expect("Device serialization must not fail");
        assert_eq!(r#"{"name":"eth0"}"#, serialized);
    }

    #[test]
    fn device_empty_name_object_roundtrip() {
        let device: Device = serde_json::from_str(r#"{"name":""}"#).expect("Device parse must not fail");
        assert_eq!("", device.name);
        let serialized = serde_json::to_string(&device).expect("Device serialization must not fail");
        assert_eq!(r#"{"name":""}"#, serialized);
    }
}
