#!/usr/bin/env python3
"""Compute statistics from Kai real-repo benchmark iteration results.

Usage: python3 repo_stats.py <results-dir>

Reads *_iter_*.json files from the results directory, groups by repo,
computes aggregate statistics, and writes:
  - summary.json   (machine-readable)
  - Console table   (human-readable, to stdout)

Supports three file naming patterns:
  - Legacy:   <repo>_iter_<N>.json          (mode field inside determines type)
  - Baseline: <repo>_baseline_iter_<N>.json
  - Kai:      <repo>_kai_iter_<N>.json
"""

import json
import math
import os
import sys
from datetime import datetime, timezone
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


def stat_block(values):
    """Compute a standard stat block for a list of numeric values."""
    s = sorted(values)
    return {
        "median": round(median(s), 3),
        "p90": round(p90(s), 3),
        "mean": round(mean(s), 3),
        "stddev": round(stddev(s), 3),
        "min": round(min(s), 3) if s else 0,
        "max": round(max(s), 3) if s else 0,
    }


def load_iterations(results_dir):
    """Load all *_iter_*.json files, grouped by (repo, mode).

    Returns:
        baseline: dict of repo_name -> [iteration_data]
        kai: dict of repo_name -> [iteration_data]
    """
    baseline = {}
    kai = {}

    for f in sorted(Path(results_dir).glob("*_iter_*.json")):
        if "warmup" in f.name:
            continue
        with open(f) as fh:
            data = json.load(fh)

        # Determine mode from file content or filename
        mode = data.get("mode", "")
        name = data.get("repo", "")

        if not mode:
            # Legacy format: has both selectiveRun and fullRun = kai-style combined report
            if "selectiveRun" in data:
                mode = "kai"
            elif "fullRun" in data:
                mode = "baseline"
            else:
                mode = "kai"  # default to kai for old format

        if not name:
            # Extract from _bench or filename
            bench = data.get("_bench", {})
            name = bench.get("repo", "")
            if not name:
                # Parse from filename: <repo>_[baseline_|kai_]iter_<N>.json
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


def compute_baseline_stats(name, iterations):
    """Compute statistics for baseline runs of a single repo."""
    durations = []
    tests_totals = []
    exit_codes = []

    for it in iterations:
        run = it.get("run", {})
        durations.append(run.get("durationS", 0))
        tests_totals.append(run.get("testsTotal", 0))
        exit_codes.append(run.get("exitCode", 0))

    commit = iterations[0].get("commit", "") if iterations else ""

    return {
        "commit": commit,
        "runs": len(iterations),
        "duration_s": stat_block(durations) if durations else {},
        "tests_total": round(mean(tests_totals)) if tests_totals else 0,
        "all_passed": all(c == 0 for c in exit_codes),
    }


