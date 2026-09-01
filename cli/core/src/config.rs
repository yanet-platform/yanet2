//! CLI configuration file: fleet defaults, personal aliases and per-key
//! precedence.
//!
//! The model is `ssh_config`, not kubeconfig: a flat set of top-level
//! defaults plus an optional `endpoints:` map of named sections that
//! override any of those keys. [`resolve`] applies the fixed precedence
//! (flag, environment variable, alias section, top-level key, built-in
//! default) and returns a [`Settings`] that also carries where each value
//! came from.

use core::time::Duration;
use std::{
    collections::{BTreeMap, BTreeSet},
    env,
    ffi::OsString,
    fs,
    path::{Path, PathBuf},
};

use serde::Deserialize;

use crate::{
    auth::{self, AuthMethod},
    client::{timeout_from_seconds, ConnectionArgs},
};

/// Built-in target used when no flag, environment variable or file supplies
/// one.
const DEFAULT_ENDPOINT: &str = "grpc://[::1]:8080";

/// System-wide configuration file, deployed alongside `controlplane.d/*`.
const SYSTEM_PATH: &str = "/etc/yanet2/cli.yaml";

/// Environment variable that replaces both default file locations with one
/// path. A missing file at that path is an error rather than falling back.
const CONFIG_ENV: &str = "YANET_CONFIG";

/// Where the configuration file lives, injectable so tests never touch the
/// real filesystem locations or `$HOME`.
#[derive(Debug, Clone)]
pub struct Sources {
    /// Fleet-wide file, merged under the user file.
    pub system_path: PathBuf,
    /// Personal file, merged over the system file. `None` when neither
    /// `XDG_CONFIG_HOME` nor `HOME` is set: there is then no location to
    /// derive a personal file from, so none is consulted, rather than
    /// guessing one relative to the current directory.
    pub user_path: Option<PathBuf>,
    /// `YANET_CONFIG` value, if set: the only file consulted when present,
    /// and a missing file at this path is an error.
    pub config_override: Option<PathBuf>,
}

impl Sources {
    /// The real locations: `/etc/yanet2/cli.yaml`, and the personal file
    /// under an absolute `XDG_CONFIG_HOME`, otherwise `$HOME/.config`,
    /// otherwise no personal file at all. `$YANET_CONFIG` replaces both.
    pub fn from_process_env() -> Self {
        let config_dir = user_config_dir(env::var_os("XDG_CONFIG_HOME"), env::var_os("HOME"));

        Self {
            system_path: PathBuf::from(SYSTEM_PATH),
            user_path: config_dir.map(|dir| dir.join("yanet2/cli.yaml")),
            config_override: env::var_os(CONFIG_ENV).map(PathBuf::from),
        }
    }
}

/// Chooses the base directory for the personal file from `XDG_CONFIG_HOME`
/// and `HOME`, treating an empty value as unset for either one, per the
/// XDG base directory spec. A relative `XDG_CONFIG_HOME` is ignored the
/// same way, since the spec requires an absolute path.
///
/// Pure so it is testable without mutating the process environment.
fn user_config_dir(xdg_config_home: Option<OsString>, home: Option<OsString>) -> Option<PathBuf> {
    non_empty(xdg_config_home)
        .map(PathBuf::from)
        .filter(|path| path.is_absolute())
        .or_else(|| non_empty(home).map(|home| PathBuf::from(home).join(".config")))
}

fn non_empty(value: Option<OsString>) -> Option<OsString> {
    value.filter(|value| !value.is_empty())
}

/// One file consulted while resolving the configuration, and whether it was
/// actually read.
///
/// Data only — kept for the `config show` seam that reports the files a
/// command consulted.
#[derive(Debug, Clone, PartialEq, Eq)]
pub struct ConsultedFile {
    pub path: PathBuf,
    pub read: bool,
}

/// Where a resolved value came from.
#[derive(Debug, Clone, PartialEq, Eq)]
pub enum Origin {
    /// A `clap` flag or its environment variable.
    Argument,
    /// The named alias's own section of the given file.
    Section { path: PathBuf, alias: String },
    /// The top-level key of the given file.
    File(PathBuf),
    /// A file value existed but was dropped because the resolved endpoint
    /// is plaintext, e.g. a fleet-wide TLS key that a lab alias never uses.
    Ignored { path: PathBuf, alias: Option<String> },
    /// No flag, environment variable or file supplied a value.
    BuiltIn,
}

/// One resolved value paired with where it came from.
#[derive(Debug, Clone)]
pub struct Resolved<T> {
    pub value: T,
    pub origin: Origin,
}

