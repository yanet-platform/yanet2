//! Negative builds of the real `layout!` declarations: a copy of this crate
//! with one classification removed or wrong must fail to compile, naming
//! the field.
//!
//! The macro is crate-private, so the check is exercised by building a
//! mutated copy of the crate rather than by declaring layouts elsewhere.

use std::{
    fs,
    path::{Path, PathBuf},
    process::Command,
};

// The library is visible to every test target.
use yanet_sys as _;

fn copy_dir(from: &Path, to: &Path) {
    fs::create_dir_all(to).unwrap();
    for entry in fs::read_dir(from).unwrap() {
        let entry = entry.unwrap();
        let target = to.join(entry.file_name());
        if entry.file_type().unwrap().is_dir() {
            copy_dir(&entry.path(), &target);
        } else {
            fs::copy(entry.path(), target).unwrap();
        }
    }
}

/// Scratch workspace holding a copy of this crate, sharing one target
/// directory across cases so dependencies build once.
struct Scratch {
    poc: PathBuf,
    work: PathBuf,
    views: String,
}

impl Scratch {
    fn new() -> Self {
        let crate_dir = Path::new(env!("CARGO_MANIFEST_DIR"));
        let poc = crate_dir.parent().unwrap().to_path_buf();
        let work = poc.join("target/negative-build/workspace");
        let _ = fs::remove_dir_all(&work);
        let sys = work.join("yanet-sys");
        for dir in ["src", "shim"] {
            copy_dir(&crate_dir.join(dir), &sys.join(dir));
        }
        for file in ["Cargo.toml", "build.rs"] {
            fs::copy(crate_dir.join(file), sys.join(file)).unwrap();
        }
        fs::copy(poc.join("Cargo.lock"), work.join("Cargo.lock")).unwrap();
        let manifest = fs::read_to_string(poc.join("Cargo.toml")).unwrap();
        let manifest = manifest
            .lines()
            .map(|line| {
                if line.starts_with("members") {
                    "members = [\"yanet-sys\"]"
                } else {
                    line
                }
            })
            .collect::<Vec<_>>()
            .join("\n");
        fs::write(work.join("Cargo.toml"), manifest).unwrap();
        let views = fs::read_to_string(crate_dir.join("src/views.rs")).unwrap();
        Self { poc, work, views }
    }

    /// Builds the copy with `views.rs` rewritten; returns success and the
    /// compiler output.
    fn build(&self, replace: &str, with: &str) -> (bool, String) {
        assert!(self.views.contains(replace), "views.rs no longer contains {replace:?}");
        fs::write(
            self.work.join("yanet-sys/src/views.rs"),
            self.views.replacen(replace, with, 1),
        )
        .unwrap();
        let output = Command::new(env!("CARGO"))
            .args(["build", "--offline", "-p", "yanet-sys", "--lib"])
            .current_dir(&self.work)
            .env("YANET_ROOT", self.poc.join("../.."))
            .env("CARGO_TARGET_DIR", self.poc.join("target/negative-build/target"))
            .output()
            .expect("run cargo");
        (
            output.status.success(),
            String::from_utf8_lossy(&output.stderr).into_owned(),
        )
    }
}

/// Verifies that the unmodified copy builds and that every broken
/// classification fails the build with a message naming the field.
#[test]
#[cfg_attr(miri, ignore = "invokes cargo")]
fn test_layout_build_fails_on_bad_classification() {
    let scratch = Scratch::new();
    let (ok, stderr) = scratch.build("", "");
    assert!(ok, "the unmodified copy must build:\n{stderr}");

    let cases = [
        (
            "unclassified relative pointer",
            "        rel pages: RelPtr<RelRef<LpmChunk>>,\n",
            "",
            "pointer-carrying field `lpm.pages` is not classified",
        ),
        (
            "unclassified embedded aggregate carrying pointers",
            "        opaque memory_context: Opaque<bindings::memory_context>,\n",
            "",
            "pointer-carrying field `lpm.memory_context` is not classified",
        ),
        (
            "unclassified absolute pointer",
            "        abs abs_object_links: COpaque,\n",
            "",
            "pointer-carrying field `module_ectx.abs_object_links` is not classified",
        ),
        (
            "pointer declared plain",
            "        rel cp_module: RelPtr<COpaque>,\n",
            "        plain cp_module: u64,\n",
            "`module_ectx.cp_module` cannot be declared plain",
        ),
        (
            "plain field declared as a pointer",
            "        plain page_count: usize,\n",
            "        rel page_count: RelPtr<COpaque>,\n",
            "`lpm.page_count` cannot be declared rel",
        ),
    ];
    for (name, replace, with, expected) in cases {
        let (ok, stderr) = scratch.build(replace, with);
        assert!(!ok, "{name}: the build must fail");
        assert!(stderr.contains(expected), "{name}: expected {expected:?} in:\n{stderr}");
    }
}
