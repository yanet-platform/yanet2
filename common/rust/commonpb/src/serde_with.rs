//! Serializers a proto crate attaches to its generated messages with
//! `serialize_with`.

use prost_types::{Duration, Timestamp};
use serde::{Deserialize, Deserializer, Serialize, Serializer, de};

#[derive(Serialize)]
struct SecondsNanos {
    seconds: i64,
    nanos: i32,
}

/// Serializes an optional timestamp as `{"seconds": i64, "nanos": i32}`,
/// or null when absent.
pub fn timestamp<S: Serializer>(value: &Option<Timestamp>, serializer: S) -> Result<S::Ok, S::Error> {
    match value {
        Some(ts) => SecondsNanos { seconds: ts.seconds, nanos: ts.nanos }.serialize(serializer),
        None => serializer.serialize_none(),
    }
}

/// Serializes an optional duration as `{"seconds": i64, "nanos": i32}`, or
/// null when absent.
pub fn duration<S: Serializer>(value: &Option<Duration>, serializer: S) -> Result<S::Ok, S::Error> {
    match value {
        Some(duration) => SecondsNanos {
            seconds: duration.seconds,
            nanos: duration.nanos,
        }
        .serialize(serializer),
        None => serializer.serialize_none(),
    }
}

/// The short name of a proto enum discriminant: `name` with `prefix`
/// stripped, the whole name when it carries no such prefix.
pub fn short_name<'a>(name: &'a str, prefix: &str) -> &'a str {
    name.strip_prefix(prefix).unwrap_or(name)
}

/// Serializes a discriminant as its lowercase short name, see
/// [`short_name`].
pub fn lowercase_name<S: Serializer>(name: &str, prefix: &str, serializer: S) -> Result<S::Ok, S::Error> {
    serializer.serialize_str(&short_name(name, prefix).to_lowercase())
}

/// Serializes a discriminant by the name `as_str_name` gives it, an
/// undeclared value as the number itself.
pub fn declared_name<S, E>(value: &i32, serializer: S, as_str_name: fn(&E) -> &'static str) -> Result<S::Ok, S::Error>
where
    S: Serializer,
    E: TryFrom<i32>,
{
    match E::try_from(*value) {
        Ok(variant) => serializer.serialize_str(as_str_name(&variant)),
        Err(_) => serializer.serialize_i32(*value),
    }
}

/// Deserializes a discriminant from its declared name in any case or from
/// its number, null as the default, refusing an undeclared value.
pub fn from_declared_name<'de, D, E>(deserializer: D, from_str_name: fn(&str) -> Option<E>) -> Result<i32, D::Error>
where
    D: Deserializer<'de>,
    E: TryFrom<i32> + Into<i32> + Default,
{
    #[derive(Deserialize)]
    #[serde(untagged)]
    enum NameOrNumber {
        Number(i32),
        Name(String),
    }

    match Option::<NameOrNumber>::deserialize(deserializer)? {
        None => Ok(E::default().into()),
        Some(NameOrNumber::Number(number)) => E::try_from(number)
            .map(Into::into)
            .map_err(|_| de::Error::custom(format!("unknown value {number}"))),
        Some(NameOrNumber::Name(name)) => from_str_name(&name.to_uppercase())
            .map(Into::into)
            .ok_or_else(|| de::Error::custom(format!("unknown name {name:?}"))),
    }
}

#[cfg(test)]
mod test {
    use super::*;

    #[derive(Clone, Copy, Default, PartialEq, Eq)]
    enum Mode {
        #[default]
        None,
        Out,
    }

    impl TryFrom<i32> for Mode {
        type Error = ();

        fn try_from(value: i32) -> Result<Self, ()> {
            match value {
                0 => Ok(Self::None),
                1 => Ok(Self::Out),
                _ => Err(()),
            }
        }
    }

    impl From<Mode> for i32 {
        fn from(mode: Mode) -> Self {
            mode as i32
        }
    }

    impl Mode {
        fn as_str_name(&self) -> &'static str {
            match self {
                Self::None => "MODE_NONE",
                Self::Out => "MODE_OUT",
            }
        }

        fn from_str_name(name: &str) -> Option<Self> {
            match name {
                "MODE_NONE" => Some(Self::None),
                "MODE_OUT" => Some(Self::Out),
                _ => None,
            }
        }
    }

    #[derive(Serialize, Deserialize)]
    struct Action {
        #[serde(serialize_with = "serialize_mode", deserialize_with = "deserialize_mode")]
        mode: i32,
    }

    fn serialize_mode<S: Serializer>(mode: &i32, serializer: S) -> Result<S::Ok, S::Error> {
        declared_name(mode, serializer, Mode::as_str_name)
    }

    fn deserialize_mode<'de, D: Deserializer<'de>>(deserializer: D) -> Result<i32, D::Error> {
        from_declared_name(deserializer, Mode::from_str_name)
    }

    #[test]
    fn test_declared_name_serializes_by_name_and_an_undeclared_value_as_is() {
        assert_eq!(
            r#"{"mode":"MODE_OUT"}"#,
            serde_json::to_string(&Action { mode: 1 }).unwrap()
        );
        assert_eq!(r#"{"mode":99}"#, serde_json::to_string(&Action { mode: 99 }).unwrap());
    }

    #[test]
    fn test_from_declared_name_accepts_a_name_in_any_case_a_number_and_null() {
        let mode = |json: &str| serde_json::from_str::<Action>(json).map(|action| action.mode);

        assert_eq!(1, mode(r#"{"mode":"MODE_OUT"}"#).unwrap());
        assert_eq!(1, mode(r#"{"mode":"mode_out"}"#).unwrap());
        assert_eq!(1, mode(r#"{"mode":1}"#).unwrap());
        assert_eq!(0, mode(r#"{"mode":null}"#).unwrap());
        assert!(mode(r#"{"mode":"BOGUS"}"#).unwrap_err().to_string().contains("BOGUS"));
        assert!(mode(r#"{"mode":99}"#).unwrap_err().to_string().contains("99"));
    }

    #[test]
    fn test_short_name_strips_the_prefix_or_keeps_the_name() {
        assert_eq!("READY", short_name("STATE_READY", "STATE_"));
        assert_eq!("READY", short_name("READY", "STATE_"));
    }

    #[test]
    fn test_timestamp_and_duration_serialize_seconds_and_nanos_or_null() {
        #[derive(Serialize)]
        struct Stamped {
            #[serde(serialize_with = "timestamp")]
            at: Option<Timestamp>,
            #[serde(serialize_with = "duration")]
            every: Option<Duration>,
        }

        let json = serde_json::to_string(&Stamped {
            at: Some(Timestamp { seconds: 30, nanos: 5 }),
            every: None,
        })
        .unwrap();

        assert_eq!(r#"{"at":{"seconds":30,"nanos":5},"every":null}"#, json);
    }
}
