//! Generic YAML config file loader shared by the CLI crates that read a
//! whole module configuration from one document.

use core::error::Error as StdError;
use std::{
    collections::BTreeSet,
    fs::{self, File},
    io::Read,
    path::{Path, PathBuf},
};

use serde::{Deserialize, de::DeserializeOwned};
use serde_yaml::Value;

/// The underlying `std::io::Error` or `serde_yaml::Error`, type-erased so
/// both failure modes fit one struct.
type Source = Box<dyn StdError + Send + Sync>;
type SourceValidator<'a> = &'a mut dyn FnMut(&str) -> Result<(), String>;

/// Loads and deserializes one YAML document at `path` as `T`.
pub fn load<T: DeserializeOwned>(path: impl AsRef<Path>) -> Result<T, Error> {
    let path = path.as_ref();
    serde_yaml::from_value(read(path)?).map_err(|source| Error::at(path, source))
}

/// Loads the one document at `path` as `T` the way the operators read a
/// file: merge keys expand and an empty document is the default value.
///
/// A file holding more than one document is refused.
pub fn load_document<T: DeserializeOwned + Default>(path: impl AsRef<Path>) -> Result<T, Error> {
    let path = path.as_ref();

    let value = read(path)?;
    if value.is_null() {
        return Ok(T::default());
    }
    serde_yaml::from_value(value).map_err(|source| Error::at(path, source))
}

/// Reads a configuration with file and directory includes before decoding.
///
/// Included mappings merge recursively in lexical directory order, then the
/// declaring document wins. Lists and scalars replace earlier values. Hidden
/// entries are skipped and visible symlinks support projected volumes.
pub fn read(path: impl AsRef<Path>) -> Result<Value, Error> {
    Reader::default().read(path.as_ref(), 0)
}

/// Reads includes after checking each source's original YAML spelling.
///
/// Schema-specific syntax checks run before value normalization can erase
/// scalar spellings or expand aliases. Typed decoding follows composition.
pub fn read_validated(
    path: impl AsRef<Path>,
    mut validate: impl FnMut(&str) -> Result<(), String>,
) -> Result<Value, Error> {
    Reader {
        validate: Some(&mut validate),
        ..Reader::default()
    }
    .read(path.as_ref(), 0)
}

const MAX_INCLUDE_DEPTH: usize = 32;
const MAX_INCLUDE_FILES: usize = 256;
const MAX_INCLUDE_BYTES: u64 = 16 << 20;

#[derive(Default)]
struct Reader<'a> {
    active: BTreeSet<PathBuf>,
    files: usize,
    bytes: u64,
    validate: Option<SourceValidator<'a>>,
}

