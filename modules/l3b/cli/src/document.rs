use core::{ffi::CStr, fmt, marker::PhantomData, mem::MaybeUninit, slice, str};

use serde::{
    Deserializer,
    de::{self, DeserializeOwned, Visitor},
};
use serde_yaml::Value;
use unsafe_libyaml as unsafe_yaml;

/// Checks syntax that typed deserialization would normalize or discard.
///
/// Use the same YAML engine as Serde, before aliases can be expanded or integer
/// spellings lost. Typed decoding remains responsible for fields and domains.
pub fn validate(content: &str) -> Result<(), String> {
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
            loop {
                let mut event = MaybeUninit::<unsafe_yaml::yaml_event_t>::uninit();
                if unsafe_yaml::yaml_parser_parse(parser, event.as_mut_ptr()).fail {
                    let parser = &*parser;
                    let problem = if parser.problem.is_null() {
                        "invalid YAML".into()
                    } else {
                        CStr::from_ptr(parser.problem).to_string_lossy()
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
                if event.type_ == unsafe_yaml::YAML_SCALAR_EVENT {
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
