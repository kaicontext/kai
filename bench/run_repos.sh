#!/bin/bash
# Kai Real-Repo Benchmark Harness
# Usage: ./bench/run_repos.sh [OPTIONS]
#
# Clones real open-source repos at pinned commits, runs kai's full
# capture + ci plan flow, then compares selective vs full test suites.
# Produces machine-readable results showing actual CI time reduction
# and zero false negatives.
#
# Options:
#   -n N          Number of measured iterations (default: 3)
#   -w N          Number of warmup iterations (default: 1)
#   -o DIR        Output directory (default: bench/results/repos-<date>)
#   -k PATH       Path to pre-built kai binary (default: builds from source)
#   --mode MODE   baseline, kai, or both (default: both)
#   --skip-build  Skip building kai binary (use existing)
#   --repo NAME   Run only one repo (for debugging)
#   --json-only   Suppress console output
#   -h            Show this help

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
REPOS_JSON="$SCRIPT_DIR/repos.json"
CACHE_DIR="$SCRIPT_DIR/.repo-cache"

# Defaults
ITERATIONS=3
WARMUP=1
OUTPUT_DIR=""
KAI_BIN=""
MODE="both"
SKIP_BUILD=false
JSON_ONLY=false
ONLY_REPO=""

usage() {
    head -19 "$0" | tail -18 | sed 's/^# //' | sed 's/^#//'
    exit 0
}

while [[ $# -gt 0 ]]; do
    case $1 in
        -n) ITERATIONS="$2"; shift 2 ;;
        -w) WARMUP="$2"; shift 2 ;;
        -o) OUTPUT_DIR="$2"; shift 2 ;;
        -k) KAI_BIN="$2"; shift 2 ;;
        --mode) MODE="$2"; shift 2 ;;
        --skip-build) SKIP_BUILD=true; shift ;;
        --repo) ONLY_REPO="$2"; shift 2 ;;
        --json-only) JSON_ONLY=true; shift ;;
        -h|--help) usage ;;
        *) echo "Unknown option: $1"; usage ;;
    esac
done

# Validate mode
case $MODE in
    baseline|kai|both) ;;
    *) echo "Unknown mode: $MODE (use baseline/kai/both)"; exit 1 ;;
esac

DATE=$(date +%Y%m%d-%H%M%S)
if [ -z "$OUTPUT_DIR" ]; then
    OUTPUT_DIR="$SCRIPT_DIR/results/repos-$DATE"
fi
mkdir -p "$OUTPUT_DIR"

log() {
    if [ "$JSON_ONLY" = false ]; then
        echo "$@"
    fi
}

log_n() {
    if [ "$JSON_ONLY" = false ]; then
        echo -n "$@"
    fi
}

