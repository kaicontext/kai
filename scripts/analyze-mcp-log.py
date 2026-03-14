#!/usr/bin/env python3
"""
Analyze MCP tool call logs for SER (Structured Exploration Ratio) and A/B comparison.

Reads .kai/mcp-calls.jsonl files and computes:
  - SER: ratio of Kai calls whose results were used vs total exploration
  - Per-tool usage breakdown
  - Session-level metrics for A/B comparison

Usage:
  # Analyze a single log
  python3 scripts/analyze-mcp-log.py .kai/mcp-calls.jsonl

  # Compare Kai-enabled vs Kai-disabled sessions
  python3 scripts/analyze-mcp-log.py --ab kai-enabled.jsonl kai-disabled.jsonl

  # Aggregate across multiple repos
  python3 scripts/analyze-mcp-log.py repos/*/mcp-calls.jsonl
"""

import json
import sys
import argparse
from collections import defaultdict
from pathlib import Path


def load_records(path):
    """Load ToolCallRecords from a JSONL file."""
    records = []
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                records.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    return records


def group_by_session(records):
    """Group records by session_id."""
    sessions = defaultdict(list)
    for rec in records:
        sessions[rec.get("session_id", "unknown")].append(rec)
    # Sort each session by seq
    for sid in sessions:
        sessions[sid].sort(key=lambda r: r.get("seq", 0))
    return dict(sessions)


def compute_result_used(session_records):
    """
    For each Kai call, check if any file or symbol from its response
    appears in the next 3 actions (co-occurrence heuristic).

    Returns list of (record, used: bool) tuples.
    """
    results = []
    for i, rec in enumerate(session_records):
        rec_files = set(rec.get("files", []))
        rec_symbols = set(rec.get("symbols", []))

        if not rec_files and not rec_symbols:
            results.append((rec, False))
            continue

        # Look ahead up to 3 actions
        used = False
        window = session_records[i + 1 : i + 4]
        for future in window:
            future_files = set(future.get("files", []))
            future_symbols = set(future.get("symbols", []))
            # Also check params for file/symbol references
            params = future.get("params", {})
            if "file" in params:
                future_files.add(params["file"])
            if "symbol" in params:
                future_symbols.add(params["symbol"])

            if rec_files & future_files or rec_symbols & future_symbols:
                used = True
                break

        results.append((rec, used))
    return results


def compute_ser(session_records):
    """
    Compute Structured Exploration Ratio for a session.

    SER = kai_useful_calls / total_calls

    A call is "useful" if its results (files/symbols) appear in subsequent actions.
    """
    annotated = compute_result_used(session_records)
    if not annotated:
        return None

    total = len(annotated)
    useful = sum(1 for _, used in annotated if used)

    return {
        "total_calls": total,
        "useful_calls": useful,
        "ser": useful / total if total > 0 else 0,
    }


def analyze_session(session_id, records):
    """Compute all metrics for a single session."""
    tool_counts = defaultdict(int)
    total_dur_ms = 0
    errors = 0
    all_files = set()
    all_symbols = set()

    for rec in records:
        tool_counts[rec["tool"]] += 1
        total_dur_ms += rec.get("dur_ms", 0)
        if rec.get("is_error"):
            errors += 1
        all_files.update(rec.get("files", []))
        all_symbols.update(rec.get("symbols", []))

    ser_data = compute_ser(records)

    return {
        "session_id": session_id,
        "total_calls": len(records),
        "tool_counts": dict(tool_counts),
        "total_dur_ms": total_dur_ms,
        "unique_files": len(all_files),
        "unique_symbols": len(all_symbols),
        "errors": errors,
        "ser": ser_data["ser"] if ser_data else None,
        "useful_calls": ser_data["useful_calls"] if ser_data else 0,
    }


def print_session_report(analysis):
    """Print a human-readable report for one session."""
    print(f"\n{'=' * 60}")
    print(f"Session: {analysis['session_id'][:8]}...")
    print(f"{'=' * 60}")
    print(f"  Total calls:    {analysis['total_calls']}")
    print(f"  Useful calls:   {analysis['useful_calls']}")
    print(f"  SER:            {analysis['ser']:.1%}" if analysis["ser"] is not None else "  SER:            N/A")
    print(f"  Total duration: {analysis['total_dur_ms']}ms")
    print(f"  Unique files:   {analysis['unique_files']}")
    print(f"  Unique symbols: {analysis['unique_symbols']}")
    print(f"  Errors:         {analysis['errors']}")
    print(f"\n  Tool usage:")
    for tool, count in sorted(analysis["tool_counts"].items(), key=lambda x: -x[1]):
        print(f"    {tool:20s} {count:4d}")