/// The fully resolved connection configuration for one invocation.
#[derive(Debug, Clone)]
pub struct Settings {
    pub endpoint: Resolved<String>,
    /// The alias name the endpoint was selected through, if any, paired
    /// with where that *name* came from (a flag/environment value or the
    /// file's top-level `endpoint` key) — distinct from `endpoint.origin`,
    /// which names where the alias's URI itself came from.
    pub alias: Option<Resolved<String>>,
    pub auth: Resolved<AuthMethod>,
    pub cert_tag: Resolved<Option<String>>,
    pub ca: Resolved<Option<PathBuf>>,
    pub client_cert: Resolved<Option<PathBuf>>,
    pub client_key: Resolved<Option<PathBuf>>,
    pub timeout: Resolved<Option<Duration>>,
    /// Every file consulted while resolving, in system-then-user order.
    pub files: Vec<ConsultedFile>,
}

impl Settings {
    /// The endpoint label used in error and status messages: `<alias>
    /// (<uri>)` when an alias was used, the bare URI otherwise.
    pub fn label(&self) -> String {
        match &self.alias {
            Some(alias) => format!("{} ({})", alias.value, self.endpoint.value),
            None => self.endpoint.value.clone(),
        }
    }

    /// Pairs the resolved auth method with its own identity, checked here
    /// rather than during [`resolve`].
    ///
    /// `config show` calls [`resolve`] alone and still needs to report an
    /// `sshcert` method with no tag rather than fail outright, so the
    /// missing-tag rejection lives on this connect-time boundary instead.
    pub fn resolved_auth(&self) -> Result<auth::ResolvedAuth, Error> {
        match self.auth.value {
            AuthMethod::None => Ok(auth::ResolvedAuth::None),
            AuthMethod::Sshcert => match self.cert_tag.value.as_deref().map(str::trim) {
                Some(tag) if !tag.is_empty() => Ok(auth::ResolvedAuth::Sshcert { tag: tag.to_owned() }),
                // An empty tag matches every identity in the agent through
                // `contains("")`, so it is treated the same as no tag at all.
                _ => Err(Error::MissingCertTag),
            },
        }
    }
}

/// Errors resolving the configuration file and applying it.
#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("failed to read configuration file {}: {source}", path.display())]
    Read {
        path: PathBuf,
        #[source]
        source: std::io::Error,
    },
    #[error("failed to parse configuration file {}: {source}", path.display())]
    Parse {
        path: PathBuf,
        #[source]
        source: serde_yaml::Error,
    },
    #[error("unknown endpoint alias \"{alias}\": {}", alias_hint(known))]
    UnknownAlias { alias: String, known: Vec<String> },
    #[error("endpoint alias \"{alias}\" has no \"endpoint\" key in {}", path.display())]
    SectionMissingEndpoint { alias: String, path: PathBuf },
    #[error("endpoint alias \"{alias}\" resolves to another alias: alias chains are not supported")]
    AliasChain { alias: String },
    #[error(
        "auth sshcert requires a certificate tag: pass --cert-tag, set YANET_CERT_TAG, \
         or set cert_tag in the configuration file"
    )]
    MissingCertTag,
    #[error("invalid \"timeout\" value in {}: {message}", path.display())]
    InvalidTimeout { path: PathBuf, message: String },
}

impl Error {
    fn unknown_alias(alias: &str, merged: &MergedFile) -> Self {
        let mut known: Vec<String> = merged.endpoints.keys().cloned().collect();
        known.sort();

        Self::UnknownAlias { alias: alias.to_owned(), known }
    }
}

/// Renders the known-aliases clause of [`Error::UnknownAlias`].
fn alias_hint(known: &[String]) -> String {
    if known.is_empty() {
        "no aliases are defined in the configuration file".to_owned()
    } else {
        format!("known aliases: {}", known.join(", "))
    }
}

/// One file's raw contents, exactly as written. Unknown keys are rejected
/// here rather than silently ignored.
#[derive(Debug, Clone, Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct RawFile {
    endpoint: Option<String>,
    auth: Option<AuthMethod>,
    cert_tag: Option<String>,
    ca: Option<PathBuf>,
    client_cert: Option<PathBuf>,
    client_key: Option<PathBuf>,
    timeout: Option<f64>,
    endpoints: Option<BTreeMap<String, RawSection>>,
}

/// One `endpoints.<alias>` section. `endpoint` is required once the section
/// itself is used, checked in [`resolve`] rather than here so a section
/// present only to be merged into by the other file need not carry it.
#[derive(Debug, Clone, Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct RawSection {
    endpoint: Option<String>,
    auth: Option<AuthMethod>,
    cert_tag: Option<String>,
    ca: Option<PathBuf>,
    client_cert: Option<PathBuf>,
    client_key: Option<PathBuf>,
    timeout: Option<f64>,
}

/// One value merged from (at most) two files, with the file it came from.
#[derive(Debug, Clone)]
struct MergedField<T> {
    value: T,
    path: PathBuf,
}

