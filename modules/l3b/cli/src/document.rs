use core::{ffi::CStr, fmt, marker::PhantomData, mem::MaybeUninit, slice, str};
use std::collections::HashSet;

use serde::{
    Deserializer,
    de::{self, DeserializeOwned, Visitor},
};
use serde_yaml::Value;
use unsafe_libyaml as unsafe_yaml;

#[derive(Clone, Eq, Hash, PartialEq)]
enum Component {
    Key(String),
    Index(usize),
}

struct Frame {
    path_len: usize,
    keys: Option<HashSet<Value>>,
    key: Option<String>,
    index: usize,
}

/// Decodes one document with bounded, value-free unknown-field diagnostics.
pub fn parse<'de, T: serde::Deserialize<'de>>(content: &'de str, strict: bool) -> Result<(T, Vec<String>), String> {
    validate(content, None)?;
    let mut ignored = Vec::new();
    let document = serde_ignored::deserialize(serde_yaml::Deserializer::from_str(content), |path| {
        let mut components = Vec::new();
        let mut path = &path;
        loop {
            use serde_ignored::Path;
            match path {
                Path::Root => break,
                Path::Map { parent, key } => {
                    components.push(Component::Key(key.clone()));
                    path = parent;
                }
                Path::Seq { parent, index } => {
                    components.push(Component::Index(*index));
                    path = parent;
                }
                Path::Some { parent } | Path::NewtypeStruct { parent } | Path::NewtypeVariant { parent } => {
                    path = parent;
                }
            }
        }
        components.reverse();
        ignored.push(components);
    })
    .map_err(|err| err.to_string())?;

    // Unknown subtrees have no numeric schema, but still passed syntax checks.
    let ignored_paths: HashSet<_> = ignored.iter().map(Vec::as_slice).collect();
    validate(content, Some(&ignored_paths))?;
    if strict && let Some(path) = ignored.first() {
        return Err(format!("unknown field {}", field_path(path)));
    }
    let mut warnings: Vec<_> = ignored
        .iter()
        .take(16)
        .map(|path| format!("ignoring unknown field {}", field_path(path)))
        .collect();
    if ignored.len() > 16 {
        warnings.push(format!("{} additional unknown fields ignored", ignored.len() - 16));
    }
    Ok((document, warnings))
}

fn field_path(path: &[Component]) -> String {
    let mut result = String::new();
    for component in path {
        let part = match component {
            Component::Index(index) => format!("[{index}]"),
            Component::Key(key)
                if !key.is_empty() && key.bytes().all(|byte| byte.is_ascii_alphanumeric() || byte == b'_') =>
            {
                format!("{}{key}", if result.is_empty() { "" } else { "." })
            }
            Component::Key(key) => format!("[{key:?}]"),
        };
        for character in part.chars().flat_map(char::escape_default) {
            if result.len() == 256 {
                result.push_str("...");
                return result;
            }
            result.push(character);
        }
    }
    result
}

