package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// MatrixInstance represents a single combination of matrix values.
type MatrixInstance struct {
	Values map[string]interface{} `json:"values"`
	Index  int                    `json:"index"`
}

// ExpandMatrix expands a matrix strategy into individual job instances.
func ExpandMatrix(strategy *Strategy) ([]MatrixInstance, error) {
	if strategy == nil || (len(strategy.Matrix.Values) == 0 && len(strategy.Matrix.Include) == 0) {
		// No matrix, return single instance with empty values
		return []MatrixInstance{{Values: make(map[string]interface{}), Index: 0}}, nil
	}

	// Generate all combinations from matrix values (may be empty for include-only matrices)
	var combinations []map[string]interface{}
	if len(strategy.Matrix.Values) > 0 {
		combinations = generateCombinations(strategy.Matrix.Values)
	}

	// Apply includes
	for _, include := range strategy.Matrix.Include {
		// Check if this include matches any existing combination
		matched := false
		for i, combo := range combinations {
			if partialMatch(combo, include) {
				// Merge include values into combo
				for k, v := range include {
					combinations[i][k] = v
				}
				matched = true
			}
		}
		if !matched {
			// Add as new combination
			combinations = append(combinations, include)
		}
	}

	// Apply excludes
	var filtered []map[string]interface{}
	for _, combo := range combinations {
		excluded := false
		for _, exclude := range strategy.Matrix.Exclude {
			if fullMatch(combo, exclude) {
				excluded = true
				break
			}
		}
		if !excluded {
			filtered = append(filtered, combo)
		}
	}

	// Convert to MatrixInstances
	instances := make([]MatrixInstance, len(filtered))
	for i, combo := range filtered {
		instances[i] = MatrixInstance{
			Values: combo,
			Index:  i,
		}
	}

	return instances, nil
}

// generateCombinations generates the Cartesian product of matrix values.
func generateCombinations(matrix map[string][]interface{}) []map[string]interface{} {
	if len(matrix) == 0 {
		return []map[string]interface{}{{}}
	}

	// Get sorted keys for deterministic order
	keys := make([]string, 0, len(matrix))
	for k := range matrix {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Start with single empty combination
	result := []map[string]interface{}{{}}

	// For each dimension, expand combinations
	for _, key := range keys {
		values := matrix[key]
		var newResult []map[string]interface{}

		for _, combo := range result {
			for _, value := range values {
				newCombo := make(map[string]interface{})
				for k, v := range combo {
					newCombo[k] = v
				}
				newCombo[key] = value
				newResult = append(newResult, newCombo)
			}
		}
		result = newResult
	}

	return result
}

// partialMatch checks if combo contains all key-value pairs in include.
func partialMatch(combo, include map[string]interface{}) bool {
	for k, v := range include {
		if comboVal, ok := combo[k]; ok {
			if !valuesEqual(comboVal, v) {
				return false
			}
		}
	}
	return true
}

// fullMatch checks if combo matches all key-value pairs in exclude exactly.
func fullMatch(combo, exclude map[string]interface{}) bool {
	for k, v := range exclude {
		comboVal, ok := combo[k]
		if !ok {
			return false
		}
		if !valuesEqual(comboVal, v) {
			return false
		}
	}
	return true
}

// valuesEqual compares two interface values for equality.
func valuesEqual(a, b interface{}) bool {
	// Try string comparison first
	aStr, aOk := a.(string)
	bStr, bOk := b.(string)
	if aOk && bOk {
		return aStr == bStr
	}

	// Try numeric comparison
	aFloat, aOk := toFloat64(a)
	bFloat, bOk := toFloat64(b)
	if aOk && bOk {
		return aFloat == bFloat
	}

	// Fall back to JSON comparison
	aJSON, _ := json.Marshal(a)
	bJSON, _ := json.Marshal(b)
	return string(aJSON) == string(bJSON)
}

// toFloat64 attempts to convert a value to float64.
func toFloat64(v interface{}) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case float32:
		return float64(n), true
	default:
		return 0, false
	}
}

// MatrixValuesToJSON converts matrix values to a JSON string.
func MatrixValuesToJSON(values map[string]interface{}) string {
	b, _ := json.Marshal(values)
	return string(b)
}

// MatrixValuesFromJSON parses matrix values from JSON.
func MatrixValuesFromJSON(s string) (map[string]interface{}, error) {
	var values map[string]interface{}
	if err := json.Unmarshal([]byte(s), &values); err != nil {
		return nil, err
	}
	return values, nil
}