/// Picks `user` over `system`, tagging the result with the file it came
/// from.
fn merge_field<T>(system: Option<T>, system_path: &Path, user: Option<T>, user_path: &Path) -> Option<MergedField<T>> {
    match user {
        Some(value) => Some(MergedField { value, path: user_path.to_owned() }),
        None => system.map(|value| MergedField { value, path: system_path.to_owned() }),
    }
}

/// One `endpoints.<alias>` section merged key-wise from both files.
#[derive(Debug, Clone)]
struct MergedSection {
    endpoint: Option<MergedField<String>>,
    auth: Option<MergedField<AuthMethod>>,
    cert_tag: Option<MergedField<String>>,
    ca: Option<MergedField<PathBuf>>,
    client_cert: Option<MergedField<PathBuf>>,
    client_key: Option<MergedField<PathBuf>>,
    timeout: Option<MergedField<f64>>,
    /// The file that defines this alias, preferring the user file: the
    /// location named in [`Error::SectionMissingEndpoint`].
    defined_in: PathBuf,
}

fn merge_section(
    system: Option<&RawSection>,
    system_path: &Path,
    user: Option<&RawSection>,
    user_path: &Path,
) -> MergedSection {
    let defined_in = if user.is_some() {
        user_path.to_owned()
    } else {
        system_path.to_owned()
    };

    MergedSection {
        endpoint: merge_field(
            system.and_then(|s| s.endpoint.clone()),
            system_path,
            user.and_then(|s| s.endpoint.clone()),
            user_path,
        ),
        auth: merge_field(
            system.and_then(|s| s.auth),
            system_path,
            user.and_then(|s| s.auth),
            user_path,
        ),
        cert_tag: merge_field(
            system.and_then(|s| s.cert_tag.clone()),
            system_path,
            user.and_then(|s| s.cert_tag.clone()),
            user_path,
        ),
        ca: merge_field(
            system.and_then(|s| s.ca.clone()),
            system_path,
            user.and_then(|s| s.ca.clone()),
            user_path,
        ),
        client_cert: merge_field(
            system.and_then(|s| s.client_cert.clone()),
            system_path,
            user.and_then(|s| s.client_cert.clone()),
            user_path,
        ),
        client_key: merge_field(
            system.and_then(|s| s.client_key.clone()),
            system_path,
            user.and_then(|s| s.client_key.clone()),
            user_path,
        ),
        timeout: merge_field(
            system.and_then(|s| s.timeout),
            system_path,
            user.and_then(|s| s.timeout),
            user_path,
        ),
        defined_in,
    }
}

fn merge_endpoints(
    system: &BTreeMap<String, RawSection>,
    system_path: &Path,
    user: &BTreeMap<String, RawSection>,
    user_path: &Path,
) -> BTreeMap<String, MergedSection> {
    let names: BTreeSet<&String> = system.keys().chain(user.keys()).collect();

    names
        .into_iter()
        .map(|name| {
            (
                name.clone(),
                merge_section(system.get(name), system_path, user.get(name), user_path),
            )
        })
        .collect()
}

/// Both files merged key-wise, the user file winning per key and per alias.
#[derive(Debug, Clone)]
struct MergedFile {
    endpoint: Option<MergedField<String>>,
    auth: Option<MergedField<AuthMethod>>,
    cert_tag: Option<MergedField<String>>,
    ca: Option<MergedField<PathBuf>>,
    client_cert: Option<MergedField<PathBuf>>,
    client_key: Option<MergedField<PathBuf>>,
    timeout: Option<MergedField<f64>>,
    endpoints: BTreeMap<String, MergedSection>,
    files: Vec<ConsultedFile>,
}

/// Reads one file, returning `None` when it is absent and `required` is
/// `false`. A missing required file and a present-but-unreadable file are
/// both [`Error::Read`].
// `core::io::ErrorKind` is behind the unstable `core_io` feature, so this
// function keeps `std::io::ErrorKind`. The allow sits here rather than on a
// `use` because clippy's `useless_attribute` rejects it directly on a `use`
// item.
#[allow(clippy::std_instead_of_core)]
fn read_file(path: &Path, required: bool) -> Result<(Option<RawFile>, ConsultedFile), Error> {
    use std::io::ErrorKind;

    match fs::read_to_string(path) {
        Ok(text) => {
            let raw: RawFile =
                serde_yaml::from_str(&text).map_err(|source| Error::Parse { path: path.to_owned(), source })?;

            Ok((Some(raw), ConsultedFile { path: path.to_owned(), read: true }))
        }
        Err(source) if source.kind() == ErrorKind::NotFound && !required => {
            Ok((None, ConsultedFile { path: path.to_owned(), read: false }))
        }
        Err(source) => Err(Error::Read { path: path.to_owned(), source }),
    }
}