def print_aggregate(sessions):
    """Print aggregate metrics across all sessions."""
    if not sessions:
        print("No sessions found.")
        return

    sers = [s["ser"] for s in sessions if s["ser"] is not None]
    total_calls = sum(s["total_calls"] for s in sessions)
    total_useful = sum(s["useful_calls"] for s in sessions)
    total_dur = sum(s["total_dur_ms"] for s in sessions)

    # Aggregate tool counts
    all_tools = defaultdict(int)
    for s in sessions:
        for tool, count in s["tool_counts"].items():
            all_tools[tool] += count

    print(f"\n{'=' * 60}")
    print(f"AGGREGATE ({len(sessions)} sessions)")
    print(f"{'=' * 60}")
    print(f"  Total calls:     {total_calls}")
    print(f"  Total useful:    {total_useful}")
    print(f"  Aggregate SER:   {total_useful / total_calls:.1%}" if total_calls > 0 else "  Aggregate SER:   N/A")
    if sers:
        print(f"  Mean SER:        {sum(sers) / len(sers):.1%}")
        print(f"  Median SER:      {sorted(sers)[len(sers) // 2]:.1%}")
        print(f"  Min SER:         {min(sers):.1%}")
        print(f"  Max SER:         {max(sers):.1%}")
    print(f"  Total duration:  {total_dur}ms ({total_dur / 1000:.1f}s)")
    print(f"\n  Tool usage (all sessions):")
    for tool, count in sorted(all_tools.items(), key=lambda x: -x[1]):
        pct = count / total_calls * 100 if total_calls > 0 else 0
        print(f"    {tool:20s} {count:4d}  ({pct:.0f}%)")


def print_ab_comparison(arm_a, arm_b):
    """Compare Kai-enabled (A) vs Kai-disabled (B) sessions."""
    def agg(sessions):
        sers = [s["ser"] for s in sessions if s["ser"] is not None]
        return {
            "n": len(sessions),
            "total_calls": sum(s["total_calls"] for s in sessions),
            "mean_calls": sum(s["total_calls"] for s in sessions) / len(sessions) if sessions else 0,
            "mean_ser": sum(sers) / len(sers) if sers else None,
            "mean_dur": sum(s["total_dur_ms"] for s in sessions) / len(sessions) if sessions else 0,
        }

    a = agg(arm_a)
    b = agg(arm_b)

    print(f"\n{'=' * 60}")
    print("A/B COMPARISON")
    print(f"{'=' * 60}")
    print(f"{'':25s} {'Kai-Enabled':>15s} {'Kai-Disabled':>15s} {'Delta':>10s}")
    print(f"  {'Sessions':20s} {a['n']:>15d} {b['n']:>15d}")
    print(f"  {'Mean calls/session':20s} {a['mean_calls']:>15.1f} {b['mean_calls']:>15.1f} {a['mean_calls'] - b['mean_calls']:>+10.1f}")
    print(f"  {'Mean duration (ms)':20s} {a['mean_dur']:>15.0f} {b['mean_dur']:>15.0f} {a['mean_dur'] - b['mean_dur']:>+10.0f}")

    if a["mean_ser"] is not None:
        print(f"  {'Mean SER':20s} {a['mean_ser']:>14.1%}")

    if a["mean_dur"] > 0 and b["mean_dur"] > 0:
        nte = (b["mean_dur"] - a["mean_dur"]) / b["mean_dur"]
        print(f"\n  Net Token Efficiency (NTE): {nte:.1%}")
        if nte > 0:
            print(f"  -> Kai-enabled is {nte:.0%} faster")
        else:
            print(f"  -> Kai-enabled is {-nte:.0%} slower (investigate)")


def main():
    parser = argparse.ArgumentParser(description="Analyze Kai MCP tool call logs")
    parser.add_argument("files", nargs="+", help="JSONL log file(s)")
    parser.add_argument("--ab", action="store_true", help="A/B comparison mode (first file = Kai-enabled, second = Kai-disabled)")
    parser.add_argument("--json", action="store_true", help="Output as JSON instead of text")
    args = parser.parse_args()

    if args.ab:
        if len(args.files) != 2:
            print("A/B mode requires exactly 2 files: <kai-enabled.jsonl> <kai-disabled.jsonl>", file=sys.stderr)
            sys.exit(1)

        records_a = load_records(args.files[0])
        records_b = load_records(args.files[1])

        sessions_a = group_by_session(records_a)
        sessions_b = group_by_session(records_b)

        analysis_a = [analyze_session(sid, recs) for sid, recs in sessions_a.items()]
        analysis_b = [analyze_session(sid, recs) for sid, recs in sessions_b.items()]

        if args.json:
            print(json.dumps({"arm_a": analysis_a, "arm_b": analysis_b}, indent=2))
        else:
            print_ab_comparison(analysis_a, analysis_b)
    else:
        all_records = []
        for f in args.files:
            all_records.extend(load_records(f))

        sessions = group_by_session(all_records)
        analyses = []
        for sid, recs in sessions.items():
            a = analyze_session(sid, recs)
            analyses.append(a)
            if not args.json:
                print_session_report(a)

        if args.json:
            print(json.dumps(analyses, indent=2))
        else:
            print_aggregate(analyses)


if __name__ == "__main__":
    main()