// GetMatrixDisplayName generates a display suffix for a matrix instance.
func GetMatrixDisplayName(values map[string]interface{}) string {
	if len(values) == 0 {
		return ""
	}

	// Get sorted keys
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	// Build display string
	var parts []string
	for _, k := range keys {
		parts = append(parts, fmt.Sprintf("%v", values[k]))
	}

	if len(parts) == 1 {
		return parts[0]
	}

	result := ""
	for i, p := range parts {
		if i > 0 {
			result += ", "
		}
		result += p
	}
	return result
}

// ExpandJobsWithMatrix expands all jobs in a workflow according to their matrix strategies.
// Returns a map of job key to list of expanded job instances.
func ExpandJobsWithMatrix(wf *Workflow) (map[string][]ExpandedJob, error) {
	result := make(map[string][]ExpandedJob)

	for key, job := range wf.Jobs {
		instances, err := ExpandMatrix(job.Strategy)
		if err != nil {
			return nil, fmt.Errorf("job %s: %w", key, err)
		}

		expanded := make([]ExpandedJob, len(instances))
		for i, instance := range instances {
			name := job.GetJobDisplayName(key)
			// Resolve ${{ matrix.* }} expressions in the job name
			if len(instance.Values) > 0 {
				for mk, mv := range instance.Values {
					placeholder := fmt.Sprintf("${{ matrix.%s }}", mk)
					name = strings.ReplaceAll(name, placeholder, fmt.Sprintf("%v", mv))
				}
			}
			// If name still contains unresolved expressions, append matrix suffix
			if len(instance.Values) > 0 && !strings.Contains(name, "${{") {
				// Name was fully resolved from expressions, no suffix needed
			} else if len(instance.Values) > 0 {
				suffix := GetMatrixDisplayName(instance.Values)
				name = fmt.Sprintf("%s (%s)", name, suffix)
			}

			// Resolve ${{ matrix.* }} in runs-on labels
			resolvedJob := job
			if len(instance.Values) > 0 {
				resolvedRunsOn := make(StringOrSlice, len(job.RunsOn))
				for ri, label := range job.RunsOn {
					resolved := label
					for mk, mv := range instance.Values {
						placeholder := fmt.Sprintf("${{ matrix.%s }}", mk)
						resolved = strings.ReplaceAll(resolved, placeholder, fmt.Sprintf("%v", mv))
					}
					resolvedRunsOn[ri] = resolved
				}
				resolvedJob.RunsOn = resolvedRunsOn
			}

			expanded[i] = ExpandedJob{
				Key:          key,
				Name:         name,
				Job:          resolvedJob,
				MatrixValues: instance.Values,
				MatrixIndex:  instance.Index,
			}
		}

		result[key] = expanded
	}

	return result, nil
}

// ExpandedJob represents a job instance after matrix expansion.
type ExpandedJob struct {
	Key          string                 // Original job key
	Name         string                 // Display name with matrix suffix
	Job          Job                    // Original job definition
	MatrixValues map[string]interface{} // Matrix values for this instance
	MatrixIndex  int                    // Index in matrix expansion
}

// GetJobOrder returns jobs in dependency order (topological sort).
func GetJobOrder(wf *Workflow) ([]string, error) {
	// Build adjacency list (reverse - what jobs depend on this one)
	deps := make(map[string][]string)
	inDegree := make(map[string]int)

	for name := range wf.Jobs {
		deps[name] = []string{}
		inDegree[name] = 0
	}

	for name, job := range wf.Jobs {
		for _, need := range job.Needs {
			deps[need] = append(deps[need], name)
			inDegree[name]++
		}
	}

	// Kahn's algorithm for topological sort
	var queue []string
	for name, degree := range inDegree {
		if degree == 0 {
			queue = append(queue, name)
		}
	}

	// Sort initial queue for determinism
	sort.Strings(queue)

	var result []string
	for len(queue) > 0 {
		// Pop from queue
		name := queue[0]
		queue = queue[1:]
		result = append(result, name)

		// Reduce in-degree of dependents
		dependents := deps[name]
		sort.Strings(dependents) // Deterministic order

		for _, dep := range dependents {
			inDegree[dep]--
			if inDegree[dep] == 0 {
				queue = append(queue, dep)
			}
		}
	}

	if len(result) != len(wf.Jobs) {
		return nil, fmt.Errorf("circular dependency detected")
	}

	return result, nil
}
