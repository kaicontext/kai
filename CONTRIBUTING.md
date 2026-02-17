# Contributing to Kai

Thanks for your interest in contributing to Kai. This guide covers how to set up a development environment, run tests, and submit changes.

Have questions? Join us on [Slack](https://join.slack.com/t/kailayer/shared_invite/zt-3q8ulczwl-vkZ05GQH~kwudonmH53hGg).

## Development Setup

### Prerequisites

- Go 1.24+
- GCC or Clang (for CGO — tree-sitter and SQLite)
- Node.js 20+ (for frontend and benchmark repos)
- Git

### Build

```bash
# CLI
cd kai-cli
CGO_ENABLED=1 go build ./cmd/kai

# Core library
cd kai-core
CGO_ENABLED=1 go build ./...
```

### Run Tests

```bash
# All tests
cd kai-cli && CGO_ENABLED=1 go test ./...
cd kai-core && CGO_ENABLED=1 go test ./...

# Regression suite only
cd kai-cli && CGO_ENABLED=1 go test ./cmd/kai/ \
  -run "TestGraph_|TestSelection_|TestFalseNeg_|TestShadow_|TestFlaky_|TestCLI_|TestPerf_" \
  -v -count=1

# Benchmarks
./bench/run_repos.sh --mode both -n 3
```

## Project Structure

```
kai-cli/           CLI binary (commands, CI plan, shadow mode)
kai-core/          Core engine (tree-sitter parsing, graph, snapshots)
bench/             Benchmark harness
docs/              Open-core boundary, licensing, and architecture docs
scripts/           Enforcement and utility scripts
```

## Making Changes

### Before You Start

- Check existing issues to avoid duplicating work
- For large changes, open an issue first to discuss the approach
- Keep PRs focused — one logical change per PR

### Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- No unnecessary abstractions — simpler is better
- Tests go next to the code they test (`*_test.go`)
- Database migrations need both SQLite and PostgreSQL versions

### Commit Messages

Write clear, concise commit messages:

```
Fix barrel import re-export handling in extractImports

export { x } from './y' was not producing IMPORTS edges because
extractImports only handled import_statement and call_expression.
Added export_statement case with parseReexportSource helper.
```

- First line: imperative mood, under 72 characters
- Body: explain *why*, not just *what*

### Testing

- Add tests for new functionality
- Run the regression suite before submitting
- For graph/selection changes, verify zero false negatives:
  ```bash
  cd kai-cli && CGO_ENABLED=1 go test ./cmd/kai/ -run "TestFalseNeg_" -v
  ```


## Developer Certificate of Origin (DCO)

All contributions to Kai must be signed off under the [Developer Certificate of Origin](https://developercertificate.org/). This certifies that you have the right to submit the code and that it can be distributed under the Apache 2.0 license.

Add a `Signed-off-by` line to your commits:

```bash
git commit -s -m "Fix barrel import re-export handling"
```

This adds a line like:

```
Signed-off-by: Your Name <your.email@example.com>
```

You can configure Git to do this automatically:

```bash
git config --global format.signoff true
```

PRs without DCO sign-off will not be merged.

## Copyright Headers

Source files should include an SPDX copyright header. Run the check script to verify:

```bash
./scripts/check-copyright-headers.sh          # Check
./scripts/check-copyright-headers.sh --fix     # Auto-add missing headers
```

## Submitting Changes

1. Fork the repository
2. Create a branch from `main`
3. Make your changes with tests
4. Sign off your commits (`git commit -s`)
5. Ensure CI passes: `go test ./...` in each module
6. Open a PR against `main`
7. Fill out the PR template

## Reporting Issues

- Use the [bug report template](.github/ISSUE_TEMPLATE/bug_report.yml) for bugs
- Use the [feature request template](.github/ISSUE_TEMPLATE/feature_request.yml) for ideas
- Include reproduction steps, expected vs actual behavior, and environment details

## Questions

Open a discussion or issue — we're happy to help.
