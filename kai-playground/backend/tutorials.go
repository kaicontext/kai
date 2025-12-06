package main

// Tutorial represents a guided tutorial.
type Tutorial struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Steps       []Step `json:"steps"`
}

// Step represents a single tutorial step.
type Step struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Commands    []string `json:"commands"`
	Expected    string   `json:"expected"`
	GitCompare  string   `json:"gitCompare,omitempty"`
}

func getTutorials() map[string]*Tutorial {
	return map[string]*Tutorial{
		// Topic 1 — Quickstart
		"quickstart": {
			Title:       "Topic 1: Quickstart",
			Description: "Initialize kai and create your first snapshot",
			Steps: []Step{
				{
					Title:       "1.1 — Init + first snapshot",
					Description: "Initialize kai, create a snapshot, and analyze symbols.\n\nAfter analyzing, list the symbols to see what kai found (functions, classes, variables).",
					Commands: []string{
						"kai init",
						"kai snap",
						"kai list symbols @snap:last",
					},
					Expected: "symbols in",
				},
				{
					Title:       "1.2 — Human refs (optional)",
					Description: "Create human-readable references to snapshots for easier access.",
					Commands: []string{
						"kai ref set snap.base @snap:last",
						"kai ref list",
					},
					Expected: "snap.base",
				},
			},
		},

		// Topic 2 — Workspaces & staging
		"workspaces": {
			Title:       "Topic 2: Workspaces & Staging",
			Description: "Create workspaces for parallel development and stage changes",
			Steps: []Step{
				{
					Title:       "2.1 — Create a workspace",
					Description: "Create a workspace from the current directory.\n\nKai auto-snapshots for you when --base is not provided.",
					Commands: []string{
						"kai init",
						"kai ws create feat/demo",
						"kai ws list",
					},
					Expected:   "Created workspace",
					GitCompare: "git checkout -b feat/demo",
				},
				{
					Title:       "2.2 — Checkout & stage",
					Description: "Checkout the workspace (sets it as current), make a change, then stage.\n\nOnce checked out, 'kai ws stage' knows which workspace to use.",
					Commands: []string{
						"kai ws checkout feat/demo",
						"echo '// modified for demo' >> src/utils/math.js",
						"kai ws stage",
					},
					Expected: "Staged",
				},
				{
					Title:       "2.3 — Check current workspace",
					Description: "See which workspace you're on.",
					Commands: []string{
						"kai ws current",
					},
					Expected: "feat/demo",
				},
			},
		},

		// Topic 3 — Semantic diffs
		"semantic": {
			Title:       "Topic 3: Semantic Diffs (AST-aware)",
			Description: "Compare snapshots at the symbol level, not just lines",
			Steps: []Step{
				{
					Title:       "3.1 — Setup baseline",
					Description: "First, initialize kai and create a baseline snapshot to compare against.",
					Commands: []string{
						"kai init",
						"kai snap",
					},
					Expected: "Created snapshot",
				},
				{
					Title:       "3.2 — Make a change",
					Description: "Modify a file by adding a new function. This gives us something to diff.",
					Commands: []string{
						"echo 'function newFeature() { return 42; }' >> src/utils/math.js",
						"cat src/utils/math.js",
					},
					Expected: "newFeature",
				},
				{
					Title:       "3.3 — Create new snapshot",
					Description: "Create a new snapshot so kai understands the change.",
					Commands: []string{
						"kai snap",
					},
					Expected: "done",
				},
				{
					Title:       "3.4 — View semantic diff",
					Description: "Now compare the two snapshots. The semantic diff shows symbol-level changes (new function added), not just line changes.",
					Commands: []string{
						"kai changeset create @snap:prev @snap:last",
						"kai diff @snap:prev @snap:last --semantic",
					},
					Expected:   "modified",
					GitCompare: "git diff (line-level only)",
				},
			},
		},

		// Topic 4 — Modules
		"modules": {
			Title:       "Topic 4: Modules",
			Description: "Define module boundaries for your codebase",
			Steps: []Step{
				{
					Title:       "4.1 — Create module rules",
					Description: "Define which files belong to which modules in .kai/rules/modules.yaml",
					Commands: []string{
						"mkdir -p .kai/rules",
						"cat > .kai/rules/modules.yaml <<'YAML'\nmodules:\n  - name: App\n    include: [\"src/app.js\"]\n  - name: Utils\n    include: [\"src/utils/**\"]\nYAML",
					},
					Expected: "",
				},
				{
					Title:       "4.2 — Re-analyze with modules",
					Description: "Create a new snapshot to apply module rules.",
					Commands: []string{
						"kai snap",
					},
					Expected: "Created snapshot",
				},
			},
		},

		// Topic 5 — Import graph & selective CI
		"ci": {
			Title:       "Topic 5: Import Graph & Selective CI",
			Description: "Build the import graph and generate a runner-agnostic selection plan",
			Steps: []Step{
				{
					Title:       "5.1 — Setup baseline",
					Description: "Initialize kai and create a baseline snapshot.",
					Commands: []string{
						"kai init",
						"kai snap",
					},
					Expected: "Created snapshot",
				},
				{
					Title:       "5.2 — Build import graph",
					Description: "Analyze file dependencies to build the import graph.",
					Commands: []string{
						"kai analyze deps @snap:last",
					},
					Expected: "done",
				},
				{
					Title:       "5.3 — Make a change",
					Description: "Modify a file to simulate a code change.",
					Commands: []string{
						"echo '// modified' >> src/utils/math.js",
						"kai snap",
						"kai changeset create @snap:prev @snap:last",
					},
					Expected: "Created changeset",
				},
				{
					Title:       "5.4 — Generate selection plan",
					Description: "Create a runner-agnostic plan showing what targets are affected.\n\nThe plan is tool-neutral JSON - your CI decides how to execute it.",
					Commands: []string{
						"kai ci plan @cs:last --strategy=auto --out plan.json",
						"cat plan.json",
					},
					Expected: "targets",
				},
				{
					Title:       "5.5 — View plan summary",
					Description: "Use kai ci print for human-readable output.",
					Commands: []string{
						"kai ci print --plan plan.json",
						"kai ci print --plan plan.json --section targets",
					},
					Expected: "Run:",
				},
			},
		},

		// Topic 6 — Running Targets (generic)
		"mocha": {
			Title:       "Topic 6: Running Targets",
			Description: "Use the plan to run your test/build commands",
			Steps: []Step{
				{
					Title:       "6.1 — Extract targets from plan",
					Description: "The plan.json contains targets.run - a list of paths.\n\nYour CI extracts these and passes to whatever tool you use.",
					Commands: []string{
						"cat plan.json | node -e \"const p=JSON.parse(require('fs').readFileSync(0)); console.log((p.targets.run||[]).join(' '))\"",
					},
					Expected: "",
				},
				{
					Title:       "6.2 — Example: shell script",
					Description: "A simple pattern for any CI system:\n\nRUN=$(jq -r '.targets.run[]?' plan.json | tr '\\n' ' ')\n./your-test-runner $RUN",
					Commands: []string{
						"echo 'In your CI, run: ./scripts/run-tests $(jq -r \".targets.run[]\" plan.json)'",
					},
					Expected: "",
					GitCompare: "No git equivalent - git doesn't know what tests to run",
				},
			},
		},

		// Topic 7 — Reviews
		"reviews": {
			Title:       "Topic 7: Code Reviews",
			Description: "Open and manage ChangeSet-centered code reviews",
			Steps: []Step{
				{
					Title:       "7.1 — Open a review",
					Description: "Open a code review for a changeset.",
					Commands: []string{
						"kai review open @cs:last --title \"Utils: adjust add()\" --desc \"demo tweak\"",
						"kai review list",
						"kai review view <review-id>",
					},
					Expected: "Review opened",
				},
				{
					Title:       "7.2 — Approve / request changes",
					Description: "Approve the review or request changes.",
					Commands: []string{
						"kai review approve <review-id>",
						"kai review request-changes <review-id>",
					},
					Expected: "Approved",
				},
			},
		},

		// Topic 8 — Push/fetch
		"remotes": {
			Title:       "Topic 8: Push & Fetch",
			Description: "Sync workspaces with a remote kailab server",
			Steps: []Step{
				{
					Title:       "8.1 — Configure a local remote",
					Description: "Set up a remote server (requires kailabd running).",
					Commands: []string{
						"kai remote set origin http://localhost:7447 --tenant acme --repo demo",
					},
					Expected:   "origin",
					GitCompare: "git remote add origin <url>",
				},
				{
					Title:       "8.2 — Push the workspace",
					Description: "Push your workspace and review metadata to the remote.",
					Commands: []string{
						"kai push origin --ws feat/demo",
					},
					Expected:   "Pushed",
					GitCompare: "git push origin feat/demo",
				},
				{
					Title:       "8.3 — Fetch on another machine",
					Description: "Fetch and checkout the workspace elsewhere.",
					Commands: []string{
						"kai fetch origin --ws feat/demo",
						"kai ws checkout --ws feat/demo --dir .",
					},
					Expected:   "Fetched",
					GitCompare: "git fetch && git checkout feat/demo",
				},
			},
		},

		// Topic 9 — Merge & integrate
		"merge": {
			Title:       "Topic 9: Merge & Integrate",
			Description: "Integrate workspaces into target snapshots",
			Steps: []Step{
				{
					Title:       "9.1 — Integrate workspace",
					Description: "Merge a workspace into a target snapshot.",
					Commands: []string{
						"kai integrate --ws feat/demo --into @snap:prev",
					},
					Expected:   "Integrated",
					GitCompare: "git merge feat/demo",
				},
			},
		},

		// Topic 10 — Idempotency & GC
		"gc": {
			Title:       "Topic 10: Idempotency & GC",
			Description: "Understand content-addressable storage and garbage collection",
			Steps: []Step{
				{
					Title:       "10.1 — Determinism check",
					Description: "Snapshots are content-addressed. Same content = same ID.",
					Commands: []string{
						"kai snap",
						"kai snap",
					},
					Expected: "IDs should repeat when content unchanged",
				},
				{
					Title:       "10.2 — Prune unreachable objects",
					Description: "Remove objects that are no longer referenced.",
					Commands: []string{
						"kai prune --dry-run",
						"kai prune",
					},
					Expected: "Pruned",
				},
			},
		},

		// Testmap rules
		"testmap": {
			Title:       "Bonus: Testmap Rules",
			Description: "Configure test mapping for safer CI",
			Steps: []Step{
				{
					Title:       "Create testmap.yaml",
					Description: "Map modules to their test files so CI can widen safely if uncertain.",
					Commands: []string{
						"cat > .kai/rules/testmap.yaml <<'YAML'\nmodules:\n  - name: App\n    includes: [\"src/app.js\"]\n    tests:    [\"tests/app.test.js\"]\n  - name: Utils\n    includes: [\"src/utils/**\"]\n    tests:    [\"tests/math.test.js\"]\nYAML",
					},
					Expected: "",
				},
			},
		},

		// CI Pipeline
		"pipeline": {
			Title:       "Bonus: CI Pipeline",
			Description: "Complete runner-agnostic CI workflow",
			Steps: []Step{
				{
					Title:       "CI Pipeline Script",
					Description: "A complete workflow for GitHub Actions, GitLab CI, or any CI system.\n\nKai produces plan.json - your CI decides how to run it.",
					Commands: []string{
						"kai snap",
						"kai changeset create @snap:prev @snap:last",
						"kai ci plan @cs:last --strategy=auto --risk-policy=expand --out plan.json",
						"kai ci print --plan plan.json",
					},
					Expected: "Plan",
				},
				{
					Title:       "Execute in your CI",
					Description: "Extract targets and pass to your runner:\n\nGitHub Actions / GitLab / Jenkins:\nRUN=$(jq -r '.targets.run[]?' plan.json | tr '\\n' ' ')\n./scripts/run-tests $RUN",
					Commands: []string{
						"echo 'Targets to run:'",
						"cat plan.json | node -e \"const p=JSON.parse(require('fs').readFileSync(0)); (p.targets.run||[]).forEach(t=>console.log('  '+t))\"",
					},
					Expected: "",
				},
			},
		},

		// Cheat Sheet
		"cheatsheet": {
			Title:       "Cheat Sheet",
			Description: "Quick reference for all kai commands",
			Steps: []Step{
				{
					Title:       "Snapshots & Analysis",
					Description: "Core commands for capturing and analyzing code.",
					Commands: []string{
						"kai init                              # initialize repo",
						"kai snap                              # snapshot current dir",
						"kai snap src/                         # snapshot specific path",
						"kai analyze deps @snap:last           # build import graph",
						"kai ref set name @snap:last           # create named ref",
					},
					Expected: "",
				},
				{
					Title:       "Workspaces",
					Description: "Parallel development workflow.",
					Commands: []string{
						"kai ws create feat/demo            # auto-snapshot base",
						"kai ws checkout feat/demo          # switch to workspace",
						"kai ws stage                       # stage (uses current ws)",
						"kai ws stage feat/demo             # stage (explicit)",
						"kai ws current                     # show current workspace",
						"kai ws list                        # list all workspaces",
					},
					Expected:   "",
					GitCompare: "git checkout -b / git branch",
				},
				{
					Title:       "ChangeSets & Diffs",
					Description: "Semantic change tracking.",
					Commands: []string{
						"kai changeset create @snap:prev @snap:last",
						"kai diff @snap:prev @snap:last --semantic",
						"kai intent render @cs:last --edit \"description\"",
						"kai dump @cs:last --json",
					},
					Expected: "",
				},
				{
					Title:       "CI & Testing",
					Description: "Runner-agnostic selective execution.",
					Commands: []string{
						"kai ci plan @cs:last --strategy=auto --out plan.json",
						"kai ci print --plan plan.json",
						"kai ci print --plan plan.json --section targets",
					},
					Expected:   "",
					GitCompare: "No git equivalent - must run all tests",
				},
				{
					Title:       "Remotes & Reviews",
					Description: "Collaboration commands.",
					Commands: []string{
						"kai remote set origin URL --tenant T --repo R",
						"kai push origin --ws X",
						"kai fetch origin --ws X",
						"kai review open @cs:last --title T --desc D",
						"kai review approve <id>",
					},
					Expected: "",
				},
				{
					Title:       "Maintenance",
					Description: "Cleanup and integration.",
					Commands: []string{
						"kai integrate --ws X --into @snap:prev",
						"kai prune --dry-run",
						"kai prune",
					},
					Expected: "",
				},
			},
		},
	}
}