def compute_kai_stats(name, iterations):
    """Compute statistics for kai runs of a single repo."""
    verdicts = []
    accuracies = []
    false_negatives_total = 0
    tests_total_vals = []
    tests_selected_vals = []
    tests_reduced_pct_vals = []
    full_duration_vals = []
    selective_duration_vals = []
    time_saved_pct_vals = []
    plan_dur_vals = []
    pipeline_vals = []
    fallback_count = 0
    confidences = []

    for it in iterations:
        verdict = it.get("verdict", "unknown")
        verdicts.append(verdict)

        metrics = it.get("metrics", {})
        accuracies.append(metrics.get("accuracy", 0))
        false_negatives_total += metrics.get("falseNegatives", 0)
        tests_reduced_pct_vals.append(metrics.get("testsReducedPct", 0))
        time_saved_pct_vals.append(metrics.get("timeSavedPct", 0))

        plan = it.get("plan", {})
        plan_dur_vals.append(plan.get("durMs", 0))
        tests_selected_vals.append(plan.get("testsSelected", 0))
        confidences.append(plan.get("confidence", 0))
        if plan.get("fallback", False):
            fallback_count += 1

        if "totalKaiPipelineMs" in it:
            pipeline_vals.append(it["totalKaiPipelineMs"])

        full_run = it.get("fullRun", {})
        sel_run = it.get("selectedRun", it.get("selectiveRun", {}))

        if full_run:
            full_duration_vals.append(full_run.get("durationS", 0))
            tests_total_vals.append(full_run.get("testsTotal", full_run.get("totalTests", 0)))

        if sel_run:
            selective_duration_vals.append(sel_run.get("durationS", 0))

    git_range = iterations[0].get("gitRange", "") if iterations else ""

    # Try to get size_class from repos.json
    size_class = ""
    repos_json = Path(__file__).parent / "repos.json"
    if repos_json.exists():
        with open(repos_json) as f:
            repo_defs = json.load(f)
        for r in repo_defs.get("repos", []):
            if r["name"] == name:
                size_class = r.get("size_class", "")
                break

    return {
        "size_class": size_class,
        "git_range": git_range,
        "verdicts": verdicts,
        "accuracy": {
            "min": round(min(accuracies), 4) if accuracies else 0,
            "max": round(max(accuracies), 4) if accuracies else 0,
            "mean": round(mean(accuracies), 4) if accuracies else 0,
        },
        "confidence": {
            "median": round(median(sorted(confidences)), 4) if confidences else 0,
            "mean": round(mean(confidences), 4) if confidences else 0,
        },
        "plan_dur_ms": stat_block(plan_dur_vals) if plan_dur_vals else {},
        "pipeline_ms": stat_block(pipeline_vals) if pipeline_vals else {},
        "fallback_rate": round(fallback_count / len(iterations), 4) if iterations else 0,
        "tests_total": round(mean(tests_total_vals)) if tests_total_vals else 0,
        "tests_selected": round(mean(tests_selected_vals)) if tests_selected_vals else 0,
        "tests_reduced_pct": round(mean(tests_reduced_pct_vals), 1) if tests_reduced_pct_vals else 0,
        "full_duration_s": stat_block(full_duration_vals) if full_duration_vals else {},
        "selective_duration_s": stat_block(selective_duration_vals) if selective_duration_vals else {},
        "time_saved_pct": round(mean(time_saved_pct_vals), 1) if time_saved_pct_vals else 0,
        "false_negatives": false_negatives_total,
    }


def compute_aggregate(kai_stats):
    """Compute aggregate stats across all repos."""
    all_safe = True
    total_fn = 0
    accuracies = []
    reduced_pcts = []
    saved_pcts = []
    total_iters = 0
    fallback_rates = []

    for name, stats in kai_stats.items():
        if not all(v.startswith("safe") for v in stats["verdicts"]):
            all_safe = False
        total_fn += stats["false_negatives"]
        accuracies.append(stats["accuracy"]["mean"])
        reduced_pcts.append(stats["tests_reduced_pct"])
        saved_pcts.append(stats["time_saved_pct"])
        total_iters += len(stats["verdicts"])
        fallback_rates.append(stats["fallback_rate"])

    return {
        "all_safe": all_safe,
        "total_false_negatives": total_fn,
        "mean_accuracy": round(mean(accuracies), 4) if accuracies else 0,
        "mean_tests_reduced_pct": round(mean(reduced_pcts), 1) if reduced_pcts else 0,
        "mean_time_saved_pct": round(mean(saved_pcts), 1) if saved_pcts else 0,
        "mean_fallback_rate": round(mean(fallback_rates), 4) if fallback_rates else 0,
        "repos_tested": len(kai_stats),
        "total_iterations": total_iters,
    }