/// Checks syntax that typed deserialization would normalize or discard.
///
/// Use the same YAML engine as Serde, before aliases can be expanded or integer
/// spellings lost. Typed decoding remains responsible for fields and domains.
fn validate(content: &str, ignored: Option<&HashSet<&[Component]>>) -> Result<(), String> {
    // The parser has internal pointers and must stay at its initialized
    // address.
    let mut storage = Box::<unsafe_yaml::yaml_parser_t>::new_uninit();
    let parser = storage.as_mut_ptr();
    // The input outlives the parser; event-owned pointers are used only before
    // their event is deleted, including on validation failures.
    unsafe {
        if unsafe_yaml::yaml_parser_initialize(parser).fail {
            return Err("cannot initialize YAML parser".to_owned());
        }
        unsafe_yaml::yaml_parser_set_input_string(parser, content.as_ptr(), content.len() as u64);
        let result = (|| {
            let mut document_seen = false;
            let mut stack: Vec<Frame> = Vec::new();
            let mut path = Vec::new();
            loop {
                let mut event = MaybeUninit::<unsafe_yaml::yaml_event_t>::uninit();
                if unsafe_yaml::yaml_parser_parse(parser, event.as_mut_ptr()).fail {
                    let parser = &*parser;
                    let problem = if parser.problem.is_null() {
                        "invalid YAML".into()
                    } else {
                        CStr::from_ptr(parser.problem.cast()).to_string_lossy()
                    };
                    let mark = parser.problem_mark;
                    return Err(format!(
                        "{problem} at line {} column {}",
                        mark.line + 1,
                        mark.column + 1
                    ));
                }
                let mut event = event.assume_init();
                let mark = event.start_mark;
                let end = event.type_ == unsafe_yaml::YAML_STREAM_END_EVENT;
                let mut error = None;
                let tag = match event.type_ {
                    unsafe_yaml::YAML_DOCUMENT_START_EVENT => {
                        if document_seen {
                            error = Some("expected exactly one YAML document");
                        }
                        document_seen = true;
                        core::ptr::null()
                    }
                    unsafe_yaml::YAML_ALIAS_EVENT => {
                        error = Some("YAML aliases are not supported");
                        core::ptr::null()
                    }
                    unsafe_yaml::YAML_SCALAR_EVENT => event.data.scalar.tag,
                    unsafe_yaml::YAML_MAPPING_START_EVENT => event.data.mapping_start.tag,
                    unsafe_yaml::YAML_SEQUENCE_START_EVENT => event.data.sequence_start.tag,
                    _ => core::ptr::null(),
                };
                let tag = (!tag.is_null()).then(|| CStr::from_ptr(tag.cast()).to_bytes());
                if let Some(tag) = tag {
                    let supported = match event.type_ {
                        unsafe_yaml::YAML_MAPPING_START_EVENT => tag == b"tag:yaml.org,2002:map",
                        unsafe_yaml::YAML_SEQUENCE_START_EVENT => tag == b"tag:yaml.org,2002:seq",
                        _ => matches!(
                            tag,
                            b"!" | b"tag:yaml.org,2002:str"
                                | b"tag:yaml.org,2002:int"
                                | b"tag:yaml.org,2002:float"
                                | b"tag:yaml.org,2002:bool"
                                | b"tag:yaml.org,2002:null"
                        ),
                    };
                    if !supported {
                        error = Some("custom or mismatched YAML tags are not supported");
                    }
                }
                let mut is_key = false;
                match event.type_ {
                    unsafe_yaml::YAML_MAPPING_END_EVENT | unsafe_yaml::YAML_SEQUENCE_END_EVENT => {
                        stack.pop();
                    }
                    unsafe_yaml::YAML_SCALAR_EVENT
                    | unsafe_yaml::YAML_MAPPING_START_EVENT
                    | unsafe_yaml::YAML_SEQUENCE_START_EVENT => {
                        if let Some(frame) = stack.last_mut() {
                            path.truncate(frame.path_len);
                            if let Some(keys) = &mut frame.keys {
                                if let Some(key) = frame.key.take() {
                                    path.push(Component::Key(key));
                                } else {
                                    is_key = true;
                                    if event.type_ != unsafe_yaml::YAML_SCALAR_EVENT {
                                        error = Some("mapping keys must be scalars");
                                    } else {
                                        let scalar = event.data.scalar;
                                        let key = String::from_utf8_lossy(slice::from_raw_parts(
                                            scalar.value,
                                            scalar.length as usize,
                                        ))
                                        .into_owned();
                                        let identity = match tag {
                                            Some(b"tag:yaml.org,2002:str") => Value::String(key.clone()),
                                            None | Some(b"!")
                                                if scalar.style != unsafe_yaml::YAML_PLAIN_SCALAR_STYLE =>
                                            {
                                                Value::String(key.clone())
                                            }
                                            None | Some(b"!") => match key.as_str() {
                                                "" | "~" | "null" | "Null" | "NULL" => Value::Null,
                                                "true" | "True" | "TRUE" => Value::Bool(true),
                                                "false" | "False" | "FALSE" => Value::Bool(false),
                                                _ => serde_yaml::from_str::<serde_yaml::Number>(&key)
                                                    .map(Value::Number)
                                                    .unwrap_or_else(|_| Value::String(key.clone())),
                                            },
                                            Some(tag) => {
                                                let quoted = serde_yaml::to_string(&key)
                                                    .expect("string serialization is infallible");
                                                serde_yaml::from_str::<Value>(&format!(
                                                    "!<{}> {quoted}",
                                                    String::from_utf8_lossy(tag)
                                                ))
                                                .unwrap_or_else(|_| Value::String(key.clone()))
                                            }
                                        };
                                        if key == "<<"
                                            && scalar.style == unsafe_yaml::YAML_PLAIN_SCALAR_STYLE
                                            && tag.is_none()
                                        {
                                            error = Some("YAML merge keys are not supported");
                                        } else if !keys.insert(identity) {
                                            error = Some("duplicate mapping key");
                                        }
                                        frame.key = Some(key);
                                    }
                                }
                            } else {
                                path.push(Component::Index(frame.index));
                                frame.index += 1;
                            }
                        }
                        if matches!(
                            event.type_,
                            unsafe_yaml::YAML_MAPPING_START_EVENT | unsafe_yaml::YAML_SEQUENCE_START_EVENT
                        ) {
                            stack.push(Frame {
                                path_len: path.len(),
                                keys: (event.type_ == unsafe_yaml::YAML_MAPPING_START_EVENT).then(HashSet::new),
                                key: None,
                                index: 0,
                            });
                        }
                    }
                    _ => {}
                }
                if event.type_ == unsafe_yaml::YAML_SCALAR_EVENT
                    && !is_key
                    && ignored.is_some_and(|ignored| !(0..=path.len()).any(|length| ignored.contains(&path[..length])))
                {
                    let scalar = event.data.scalar;
                    let value = slice::from_raw_parts(scalar.value, scalar.length as usize);
                    let plain = scalar.style == unsafe_yaml::YAML_PLAIN_SCALAR_STYLE;
                    let integer_tag = tag == Some(b"tag:yaml.org,2002:int".as_slice());
                    let implicit_integer = plain
                        && (tag.is_none() || tag == Some(b"!".as_slice()))
                        && str::from_utf8(value)
                            .ok()
                            .and_then(|value| serde_yaml::from_str::<serde_yaml::Number>(value).ok())
                            .is_some_and(|number| number.is_i64() || number.is_u64());
                    if (integer_tag || implicit_integer)
                        && (!plain || value.is_empty() || !value.iter().all(u8::is_ascii_digit))
                    {
                        error = Some("integers require nonnegative decimal scalars");
                    }
                }
                unsafe_yaml::yaml_event_delete(&mut event);
                if let Some(error) = error {
                    return Err(format!("{error} at line {} column {}", mark.line + 1, mark.column + 1));
                }
                if end {
                    return Ok(());
                }
            }
        })();
        unsafe_yaml::yaml_parser_delete(parser);
        result
    }
}

