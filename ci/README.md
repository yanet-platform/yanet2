# CI image

`ci/Dockerfile` defines the toolchain image that native CI jobs run in: the system packages their C, Go and Rust builds need, plus Go, Rust and the protobuf code generators at the versions the workflows pin. The `Build and push CI image` workflow builds it natively on amd64 and arm64 runners, probes it, and on `main` publishes one multi-architecture reference to `ghcr.io/yanet-platform/yanet2-ci`.

## Why jobs pin a digest

A job that names the image by digest runs exactly the toolchain behind that digest. A rebuild, an Ubuntu package update or a hosted runner image rollout changes nothing until a new digest is pinned, and the compiler caches of those jobs stay valid for as long as the pin does.

## Bumping the image

1. Change `ci/Dockerfile`: a package, a Go version (together with `ci/go-version`), a Rust version, or the base image digest. Dependabot proposes new digests of the base image weekly; the Ubuntu release changes only by hand. The pull request builds and probes both architectures and lists the tool versions in its job summary, but publishes nothing.
2. After the change reaches `main`, the workflow publishes the image and prints the new reference in its job summary. A manual run on `main` rebuilds it with the current versions of the listed packages and their dependencies; the rest of the base image changes only with its digest. Runs on other branches build and probe but never publish.
3. Open a follow-up pull request that pins the new reference wherever the image is pinned (below). Its CI is the first run on the new toolchain, and the compiler caches of each job stay warm only for what the bump left unchanged.

## Where the image is pinned

The native jobs of `.github/workflows/build.yml` and `.github/workflows/go-race.yml`, and the jobs of `.github/workflows/rust-sdk.yml` and `.github/workflows/rust-miri.yml` that compile the Rust SDK, name the image by digest in their `container:` key and pull it anonymously. A bump replaces the old digest in every reference at once:

```
grep -rl 'yanet2-ci@sha256:' .github/workflows | xargs sed -i 's/<old digest>/<new digest>/'
```

The `CI image pins agree` check fails a pull request, and a push to `main`, whose workflows reference the image by anything but one digest.

The Go build cache of these jobs is keyed on the image's package set, so a rebuild that leaves the packages unchanged keeps it.

## The gotcha

Editing `ci/Dockerfile` changes nothing that CI runs until step 3. A pull request that adds a package together with the code that needs it stays red, because its jobs still run the previously pinned image: land the image change first, then the code that depends on it.

## Adding a package

Add it to the package list in `ci/Dockerfile` and follow the bump steps. The hosted runner image ships many tools this image does not; a build that passes on the runner but fails in the image with a missing tool usually needs one more package here, which is how bison and flex for the libpcap subproject were found.

## Reading a published digest

```
docker buildx imagetools inspect ghcr.io/yanet-platform/yanet2-ci:sha-<full commit SHA> --format '{{.Manifest.Digest}}'
```

An image published from `main` is tagged with the full SHA of the commit it was built from. The package is public and needs no authentication.

## Notes

`ci/go-version` names the Go that every Go setup step of the workflows installs, and the image build fails when the image carries a different one. A Go bump therefore changes `ci/go-version` and `ci/Dockerfile` (version and checksums) in the same pull request; until the follow-up pin lands, the jobs in the image still run the previous Go. A manual release rerun that builds an older tag takes the file from the workflow's own commit, since the tag may predate it.

`LLVM_VERSION` in `ci/Dockerfile` names the one LLVM the image carries, from the LLVM archive at apt.llvm.org, which `ci/llvm-snapshot.gpg.key` verifies (fingerprint `6084 F3CF 814B 57C1 CF12 EFD5 15CF 4D18 AF4F 7421`). The archive builds snapshots of the release branch, so every image build takes its newest one; the digest pin keeps a job on one build. The unversioned `clang`, `clang++` and `llvm-symbolizer` point at it, but bindgen in `rust-sdk.yml` and `rust-miri.yml` names its versioned paths, as does `lib/rust/README.md`, so the pull request that pins an LLVM bump updates them too.

Rust and the protobuf generators must match what the jobs on the hosted runner install (the Rust toolchain steps, `.github/actions/protoc-plugins`) for as long as both kinds of jobs exist.

The jobs in the image do not run under act: act runs JavaScript actions inside the job container, and the image carries no Node.js.