/// Reads and merges the configuration file(s) named by `sources`.
fn load(sources: &Sources) -> Result<MergedFile, Error> {
    if let Some(path) = &sources.config_override {
        let (raw, file) = read_file(path, true)?;

        return Ok(merge_files(
            RawFile::default(),
            path,
            raw.unwrap_or_default(),
            path,
            vec![file],
        ));
    }

    let (system, system_file) = read_file(&sources.system_path, false)?;
    let system_path = sources.system_path.as_path();

    let Some(user_path) = &sources.user_path else {
        // No personal file to merge over the system one at all.
        return Ok(merge_files(
            system.unwrap_or_default(),
            system_path,
            RawFile::default(),
            system_path,
            vec![system_file],
        ));
    };

    let (user, user_file) = read_file(user_path, false)?;

    Ok(merge_files(
        system.unwrap_or_default(),
        system_path,
        user.unwrap_or_default(),
        user_path,
        vec![system_file, user_file],
    ))
}

/// Merges an already-read system/user pair of raw files, key-wise and per
/// alias, into one [`MergedFile`].
///
/// `user_path` is only ever read back out of `user`'s own fields, so it is
/// never dereferenced when `user` carries no values of its own — the shape
/// [`load`] relies on when there is no personal file to merge.
fn merge_files(
    system: RawFile,
    system_path: &Path,
    user: RawFile,
    user_path: &Path,
    files: Vec<ConsultedFile>,
) -> MergedFile {
    MergedFile {
        endpoint: merge_field(system.endpoint.clone(), system_path, user.endpoint.clone(), user_path),
        auth: merge_field(system.auth, system_path, user.auth, user_path),
        cert_tag: merge_field(system.cert_tag.clone(), system_path, user.cert_tag.clone(), user_path),
        ca: merge_field(system.ca.clone(), system_path, user.ca.clone(), user_path),
        client_cert: merge_field(
            system.client_cert.clone(),
            system_path,
            user.client_cert.clone(),
            user_path,
        ),
        client_key: merge_field(
            system.client_key.clone(),
            system_path,
            user.client_key.clone(),
            user_path,
        ),
        timeout: merge_field(system.timeout, system_path, user.timeout, user_path),
        endpoints: merge_endpoints(
            &system.endpoints.unwrap_or_default(),
            system_path,
            &user.endpoints.unwrap_or_default(),
            user_path,
        ),
        files,
    }
}

/// Resolves one key by the fixed precedence: the `clap` value, the alias
/// section, the top-level file key, then `default`.
fn resolve_key<T: Clone>(
    argument: Option<T>,
    section: Option<&MergedField<T>>,
    top_level: Option<&MergedField<T>>,
    alias: Option<&str>,
    default: Option<T>,
) -> (Option<T>, Origin) {
    if let Some(value) = argument {
        return (Some(value), Origin::Argument);
    }

    if let Some(field) = section {
        let alias = alias.expect("a section value implies its alias").to_owned();

        return (
            Some(field.value.clone()),
            Origin::Section { path: field.path.clone(), alias },
        );
    }

    if let Some(field) = top_level {
        return (Some(field.value.clone()), Origin::File(field.path.clone()));
    }

    (default, Origin::BuiltIn)
}

/// Drops a TLS value that came from a file when the resolved endpoint is
/// plaintext, so a fleet-wide `ca` cannot break a plaintext lab alias.
///
/// A value given on the command line or through its environment variable
/// is kept, and still rejected downstream by the existing TLS/plaintext
/// check. The dropped value's origin becomes [`Origin::Ignored`] rather
/// than vanishing into [`Origin::BuiltIn`], so a caller can still say
/// where the ignored value came from.
fn drop_file_tls_if_plaintext<T>(
    value: Option<T>,
    origin: Origin,
    is_plaintext: bool,
    key: &str,
) -> (Option<T>, Origin) {
    if !is_plaintext || value.is_none() {
        return (value, origin);
    }

    match origin {
        Origin::Section { path, alias } => {
            log::debug!("dropping file-provided {key} for a plaintext endpoint");

            (None, Origin::Ignored { path, alias: Some(alias) })
        }
        Origin::File(path) => {
            log::debug!("dropping file-provided {key} for a plaintext endpoint");

            (None, Origin::Ignored { path, alias: None })
        }
        other => (value, other),
    }
}

