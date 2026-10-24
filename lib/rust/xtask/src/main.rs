//! Workspace automation: `cargo xtask miri [stacked|tree|all]`.

use std::{
    env, fmt,
    io::{self, Read},
    path::Path,
    process::{Command, ExitCode, Stdio},
    thread,
};

use regex::Regex;

/// Test binaries Miri runs as positive suites: a package for its unit tests,
/// a package and a test name for its integration test.
const SUITES: &[(&str, Option<&str>)] = &[
    ("yanet-shm", None),
    ("yanet-shm", Some("provenance")),
    ("yanet-sys", Some("lpm")),
    ("yanet-packet", None),
];

/// An ignored test that Miri must report as undefined behavior or accept.
struct Case {
    package: &'static str,
    target: &'static str,
    test: &'static str,
    stacked: Outcome,
    tree: Outcome,
    /// The report of the undefined behavior, so that a case hitting some
    /// other one first does not count.
    ub: &'static str,
}

impl Case {
    fn expected(&self, model: Model) -> Outcome {
        match model {
            Model::Stacked => self.stacked,
            Model::Tree => self.tree,
        }
    }
}

const CASES: &[Case] = &[
    Case {
        package: "yanet-shm",
        target: "provenance",
        test: "test_ub_freed_block",
        stacked: Outcome::Ub,
        tree: Outcome::Ub,
        ub: "type &Item: encountered a dangling reference",
    },
    Case {
        package: "yanet-shm",
        target: "provenance",
        test: "test_ub_cross_block_read",
        stacked: Outcome::Ub,
        tree: Outcome::Ub,
        ub: r"type &\[Item\]: encountered a dangling reference",
    },
    Case {
        package: "yanet-shm",
        target: "provenance",
        test: "test_ub_reference_over_c_mutated_bytes",
        stacked: Outcome::Ub,
        tree: Outcome::Ub,
        ub: r"(retag from|reborrow through) .*\[0x8\]",
    },
    Case {
        package: "yanet-shm",
        target: "provenance",
        test: "test_ub_slot_reference_provenance",
        stacked: Outcome::Ub,
        tree: Outcome::Pass,
        ub: "read access using .* does not exist in the borrow stack",
    },
];

#[derive(Clone, Copy, PartialEq, Eq)]
enum Outcome {
    Pass,
    Ub,
    /// Any other failure, another undefined behavior among them.
    Error,
}

impl fmt::Display for Outcome {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.pad(match self {
            Self::Pass => "pass",
            Self::Ub => "ub",
            Self::Error => "error",
        })
    }
}

#[derive(Clone, Copy)]
enum Model {
    Stacked,
    Tree,
}

impl Model {
    fn name(self) -> &'static str {
        match self {
            Self::Stacked => "stacked",
            Self::Tree => "tree",
        }
    }

    fn flags(self) -> &'static str {
        match self {
            Self::Stacked => "",
            Self::Tree => "-Zmiri-tree-borrows",
        }
    }
}

/// The run of one model: the suite output and the case report.
struct Report {
    suites: String,
    cases: String,
    failures: usize,
}

/// How to invoke Miri, from the environment.
struct Miri {
    toolchain: String,
    cargo_flags: String,
}

impl Miri {
    /// Runs `cargo miri nextest run` for the model; returns whether it
    /// succeeded and its combined output.
    fn nextest(&self, model: Model, args: &[String]) -> (bool, String) {
        self.try_nextest(model, args)
            .unwrap_or_else(|err| (false, format!("cannot run cargo: {err}\n")))
    }

    fn try_nextest(&self, model: Model, args: &[String]) -> io::Result<(bool, String)> {
        // One pipe for both streams, so that their lines keep their order.
        let (mut reader, writer) = io::pipe()?;
        let mut cmd = Command::new("cargo");
        cmd.current_dir(Path::new(env!("CARGO_MANIFEST_DIR")).join(".."))
            .arg(format!("+{}", self.toolchain))
            .args(["miri", "nextest", "run"])
            .args(self.cargo_flags.split_whitespace())
            .args(args)
            .env("MIRIFLAGS", format!("-Zmiri-strict-provenance {}", model.flags()))
            .stdout(Stdio::from(writer.try_clone()?))
            .stderr(Stdio::from(writer));
        let mut child = cmd.spawn()?;
        // The command holds the write ends; the reader sees the end only
        // once they are closed.
        drop(cmd);
        let mut output = Vec::new();
        reader.read_to_end(&mut output)?;
        let status = child.wait()?;
        Ok((status.success(), String::from_utf8_lossy(&output).into_owned()))
    }
}

fn main() -> ExitCode {
    let mut args = env::args().skip(1);
    let models: &[Model] = match (args.next().as_deref(), args.next().as_deref(), args.next().as_deref()) {
        (Some("miri"), None | Some("all"), None) => &[Model::Stacked, Model::Tree],
        (Some("miri"), Some("stacked"), None) => &[Model::Stacked],
        (Some("miri"), Some("tree"), None) => &[Model::Tree],
        _ => {
            eprintln!("usage: cargo xtask miri [stacked|tree|all]");
            return ExitCode::from(2);
        }
    };
    match miri(models) {
        Ok(failures) => ExitCode::from(u8::try_from(failures).unwrap_or(u8::MAX)),
        Err(err) => {
            eprintln!("error: {err}");
            ExitCode::from(2)
        }
    }
}

