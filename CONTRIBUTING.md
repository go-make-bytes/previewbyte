# Contributing

Thank you for considering a contribution. Bug reports, fixes and improvements are
welcome. For anything that could be exploited, use the private route in
[SECURITY.md](SECURITY.md) — never a public issue.

For anything larger than a small fix, please open an issue first and describe what
you want to change and why. It protects your time: a change that fights the service's
design is better redirected before it is written than after.

## Building and testing

You need the Go toolchain at the version named in [go.mod](go.mod); no CGO, no external
services. The gate a change must pass is the same one CI runs on every push:

```sh
go build ./...
go vet ./...
go test -race -count=1 ./...
```

The render tests drive the **real** embedded PDF engine in-process (WebAssembly, no
Docker, no network), so a change to rendering is tested against the engine, not a mock.
Three more checks run in CI and are worth running locally before you push:

- **Lint** — `golangci-lint run`, at the version pinned in
  [.github/workflows/ci.yml](.github/workflows/ci.yml) (the repo's
  [.golangci.yml](.golangci.yml) carries the configuration).
- **Vulnerabilities** — `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`.
- **Fuzzing** — CI runs each fuzz target for a short burst; a change to input handling
  (content sniffing, caps, the render entry points) should keep the existing targets green
  and, where it adds a new untrusted-input path, add a target for it.

The committed tree must already be tidy: CI runs `go mod tidy -diff` and fails if it
would change anything, so run `go mod tidy` after touching dependencies. All Go code
is `gofmt`-formatted.

## Proposing a change

- Work on a branch and open a pull request against `develop`.
- **Sign off every commit.** This project uses the
  [Developer Certificate of Origin](https://developercertificate.org/): by adding a
  `Signed-off-by: Your Name <you@example.org>` line you certify that you wrote the change or
  otherwise have the right to submit it under this project's licence. `git commit -s` adds the
  line for you; the name and address must match the commit author. A pull request whose commits
  lack it fails the DCO check and cannot be merged.
- Keep the change focused: one concern per pull request.
- A change in behaviour comes with a test that fails without it. A change that touches how
  untrusted bytes are handled comes with a negative test — the input that must be refused.
- Match the style around you — naming, error handling, comment density. Comments
  explain what and why in plain domain terms.
- Pull requests also run a dependency review; a new dependency needs a reason the
  standard library or the existing ones cannot cover — and for this service in particular,
  a new parser or decoder is a new attack surface and needs the case made.

## Licence

This project is licensed under the MIT License (see [LICENSE](LICENSE)). By submitting
a contribution you agree that it is provided under the same licence.