/// Resolves every connection setting for one invocation.
///
/// Applies the target resolution (a scheme-less endpoint is an alias
/// looked up in `endpoints`), then the flag > alias section > top-level
/// key > built-in precedence for every other key, then the plaintext TLS
/// drop. The `sshcert` certificate-tag requirement is not checked here —
/// see [`Settings::resolved_auth`].
pub fn resolve(args: &ConnectionArgs, sources: &Sources) -> Result<Settings, Error> {
    let merged = load(sources)?;

    let (mut endpoint, target_origin) = match &args.endpoint {
        Some(value) => (value.clone(), Origin::Argument),
        None => match &merged.endpoint {
            Some(field) => (field.value.clone(), Origin::File(field.path.clone())),
            None => (DEFAULT_ENDPOINT.to_owned(), Origin::BuiltIn),
        },
    };

    let mut endpoint_origin = target_origin.clone();
    // The alias *name*'s own origin (how `--endpoint`/`YANET_ENDPOINT`/the
    // file selected it), kept apart from `endpoint_origin` above, which
    // becomes the alias section's own origin once the name resolves.
    let mut alias: Option<Resolved<String>> = None;
    let mut section: Option<&MergedSection> = None;

    if !endpoint.contains("://") {
        let name = endpoint.clone();
        let found = merged
            .endpoints
            .get(&name)
            .ok_or_else(|| Error::unknown_alias(&name, &merged))?;

        let target = found.endpoint.as_ref().ok_or_else(|| Error::SectionMissingEndpoint {
            alias: name.clone(),
            path: found.defined_in.clone(),
        })?;

        if !target.value.contains("://") {
            return Err(Error::AliasChain { alias: name });
        }

        endpoint = target.value.clone();
        endpoint_origin = Origin::Section {
            path: target.path.clone(),
            alias: name.clone(),
        };
        alias = Some(Resolved {
            value: name.clone(),
            origin: target_origin,
        });
        section = Some(found);
    }

    let alias_ref = alias.as_ref().map(|a| a.value.as_str());

    let (auth, auth_origin) = resolve_key(
        args.auth.auth,
        section.and_then(|s| s.auth.as_ref()),
        merged.auth.as_ref(),
        alias_ref,
        Some(AuthMethod::None),
    );
    let auth = auth.expect("a built-in default was supplied");

    let (cert_tag, cert_tag_origin) = resolve_key(
        args.auth.cert_tag.clone(),
        section.and_then(|s| s.cert_tag.as_ref()),
        merged.cert_tag.as_ref(),
        alias_ref,
        None,
    );

    let (ca, ca_origin) = resolve_key(
        args.tls.ca.clone(),
        section.and_then(|s| s.ca.as_ref()),
        merged.ca.as_ref(),
        alias_ref,
        None,
    );
    let (client_cert, client_cert_origin) = resolve_key(
        args.tls.client_cert.clone(),
        section.and_then(|s| s.client_cert.as_ref()),
        merged.client_cert.as_ref(),
        alias_ref,
        None,
    );
    let (client_key, client_key_origin) = resolve_key(
        args.tls.client_key.clone(),
        section.and_then(|s| s.client_key.as_ref()),
        merged.client_key.as_ref(),
        alias_ref,
        None,
    );

    let (timeout_seconds, timeout_seconds_origin) = resolve_key(
        None,
        section.and_then(|s| s.timeout.as_ref()),
        merged.timeout.as_ref(),
        alias_ref,
        None,
    );
    let (timeout, timeout_origin) = match args.timeout {
        Some(value) => (Some(value), Origin::Argument),
        None => match timeout_seconds {
            Some(seconds) => {
                let path = match &timeout_seconds_origin {
                    Origin::Section { path, .. } | Origin::File(path) => path.clone(),
                    Origin::Argument | Origin::Ignored { .. } | Origin::BuiltIn => {
                        unreachable!("resolve_key never returns these without a value")
                    }
                };
                let value = timeout_from_seconds(seconds).map_err(|message| Error::InvalidTimeout { path, message })?;

                (Some(value), timeout_seconds_origin)
            }
            None => (None, Origin::BuiltIn),
        },
    };

    let is_plaintext = matches!(endpoint.split_once("://"), Some(("grpc" | "unix", ..)));
    let (ca, ca_origin) = drop_file_tls_if_plaintext(ca, ca_origin, is_plaintext, "ca");
    let (client_cert, client_cert_origin) =
        drop_file_tls_if_plaintext(client_cert, client_cert_origin, is_plaintext, "client_cert");
    let (client_key, client_key_origin) =
        drop_file_tls_if_plaintext(client_key, client_key_origin, is_plaintext, "client_key");

    Ok(Settings {
        endpoint: Resolved {
            value: endpoint,
            origin: endpoint_origin,
        },
        alias,
        auth: Resolved { value: auth, origin: auth_origin },
        cert_tag: Resolved {
            value: cert_tag,
            origin: cert_tag_origin,
        },
        ca: Resolved { value: ca, origin: ca_origin },
        client_cert: Resolved {
            value: client_cert,
            origin: client_cert_origin,
        },
        client_key: Resolved {
            value: client_key,
            origin: client_key_origin,
        },
        timeout: Resolved {
            value: timeout,
            origin: timeout_origin,
        },
        files: merged.files,
    })
}

#[cfg(test)]
mod test {
    use std::fs;

    use super::*;
    use crate::{auth::AuthArgs, client::TlsArgs};

