//! Generic YAML config file loader shared by the CLI crates that read a
//! whole module configuration from one document.

use core::error::Error as StdError;
use std::{
    fs::{self, File},
    path::{Path, PathBuf},
};

use serde::{Deserialize, de::DeserializeOwned};
use serde_yaml::Value;

/// The underlying `std::io::Error` or `serde_yaml::Error`, type-erased so
/// both failure modes fit one struct.
type Source = Box<dyn StdError + Send + Sync>;

/// Loads and deserializes one YAML document at `path` as `T`.
pub fn load<T: DeserializeOwned>(path: impl AsRef<Path>) -> Result<T, Error> {
    let path = path.as_ref();

    let at_path = |source: Source| Error { path: path.to_owned(), source };

    let file = File::open(path).map_err(|source| at_path(Box::new(source)))?;

    serde_yaml::from_reader(file).map_err(|source| at_path(Box::new(source)))
}

/// Loads the one document at `path` as `T` the way the operators read a
/// file: merge keys expand and an empty document is the default value.
///
/// A file holding more than one document is refused.
pub fn load_document<T: DeserializeOwned + Default>(path: impl AsRef<Path>) -> Result<T, Error> {
    let path = path.as_ref();

    let at_path = |source: Source| Error { path: path.to_owned(), source };

    let content = fs::read_to_string(path).map_err(|source| at_path(Box::new(source)))?;
    let mut documents = Vec::new();
    for document in serde_yaml::Deserializer::from_str(&content) {
        let value = Value::deserialize(document).map_err(|source| at_path(Box::new(source)))?;
        if !value.is_null() {
            documents.push(value);
        }
    }

    let mut value = match documents.pop() {
        None => return Ok(T::default()),
        Some(_) if !documents.is_empty() => return Err(at_path("the file holds more than one document".into())),
        Some(value) => value,
    };
    value.apply_merge().map_err(|source| at_path(Box::new(source)))?;

    serde_yaml::from_value(value).map_err(|source| at_path(Box::new(source)))
}

/// Binds the name given on the command line into a file's name field:
/// fills an empty one, accepts an equal one and refuses another.
pub fn bind_name(field: &mut String, name: &str) -> Result<(), String> {
    if field.is_empty() {
        *field = name.to_owned();
        return Ok(());
    }

    if field != name {
        return Err(format!("the file names config {field:?}, but --name is {name:?}"));
    }

    Ok(())
}

/// A YAML file that failed to open or to parse, naming the offending path.
#[derive(Debug, thiserror::Error)]
#[error("{}: {source}", path.display())]
pub struct Error {
    path: PathBuf,
    #[source]
    source: Source,
}

#[cfg(test)]
mod test {
    use std::{env, fs, process};

    use serde::Deserialize;

    use super::*;

    #[derive(Debug, Deserialize)]
    struct Doc {
        name: String,
    }

    #[derive(Debug, Default, PartialEq, Deserialize)]
    #[serde(default, deny_unknown_fields)]
    struct Config {
        name: String,
        rules: Vec<Rule>,
    }

    #[derive(Debug, Default, PartialEq, Deserialize)]
    #[serde(default, deny_unknown_fields)]
    struct Rule {
        target: String,
        weight: u32,
    }

    /// Loads `content` as a config through a scratch file.
    fn load_config(case: &str, content: &str) -> Result<Config, Error> {
        let path = scratch_path(case);
        fs::write(&path, content).unwrap();

        let loaded = load_document(&path);
        fs::remove_file(&path).unwrap();

        loaded
    }

    /// Builds a scratch path under the process id so parallel test runs
    /// never collide on the same file.
    fn scratch_path(case: &str) -> PathBuf {
        env::temp_dir().join(format!("yanet-cli-core-yaml-{case}-{}.yaml", process::id()))
    }

    #[test]
    fn test_load_missing_file_names_the_path() {
        let path = scratch_path("missing");

        let err = load::<Doc>(&path).expect_err("no file exists at this path");

        assert!(err.to_string().starts_with(&path.display().to_string()), "{err}");
    }

    #[test]
    fn test_load_invalid_document_names_the_path() {
        let path = scratch_path("invalid");
        fs::write(&path, "name: [unterminated\n").unwrap();

        let err = load::<Doc>(&path).expect_err("the document is not valid YAML");
        fs::remove_file(&path).unwrap();

        assert!(err.to_string().starts_with(&path.display().to_string()), "{err}");
    }

    #[test]
    fn test_load_document_empty_and_comment_only_files_are_the_default() {
        for (case, content) in [("empty", ""), ("comment", "# nothing yet\n")] {
            assert_eq!(Config::default(), load_config(case, content).unwrap());
        }
    }

    #[test]
    fn test_load_document_refuses_an_unknown_null_valued_key() {
        let err = load_config("nullkey", "rulez: null\n").expect_err("a misspelled null-valued key must be refused");

        assert!(err.to_string().contains("rulez"), "{err}");
    }

    #[test]
    fn test_load_document_tolerates_a_trailing_separator_and_refuses_a_second_document() {
        let tolerated = load_config("separator", "name: c0\n---\n").unwrap();
        let refused = load_config("two", "name: c0\n---\nname: c1\n").expect_err("a second document must be refused");

        assert_eq!("c0", tolerated.name);
        assert!(refused.to_string().contains("more than one document"), "{refused}");
    }

    #[test]
    fn test_load_document_expands_merge_keys() {
        let content = "rules:\n  - &base\n    target: base\n    weight: 10\n  - <<: *base\n    target: other\n";

        let config = load_config("merge", content).unwrap();

        assert_eq!(
            vec![
                Rule {
                    target: "base".to_owned(),
                    weight: 10
                },
                Rule {
                    target: "other".to_owned(),
                    weight: 10
                },
            ],
            config.rules
        );
    }

    #[test]
    fn test_bind_name_fills_checks_and_refuses() {
        let mut empty = String::new();
        bind_name(&mut empty, "c0").expect("an empty name must bind");
        assert_eq!("c0", empty);

        let mut matching = "c0".to_owned();
        bind_name(&mut matching, "c0").expect("a matching name must pass");

        let mut other = "c1".to_owned();
        let err = bind_name(&mut other, "c0").expect_err("a mismatch must be refused");
        assert!(err.contains("c1") && err.contains("c0"), "{err}");
    }

    #[test]
    fn test_load_valid_document_parses() {
        let path = scratch_path("valid");
        fs::write(&path, "name: route\n").unwrap();

        let doc: Doc = load(&path).unwrap();
        fs::remove_file(&path).unwrap();

        assert_eq!("route", doc.name);
    }
}