/// Resolves YAML scalar types before decoding, preventing implicit coercions.
pub fn scalar<'de, D, T>(deserializer: D) -> Result<T, D::Error>
where
    D: Deserializer<'de>,
    T: DeserializeOwned,
{
    struct Scalar<T>(PhantomData<T>);

    impl<'de, T: DeserializeOwned> Visitor<'de> for Scalar<T> {
        type Value = T;

        fn expecting(&self, formatter: &mut fmt::Formatter) -> Result<(), fmt::Error> {
            formatter.write_str(core::any::type_name::<T>())
        }

        fn visit_u64<E: de::Error>(self, value: u64) -> Result<T, E> {
            T::deserialize(Value::from(value)).map_err(E::custom)
        }

        fn visit_i64<E: de::Error>(self, value: i64) -> Result<T, E> {
            T::deserialize(Value::from(value)).map_err(E::custom)
        }

        fn visit_f64<E: de::Error>(self, value: f64) -> Result<T, E> {
            T::deserialize(Value::from(value)).map_err(E::custom)
        }

        fn visit_bool<E: de::Error>(self, value: bool) -> Result<T, E> {
            T::deserialize(Value::from(value)).map_err(E::custom)
        }

        fn visit_str<E: de::Error>(self, value: &str) -> Result<T, E> {
            T::deserialize(Value::from(value)).map_err(E::custom)
        }

        fn visit_unit<E: de::Error>(self) -> Result<T, E> {
            T::deserialize(Value::Null).map_err(E::custom)
        }
    }

    deserializer.deserialize_any(Scalar(PhantomData))
}
