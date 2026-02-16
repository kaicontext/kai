#!/usr/bin/env python3
"""Compute statistics from Kai benchmark iteration results.

Usage: python3 stats.py <results-dir>

Reads iter_*.json files from the results directory, computes median, p90,
mean, min, max, and stddev for each timing phase, and writes:
  - summary.json   (machine-readable)
  - Console table   (human-readable, to stdout)
"""

import json
import math
import os
import sys
from pathlib import Path


def percentile(sorted_data, p):
    """Compute the p-th percentile (0-100) of sorted data."""
    if not sorted_data:
        return 0
    k = (len(sorted_data) - 1) * (p / 100.0)
    f = math.floor(k)
    c = math.ceil(k)
    if f == c:
        return sorted_data[int(k)]
    return sorted_data[int(f)] * (c - k) + sorted_data[int(c)] * (k - f)


def median(data):
    return percentile(data, 50)


def p90(data):
    return percentile(data, 90)


def mean(data):
    return sum(data) / len(data) if data else 0


def stddev(data):
    if len(data) < 2:
        return 0
    m = mean(data)
    return math.sqrt(sum((x - m) ** 2 for x in data) / (len(data) - 1))


def load_iterations(results_dir):
    """Load all iter_N.json files (skip warmup files)."""
    iterations = []
    for f in sorted(Path(results_dir).glob("iter_*.json")):
        if "warmup" in f.name:
            continue
        with open(f) as fh:
            iterations.append(json.load(fh))
    return iterations


def compute_phase_stats(iterations, phase):
    """Compute stats for a single timing phase."""
    values = sorted([it["timings_ms"][phase] for it in iterations])
    if not values:
        return {}
    return {
        "n": len(values),
        "min": min(values),
        "max": max(values),
        "mean": round(mean(values), 1),
        "median": round(median(values), 1),
        "p90": round(p90(values), 1),
        "stddev": round(stddev(values), 1),
    }


def compute_summary(iterations):
    """Compute full summary across all phases."""
    phases = ["init", "capture_base", "capture_head", "ci_plan", "total"]
    timings = {}
    for phase in phases:
        timings[phase] = compute_phase_stats(iterations, phase)

    # Graph stats from last iteration (all should be identical)
    graph = iterations[-1].get("graph", {}) if iterations else {}
    plan = iterations[-1].get("plan", {}) if iterations else {}
    fixture = iterations[-1].get("fixture", {}) if iterations else {}

    return {
        "iterations": len(iterations),
        "timings_ms": timings,
        "graph": graph,
        "plan": plan,
        "fixture": fixture,
    }


def format_table(summary):
    """Format a human-readable console table."""
    lines = []
    lines.append("=== Kai Benchmark Results ===")
    lines.append("")

    fixture = summary.get("fixture", {})
    lines.append(
        f"Fixture: {fixture.get('files', '?')} files, "
        f"{fixture.get('tests', '?')} tests, "
        f"{fixture.get('modified_pct', '?')}% modified"
    )
    lines.append(f"Iterations: {summary['iterations']}")

    graph = summary.get("graph", {})
    lines.append(f"Graph: {graph.get('nodes', '?')} nodes, {graph.get('edges', '?')} edges")
    lines.append("")

    # Timing table
    header = f"{'Phase':<16} {'Median':>8} {'p90':>8} {'Mean':>8} {'StdDev':>8} {'Min':>8} {'Max':>8}"
    lines.append(header)
    lines.append("-" * len(header))

    for phase in ["init", "capture_base", "capture_head", "ci_plan", "total"]:
        stats = summary["timings_ms"].get(phase, {})
        if not stats:
            continue
        label = phase.replace("_", " ").title()
        lines.append(
            f"{label:<16} "
            f"{stats['median']:>7.0f}ms"
            f"{stats['p90']:>7.0f}ms"
            f"{stats['mean']:>7.0f}ms"
            f"{stats['stddev']:>7.0f}ms"
            f"{stats['min']:>7.0f}ms"
            f"{stats['max']:>7.0f}ms"
        )

    lines.append("")

    plan = summary.get("plan", {})
    if plan:
        lines.append(
            f"CI Plan: mode={plan.get('mode', '?')}, "
            f"selected={plan.get('tests_selected', '?')}/{plan.get('tests_total', '?')} tests, "
            f"confidence={plan.get('confidence', '?')}"
        )

    return "\n".join(lines)


def main():
    if len(sys.argv) < 2:
        print("Usage: python3 stats.py <results-dir>", file=sys.stderr)
        sys.exit(1)

    results_dir = sys.argv[1]
    iterations = load_iterations(results_dir)

    if not iterations:
        print(f"No iteration files found in {results_dir}", file=sys.stderr)
        sys.exit(1)

    summary = compute_summary(iterations)

    # Write JSON
    summary_file = os.path.join(results_dir, "summary.json")
    with open(summary_file, "w") as f:
        json.dump(summary, f, indent=2)

    # Print console table
    print(format_table(summary))


if __name__ == "__main__":
    main()