impl Reader<'_> {
    fn read(&mut self, path: &Path, depth: usize) -> Result<Value, Error> {
        if depth > MAX_INCLUDE_DEPTH {
            return Err(Error::at(path, "include depth exceeds 32"));
        }
        let canonical = path.canonicalize().map_err(|source| Error::at(path, source))?;
        if !self.active.insert(canonical.clone()) {
            return Err(Error::at(path, "include cycle"));
        }
        let result = self.read_source(path, depth);
        self.active.remove(&canonical);
        result
    }

    fn read_source(&mut self, path: &Path, depth: usize) -> Result<Value, Error> {
        let metadata = fs::metadata(path).map_err(|source| Error::at(path, source))?;
        if metadata.is_dir() {
            let mut entries = fs::read_dir(path)
                .map_err(|source| Error::at(path, source))?
                .collect::<Result<Vec<_>, _>>()
                .map_err(|source| Error::at(path, source))?;
            entries.sort_by_key(std::fs::DirEntry::file_name);
            let files_before = self.files;
            let mut merged = Value::Mapping(serde_yaml::Mapping::new());
            for entry in entries {
                if entry.file_name().to_string_lossy().starts_with('.') {
                    continue;
                }
                let name = entry.path();
                let metadata = fs::metadata(&name).map_err(|source| Error::at(&name, source))?;
                if !metadata.is_dir()
                    && !matches!(name.extension().and_then(|value| value.to_str()), Some("yaml" | "yml"))
                {
                    continue;
                }
                let fragment = self.read_fragment(&name, depth + 1)?;
                merge(&mut merged, fragment);
            }
            if self.files == files_before {
                return Err(Error::at(path, "include directory contains no YAML files"));
            }
            return Ok(merged);
        }
        if !metadata.is_file() {
            return Err(Error::at(path, "include must be a regular file or directory"));
        }
        self.files += 1;
        if self.files > MAX_INCLUDE_FILES {
            return Err(Error::at(path, "includes exceed 256 files"));
        }
        let mut content = String::new();
        File::open(path)
            .map_err(|source| Error::at(path, source))?
            .take(MAX_INCLUDE_BYTES - self.bytes + 1)
            .read_to_string(&mut content)
            .map_err(|source| Error::at(path, source))?;
        self.bytes += content.len() as u64;
        if self.bytes > MAX_INCLUDE_BYTES {
            return Err(Error::at(path, "includes exceed 16777216 bytes"));
        }
        if let Some(validate) = &mut self.validate {
            validate(&content).map_err(|source| Error::at(path, source))?;
        }
        let mut value = Value::Null;
        for document in serde_yaml::Deserializer::from_str(&content) {
            let document = Value::deserialize(document).map_err(|source| Error::at(path, source))?;
            if !document.is_null() {
                if !value.is_null() {
                    return Err(Error::at(path, "the file holds more than one document"));
                }
                value = document;
            }
        }
        value.apply_merge().map_err(|source| Error::at(path, source))?;
        let Some(mapping) = value.as_mapping_mut() else {
            return Ok(value);
        };
        let Some(include) = mapping.remove(Value::String("include".to_owned())) else {
            return Ok(value);
        };
        let entries = include
            .as_sequence()
            .ok_or_else(|| Error::at(path, "include must be a list of paths"))?;
        let mut merged = Value::Mapping(serde_yaml::Mapping::new());
        for entry in entries {
            let name = entry
                .as_str()
                .filter(|name| !name.trim().is_empty())
                .ok_or_else(|| Error::at(path, "include entries must be non-empty strings"))?;
            let name = path.parent().unwrap_or_else(|| Path::new(".")).join(name);
            let fragment = self.read_fragment(&name, depth + 1)?;
            merge(&mut merged, fragment);
        }
        merge(&mut merged, value);
        Ok(merged)
    }

    fn read_fragment(&mut self, path: &Path, depth: usize) -> Result<Value, Error> {
        match self.read(path, depth)? {
            Value::Null => Ok(Value::Mapping(serde_yaml::Mapping::new())),
            value @ Value::Mapping(_) => Ok(value),
            _ => Err(Error::at(path, "included configuration must be a mapping")),
        }
    }
}