    /// A scratch directory unique to the calling test, so parallel tests
    /// never share a file.
    fn scratch_dir(name: &str) -> PathBuf {
        let dir = env::temp_dir().join(format!("yanet-cli-config-test-{name}-{}", std::process::id()));
        // A PID can be recycled across runs. Start from a clean directory
        // rather than risking a leftover file from an earlier one.
        let _ = fs::remove_dir_all(&dir);
        fs::create_dir_all(&dir).unwrap();

        dir
    }

    #[test]
    fn test_user_config_dir_prefers_xdg_config_home() {
        let dir = user_config_dir(Some("/xdg".into()), Some("/home/x".into()));

        assert_eq!(Some(PathBuf::from("/xdg")), dir);
    }

    #[test]
    fn test_user_config_dir_falls_back_to_home_dot_config() {
        let dir = user_config_dir(None, Some("/home/x".into()));

        assert_eq!(Some(PathBuf::from("/home/x/.config")), dir);
    }

    #[test]
    fn test_user_config_dir_treats_empty_xdg_config_home_as_unset() {
        let dir = user_config_dir(Some("".into()), Some("/home/x".into()));

        assert_eq!(Some(PathBuf::from("/home/x/.config")), dir);
    }

    #[test]
    fn test_user_config_dir_ignores_a_relative_xdg_config_home() {
        let dir = user_config_dir(Some("relative/xdg".into()), Some("/home/x".into()));

        assert_eq!(Some(PathBuf::from("/home/x/.config")), dir);
    }

    #[test]
    fn test_user_config_dir_treats_empty_home_as_unset() {
        let dir = user_config_dir(Some("".into()), Some("".into()));

        assert_eq!(None, dir);
    }

    #[test]
    fn test_user_config_dir_none_when_neither_is_set() {
        assert_eq!(None, user_config_dir(None, None));
    }

    fn write(dir: &Path, name: &str, contents: &str) -> PathBuf {
        let path = dir.join(name);
        fs::write(&path, contents).unwrap();

        path
    }

    /// A `Sources` pointing at two files under `dir` that need not exist.
    fn sources(dir: &Path) -> Sources {
        Sources {
            system_path: dir.join("system.yaml"),
            user_path: Some(dir.join("user.yaml")),
            config_override: None,
        }
    }

    fn bare_args() -> ConnectionArgs {
        ConnectionArgs {
            endpoint: None,
            auth: AuthArgs { auth: None, cert_tag: None },
            tls: TlsArgs::default(),
            timeout: None,
        }
    }

    #[test]
    fn test_resolve_endpoint_falls_back_through_all_five_levels() {
        let dir = scratch_dir("precedence");
        let sources = sources(&dir);

        let builtin = resolve(&bare_args(), &sources).unwrap();
        assert_eq!(DEFAULT_ENDPOINT, builtin.endpoint.value);
        assert_eq!(Origin::BuiltIn, builtin.endpoint.origin);

        write(&dir, "system.yaml", "endpoint: grpc://system:1\n");
        let file = resolve(&bare_args(), &sources).unwrap();
        assert_eq!("grpc://system:1", file.endpoint.value);
        assert!(matches!(file.endpoint.origin, Origin::File(..)));

        write(&dir, "user.yaml", "endpoint: grpc://user:1\n");
        let user_over_system = resolve(&bare_args(), &sources).unwrap();
        assert_eq!("grpc://user:1", user_over_system.endpoint.value);

        let mut args = bare_args();
        args.endpoint = Some("grpc://argument:1".to_owned());
        let argument = resolve(&args, &sources).unwrap();
        assert_eq!("grpc://argument:1", argument.endpoint.value);
        assert_eq!(Origin::Argument, argument.endpoint.origin);
    }

    #[test]
    fn test_resolve_endpoint_alias_from_argument() {
        // A flag value and its environment variable both land in
        // `ConnectionArgs.endpoint` before `resolve` ever runs, so this
        // layer sees one `Origin::Argument` for both. Telling them apart
        // is `main.rs`'s `describe_origin`, from `ArgMatches::value_source`.
        let dir = scratch_dir("alias-arg");
        let sources = sources(&dir);
        write(
            &dir,
            "user.yaml",
            "endpoints:\n  lab: { endpoint: grpc://lab.example.net:8080 }\n",
        );

        let mut args = bare_args();
        args.endpoint = Some("lab".to_owned());
        let settings = resolve(&args, &sources).unwrap();

        assert_eq!("grpc://lab.example.net:8080", settings.endpoint.value);
        let alias = settings.alias.as_ref().unwrap();
        assert_eq!("lab", alias.value);
        assert_eq!(Origin::Argument, alias.origin);
        assert_eq!("lab (grpc://lab.example.net:8080)", settings.label());
    }

    #[test]
    fn test_resolve_top_level_endpoint_may_itself_be_an_alias() {
        let dir = scratch_dir("alias-top-level");
        let sources = sources(&dir);
        let user_path = write(
            &dir,
            "user.yaml",
            "endpoint: lab\nendpoints:\n  lab: { endpoint: grpc://lab.example.net:8080 }\n",
        );

        let settings = resolve(&bare_args(), &sources).unwrap();

        assert_eq!("grpc://lab.example.net:8080", settings.endpoint.value);
        let alias = settings.alias.as_ref().unwrap();
        assert_eq!("lab", alias.value);
        assert_eq!(Origin::File(user_path), alias.origin);
    }