# Collect environment info
collect_env() {
    local env_file="$OUTPUT_DIR/environment.json"
    local os_name=$(uname -s)
    local os_version=$(uname -r)
    local arch=$(uname -m)
    local cpu_info=""
    local cpu_cores=""
    local go_version=$(go version 2>/dev/null | awk '{print $3}')
    local node_version=$(node --version 2>/dev/null || echo "unknown")
    local npm_version=$(npm --version 2>/dev/null || echo "unknown")
    local pnpm_version=$(pnpm --version 2>/dev/null || echo "unknown")
    local yarn_version=$(yarn --version 2>/dev/null || echo "unknown")

    if [ "$os_name" = "Darwin" ]; then
        cpu_info=$(sysctl -n machdep.cpu.brand_string 2>/dev/null || echo "unknown")
        cpu_cores=$(sysctl -n hw.ncpu 2>/dev/null || echo "unknown")
    else
        cpu_info=$(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2 | xargs || echo "unknown")
        cpu_cores=$(nproc 2>/dev/null || echo "unknown")
    fi

    local kai_version=""
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
  "node_version": "$node_version",
  "npm_version": "$npm_version",
  "pnpm_version": "$pnpm_version",
  "yarn_version": "$yarn_version",
  "kai_version": "$kai_version",
  "kai_commit": "$kai_commit",
  "kai_branch": "$kai_branch",
  "kai_dirty": $kai_dirty,
  "benchmark_params": {
    "iterations": $ITERATIONS,
    "warmup": $WARMUP
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
    log "Building kai binary..."
    (cd "$REPO_ROOT/kai-cli" && CGO_ENABLED=1 go build -o "$KAI_BIN" ./cmd/kai)
    log "Built: $KAI_BIN"
}

# Read repo config from repos.json using python3
read_repos() {
    python3 -c "
import json, sys
with open('$REPOS_JSON') as f:
    data = json.load(f)
for r in data['repos']:
    print(r['name'])
"
}

read_repo_field() {
    local name="$1"
    local field="$2"
    python3 -c "
import json
with open('$REPOS_JSON') as f:
    data = json.load(f)
for r in data['repos']:
    if r['name'] == '$name':
        print(r.get('$field', ''))
        break
"
}

# Clone and install dependencies for a repo
setup_repo() {
    local name="$1"
    local url head_commit install_cmd
    url=$(read_repo_field "$name" "url")
    head_commit=$(read_repo_field "$name" "head_commit")
    install_cmd=$(read_repo_field "$name" "install")

    local repo_dir="$CACHE_DIR/$name"

    if [ ! -d "$repo_dir/.git" ]; then
        log "  Cloning $name..."
        git clone -q "$url" "$repo_dir"
    fi

    log "  Checking out $head_commit..."
    cd "$repo_dir"
    git checkout "$head_commit" --force -q
    git clean -fdx -q

    log "  Installing dependencies ($install_cmd)..."
    eval "$install_cmd" > /dev/null 2>&1
    cd - > /dev/null
}

# Run a test command in a repo, capturing duration and parsing results
# Sets global variables: CMD_EXIT, CMD_DURATION, CMD_TOTAL, CMD_PASSED, CMD_FAILED, CMD_SKIPPED
run_test_cmd() {
    local repo_dir="$1"
    local cmd="$2"

    cd "$repo_dir"
    # Clean up test results from any location
    find . -name 'test-results.json' -not -path '*/node_modules/*' -delete 2>/dev/null || true

    local start_time end_time
    start_time=$(python3 -c 'import time; print(time.time())')

    CMD_EXIT=0
    eval "$cmd" > /dev/null 2>&1 || CMD_EXIT=$?

    end_time=$(python3 -c 'import time; print(time.time())')
    CMD_DURATION=$(python3 -c "print(round($end_time - $start_time, 3))")

    # Find test-results.json (may be in subdirectory if command uses cd)
    local results_file
    results_file=$(find . -name 'test-results.json' -not -path '*/node_modules/*' 2>/dev/null | head -1)

    # Parse test results
    CMD_TOTAL=0
    CMD_PASSED=0
    CMD_FAILED=0
    CMD_SKIPPED=0

    if [ -n "$results_file" ] && [ -f "$results_file" ]; then
        eval "$(python3 -c "
import json, sys
try:
    with open('$results_file') as f:
        d = json.load(f)
    # Jest format
    if 'numTotalTests' in d:
        print(f'CMD_TOTAL={d[\"numTotalTests\"]}')
        print(f'CMD_PASSED={d[\"numPassedTests\"]}')
        print(f'CMD_FAILED={d[\"numFailedTests\"]}')
        print(f'CMD_SKIPPED={d.get(\"numPendingTests\", 0)}')
    # Mocha JSON format
    elif 'stats' in d:
        s = d['stats']
        print(f'CMD_TOTAL={s.get(\"tests\", 0)}')
        print(f'CMD_PASSED={s.get(\"passes\", 0)}')
        print(f'CMD_FAILED={s.get(\"failures\", 0)}')
        print(f'CMD_SKIPPED={s.get(\"pending\", 0)}')
    # Vitest JSON format (similar to Jest)
    elif 'testResults' in d:
        total = passed = failed = skipped = 0
        for suite in d['testResults']:
            for t in suite.get('assertionResults', []):
                total += 1
                s = t.get('status', '')
                if s == 'passed': passed += 1
                elif s == 'failed': failed += 1
                else: skipped += 1
        print(f'CMD_TOTAL={total}')
        print(f'CMD_PASSED={passed}')
        print(f'CMD_FAILED={failed}')
        print(f'CMD_SKIPPED={skipped}')
    else:
        print('CMD_TOTAL=0')
        print('CMD_PASSED=0')
        print('CMD_FAILED=0')
        print('CMD_SKIPPED=0')
except Exception as e:
    print('CMD_TOTAL=0', file=sys.stderr)
    print(f'# Error parsing: {e}', file=sys.stderr)
    print('CMD_TOTAL=0')
    print('CMD_PASSED=0')
    print('CMD_FAILED=0')
    print('CMD_SKIPPED=0')
" 2>/dev/null)"
    fi

    cd - > /dev/null
}

# Run baseline-only iteration: just the full test suite, no kai
run_baseline_iteration() {
    local name="$1"
    local iter_num="$2"
    local is_warmup="${3:-false}"

    local head_commit full_test
    head_commit=$(read_repo_field "$name" "head_commit")
    full_test=$(read_repo_field "$name" "full_test")

    local repo_dir="$CACHE_DIR/$name"
    local report_file
    if [ "$is_warmup" = true ]; then
        report_file="$OUTPUT_DIR/${name}_baseline_warmup_${iter_num}.json"
    else
        report_file="$OUTPUT_DIR/${name}_baseline_iter_${iter_num}.json"
    fi

    cd "$repo_dir"
    git checkout "$head_commit" --force -q
    cd - > /dev/null

    run_test_cmd "$repo_dir" "$full_test"

    python3 << PYEOF
import json

report = {
    "version": 1,
    "mode": "baseline",
    "repo": "$name",
    "commit": "$head_commit",
    "iteration": $iter_num,
    "run": {
        "command": $(python3 -c "import json; print(json.dumps('$full_test'))"),
        "exitCode": $CMD_EXIT,
        "durationS": $CMD_DURATION,
        "testsTotal": $CMD_TOTAL,
        "passed": $CMD_PASSED,
        "failed": $CMD_FAILED,
        "skipped": $CMD_SKIPPED,
    },
}

with open("$report_file", "w") as f:
    json.dump(report, f, indent=2)
PYEOF

    if [ "$is_warmup" = true ]; then
        rm -f "$report_file"
    fi
}

# Run kai iteration: init, capture, plan, selective run + full run (shadow)
run_kai_iteration() {
    local name="$1"
    local iter_num="$2"
    local is_warmup="${3:-false}"

    local base_commit head_commit full_test kai_test
    base_commit=$(read_repo_field "$name" "base_commit")
    head_commit=$(read_repo_field "$name" "head_commit")
    full_test=$(read_repo_field "$name" "full_test")
    kai_test=$(read_repo_field "$name" "kai_test")

    local repo_dir="$CACHE_DIR/$name"
    local report_file
    if [ "$is_warmup" = true ]; then
        report_file="$OUTPUT_DIR/${name}_kai_warmup_${iter_num}.json"
    else
        report_file="$OUTPUT_DIR/${name}_kai_iter_${iter_num}.json"
    fi

    local start_time
    start_time=$(python3 -c 'import time; print(time.time())')

    # Step 1: Checkout base commit, init kai, capture
    cd "$repo_dir"
    git checkout "$base_commit" --force -q
    git clean -fd -q
    rm -rf .kai

    local plan_start
    plan_start=$(python3 -c 'import time; print(time.time())')

    "$KAI_BIN" init > /dev/null 2>&1
    "$KAI_BIN" capture . > /dev/null 2>&1

    # Step 2: Checkout head commit, capture again
    git checkout "$head_commit" --force -q
    "$KAI_BIN" capture . > /dev/null 2>&1

    # Step 3: Generate CI plan
    local plan_file="$repo_dir/.kai/plan.json"
    "$KAI_BIN" ci plan --out "$plan_file" > /dev/null 2>&1

    local plan_end
    plan_end=$(python3 -c 'import time; print(time.time())')
    local plan_dur_ms
    plan_dur_ms=$(python3 -c "print(int(($plan_end - $plan_start) * 1000))")

    # Step 4: Extract selective targets from plan
    local strip_prefix
    strip_prefix=$(read_repo_field "$name" "strip_prefix")

    local targets_str num_targets
    targets_str=$(python3 -c "
import json
with open('$plan_file') as f:
    d = json.load(f)
targets = d.get('targets', {}).get('run', [])
prefix = '$strip_prefix'
if prefix:
    targets = [t[len(prefix):] if t.startswith(prefix) else t for t in targets]
print(' '.join(targets))
" 2>/dev/null || echo "")

    num_targets=$(python3 -c "
import json
with open('$plan_file') as f:
    d = json.load(f)
print(len(d.get('targets', {}).get('run', [])))
" 2>/dev/null || echo "0")

    local plan_confidence plan_mode fallback
    plan_confidence=$(python3 -c "
import json
with open('$plan_file') as f:
    d = json.load(f)
print(d.get('confidence', 0))
" 2>/dev/null || echo "0")

    plan_mode=$(python3 -c "
import json
with open('$plan_file') as f:
    d = json.load(f)
print(d.get('mode', 'unknown'))
" 2>/dev/null || echo "unknown")

    # Detect fallback (mode != selective or confidence == 0)
    fallback="false"
    if [ "$plan_mode" != "selective" ]; then
        fallback="true"
    fi

    cd - > /dev/null

    # Step 5: Run selective tests
    local selective_cmd
    if [ -n "$targets_str" ] && [ "$num_targets" -gt 0 ]; then
        selective_cmd="${kai_test//\{\{tests\}\}/$targets_str}"
    else
        selective_cmd="$full_test"
        fallback="true"
    fi

    run_test_cmd "$repo_dir" "$selective_cmd"
    local sel_exit=$CMD_EXIT sel_dur=$CMD_DURATION sel_total=$CMD_TOTAL
    local sel_passed=$CMD_PASSED sel_failed=$CMD_FAILED sel_skipped=$CMD_SKIPPED

    # Step 6: Run full test suite (shadow mode for correctness)
    run_test_cmd "$repo_dir" "$full_test"
    local full_exit=$CMD_EXIT full_dur=$CMD_DURATION full_total=$CMD_TOTAL
    local full_passed=$CMD_PASSED full_failed=$CMD_FAILED full_skipped=$CMD_SKIPPED

    # Step 7: Compute verdict and build report
    local end_time
    end_time=$(python3 -c 'import time; print(time.time())')

    local kai_version_str
    kai_version_str=$("$KAI_BIN" --version 2>/dev/null | head -1 || echo "unknown")

    # Write command to temp file to avoid quoting issues
    local sel_cmd_file full_cmd_file
    sel_cmd_file=$(mktemp)
    full_cmd_file=$(mktemp)
    printf '%s' "$selective_cmd" > "$sel_cmd_file"
    printf '%s' "$full_test" > "$full_cmd_file"

    local is_warmup_py
    if [ "$is_warmup" = true ]; then is_warmup_py="True"; else is_warmup_py="False"; fi

    python3 << PYEOF
import json

sel_total = $sel_total
sel_failed = $sel_failed
full_total = $full_total
full_failed = $full_failed
sel_dur = $sel_dur
full_dur = $full_dur

# Read commands from temp files
with open("$sel_cmd_file") as f:
    sel_cmd_str = f.read()
with open("$full_cmd_file") as f:
    full_cmd_str = f.read()

# False negatives: failures in full run not caught by selective run
false_negatives = max(0, full_failed - sel_failed) if full_failed > 0 else 0

# Verdict
if false_negatives > 0:
    verdict = "false_negative"
elif sel_failed > 0 and full_failed > 0:
    verdict = "safe_fail"
else:
    verdict = "safe_pass"

# Compute reduction
tests_reduced = max(0, full_total - sel_total)
tests_reduced_pct = (tests_reduced / full_total * 100) if full_total > 0 else 0
time_saved_s = full_dur - sel_dur
time_saved_pct = (time_saved_s / full_dur * 100) if full_dur > 0 else 0
accuracy = 1.0 if false_negatives == 0 else (1.0 - false_negatives / max(full_failed, 1))

total_pipeline_ms = int(($plan_dur_ms) + (sel_dur * 1000))

report = {
    "version": 1,
    "mode": "kai",
    "repo": "$name",
    "commit": "$head_commit",
    "iteration": $iter_num,
    "kaiVersion": "$kai_version_str",
    "gitRange": "${base_commit}..${head_commit}",
    "plan": {
        "durMs": $plan_dur_ms,
        "confidence": $plan_confidence,
        "fallback": $fallback,
        "mode": "$plan_mode",
        "testsSelected": $num_targets,
    },
    "selectedRun": {
        "command": sel_cmd_str,
        "exitCode": $sel_exit,
        "durationS": sel_dur,
        "testsTotal": sel_total,
        "passed": $sel_passed,
        "failed": sel_failed,
        "skipped": $sel_skipped,
    },
    "fullRun": {
        "command": full_cmd_str,
        "exitCode": $full_exit,
        "durationS": full_dur,
        "testsTotal": full_total,
        "passed": $full_passed,
        "failed": full_failed,
        "skipped": $full_skipped,
    },
    "verdict": verdict,
    "totalKaiPipelineMs": total_pipeline_ms,
    "metrics": {
        "testsReduced": tests_reduced,
        "testsReducedPct": round(tests_reduced_pct, 1),
        "timeSavedS": round(time_saved_s, 3),
        "timeSavedPct": round(time_saved_pct, 1),
        "falseNegatives": false_negatives,
        "accuracy": round(accuracy, 4),
    },
    "_bench": {
        "repo": "$name",
        "iteration": $iter_num,
        "warmup": $is_warmup_py,
        "wallTimeS": round(float("$end_time") - float("$start_time"), 3),
    },
}

with open("$report_file", "w") as f:
    json.dump(report, f, indent=2)
PYEOF

    rm -f "$sel_cmd_file" "$full_cmd_file"

    if [ "$is_warmup" = true ]; then
        rm -f "$report_file"
    fi
}

# Run a single benchmark iteration for one repo (dispatches by MODE)
run_repo_iteration() {
    local name="$1"
    local iter_num="$2"
    local is_warmup="${3:-false}"

    case $MODE in
        baseline)
            run_baseline_iteration "$name" "$iter_num" "$is_warmup"
            ;;
        kai)
            run_kai_iteration "$name" "$iter_num" "$is_warmup"
            ;;
        both)
            run_baseline_iteration "$name" "$iter_num" "$is_warmup"
            run_kai_iteration "$name" "$iter_num" "$is_warmup"
            ;;
    esac
}

# Main
main() {
    log "=== Kai Real-Repo Benchmark Harness ==="
    log "Mode: $MODE"
    log "Iterations: $WARMUP warmup + $ITERATIONS measured"
    log "Output: $OUTPUT_DIR"
    log ""

    if [ "$MODE" != "baseline" ]; then
        build_kai
    fi
    collect_env

    # Get list of repos
    local repos
    repos=$(read_repos)

    # Filter to single repo if requested
    if [ -n "$ONLY_REPO" ]; then
        if ! echo "$repos" | grep -qx "$ONLY_REPO"; then
            echo "Error: repo '$ONLY_REPO' not found in repos.json" >&2
            echo "Available repos: $(echo "$repos" | tr '\n' ' ')" >&2
            exit 1
        fi
        repos="$ONLY_REPO"
    fi

    # Clone and install all repos
    log "--- Setting up repos ---"
    mkdir -p "$CACHE_DIR"
    for name in $repos; do
        log "Setting up $name..."
        setup_repo "$name"
    done
    log ""

    # Warmup
    if [ "$WARMUP" -gt 0 ]; then
        log "--- Warmup ---"
        for i in $(seq 1 $WARMUP); do
            for name in $repos; do
                log_n "  Warmup $i/$WARMUP ($name)... "
                run_repo_iteration "$name" "$i" true
                log "done"
            done
        done
        log ""
    fi

    # Measured runs
    log "--- Measured iterations ---"
    for i in $(seq 1 $ITERATIONS); do
        for name in $repos; do
            log_n "  Iteration $i/$ITERATIONS ($name)... "
            run_repo_iteration "$name" "$i" false
            # Print progress info from whichever files were produced
            local progress=""
            if [ -f "$OUTPUT_DIR/${name}_kai_iter_${i}.json" ]; then
                progress=$(python3 -c "
import json
with open('$OUTPUT_DIR/${name}_kai_iter_${i}.json') as f:
    d = json.load(f)
b = d.get('_bench', {})
m = d.get('metrics', {})
print(f\"{m.get('testsReducedPct',0):.0f}% reduced, verdict={d.get('verdict','?')}\")
" 2>/dev/null || echo "")
            fi
            if [ -f "$OUTPUT_DIR/${name}_baseline_iter_${i}.json" ]; then
                local bl_dur
                bl_dur=$(python3 -c "
import json
with open('$OUTPUT_DIR/${name}_baseline_iter_${i}.json') as f:
    d = json.load(f)
print(f\"baseline={d['run']['durationS']:.1f}s\")
" 2>/dev/null || echo "")
                if [ -n "$progress" ]; then
                    progress="$bl_dur, $progress"
                else
                    progress="$bl_dur"
                fi
            fi
            if [ -n "$progress" ]; then
                log "done ($progress)"
            else
                log "done"
            fi
        done
    done
    log ""

    # Aggregate
    log "--- Aggregating results ---"
    python3 "$SCRIPT_DIR/repo_stats.py" "$OUTPUT_DIR"

    log ""
    log "Results written to: $OUTPUT_DIR/"
}

main
