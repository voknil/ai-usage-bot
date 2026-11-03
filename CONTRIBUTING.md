# Contributing

Small, focused pull requests are easiest to review.

## Setup

You need Go 1.22+ and [golangci-lint](https://golangci-lint.run/) v2.

```bash
git clone https://github.com/voknil/ai-usage-bot && cd ai-usage-bot
make check
```

`make check` runs what CI runs: `go vet`, `golangci-lint` and the tests with the race detector.

## Guidelines

- No third-party dependencies unless there is a strong reason.
- Parsers of external output (CLI text, API JSON) get a test with a real sample.
- Changes to the message layout update the golden test in `internal/render`.
- Never put tokens, chat IDs or real account data in tests or fixtures.
- Add a `CHANGELOG.md` line under *Unreleased* for user-visible changes.
- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat:`, `fix:`, `docs:`, `chore:`).

To add a provider, see *Adding a provider* in [docs/architecture.md](docs/architecture.md).

Report vulnerabilities privately, as described in [SECURITY.md](SECURITY.md).