    #[test]
    fn test_resolve_unknown_alias_lists_the_known_ones() {
        let dir = scratch_dir("unknown-alias");
        let sources = sources(&dir);
        write(
            &dir,
            "user.yaml",
            "endpoints:\n  lab: { endpoint: grpc://lab:1 }\n  m9: { endpoint: grpc://m9:1 }\n",
        );

        let mut args = bare_args();
        args.endpoint = Some("nope".to_owned());
        let err = resolve(&args, &sources).unwrap_err();

        assert_eq!(
            "unknown endpoint alias \"nope\": known aliases: lab, m9",
            err.to_string()
        );
    }

    #[test]
    fn test_resolve_unknown_alias_without_any_defined_says_so() {
        let dir = scratch_dir("no-aliases");
        let sources = sources(&dir);

        let mut args = bare_args();
        args.endpoint = Some("nope".to_owned());
        let err = resolve(&args, &sources).unwrap_err();

        assert_eq!(
            "unknown endpoint alias \"nope\": no aliases are defined in the configuration file",
            err.to_string()
        );
    }

    #[test]
    fn test_resolve_section_without_endpoint_names_the_alias_and_path() {
        let dir = scratch_dir("section-no-endpoint");
        let sources = sources(&dir);
        let user_path = write(&dir, "user.yaml", "endpoints:\n  lab: { auth: none }\n");

        let mut args = bare_args();
        args.endpoint = Some("lab".to_owned());
        let err = resolve(&args, &sources).unwrap_err();

        assert!(
            matches!(err, Error::SectionMissingEndpoint { ref alias, ref path } if alias == "lab" && *path == user_path)
        );
    }

    #[test]
    fn test_resolve_rejects_alias_chains() {
        let dir = scratch_dir("alias-chain");
        let sources = sources(&dir);
        write(
            &dir,
            "user.yaml",
            "endpoints:\n  a: { endpoint: b }\n  b: { endpoint: grpc://b:1 }\n",
        );

        let mut args = bare_args();
        args.endpoint = Some("a".to_owned());
        let err = resolve(&args, &sources).unwrap_err();

        assert!(matches!(err, Error::AliasChain { ref alias } if alias == "a"));
    }

    #[test]
    fn test_resolve_merges_two_files_per_alias_per_key() {
        let dir = scratch_dir("merge-per-key");
        let sources = sources(&dir);
        write(
            &dir,
            "system.yaml",
            "auth: sshcert\ncert_tag: fleet\nendpoints:\n  lab: { endpoint: grpc://lab:1, auth: sshcert }\n",
        );
        write(&dir, "user.yaml", "endpoints:\n  lab: { auth: none }\n");

        let mut args = bare_args();
        args.endpoint = Some("lab".to_owned());
        let settings = resolve(&args, &sources).unwrap();

        // The alias overrides `auth` in the user file, but its `endpoint`
        // still comes from the system file, and the top-level `cert_tag`
        // is left in effect even though the alias overrides `auth`.
        assert_eq!("grpc://lab:1", settings.endpoint.value);
        assert_eq!(AuthMethod::None, settings.auth.value);
        assert_eq!(Some("fleet".to_owned()), settings.cert_tag.value);
    }

    #[test]
    fn test_resolve_malformed_file_names_path_and_offending_key() {
        let dir = scratch_dir("malformed");
        let sources = sources(&dir);
        let path = write(&dir, "user.yaml", "bogus_key: 1\n");

        let err = resolve(&bare_args(), &sources).unwrap_err();
        let message = err.to_string();

        assert!(message.contains(&path.display().to_string()), "{message}");
        assert!(message.contains("bogus_key"), "{message}");
    }

    #[test]
    fn test_resolve_missing_default_files_are_not_an_error() {
        let dir = scratch_dir("missing-default");
        let sources = sources(&dir);

        assert!(resolve(&bare_args(), &sources).is_ok());
    }

    #[test]
    fn test_resolve_without_a_user_path_only_consults_the_system_file() {
        let dir = scratch_dir("no-user-path");
        write(&dir, "system.yaml", "endpoint: grpc://system:1\n");
        let sources = Sources {
            system_path: dir.join("system.yaml"),
            user_path: None,
            config_override: None,
        };

        let settings = resolve(&bare_args(), &sources).unwrap();

        assert_eq!("grpc://system:1", settings.endpoint.value);
        assert_eq!(1, settings.files.len());
    }

