# CI image

`ci/Dockerfile` defines the toolchain image that native CI jobs run in: the system packages their C, Go and Rust builds need, plus Go, Rust and the protobuf code generators at the versions the workflows pin. The `Build and push CI image` workflow builds it natively on amd64 and arm64 runners, probes it, and on `main` publishes one multi-architecture reference to `ghcr.io/yanet-platform/yanet2-ci`.

## Why jobs pin a digest

A job that names the image by digest runs exactly the toolchain behind that digest. A rebuild, an Ubuntu package update or a hosted runner image rollout changes nothing until a new digest is pinned, and the compiler caches of those jobs stay valid for as long as the pin does.

## Bumping the image

1. Change `ci/Dockerfile`: a package, a Go or Rust version, or the base image digest. The pull request builds and probes both architectures and lists the tool versions in its job summary, but publishes nothing.
2. After the change reaches `main`, the workflow publishes the image and prints the new reference in its job summary. A manual run on `main` rebuilds it with the current versions of the listed packages and their dependencies; the rest of the base image changes only with its digest. Runs on other branches build and probe but never publish.
3. Open a follow-up pull request that pins the new reference in the jobs that run in the image. Its CI is the first run on the new toolchain, and each job's first run on it starts with cold compiler caches.

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

Go, Rust and the protobuf generators must match what the jobs on the hosted runner install (the Go setup and Rust toolchain steps, `.github/actions/protoc-plugins`) for as long as both kinds of jobs exist.
