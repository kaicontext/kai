# Kai Benchmark Harness

Reproducible benchmarks for Kai's core operations: init, capture, and CI plan generation.

## Quick Start

```bash
# Run with defaults (medium fixture, 5 iterations, 1 warmup)
./bench/run.sh

# Small fixture, 3 iterations
./bench/run.sh -s small -n 3

# Large fixture, 10 iterations, skip build
./bench/run.sh -s large -n 10 --skip-build -k ./kai-cli/kai

# From Makefile
cd kai-cli && make bench
```

## Options

| Flag | Default | Description |
|------|---------|-------------|
| `-n N` | 5 | Number of measured iterations |
| `-w N` | 1 | Number of warmup iterations (discarded) |
| `-s SIZE` | medium | Fixture size: `small` (20 files), `medium` (100), `large` (500) |
| `-o DIR` | `bench/results/<date>` | Output directory |
| `-k PATH` | (builds from source) | Path to pre-built kai binary |
| `--skip-build` | false | Skip building kai binary |
| `--json-only` | false | Suppress console output, only write JSON |

## What It Measures

Each iteration creates a fresh fixture project and times four phases:

1. **init** — `kai init` (creates `.kai/` directory and database)
2. **capture_base** — `kai capture .` on the initial fixture
3. **capture_head** — `kai capture .` after modifying files (simulates a code change)
4. **ci_plan** — `kai ci plan` (generates selective test plan from the diff)

## Fixture Design

The fixture is a synthetic TypeScript project with controlled dependency chains:

- **Source files** organized in 10 modules (`src/mod0/` through `src/mod9/`)
- Each file imports from the previous file, creating a linear dependency chain
- **Test files** target evenly-spaced source files
- A percentage of files are modified between captures to simulate a realistic change

| Size | Source Files | Test Files | Modified % |
|------|-------------|------------|------------|
| small | 20 | 5 | 20% |
| medium | 100 | 15 | 10% |
| large | 500 | 30 | 5% |

## Output

Results are written to `bench/results/<timestamp>/`:

```
bench/results/20260216-143022/
├── environment.json    # Machine info, Go version, kai commit, params
├── iter_1.json         # Per-iteration timings, graph stats, plan metrics
├── iter_2.json
├── ...
└── summary.json        # Aggregated stats: median, p90, mean, stddev, min, max
```

### summary.json schema

```json
{
  "iterations": 5,
  "timings_ms": {
    "init":         { "n": 5, "min": 12, "max": 18, "mean": 14.2, "median": 14, "p90": 17, "stddev": 2.1 },
    "capture_base": { "...": "..." },
    "capture_head": { "...": "..." },
    "ci_plan":      { "...": "..." },
    "total":        { "...": "..." }
  },
  "graph": { "nodes": 312, "edges": 498 },
  "plan":  { "mode": "selective", "tests_selected": 3, "tests_total": 15, "confidence": 0.95 },
  "fixture": { "files": 100, "tests": 15, "modified_pct": 10 }
}
```

## Docker Runner

For fully reproducible results isolated from host environment:

```bash
docker build -t kai-bench -f bench/Dockerfile.bench .
docker run --rm -v $(pwd)/bench/results:/bench/results kai-bench -s medium -n 10
```

## Methodology

### Warmup

Warmup iterations prime filesystem caches, Go runtime, and SQLite page cache. Their results are discarded. Default: 1 warmup iteration.

### Statistical Approach

- **Median** — primary metric, robust to outliers
- **p90** — tail latency indicator
- **Mean + StdDev** — for detecting high variance (noisy environment)
- **Min/Max** — bounds for sanity checking

### Reproducibility

Each run captures:
- OS, CPU, core count, architecture
- Go version
- Kai binary version, git commit, branch, dirty state
- All benchmark parameters

To compare runs, use the JSON output and control for environment differences.

### Limitations

- Synthetic fixtures don't perfectly represent real-world codebases
- Wall-clock timing includes process startup overhead
- Docker runner uses emulated I/O which may differ from native performance
- CI plan quality depends on the fixture's dependency structure
