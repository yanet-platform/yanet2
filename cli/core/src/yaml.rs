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
/// A file holding more than one document is refused, a bare separator
/// closing the stream excepted. A rejected field names the place it came
/// from, unless a merge key rebuilt the document first.
pub fn load_document<T: DeserializeOwned + Default>(path: impl AsRef<Path>) -> Result<T, Error> {
    let path = path.as_ref();

    let at_path = |source: Source| Error { path: path.to_owned(), source };

    let content = fs::read_to_string(path).map_err(|source| at_path(Box::new(source)))?;
    let mut documents = Vec::new();
    for document in serde_yaml::Deserializer::from_str(&content) {
        documents.push(Value::deserialize(document).map_err(|source| at_path(Box::new(source)))?);
    }
    let closes_the_stream = documents.iter().skip(1).all(Value::is_null) && only_bare_separators_follow(&content);
    if documents.len() > 1 && !closes_the_stream {
        return Err(at_path("the file holds more than one document".into()));
    }

    let value = match documents.into_iter().next() {
        Some(value) if !value.is_null() => value,
        _ => return Ok(T::default()),
    };

    // Expanding a merge key means reading a rebuilt document, which no
    // longer knows where its fields came from, so a document that merges
    // nothing is read straight from the text and keeps the place it
    // reports.
    let mut merged = value.clone();
    merged.apply_merge().map_err(|source| at_path(Box::new(source)))?;
    if merged != value {
        return serde_yaml::from_value(merged).map_err(|source| at_path(Box::new(source)));
    }

    let document = serde_yaml::Deserializer::from_str(&content)
        .next()
        .expect("the content holds the document just parsed");

    T::deserialize(document).map_err(|source| at_path(Box::new(source)))
}