def format_table(baseline_stats, kai_stats, aggregate):
    """Format a human-readable console table."""
    lines = []
    lines.append("=== Kai Real-Repo Benchmark Results ===")
    lines.append("")

    all_repos = sorted(set(list(baseline_stats.keys()) + list(kai_stats.keys())))

    if baseline_stats:
        lines.append("--- Baseline (full suite) ---")
        header = f"{'Repo':<14} {'Tests':>6} {'Duration(s)':>12} {'Runs':>5} {'Pass':>5}"
        lines.append(header)
        lines.append("-" * len(header))
        for name in all_repos:
            if name not in baseline_stats:
                continue
            s = baseline_stats[name]
            dur_med = s["duration_s"].get("median", 0) if s["duration_s"] else 0
            lines.append(
                f"{name:<14} {s['tests_total']:>6} "
                f"{dur_med:>11.3f}s {s['runs']:>5} "
                f"{'yes' if s['all_passed'] else 'NO':>5}"
            )
        lines.append("")

    if kai_stats:
        lines.append("--- Kai (selective) ---")
        header = (
            f"{'Repo':<14} {'Tests':>6} {'Selected':>9} {'Reduced%':>9} "
            f"{'Full(s)':>8} {'Kai(s)':>7} {'Saved%':>7} {'Accuracy':>9} {'Verdict':>10}"
        )
        lines.append(header)
        lines.append("-" * len(header))
        for name in all_repos:
            if name not in kai_stats:
                continue
            s = kai_stats[name]
            full_med = s["full_duration_s"].get("median", 0) if s["full_duration_s"] else 0
            sel_med = s["selective_duration_s"].get("median", 0) if s["selective_duration_s"] else 0
            verdict = max(set(s["verdicts"]), key=s["verdicts"].count) if s["verdicts"] else "?"
            lines.append(
                f"{name:<14} {s['tests_total']:>6} {s['tests_selected']:>9} "
                f"{s['tests_reduced_pct']:>8.1f}% "
                f"{full_med:>7.1f}s {sel_med:>6.1f}s "
                f"{s['time_saved_pct']:>6.1f}% "
                f"{s['accuracy']['mean']*100:>8.1f}% "
                f"{verdict:>10}"
            )
        lines.append("")

    if aggregate:
        a = aggregate
        lines.append(
            f"Aggregate: {a['repos_tested']} repos, {a['total_iterations']} iterations, "
            f"{a['total_false_negatives']} false negatives, "
            f"mean {a['mean_tests_reduced_pct']:.1f}% tests reduced, "
            f"fallback rate {a['mean_fallback_rate']:.1%}"
        )

    return "\n".join(lines)


def main():
    if len(sys.argv) < 2:
        print("Usage: python3 repo_stats.py <results-dir>", file=sys.stderr)
        sys.exit(1)

    results_dir = sys.argv[1]
    baseline_iters, kai_iters = load_iterations(results_dir)

    if not baseline_iters and not kai_iters:
        print(f"No iteration files found in {results_dir}", file=sys.stderr)
        sys.exit(1)

    # Load environment if available
    env = {}
    env_file = os.path.join(results_dir, "environment.json")
    if os.path.exists(env_file):
        with open(env_file) as f:
            env = json.load(f)

    # Compute per-repo stats
    baseline_stats = {}
    for name, iterations in baseline_iters.items():
        baseline_stats[name] = compute_baseline_stats(name, iterations)

    kai_stats = {}
    for name, iterations in kai_iters.items():
        kai_stats[name] = compute_kai_stats(name, iterations)

    # Compute aggregate (kai only, since that's where reduction metrics live)
    aggregate = compute_aggregate(kai_stats) if kai_stats else {}

    # Build summary
    max_iters = 0
    for its in list(baseline_iters.values()) + list(kai_iters.values()):
        max_iters = max(max_iters, len(its))

    summary = {
        "version": 2,
        "generatedAt": datetime.now(timezone.utc).isoformat(),
        "environment": env,
        "iterations": max_iters,
    }

    if baseline_stats:
        summary["baseline"] = baseline_stats
    if kai_stats:
        summary["kai"] = kai_stats
    if aggregate:
        summary["aggregate"] = aggregate

    # Write JSON
    summary_file = os.path.join(results_dir, "summary.json")
    with open(summary_file, "w") as f:
        json.dump(summary, f, indent=2)

    # Print console table
    print(format_table(baseline_stats, kai_stats, aggregate))


if __name__ == "__main__":
    main()
