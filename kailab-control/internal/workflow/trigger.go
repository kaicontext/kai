package workflow

import (
	"path/filepath"
	"strings"
)

// TriggerEvent represents an event that can trigger workflows.
type TriggerEvent struct {
	Type    string                 // push, review_created, review_updated, workflow_dispatch
	Ref     string                 // refs/heads/main, refs/tags/v1.0.0
	SHA     string                 // Commit SHA
	Payload map[string]interface{} // Event-specific data
}

// MatchTrigger checks if an event matches the workflow trigger configuration.
func (w *Workflow) MatchTrigger(event *TriggerEvent) bool {
	switch event.Type {
	case "push":
		return w.matchPushTrigger(event)
	case "review_created", "review_updated":
		return w.matchReviewTrigger(event) || w.matchPullRequestTrigger(event)
	case "workflow_dispatch":
		return w.On.WorkflowDispatch != nil
	case "schedule":
		return len(w.On.Schedule) > 0
	case "workflow_call":
		return w.On.WorkflowCall != nil
	default:
		return false
	}
}

// matchPushTrigger checks if a push event matches the push trigger.
func (w *Workflow) matchPushTrigger(event *TriggerEvent) bool {
	if w.On.Push == nil {
		return false
	}

	push := w.On.Push

	// Extract branch/tag name from ref
	ref := event.Ref
	isBranch := strings.HasPrefix(ref, "refs/heads/")
	isTag := strings.HasPrefix(ref, "refs/tags/")

	var name string
	if isBranch {
		name = strings.TrimPrefix(ref, "refs/heads/")
	} else if isTag {
		name = strings.TrimPrefix(ref, "refs/tags/")
	}

	// Check tags
	if isTag {
		if len(push.Tags) > 0 {
			if !matchPatterns(name, push.Tags) {
				return false
			}
		}
		if len(push.TagsIgnore) > 0 {
			if matchPatterns(name, push.TagsIgnore) {
				return false
			}
		}
		// If no tag filters, and this is a tag push, check if branch filters exist
		// (tags only match if tag filters exist or no branch filters exist)
		if len(push.Tags) == 0 && len(push.TagsIgnore) == 0 {
			if len(push.Branches) > 0 || len(push.BranchesIgnore) > 0 {
				return false
			}
		}
	}

	// Check branches
	if isBranch {
		if len(push.Branches) > 0 {
			if !matchPatterns(name, push.Branches) {
				return false
			}
		}
		if len(push.BranchesIgnore) > 0 {
			if matchPatterns(name, push.BranchesIgnore) {
				return false
			}
		}
	}

	// Check paths (if provided in payload)
	if paths, ok := event.Payload["changed_files"].([]string); ok {
		if len(push.Paths) > 0 {
			if !matchAnyPath(paths, push.Paths) {
				return false
			}
		}
		if len(push.PathsIgnore) > 0 {
			if matchAllPaths(paths, push.PathsIgnore) {
				return false
			}
		}
	}

	return true
}

// matchReviewTrigger checks if a review event matches the review trigger.
func (w *Workflow) matchReviewTrigger(event *TriggerEvent) bool {
	if w.On.Review == nil {
		return false
	}

	review := w.On.Review

	// Map event type to review action
	var action string
	switch event.Type {
	case "review_created":
		action = "opened"
	case "review_updated":
		action = "synchronize"
	default:
		return false
	}

	// If no types specified, match all
	if len(review.Types) == 0 {
		return true
	}

	// Check if action is in types list
	for _, t := range review.Types {
		if t == action {
			return true
		}
	}

	return false
}