/// Tells whether everything past the first document is a bare separator
/// carrying nothing but blank lines and comments.
///
/// A separator alone and a spelled-out null reach the parser as the same
/// null document, and only the text tells them apart. A line break this
/// scan does not know makes it answer yes too readily, so the parser's own
/// count has to agree with it.
fn only_bare_separators_follow(content: &str) -> bool {
    // The parser breaks lines on more than Rust does, and treats fewer
    // characters as the blank that follows a marker, so both are spelled
    // out here rather than borrowed from the standard library.
    let is_break = |c: char| matches!(c, '\n' | '\r' | '\u{85}' | '\u{2028}' | '\u{2029}');
    let is_blank = |c: char| matches!(c, ' ' | '\t');

    let mut documents: Vec<bool> = Vec::new();

    for line in content.split(is_break) {
        // A byte-order mark is presentation the parser drops, so a line
        // holding one is not a document of its own.
        let line = line.strip_prefix('\u{feff}').unwrap_or(line);
        let rest = match line.strip_prefix("---").or_else(|| line.strip_prefix("...")) {
            Some(rest) if rest.is_empty() || rest.starts_with(is_blank) => {
                documents.push(false);
                rest
            }
            _ => line,
        };

        let rest = rest.trim_start_matches(is_blank);
        // A directive belongs to the stream rather than to a document, so
        // it must not count as one on its own.
        if rest.is_empty() || rest.starts_with('#') || rest.starts_with('%') {
            continue;
        }
        match documents.last_mut() {
            Some(spelled) => *spelled = true,
            None => documents.push(true),
        }
    }

    !documents.iter().skip(1).any(|spelled| *spelled)
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
        for (case, content) in [
            ("empty", ""),
            ("comment", "# nothing yet\n"),
            ("separator-only", "---\n"),
            ("two-separators", "--- \n---\n"),
        ] {
            assert_eq!(Config::default(), load_config(case, content).unwrap());
        }
    }

    #[test]
    fn test_load_document_refuses_an_unknown_null_valued_key() {
        let err = load_config("nullkey", "rulez: null\n").expect_err("a misspelled null-valued key must be refused");

        assert!(err.to_string().contains("rulez"), "{err}");
    }

    #[test]
    fn test_load_document_tolerates_a_bare_separator_closing_the_stream() {
        for (case, content) in [
            ("separator", "name: c0\n---\n"),
            ("unterminated", "name: c0\n---"),
            ("commented", "name: c0\n---\n# end\n"),
        ] {
            assert_eq!("c0", load_config(case, content).unwrap().name, "{case}");
        }
    }

    #[test]
    fn test_load_document_refuses_a_second_document_however_it_is_spelled() {
        for (case, content) in [
            ("two", "name: c0\n---\nname: c1\n"),
            ("leading-null", "~\n---\nname: c1\n"),
            ("trailing-null", "name: c0\n---\nnull\n"),
            ("trailing-mapping", "name: c0\n---\n{}\n"),
        ] {
            let refused = load_config(case, content).expect_err("a second document must be refused");

            assert!(
                refused.to_string().contains("more than one document"),
                "{case}: {refused}"
            );
        }
    }

    #[test]
    fn test_load_document_refuses_a_second_document_past_any_line_break() {
        for (case, content) in [
            ("carriage-return", "name: c0\r---\rname: c1\r"),
            ("next-line", "name: c0\u{85}---\u{85}name: c1\u{85}"),
            ("line-separator", "name: c0\u{2028}---\u{2028}name: c1\u{2028}"),
            ("paragraph-separator", "name: c0\u{2029}---\u{2029}name: c1\u{2029}"),
            ("carriage-return-null", "name: c0\r---\rnull\r"),
            ("next-line-null", "name: c0\u{85}---\u{85}null\u{85}"),
            ("line-separator-null", "name: c0\u{2028}---\u{2028}null\u{2028}"),
            ("paragraph-separator-null", "name: c0\u{2029}---\u{2029}null\u{2029}"),
            ("non-breaking-space", "name: c0\n---\n---\u{a0}\n"),
        ] {
            let refused = load_config(case, content).expect_err("a second document must be refused");

            assert!(
                refused.to_string().contains("more than one document"),
                "{case}: {refused}"
            );
        }
    }

    #[test]
    fn test_load_document_reads_windows_line_endings() {
        let tolerated = load_config("crlf", "name: c0\r\n---\r\n").unwrap();
        let refused =
            load_config("crlf-two", "name: c0\r\n---\r\nnull\r\n").expect_err("a second document must be refused");

        assert_eq!("c0", tolerated.name);
        assert!(refused.to_string().contains("more than one document"), "{refused}");
    }

    #[test]
    fn test_load_document_tolerates_a_bare_separator_past_any_line_break() {
        for (case, content) in [
            ("bare-carriage-return", "name: c0\r---\r"),
            ("bare-next-line", "name: c0\u{85}---\u{85}"),
            ("bare-line-separator", "name: c0\u{2028}---\u{2028}"),
            ("bare-paragraph-separator", "name: c0\u{2029}---\u{2029}"),
        ] {
            assert_eq!("c0", load_config(case, content).unwrap().name, "{case}");
        }
    }

    #[test]
    fn test_load_document_tolerates_a_byte_order_mark() {
        for (case, content) in [
            ("bom-preamble", "\u{feff}\n---\nname: c0\n---\n"),
            ("bom-content", "\u{feff}name: c0\n---\n"),
        ] {
            assert_eq!("c0", load_config(case, content).unwrap().name, "{case}");
        }
    }

    #[test]
    fn test_load_document_tolerates_a_directive_before_the_document() {
        let content = "%YAML 1.1\n---\nname: c0\n---\n";

        let config = load_config("directive", content).unwrap();

        assert_eq!("c0", config.name);
    }

    #[test]
    fn test_load_document_expands_a_merge_key_inside_a_tagged_value() {
        let content = "rules: !Custom\n  - &base\n    target: base\n    weight: 10\n  - <<: *base\n";
        let path = scratch_path("tagged-merge");
        fs::write(&path, content).unwrap();

        let config = load_document::<Config>(&path).unwrap();
        fs::remove_file(&path).unwrap();

        assert_eq!(
            vec![
                Rule {
                    target: "base".to_owned(),
                    weight: 10
                },
                Rule {
                    target: "base".to_owned(),
                    weight: 10
                },
            ],
            config.rules
        );
    }

    #[test]
    fn test_load_document_error_names_the_offending_field_and_line() {
        let content = "name: c0\nrules:\n  - target: t\n  - targetx: t\n";

        let refused = load_config("position", content).expect_err("an unknown field must be refused");

        let message = refused.to_string();
        assert!(message.contains("rules[1]"), "{message}");
        assert!(message.contains("line 4"), "{message}");
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
