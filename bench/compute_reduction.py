#!/usr/bin/env python3
"""Compute % reduction metrics from baseline + Kai benchmark results.

Usage:
    python3 bench/compute_reduction.py <results-dir>
    python3 bench/compute_reduction.py bench/results/repos-20260216-124125

Reads the summary.json produced by repo_stats.py (or raw iteration files)
and cross-references baseline vs Kai medians to produce:
  - Per-repo reduction metrics
  - Aggregate totals
  - Console table (publishable)
  - summary.csv (raw data)

If summary.json doesn't exist yet, runs repo_stats.py first.
"""

import csv
import json
import math
import os
import subprocess
import sys
from pathlib import Path


def median(data):
    if not data:
        return 0
    s = sorted(data)
    n = len(s)
    if n % 2 == 0:
        return (s[n // 2 - 1] + s[n // 2]) / 2
    return s[n // 2]


def load_summary(results_dir):
    """Load or generate summary.json."""
    summary_file = os.path.join(results_dir, "summary.json")

    if not os.path.exists(summary_file):
        # Try to generate it
        stats_script = os.path.join(os.path.dirname(__file__), "repo_stats.py")
        if os.path.exists(stats_script):
            print(f"Generating summary.json via repo_stats.py...", file=sys.stderr)
            subprocess.run([sys.executable, stats_script, results_dir], check=True)
        else:
            print(f"Error: {summary_file} not found and repo_stats.py unavailable", file=sys.stderr)
            sys.exit(1)

    with open(summary_file) as f:
        return json.load(f)


def load_raw_iterations(results_dir):
    """Load raw iteration files for fallback-aware computation."""
    baseline = {}  # repo -> [iteration]
    kai = {}       # repo -> [iteration]

    for f in sorted(Path(results_dir).glob("*_iter_*.json")):
        if "warmup" in f.name:
            continue
        with open(f) as fh:
            data = json.load(fh)

        mode = data.get("mode", "")
        name = data.get("repo", "")

        if not mode:
            if "selectiveRun" in data or "selectedRun" in data:
                mode = "kai"
            else:
                mode = "kai"

        if not name:
            bench = data.get("_bench", {})
            name = bench.get("repo", "")
            if not name:
                fname = f.stem
                for suffix in ("_baseline_iter_", "_kai_iter_", "_iter_"):
                    if suffix in fname:
                        name = fname.split(suffix)[0]
                        break

        if not name:
            continue

        target = baseline if mode == "baseline" else kai
        if name not in target:
            target[name] = []
        target[name].append(data)

    return baseline, kai


def compute_reduction(summary, raw_baseline, raw_kai):
    """Compute per-repo and aggregate reduction metrics."""
    baseline_repos = summary.get("baseline", {})
    kai_repos = summary.get("kai", {})
    all_repos = sorted(set(list(baseline_repos.keys()) + list(kai_repos.keys())))

    rows = []

    for repo in all_repos:
        bl = baseline_repos.get(repo, {})
        kai = kai_repos.get(repo, {})

        # Baseline median duration
        bl_dur = bl.get("duration_s", {})
        baseline_median_s = bl_dur.get("median", 0) if bl_dur else 0
        baseline_median_ms = round(baseline_median_s * 1000)
        bl_tests_total = bl.get("tests_total", 0)
        bl_runs = bl.get("runs", 0)

        # Kai metrics
        kai_sel_dur = kai.get("selective_duration_s", {})
        kai_sel_median_s = kai_sel_dur.get("median", 0) if kai_sel_dur else 0

        kai_plan_dur = kai.get("plan_dur_ms", {})
        kai_plan_median_ms = kai_plan_dur.get("median", 0) if kai_plan_dur else 0

        kai_pipeline = kai.get("pipeline_ms", {})
        kai_pipeline_median_ms = kai_pipeline.get("median", 0) if kai_pipeline else 0

        # Use pipeline_ms if available, otherwise plan + selective
        if kai_pipeline_median_ms > 0:
            kai_total_ms = round(kai_pipeline_median_ms)
        else:
            kai_total_ms = round(kai_plan_median_ms + kai_sel_median_s * 1000)

        # Fallback-aware: if all runs were fallback, kai_total = baseline_total
        fallback_rate = kai.get("fallback_rate", 0)
        kai_runs = len(kai.get("verdicts", []))

        # For fallback runs, adjust kai_total: blend fallback (=baseline) with non-fallback
        if fallback_rate == 1.0 and baseline_median_ms > 0:
            # All fallback — no savings
            kai_total_ms_adjusted = baseline_median_ms
        elif fallback_rate > 0 and baseline_median_ms > 0 and kai_total_ms > 0:
            # Partial fallback: weighted blend
            kai_total_ms_adjusted = round(
                (1 - fallback_rate) * kai_total_ms + fallback_rate * baseline_median_ms
            )
        else:
            kai_total_ms_adjusted = kai_total_ms

        # Tests
        tests_total = kai.get("tests_total", bl_tests_total)
        tests_selected = kai.get("tests_selected", 0)

        # Compute reductions
        if baseline_median_ms > 0 and kai_total_ms_adjusted > 0:
            time_reduction_pct = round((1 - kai_total_ms_adjusted / baseline_median_ms) * 100, 1)
        elif baseline_median_ms > 0:
            # Kai data missing — can't compute
            time_reduction_pct = 0
        else:
            # No baseline — use kai's own full vs selective comparison
            time_reduction_pct = kai.get("time_saved_pct", 0)

        if tests_total > 0:
            test_reduction_pct = round((1 - tests_selected / tests_total) * 100, 1)
        else:
            test_reduction_pct = kai.get("tests_reduced_pct", 0)

        # Safety
        false_negatives = kai.get("false_negatives", 0)
        accuracy = kai.get("accuracy", {})
        accuracy_mean = accuracy.get("mean", 1.0) if accuracy else 1.0
        confidence = kai.get("confidence", {})
        confidence_median = confidence.get("median", 0) if confidence else 0

        verdicts = kai.get("verdicts", [])
        safe_count = sum(1 for v in verdicts if v.startswith("safe"))

        row = {
            "repo": repo,
            "baseline_median_ms": baseline_median_ms,
            "baseline_runs": bl_runs,
            "kai_median_ms": kai_total_ms,
            "kai_adjusted_ms": kai_total_ms_adjusted,
            "kai_runs": kai_runs,
            "time_reduction_pct": time_reduction_pct,
            "tests_total": tests_total,
            "tests_selected": tests_selected,
            "test_reduction_pct": test_reduction_pct,
            "fallback_rate": round(fallback_rate, 4),
            "false_negative_count": false_negatives,
            "accuracy": round(accuracy_mean, 4),
            "confidence": round(confidence_median, 4),
            "safe_verdicts": f"{safe_count}/{kai_runs}" if kai_runs > 0 else "n/a",
        }
        rows.append(row)

    # Aggregate
    repos_with_time = [r for r in rows if r["baseline_median_ms"] > 0 and r["kai_adjusted_ms"] > 0]
    repos_with_tests = [r for r in rows if r["tests_total"] > 0]

    if repos_with_time:
        total_baseline = sum(r["baseline_median_ms"] for r in repos_with_time)
        total_kai = sum(r["kai_adjusted_ms"] for r in repos_with_time)
        agg_time_pct = round((1 - total_kai / total_baseline) * 100, 1) if total_baseline > 0 else 0
    else:
        agg_time_pct = round(sum(r["time_reduction_pct"] for r in rows) / max(len(rows), 1), 1)

    if repos_with_tests:
        total_tests = sum(r["tests_total"] for r in repos_with_tests)
        total_selected = sum(r["tests_selected"] for r in repos_with_tests)
        agg_test_pct = round((1 - total_selected / total_tests) * 100, 1) if total_tests > 0 else 0
    else:
        agg_test_pct = 0

    total_fn = sum(r["false_negative_count"] for r in rows)
    total_runs = sum(r["kai_runs"] for r in rows)
    total_fallbacks = sum(round(r["fallback_rate"] * r["kai_runs"]) for r in rows)
    agg_fallback = round(total_fallbacks / total_runs, 4) if total_runs > 0 else 0

    aggregate = {
        "repos": len(rows),
        "total_runs": total_runs,
        "time_reduction_pct": agg_time_pct,
        "test_reduction_pct": agg_test_pct,
        "false_negatives": total_fn,
        "fallback_rate": agg_fallback,
    }

    return rows, aggregate


def format_table(rows, aggregate):
    """Format publishable console table."""
    lines = []
    lines.append("=== Kai CI Reduction Report ===")
    lines.append("")

    header = (
        f"{'Repo':<14} {'Baseline':>10} {'Kai':>10} {'TimeSaved':>10} "
        f"{'Tests':>6} {'Selected':>9} {'TestSaved':>10} "
        f"{'Fallback':>9} {'FN':>4} {'Safe':>7}"
    )
    lines.append(header)
    lines.append("-" * len(header))

    for r in rows:
        bl_str = f"{r['baseline_median_ms']}ms" if r['baseline_median_ms'] > 0 else "n/a"
        kai_str = f"{r['kai_adjusted_ms']}ms" if r['kai_adjusted_ms'] > 0 else "n/a"
        time_str = f"{r['time_reduction_pct']:+.1f}%"
        fb_str = f"{r['fallback_rate']:.0%}"

        lines.append(
            f"{r['repo']:<14} {bl_str:>10} {kai_str:>10} {time_str:>10} "
            f"{r['tests_total']:>6} {r['tests_selected']:>9} "
            f"{r['test_reduction_pct']:>9.1f}% "
            f"{fb_str:>9} {r['false_negative_count']:>4} "
            f"{r['safe_verdicts']:>7}"
        )

    lines.append("-" * len(header))

    a = aggregate
    lines.append(
        f"{'AGGREGATE':<14} {'':>10} {'':>10} "
        f"{a['time_reduction_pct']:>+9.1f}% "
        f"{'':>6} {'':>9} "
        f"{a['test_reduction_pct']:>9.1f}% "
        f"{a['fallback_rate']:>8.0%} {a['false_negatives']:>4} "
        f"{a['total_runs']:>4}run"
    )
    lines.append("")
    lines.append(f"Repos: {a['repos']}  |  Runs: {a['total_runs']}  |  False negatives: {a['false_negatives']}")

    return "\n".join(lines)


def write_csv(rows, aggregate, filepath):
    """Write per-repo rows + aggregate to CSV."""
    fieldnames = [
        "repo", "baseline_median_ms", "baseline_runs",
        "kai_median_ms", "kai_adjusted_ms", "kai_runs",
        "time_reduction_pct", "tests_total", "tests_selected",
        "test_reduction_pct", "fallback_rate", "false_negative_count",
        "accuracy", "confidence", "safe_verdicts",
    ]

    with open(filepath, "w", newline="") as f:
        writer = csv.DictWriter(f, fieldnames=fieldnames)
        writer.writeheader()
        for r in rows:
            writer.writerow(r)

        # Aggregate row
        writer.writerow({
            "repo": "AGGREGATE",
            "baseline_median_ms": "",
            "baseline_runs": "",
            "kai_median_ms": "",
            "kai_adjusted_ms": "",
            "kai_runs": aggregate["total_runs"],
            "time_reduction_pct": aggregate["time_reduction_pct"],
            "tests_total": "",
            "tests_selected": "",
            "test_reduction_pct": aggregate["test_reduction_pct"],
            "fallback_rate": aggregate["fallback_rate"],
            "false_negative_count": aggregate["false_negatives"],
            "accuracy": "",
            "confidence": "",
            "safe_verdicts": "",
        })


def main():
    if len(sys.argv) < 2:
        print("Usage: python3 compute_reduction.py <results-dir>", file=sys.stderr)
        sys.exit(1)

    results_dir = sys.argv[1]

    if not os.path.isdir(results_dir):
        print(f"Error: {results_dir} is not a directory", file=sys.stderr)
        sys.exit(1)

    summary = load_summary(results_dir)
    raw_baseline, raw_kai = load_raw_iterations(results_dir)

    rows, aggregate = compute_reduction(summary, raw_baseline, raw_kai)

    if not rows:
        print("No repos found in results.", file=sys.stderr)
        sys.exit(1)

    # Print table
    print(format_table(rows, aggregate))

    # Write CSV
    csv_path = os.path.join(results_dir, "reduction.csv")
    write_csv(rows, aggregate, csv_path)
    print(f"\nCSV written to: {csv_path}")

    # Write reduction JSON
    reduction_json = {
        "version": 1,
        "repos": rows,
        "aggregate": aggregate,
    }
    json_path = os.path.join(results_dir, "reduction.json")
    with open(json_path, "w") as f:
        json.dump(reduction_json, f, indent=2)
    print(f"JSON written to: {json_path}")


if __name__ == "__main__":
    main()