// matchPullRequestTrigger checks if a review event matches a pull_request trigger.
// This provides GitHub Actions compatibility by mapping pull_request triggers to Kai review events.
func (w *Workflow) matchPullRequestTrigger(event *TriggerEvent) bool {
	if w.On.PullRequest == nil {
		return false
	}

	pr := w.On.PullRequest

	// Map Kai event type to pull_request action
	var action string
	switch event.Type {
	case "review_created":
		action = "opened"
	case "review_updated":
		action = "synchronize"
	default:
		return false
	}

	// If types are specified, check if action matches
	if len(pr.Types) > 0 {
		matched := false
		for _, t := range pr.Types {
			if t == action {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	// Check branch filters against the target branch
	if targetBranch, ok := event.Payload["target_branch"].(string); ok && targetBranch != "" {
		if len(pr.Branches) > 0 {
			if !matchPatterns(targetBranch, pr.Branches) {
				return false
			}
		}
		if len(pr.BranchesIgnore) > 0 {
			if matchPatterns(targetBranch, pr.BranchesIgnore) {
				return false
			}
		}
	}

	// Check path filters
	if paths, ok := event.Payload["changed_files"].([]string); ok {
		if len(pr.Paths) > 0 {
			if !matchAnyPath(paths, pr.Paths) {
				return false
			}
		}
		if len(pr.PathsIgnore) > 0 {
			if matchAllPaths(paths, pr.PathsIgnore) {
				return false
			}
		}
	}

	return true
}

// matchPatterns checks if a name matches any of the glob patterns.
func matchPatterns(name string, patterns []string) bool {
	for _, pattern := range patterns {
		if matchPattern(name, pattern) {
			return true
		}
	}
	return false
}

// matchPattern checks if a name matches a glob pattern.
// Supports:
// - * matches any sequence except /
// - ** matches any sequence including /
// - ? matches any single character
func matchPattern(name, pattern string) bool {
	// Handle ** patterns
	if strings.Contains(pattern, "**") {
		return matchDoubleStarPattern(name, pattern)
	}

	// Use filepath.Match for simple patterns
	matched, _ := filepath.Match(pattern, name)
	return matched
}

// matchDoubleStarPattern handles ** glob patterns.
func matchDoubleStarPattern(name, pattern string) bool {
	// Split pattern by **
	parts := strings.Split(pattern, "**")

	if len(parts) == 1 {
		// No **, use simple match
		matched, _ := filepath.Match(pattern, name)
		return matched
	}

	// Check prefix
	if parts[0] != "" {
		prefix := strings.TrimSuffix(parts[0], "/")
		if prefix != "" && !strings.HasPrefix(name, prefix) {
			return false
		}
		name = strings.TrimPrefix(name, prefix)
		name = strings.TrimPrefix(name, "/")
	}

	// Check suffix
	if len(parts) > 1 && parts[len(parts)-1] != "" {
		suffix := strings.TrimPrefix(parts[len(parts)-1], "/")
		if suffix != "" && !strings.HasSuffix(name, suffix) {
			return false
		}
	}

	// For patterns like "feature/**", any remaining name after prefix is valid
	return true
}

// matchAnyPath checks if any of the changed files match any of the path patterns.
func matchAnyPath(changedFiles, patterns []string) bool {
	for _, file := range changedFiles {
		for _, pattern := range patterns {
			if matchPathPattern(file, pattern) {
				return true
			}
		}
	}
	return false
}

// matchAllPaths checks if all changed files match any of the path patterns (for ignore).
func matchAllPaths(changedFiles, patterns []string) bool {
	for _, file := range changedFiles {
		matched := false
		for _, pattern := range patterns {
			if matchPathPattern(file, pattern) {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}
	return true
}

// matchPathPattern matches a file path against a pattern.
func matchPathPattern(path, pattern string) bool {
	// Normalize paths
	path = strings.TrimPrefix(path, "/")
	pattern = strings.TrimPrefix(pattern, "/")

	// Handle **
	if strings.Contains(pattern, "**") {
		return matchDoubleStarPath(path, pattern)
	}

	// Simple glob match
	matched, _ := filepath.Match(pattern, path)
	if matched {
		return true
	}

	// Try matching as directory prefix
	if strings.HasSuffix(pattern, "/*") {
		dir := strings.TrimSuffix(pattern, "/*")
		if strings.HasPrefix(path, dir+"/") {
			return true
		}
	}

	return false
}

// matchDoubleStarPath handles ** patterns in paths.
func matchDoubleStarPath(path, pattern string) bool {
	parts := strings.Split(pattern, "**")

	// Check prefix
	if parts[0] != "" {
		prefix := strings.TrimSuffix(parts[0], "/")
		if !strings.HasPrefix(path, prefix) {
			return false
		}
		path = strings.TrimPrefix(path, prefix)
		path = strings.TrimPrefix(path, "/")
	}

	// Check suffix
	if len(parts) > 1 && parts[len(parts)-1] != "" {
		suffix := strings.TrimPrefix(parts[len(parts)-1], "/")
		// Handle patterns like **/*.go
		if strings.HasPrefix(suffix, "*.") {
			ext := strings.TrimPrefix(suffix, "*")
			return strings.HasSuffix(path, ext)
		}
		if !strings.HasSuffix(path, suffix) {
			return false
		}
	}

	return true
}

// FilterWorkflowsByEvent filters workflows that should run for a given event.
func FilterWorkflowsByEvent(workflows []*Workflow, event *TriggerEvent) []*Workflow {
	var matching []*Workflow
	for _, wf := range workflows {
		if wf.MatchTrigger(event) {
			matching = append(matching, wf)
		}
	}
	return matching
}
