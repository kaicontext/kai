#!/bin/bash
# Kai Reproducible Benchmark Harness
# Usage: ./bench/run.sh [OPTIONS]
#
# Options:
#   -n N          Number of measured iterations (default: 5)
#   -w N          Number of warmup iterations (default: 1)
#   -s SIZE       Fixture size: small (20 files), medium (100), large (500) (default: medium)
#   -o DIR        Output directory (default: bench/results/<date>)
#   -k PATH       Path to pre-built kai binary (default: builds from source)
#   --skip-build  Skip building kai binary (use existing)
#   --json-only   Only output JSON, no console summary
#   -h            Show this help

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"

# Defaults
ITERATIONS=5
WARMUP=1
SIZE="medium"
OUTPUT_DIR=""
KAI_BIN=""
SKIP_BUILD=false
JSON_ONLY=false

usage() {
    head -14 "$0" | tail -13 | sed 's/^# //' | sed 's/^#//'
    exit 0
}

while [[ $# -gt 0 ]]; do
    case $1 in
        -n) ITERATIONS="$2"; shift 2 ;;
        -w) WARMUP="$2"; shift 2 ;;
        -s) SIZE="$2"; shift 2 ;;
        -o) OUTPUT_DIR="$2"; shift 2 ;;
        -k) KAI_BIN="$2"; shift 2 ;;
        --skip-build) SKIP_BUILD=true; shift ;;
        --json-only) JSON_ONLY=true; shift ;;
        -h|--help) usage ;;
        *) echo "Unknown option: $1"; usage ;;
    esac
done

# Size → file count mapping
case $SIZE in
    small)  NUM_FILES=20;  NUM_TESTS=5;  MODIFY_PCT=20 ;;
    medium) NUM_FILES=100; NUM_TESTS=15; MODIFY_PCT=10 ;;
    large)  NUM_FILES=500; NUM_TESTS=30; MODIFY_PCT=5  ;;
    *) echo "Unknown size: $SIZE (use small/medium/large)"; exit 1 ;;
esac

DATE=$(date +%Y%m%d-%H%M%S)
if [ -z "$OUTPUT_DIR" ]; then
    OUTPUT_DIR="$SCRIPT_DIR/results/$DATE"
fi
mkdir -p "$OUTPUT_DIR"

# Collect environment info
collect_env() {
    local env_file="$OUTPUT_DIR/environment.json"
    local os_name=$(uname -s)
    local os_version=$(uname -r)
    local arch=$(uname -m)
    local cpu_info=""
    local go_version=$(go version 2>/dev/null | awk '{print $3}')
    local kai_version=""

    if [ "$os_name" = "Darwin" ]; then
        cpu_info=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo "unknown")
        cpu_cores=$(sysctl -n hw.ncpu 2>/dev/null || echo "unknown")
    else
        cpu_info=$(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs || echo "unknown")
        cpu_cores=$(nproc 2>/dev/null || echo "unknown")
    fi

    if [ -n "$KAI_BIN" ] && [ -x "$KAI_BIN" ]; then
        kai_version=$("$KAI_BIN" --version 2>/dev/null | head -1 || echo "unknown")
    fi

    local kai_commit=$(cd "$REPO_ROOT" && git rev-parse HEAD 2>/dev/null || echo "unknown")
    local kai_branch=$(cd "$REPO_ROOT" && git rev-parse --abbrev-ref HEAD 2>/dev/null || echo "unknown")
    local kai_dirty=$(cd "$REPO_ROOT" && git diff --quiet 2>/dev/null && echo "false" || echo "true")

    cat > "$env_file" << ENVEOF
{
  "timestamp": "$(date -u +%Y-%m-%dT%H:%M:%SZ)",
  "os": "$os_name",
  "os_version": "$os_version",
  "arch": "$arch",
  "cpu": "$cpu_info",
  "cpu_cores": "$cpu_cores",
  "go_version": "$go_version",
  "kai_version": "$kai_version",
  "kai_commit": "$kai_commit",
  "kai_branch": "$kai_branch",
  "kai_dirty": $kai_dirty,
  "benchmark_params": {
    "iterations": $ITERATIONS,
    "warmup": $WARMUP,
    "size": "$SIZE",
    "num_files": $NUM_FILES,
    "num_tests": $NUM_TESTS,
    "modify_pct": $MODIFY_PCT
  }
}
ENVEOF
}

