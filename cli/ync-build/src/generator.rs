//! The client generator: tonic-compatible clients over the shared runtime.

use core::fmt::{self, Write};

use prost_build::{Method, Service, ServiceGenerator};

/// Writes, per service, a client whose methods call the CLI gRPC runtime at
/// `runtime` and a `<service>_server` module holding the service name.
///
/// The client keeps tonic's shape (`new`, the compression and size settings,
/// one method per RPC), so callers stay unchanged, but each method only names
/// its messages: the call itself is compiled once, in the runtime. Methods
/// streaming requests are not generated, since no CLI calls one.
pub struct ClientGenerator {
    runtime: &'static str,
}

impl ClientGenerator {
    pub fn new(runtime: &'static str) -> Self {
        Self { runtime }
    }

    fn write(&self, service: &Service, buf: &mut String) -> Result<(), fmt::Error> {
        let Self { runtime } = self;
        let module = naive_snake_case(&service.name);
        let client = format!("{}Client", service.name);
        let name = match service.package.as_str() {
            "" => service.proto_name.clone(),
            package => format!("{package}.{}", service.proto_name),
        };

        writeln!(buf, "/// Generated client implementations.")?;
        writeln!(buf, "pub mod {module}_client {{")?;
        writeln!(buf, "#![allow(dead_code)]")?;
        write_doc(buf, &service.comments.leading)?;
        writeln!(buf, "#[derive(Clone)]")?;
        writeln!(buf, "pub struct {client}<T = {runtime}::Channel> {{")?;
        writeln!(buf, "    inner: {runtime}::Client,")?;
        writeln!(buf, "    channel: ::core::marker::PhantomData<fn() -> T>,")?;
        writeln!(buf, "}}")?;
        writeln!(buf, "impl {client}<{runtime}::Channel> {{")?;
        writeln!(buf, "    pub fn new(channel: {runtime}::Channel) -> Self {{")?;
        writeln!(buf, "        Self {{")?;
        writeln!(buf, "            inner: {runtime}::Client::new(channel),")?;
        writeln!(buf, "            channel: ::core::marker::PhantomData,")?;
        writeln!(buf, "        }}")?;
        writeln!(buf, "    }}")?;
        for (setting, value, kind) in [
            ("send_compressed", "encoding", "::tonic::codec::CompressionEncoding"),
            ("accept_compressed", "encoding", "::tonic::codec::CompressionEncoding"),
            ("max_decoding_message_size", "limit", "usize"),
            ("max_encoding_message_size", "limit", "usize"),
        ] {
            writeln!(buf, "    #[must_use]")?;
            writeln!(buf, "    pub fn {setting}(mut self, {value}: {kind}) -> Self {{")?;
            writeln!(buf, "        self.inner = self.inner.{setting}({value});")?;
            writeln!(buf, "        self")?;
            writeln!(buf, "    }}")?;
        }
        for method in &service.methods {
            self.write_method(&name, method, buf)?;
        }
        writeln!(buf, "}}")?;
        writeln!(buf, "}}")?;

        writeln!(buf, "/// The service name of the generated client.")?;
        writeln!(buf, "pub mod {module}_server {{")?;
        writeln!(buf, "#![allow(dead_code)]")?;
        writeln!(buf, "pub const SERVICE_NAME: &str = {name:?};")?;
        writeln!(buf, "}}")
    }

    fn write_method(&self, service: &str, method: &Method, buf: &mut String) -> Result<(), fmt::Error> {
        let Self { runtime } = self;
        let request = message_type(&method.input_type);
        let response = message_type(&method.output_type);
        let (call, output) = match (method.client_streaming, method.server_streaming) {
            (false, false) => ("unary", response),
            (false, true) => ("server_streaming", format!("{runtime}::Streaming<{response}>")),
            (true, _) => return Ok(()),
        };
        let proto = &method.proto_name;

        write_doc(buf, &method.comments.leading)?;
        writeln!(buf, "    pub fn {}(", method.name)?;
        writeln!(buf, "        &mut self,")?;
        writeln!(buf, "        request: impl ::tonic::IntoRequest<{request}>,")?;
        writeln!(
            buf,
            "    ) -> impl ::core::future::Future<Output = ::core::result::Result<::tonic::Response<{output}>, ::tonic::Status>> + Send + '_ {{"
        )?;
        writeln!(
            buf,
            "        const METHOD: {runtime}::Method = {runtime}::Method::new({service:?}, {proto:?}, \"/{service}/{proto}\");"
        )?;
        writeln!(buf, "        self.inner.{call}(request.into_request(), &METHOD)")?;
        writeln!(buf, "    }}")
    }
}

impl ServiceGenerator for ClientGenerator {
    fn generate(&mut self, service: Service, buf: &mut String) {
        self.write(&service, buf).expect("writing to a String cannot fail");
    }
}

/// The Rust type of a method's message as seen from inside the client
/// module, following tonic's mapping.
fn message_type(rust_type: &str) -> String {
    match rust_type {
        ".google.protobuf.Empty" => "()".to_owned(),
        ".google.protobuf.Any" => "::prost_types::Any".to_owned(),
        ".google.protobuf.StringValue" => "::prost::alloc::string::String".to_owned(),
        _ => match rust_type.strip_prefix(".google.protobuf.") {
            Some(name) => format!("::prost_types::{name}"),
            None if rust_type == "()" || rust_type.starts_with("::") || rust_type.starts_with("crate::") => {
                rust_type.to_owned()
            }
            None => format!("super::{}", rust_type.trim_start_matches("::")),
        },
    }
}

fn write_doc(buf: &mut String, lines: &[String]) -> Result<(), fmt::Error> {
    for line in lines {
        let line = if line.starts_with(' ') {
            line.clone()
        } else {
            format!(" {line}")
        };

        writeln!(buf, "#[doc = {line:?}]")?;
    }

    Ok(())
}

/// The module name tonic derives from a service name: `RouteService` becomes
/// `route_service`.
fn naive_snake_case(name: &str) -> String {
    let mut snake = String::new();
    let mut chars = name.chars().peekable();

    while let Some(c) = chars.next() {
        snake.push(c.to_ascii_lowercase());
        if chars.peek().is_some_and(|next| next.is_uppercase()) {
            snake.push('_');
        }
    }

    snake
}