/// Runs the models concurrently and prints their reports; returns the number
/// of failed suites and mismatched cases.
fn miri(models: &[Model]) -> Result<usize, String> {
    let miri = Miri {
        toolchain: env::var("MIRI_TOOLCHAIN").unwrap_or_else(|_| "nightly-2026-10-22".into()),
        cargo_flags: env::var("MIRI_CARGO_FLAGS")
            .ok()
            .filter(|flags| !flags.is_empty())
            .unwrap_or_else(|| "--offline".into()),
    };
    let jobs = match env::var("MIRI_JOBS") {
        Ok(jobs) => jobs
            .parse()
            .map_err(|err| format!("invalid MIRI_JOBS {jobs:?}: {err}"))?,
        Err(_) => thread::available_parallelism().map_or(2, |cores| cores.get() / 2),
    };
    let threads = (jobs / models.len()).max(1);
    let miri = &miri;

    let reports: Vec<Report> = thread::scope(|scope| {
        let runs: Vec<_> = models
            .iter()
            .map(|&model| scope.spawn(move || run_model(miri, model, threads)))
            .collect();
        runs.into_iter()
            .map(|run| run.join().expect("model thread panicked"))
            .collect()
    });

    let mut failures = 0;
    for (model, report) in models.iter().zip(reports) {
        failures += report.failures;
        println!("== {} borrows: positive suites\n{}", model.name(), report.suites);
        println!("== {} borrows: expected outcomes\n{}", model.name(), report.cases);
    }
    Ok(failures)
}

/// Runs the positive suites of a model in one nextest process and every case
/// in a process of its own, all concurrently.
fn run_model(miri: &Miri, model: Model, threads: usize) -> Report {
    let mut packages = Vec::new();
    let mut targets = vec!["--lib".to_owned()];
    let mut filter = Vec::new();
    for &(package, test) in SUITES {
        if !packages.contains(&package) {
            packages.push(package);
        }
        match test {
            Some(test) => {
                targets.extend(["--test".to_owned(), test.to_owned()]);
                filter.push(format!("binary_id(={package}::{test})"));
            }
            None => filter.push(format!("binary_id(={package})")),
        }
    }
    let mut suite_args: Vec<String> = packages
        .iter()
        .flat_map(|package| ["-p", package])
        .map(str::to_owned)
        .collect();
    suite_args.extend(targets);
    suite_args.extend([
        "--test-threads".to_owned(),
        threads.to_string(),
        "-E".to_owned(),
        filter.join(" | "),
    ]);

    thread::scope(|scope| {
        let suites = scope.spawn(|| miri.nextest(model, &suite_args));
        let cases: Vec<_> = CASES
            .iter()
            .map(|case| scope.spawn(|| run_case(miri, model, case)))
            .collect();

        let (ok, suites) = suites.join().expect("suites thread panicked");
        let mut report = Report {
            suites,
            cases: String::new(),
            failures: usize::from(!ok),
        };
        for (case, run) in CASES.iter().zip(cases) {
            let (outcome, output) = run.join().expect("case thread panicked");
            let expected = case.expected(model);
            let verdict = if outcome == expected { "ok" } else { "MISMATCH" };
            report.failures += usize::from(outcome != expected);
            report.cases += &format!(
                "{:<45} {:<8} expected {expected:<4} got {outcome:<5} {verdict}\n",
                case.test,
                model.name()
            );
            match outcome {
                Outcome::Ub => {
                    if let Some(line) = output.lines().find(|line| line.contains("error: Undefined Behavior")) {
                        report.cases += &format!("    {line}\n");
                    }
                }
                Outcome::Error => {
                    let lines: Vec<_> = output.lines().collect();
                    for line in &lines[lines.len().saturating_sub(20)..] {
                        report.cases += &format!("    {line}\n");
                    }
                }
                Outcome::Pass => {}
            }
        }
        report
    })
}

/// Runs the case and classifies its outcome.
fn run_case(miri: &Miri, model: Model, case: &Case) -> (Outcome, String) {
    let Case { package, target, test, ub, .. } = case;
    let args = [
        "-p",
        package,
        "--test",
        target,
        "--run-ignored",
        "only",
        "--no-tests",
        "fail",
        "-E",
    ]
    .map(str::to_owned)
    .into_iter()
    .chain([format!("binary_id(={package}::{target}) & test(={test})")])
    .collect::<Vec<_>>();
    let (ok, output) = miri.nextest(model, &args);
    let ub = Regex::new(&format!("error: Undefined Behavior: .*({ub})")).expect("invalid UB pattern");
    let outcome = if ok {
        Outcome::Pass
    } else if ub.is_match(&output) {
        Outcome::Ub
    } else {
        Outcome::Error
    };
    (outcome, output)
}