# Build kai binary
build_kai() {
    if [ "$SKIP_BUILD" = true ] && [ -n "$KAI_BIN" ] && [ -x "$KAI_BIN" ]; then
        return 0
    fi
    if [ -z "$KAI_BIN" ]; then
        KAI_BIN="$OUTPUT_DIR/kai"
    fi
    if [ "$SKIP_BUILD" = true ] && [ -x "$KAI_BIN" ]; then
        return 0
    fi
    echo "Building kai binary..."
    (cd "$REPO_ROOT/kai-cli" && CGO_ENABLED=1 go build -o "$KAI_BIN" ./cmd/kai)
    echo "Built: $KAI_BIN"
}

# Generate fixture project
generate_fixture() {
    local dir="$1"
    local fi ti
    mkdir -p "$dir/src" "$dir/tests"

    # Generate source files with import chains
    for fi in $(seq 0 $((NUM_FILES - 1))); do
        local mod_dir="$dir/src/mod$((fi % 10))"
        mkdir -p "$mod_dir"
        local file="$mod_dir/file$fi.ts"

        if [ $fi -gt 0 ]; then
            # Import from previous file to create dependency chain
            local prev=$((fi - 1))
            local prev_mod="mod$((prev % 10))"
            cat > "$file" << TSEOF
import { fn_$prev } from '../$prev_mod/file$prev';
export function fn_$fi() { return fn_$prev() + $fi; }
export function helper_$fi(x: number) { return x * $fi; }
TSEOF
        else
            cat > "$file" << TSEOF
export function fn_0() { return 0; }
export function helper_0(x: number) { return x; }
TSEOF
        fi
    done

    # Generate test files that import specific source files
    for ti in $(seq 0 $((NUM_TESTS - 1))); do
        local target=$((ti * (NUM_FILES / NUM_TESTS)))
        local target_mod="mod$((target % 10))"
        cat > "$dir/tests/file${target}.test.ts" << TSEOF
import { fn_$target, helper_$target } from '../src/$target_mod/file$target';
test('fn_$target', () => expect(fn_$target()).toBeDefined());
test('helper_$target', () => expect(helper_$target(1)).toBeDefined());
TSEOF
    done
}

# Modify files to simulate a change
modify_fixture() {
    local dir="$1"
    local mi
    local num_modified=$((NUM_FILES * MODIFY_PCT / 100))
    if [ $num_modified -lt 1 ]; then num_modified=1; fi

    for mi in $(seq 0 $((num_modified - 1))); do
        local idx=$((mi * (100 / MODIFY_PCT)))
        local mod_dir="$dir/src/mod$((idx % 10))"
        local file="$mod_dir/file$idx.ts"
        if [ -f "$file" ]; then
            echo "// Modified at $(date +%s)" >> "$file"
        fi
    done
}

# Time a command in milliseconds, return via global
ELAPSED_MS=0
time_cmd() {
    local start end
    if [ "$(uname)" = "Darwin" ]; then
        start=$(python3 -c 'import time; print(int(time.time()*1000))')
    else
        start=$(($(date +%s%N) / 1000000))
    fi

    "$@" > /dev/null 2>&1 || true

    if [ "$(uname)" = "Darwin" ]; then
        end=$(python3 -c 'import time; print(int(time.time()*1000))')
    else
        end=$(($(date +%s%N) / 1000000))
    fi
    ELAPSED_MS=$((end - start))
}

