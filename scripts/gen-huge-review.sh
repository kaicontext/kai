#!/bin/bash
# Generate a huge review for testing diff performance
# Usage: ./gen-huge-review.sh <kailayer-org> <repo-name> [num_files] [lines_per_file]

set -e

ORG=${1:-"testorg"}
REPO=${2:-"huge-review-test"}
NUM_FILES=${3:-500}
LINES_PER_FILE=${4:-200}

WORKDIR=$(mktemp -d)
echo "Working in $WORKDIR"

cd "$WORKDIR"
mkdir -p "$REPO"
cd "$REPO"

# Initialize kai repo
kai init

echo "Generating $NUM_FILES files with $LINES_PER_FILE lines each..."

# Generate initial files
for i in $(seq 1 $NUM_FILES); do
    dir="src/module$((i % 20))"
    mkdir -p "$dir"
    file="$dir/file$i.go"

    echo "package module$((i % 20))" > "$file"
    echo "" >> "$file"
    echo "// File $i - auto-generated for testing" >> "$file"
    echo "" >> "$file"

    for j in $(seq 1 $LINES_PER_FILE); do
        echo "func Function${i}_${j}() string {" >> "$file"
        echo "    return \"result from function $i line $j\"" >> "$file"
        echo "}" >> "$file"
        echo "" >> "$file"
    done
done

echo "Created $NUM_FILES files"

# Create initial snapshot
echo "Creating initial snapshot..."
kai capture
echo "Created initial snapshot"

# Make changes to simulate a review
echo "Making changes to 10% of files..."
for i in $(seq 1 $((NUM_FILES / 10))); do
    idx=$((i * 10))
    dir="src/module$((idx % 20))"
    file="$dir/file$idx.go"

    if [ -f "$file" ]; then
        # Modify some lines
        sed -i '' "s/result from function/MODIFIED result from function/g" "$file" 2>/dev/null || \
        sed -i "s/result from function/MODIFIED result from function/g" "$file"
    fi
done

# Add a few new files
for i in $(seq 1 5); do
    file="src/new/newfile$i.go"
    mkdir -p "src/new"
    echo "package new" > "$file"
    echo "" >> "$file"
    echo "// Newly added file $i" >> "$file"
    echo "func NewFunction$i() {}" >> "$file"
done

echo "Modified $((NUM_FILES / 10)) files, added 5 new files"

# Create second snapshot
echo "Creating review snapshot..."
kai capture
echo "Created review snapshot"

# Changeset was already created by capture
echo "Changeset created by capture"

# Create a review
echo "Creating review..."
kai review open --title "Large Review: Changes across $((NUM_FILES / 10)) files" 2>/dev/null || echo "(review open requires remote)"

# Show stats
echo ""
echo "=== Test Data Generated ==="
echo "Location: $WORKDIR/$REPO"
echo "Files: $NUM_FILES"
echo "Lines per file: $LINES_PER_FILE"
echo "Total lines: $((NUM_FILES * LINES_PER_FILE * 4))"
echo "Modified files: $((NUM_FILES / 10))"
echo ""
echo "To push to kailayer:"
echo "  cd $WORKDIR/$REPO"
echo "  kai remote add origin git@git.kaicontext.com:$ORG/$REPO.git"
echo "  kai push"