    #[test]
    fn test_resolve_missing_config_override_is_an_error() {
        let dir = scratch_dir("missing-override");
        let sources = Sources {
            system_path: dir.join("system.yaml"),
            user_path: Some(dir.join("user.yaml")),
            config_override: Some(dir.join("missing.yaml")),
        };

        let err = resolve(&bare_args(), &sources).unwrap_err();

        assert!(matches!(err, Error::Read { .. }));
    }

    #[test]
    fn test_resolve_sshcert_without_tag_still_resolves() {
        // `resolve` alone must not fail here: `config show` calls only
        // `resolve` and still needs to report `auth sshcert` with no tag
        // rather than exit outright. `Settings::resolved_auth` is where
        // that combination is finally rejected, at connect time.
        let dir = scratch_dir("sshcert-no-tag");
        let sources = sources(&dir);

        let mut args = bare_args();
        args.auth.auth = Some(AuthMethod::Sshcert);
        let settings = resolve(&args, &sources).unwrap();

        assert_eq!(AuthMethod::Sshcert, settings.auth.value);
        assert_eq!(None, settings.cert_tag.value);
    }

    #[test]
    fn test_settings_resolved_auth_rejects_sshcert_without_tag() {
        let dir = scratch_dir("resolved-auth-no-tag");
        let sources = sources(&dir);

        let mut args = bare_args();
        args.auth.auth = Some(AuthMethod::Sshcert);
        let settings = resolve(&args, &sources).unwrap();

        assert!(matches!(settings.resolved_auth(), Err(Error::MissingCertTag)));
    }

    #[test]
    fn test_settings_resolved_auth_pairs_sshcert_with_its_tag() {
        let dir = scratch_dir("resolved-auth-with-tag");
        let sources = sources(&dir);

        let mut args = bare_args();
        args.auth.auth = Some(AuthMethod::Sshcert);
        args.auth.cert_tag = Some("prod".to_owned());
        let settings = resolve(&args, &sources).unwrap();

        assert_eq!(
            auth::ResolvedAuth::Sshcert { tag: "prod".to_owned() },
            settings.resolved_auth().unwrap()
        );
    }

    #[test]
    fn test_settings_resolved_auth_none_needs_no_tag() {
        let dir = scratch_dir("resolved-auth-none");
        let sources = sources(&dir);

        let settings = resolve(&bare_args(), &sources).unwrap();

        assert_eq!(auth::ResolvedAuth::None, settings.resolved_auth().unwrap());
    }

    #[test]
    fn test_settings_resolved_auth_rejects_a_blank_tag() {
        let dir = scratch_dir("resolved-auth-blank-tag");
        let sources = sources(&dir);

        let mut args = bare_args();
        args.auth.auth = Some(AuthMethod::Sshcert);
        args.auth.cert_tag = Some("   ".to_owned());
        let settings = resolve(&args, &sources).unwrap();

        assert!(matches!(settings.resolved_auth(), Err(Error::MissingCertTag)));
    }

    #[test]
    fn test_resolve_drops_file_tls_for_plaintext_alias() {
        let dir = scratch_dir("drop-file-tls");
        let sources = sources(&dir);
        let system_path = write(
            &dir,
            "system.yaml",
            "ca: /etc/yanet2/tls/ca.pem\nendpoints:\n  lab: { endpoint: grpc://lab:1 }\n",
        );

        let mut args = bare_args();
        args.endpoint = Some("lab".to_owned());
        let settings = resolve(&args, &sources).unwrap();

        assert_eq!(None, settings.ca.value);
        assert_eq!(Origin::Ignored { path: system_path, alias: None }, settings.ca.origin);
    }

    #[test]
    fn test_resolve_keeps_argument_tls_for_plaintext_endpoint() {
        let dir = scratch_dir("keep-argument-tls");
        let sources = sources(&dir);

        let mut args = bare_args();
        args.endpoint = Some("grpc://lab:1".to_owned());
        args.tls.ca = Some(PathBuf::from("/tmp/ca.pem"));
        let settings = resolve(&args, &sources).unwrap();

        assert_eq!(Some(PathBuf::from("/tmp/ca.pem")), settings.ca.value);
        assert_eq!(Origin::Argument, settings.ca.origin);
    }

    #[test]
    fn test_resolve_timeout_shares_the_flag_parsing_rule() {
        let dir = scratch_dir("timeout-rule");
        let sources = sources(&dir);
        write(&dir, "user.yaml", "timeout: 0\n");

        let err = resolve(&bare_args(), &sources).unwrap_err();

        assert!(matches!(err, Error::InvalidTimeout { .. }));
    }

    #[test]
    fn test_resolve_timeout_from_file_parses_fractional_seconds() {
        let dir = scratch_dir("timeout-fractional");
        let sources = sources(&dir);
        write(&dir, "user.yaml", "timeout: 2.5\n");

        let settings = resolve(&bare_args(), &sources).unwrap();

        assert_eq!(Some(Duration::from_millis(2500)), settings.timeout.value);
    }
}
