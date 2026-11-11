# Building mybckchk

Use Go 1.26.11 or a newer patched release for deployment. The Go module
suggests Go 1.26.11 for local builds. From the repository root, run `make` to build
`mybckchk` for your current platform. Go downloads the pinned dependencies
as needed. Use `make deps` to download them separately.

Use `make linux` or `make darwin` to target another operating system.
Set `GOARCH` when targeting a different architecture. Each build writes
`mybckchk`. Use `make clean` to remove the binary.

Run `go vet ./...` and `go test -race ./...` before submitting changes.