fn merge(base: &mut Value, overlay: Value) {
    match (base, overlay) {
        (Value::Mapping(base), Value::Mapping(overlay)) => {
            for (key, value) in overlay {
                merge(base.entry(key).or_insert(Value::Null), value);
            }
        }
        (base, overlay) => *base = overlay,
    }
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

impl Error {
    /// The source file whose contents or include could not be read.
    pub fn path(&self) -> &Path {
        &self.path
    }

    fn at(path: &Path, source: impl Into<Source>) -> Self {
        Self {
            path: path.to_owned(),
            source: source.into(),
        }
    }
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

    /// Creates isolated sources for one composition scenario.
    fn include_dir(case: &str) -> PathBuf {
        let path = scratch_path(case).with_extension("d");
        fs::create_dir_all(&path).unwrap();
        path
    }

    #[test]
    fn test_load_includes_merge_nested_files_and_replace_lists() {
        let directory = include_dir("merge-includes");
        fs::create_dir_all(directory.join("nested")).unwrap();
        fs::write(
            directory.join("base.yaml"),
            "name: base\nrules: [{target: removed, weight: 1}]\n",
        )
        .unwrap();
        fs::write(
            directory.join("nested/fragment.yaml"),
            "include: [../base.yaml]\nrules: [{target: retained, weight: 10}]\n",
        )
        .unwrap();
        fs::write(
            directory.join("main.yaml"),
            "include: [nested/fragment.yaml]\nname: local\n",
        )
        .unwrap();
        let config: Config = load(directory.join("main.yaml")).unwrap();
        fs::remove_dir_all(directory).unwrap();
        assert_eq!("local", config.name);
        assert_eq!(
            vec![Rule {
                target: "retained".into(),
                weight: 10
            }],
            config.rules
        );
    }

    #[test]
    fn test_load_includes_directory_refuses_non_mapping_before_later_valid_file() {
        let directory = include_dir("invalid-fragment");
        fs::create_dir_all(directory.join("fragments")).unwrap();
        fs::write(directory.join("fragments/10-invalid.yaml"), "[not, a, mapping]\n").unwrap();
        fs::write(directory.join("fragments/20-valid.yaml"), "name: valid\n").unwrap();
        fs::write(directory.join("main.yaml"), "include: [fragments]\n").unwrap();
        let error = load::<Config>(directory.join("main.yaml")).unwrap_err();
        fs::remove_dir_all(directory).unwrap();
        assert!(error.to_string().contains("10-invalid.yaml"), "{error}");
        assert!(error.to_string().contains("must be a mapping"), "{error}");
    }

    #[test]
    fn test_load_includes_refuses_invalid_paths_and_documents() {
        for (case, main, fragment) in [
            ("scalar", "include: fragment.yaml\n", "{}"),
            ("null", "include: null\n", "{}"),
            ("number", "include: [1]\n", "{}"),
            ("blank", "include: [' ']\n", "{}"),
            ("missing", "include: [missing.yaml]\n", "{}"),
            ("cycle", "include: [fragment.yaml]\n", "include: [main.yaml]\n"),
            ("duplicate", "include: [fragment.yaml]\n", "name: one\nname: two\n"),
            ("multiple", "include: [fragment.yaml]\n", "name: one\n---\nname: two\n"),
            ("malformed", "include: [fragment.yaml]\n", "name: ["),
        ] {
            let directory = include_dir(case);
            fs::write(directory.join("main.yaml"), main).unwrap();
            fs::write(directory.join("fragment.yaml"), fragment).unwrap();
            assert!(load::<Config>(directory.join("main.yaml")).is_err(), "{case}");
            fs::remove_dir_all(directory).unwrap();
        }
    }

    #[test]
    fn test_load_includes_refuses_depth_file_count_bytes_and_empty_directory() {
        for case in ["depth", "files", "bytes", "empty-directory"] {
            let directory = include_dir(case);
            match case {
                "depth" => {
                    for index in 0..34 {
                        fs::write(
                            directory.join(format!("{index}.yaml")),
                            format!("include: [{}.yaml]\n", index + 1),
                        )
                        .unwrap();
                    }
                    fs::write(directory.join("main.yaml"), "include: [0.yaml]\n").unwrap();
                }
                "files" => {
                    fs::write(directory.join("fragment.yaml"), "{}").unwrap();
                    fs::write(
                        directory.join("main.yaml"),
                        format!("include: [{}]\n", "fragment.yaml,".repeat(256)),
                    )
                    .unwrap();
                }
                "bytes" => {
                    fs::write(directory.join("fragment.yaml"), format!("#{}\n", "x".repeat(8 << 20))).unwrap();
                    fs::write(directory.join("main.yaml"), "include: [fragment.yaml, fragment.yaml]\n").unwrap();
                }
                _ => {
                    fs::create_dir_all(directory.join("empty")).unwrap();
                    fs::write(directory.join("main.yaml"), "include: [empty]\n").unwrap();
                }
            }
            assert!(load::<Config>(directory.join("main.yaml")).is_err(), "{case}");
            fs::remove_dir_all(directory).unwrap();
        }
    }

    #[cfg(unix)]
    #[test]
    fn test_load_includes_projected_directory_is_sorted_and_ignores_hidden_generation() {
        use std::os::unix::fs::symlink;
        let directory = include_dir("projected");
        let fragments = directory.join("fragments");
        fs::create_dir_all(fragments.join("..generation")).unwrap();
        fs::write(fragments.join("..generation/config.yaml"), "name: first\n").unwrap();
        symlink("..generation", fragments.join("..data")).unwrap();
        symlink("..data/config.yaml", fragments.join("10-projected.yaml")).unwrap();
        fs::write(fragments.join("20-later.yml"), "name: later\n").unwrap();
        fs::write(fragments.join(".ignored.yaml"), "include: [missing]\n").unwrap();
        fs::write(directory.join("main.yaml"), "include: [fragments]\n").unwrap();
        let config: Config = load(directory.join("main.yaml")).unwrap();
        assert_eq!("later", config.name);
        fs::remove_dir_all(directory).unwrap();
    }

    #[cfg(unix)]
    #[test]
    fn test_load_includes_detects_symlink_cycle() {
        use std::os::unix::fs::symlink;
        let directory = include_dir("symlink-cycle");
        fs::write(directory.join("main.yaml"), "include: [alias.yaml]\n").unwrap();
        symlink("main.yaml", directory.join("alias.yaml")).unwrap();
        let error = load::<Config>(directory.join("main.yaml")).unwrap_err();
        assert!(error.to_string().contains("include cycle"), "{error}");
        fs::remove_dir_all(directory).unwrap();
    }
}