# Run a single benchmark iteration
run_iteration() {
    local iter_num="$1"
    local result_file="$OUTPUT_DIR/iter_${iter_num}.json"

    # Fresh fixture per iteration
    local fixture_dir=$(mktemp -d)
    trap "rm -rf $fixture_dir" RETURN

    # Generate base state
    generate_fixture "$fixture_dir"

    # Init git repo for git-range support
    (cd "$fixture_dir" && git init -q && git add . && git commit -q -m "base" \
        --author="bench <bench@kai>" \
        2>/dev/null) || true

    # Phase 1: kai init
    pushd "$fixture_dir" > /dev/null
    time_cmd "$KAI_BIN" init
    local init_ms=$ELAPSED_MS

    # Phase 2: kai capture (snapshot + analyze symbols + analyze calls)
    time_cmd "$KAI_BIN" capture .
    local capture_ms=$ELAPSED_MS

    # Phase 3: Modify files and create second capture
    modify_fixture "$fixture_dir"
    git add . && git commit -q -m "change" \
        --author="bench <bench@kai>" \
        2>/dev/null || true

    time_cmd "$KAI_BIN" capture .
    local capture2_ms=$ELAPSED_MS

    # Phase 4: CI plan generation
    local plan_file="$fixture_dir/plan.json"
    time_cmd "$KAI_BIN" ci plan --out "$plan_file"
    local plan_ms=$ELAPSED_MS

    # Extract plan metrics
    local tests_selected=0 tests_total=0 plan_mode="unknown" confidence=0
    if [ -f "$plan_file" ]; then
        tests_selected=$(python3 -c "
import json, sys
try:
    d = json.load(open('$plan_file'))
    print(len(d.get('targets',{}).get('run',[])))
except: print(0)
" 2>/dev/null || echo 0)
        tests_total=$(python3 -c "
import json, sys
try:
    d = json.load(open('$plan_file'))
    print(len(d.get('targets',{}).get('full',[])))
except: print(0)
" 2>/dev/null || echo 0)
        plan_mode=$(python3 -c "
import json, sys
try:
    d = json.load(open('$plan_file'))
    print(d.get('mode','unknown'))
except: print('unknown')
" 2>/dev/null || echo "unknown")
        confidence=$(python3 -c "
import json, sys
try:
    d = json.load(open('$plan_file'))
    print(d.get('confidence',0))
except: print(0)
" 2>/dev/null || echo 0)
    fi

    # Count nodes and edges in graph
    local node_count=0 edge_count=0
    if [ -f ".kai/db.sqlite" ]; then
        node_count=$(sqlite3 ".kai/db.sqlite" "SELECT COUNT(*) FROM nodes" 2>/dev/null || echo 0)
        edge_count=$(sqlite3 ".kai/db.sqlite" "SELECT COUNT(*) FROM edges" 2>/dev/null || echo 0)
    fi

    popd > /dev/null

    # Write iteration result
    cat > "$result_file" << ITEREOF
{
  "iteration": $iter_num,
  "timings_ms": {
    "init": $init_ms,
    "capture_base": $capture_ms,
    "capture_head": $capture2_ms,
    "ci_plan": $plan_ms,
    "total": $((init_ms + capture_ms + capture2_ms + plan_ms))
  },
  "graph": {
    "nodes": $node_count,
    "edges": $edge_count
  },
  "plan": {
    "mode": "$plan_mode",
    "tests_selected": $tests_selected,
    "tests_total": $tests_total,
    "confidence": $confidence
  },
  "fixture": {
    "files": $NUM_FILES,
    "tests": $NUM_TESTS,
    "modified_pct": $MODIFY_PCT
  }
}
ITEREOF
}

# Compute statistics from iteration results
compute_stats() {
    python3 "$SCRIPT_DIR/stats.py" "$OUTPUT_DIR"
}

# Main
main() {
    echo "=== Kai Benchmark Harness ==="
    echo "Size: $SIZE ($NUM_FILES files, $NUM_TESTS tests)"
    echo "Iterations: $WARMUP warmup + $ITERATIONS measured"
    echo "Output: $OUTPUT_DIR"
    echo ""

    build_kai
    collect_env

    # Warmup
    if [ "$WARMUP" -gt 0 ]; then
        for i in $(seq 1 $WARMUP); do
            if [ "$JSON_ONLY" = false ]; then
                echo -n "Warmup $i/$WARMUP... "
            fi
            run_iteration "warmup_$i"
            rm -f "$OUTPUT_DIR/iter_warmup_$i.json"
            if [ "$JSON_ONLY" = false ]; then
                echo "done"
            fi
        done
    fi

    # Measured runs
    for i in $(seq 1 $ITERATIONS); do
        if [ "$JSON_ONLY" = false ]; then
            echo -n "Iteration $i/$ITERATIONS... "
        fi
        run_iteration "$i"
        if [ "$JSON_ONLY" = false ]; then
            echo "done ($(python3 -c "import json; d=json.load(open('$OUTPUT_DIR/iter_$i.json')); print(d['timings_ms']['total'])")ms total)"
        fi
    done

    # Compute and display stats
    echo ""
    compute_stats

    echo ""
    echo "Results written to: $OUTPUT_DIR/"
}

main
