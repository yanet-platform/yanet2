//! Generic YAML config file loader shared by the CLI crates that read a
//! whole module configuration from one document.

use core::error::Error as StdError;
use std::{
    fs::File,
    path::{Path, PathBuf},
};

use serde::de::DeserializeOwned;

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
    fn test_load_valid_document_parses() {
        let path = scratch_path("valid");
        fs::write(&path, "name: route\n").unwrap();

        let doc: Doc = load(&path).unwrap();
        fs::remove_file(&path).unwrap();

        assert_eq!("route", doc.name);
    }
}
