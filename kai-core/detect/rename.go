// Package detect provides rename detection for functions.
package detect

import (
	"kai-core/parse"
)

// RenameDetector detects function renames by comparing body similarity.
type RenameDetector struct {
	SimilarityThreshold float64 // default 0.7
}

// NewRenameDetector creates a new rename detector with default settings.
func NewRenameDetector() *RenameDetector {
	return &RenameDetector{
		SimilarityThreshold: 0.7,
	}
}

// RenameCandidate represents a potential rename detection.
type RenameCandidate struct {
	OldName    string
	NewName    string
	Similarity float64
	OldBody    string
	NewBody    string
}

// DetectRenames identifies function renames by comparing removed and added functions.
// It returns ChangeSignals for detected renames.
func (d *RenameDetector) DetectRenames(
	beforeFuncs, afterFuncs map[string]*FuncInfo,
	path string,
) []*ChangeSignal {
	var signals []*ChangeSignal

	// Find removed functions (in before, not in after)
	var removed []*FuncInfo
	for name, info := range beforeFuncs {
		if _, exists := afterFuncs[name]; !exists {
			removed = append(removed, info)
		}
	}

	// Find added functions (in after, not in before)
	var added []*FuncInfo
	for name, info := range afterFuncs {
		if _, exists := beforeFuncs[name]; !exists {
			added = append(added, info)
		}
	}

	// For each removed+added pair, compute body similarity
	usedAdded := make(map[string]bool)
	for _, removedFunc := range removed {
		if removedFunc.Body == "" {
			continue
		}

		var bestMatch *FuncInfo
		var bestSimilarity float64

		for _, addedFunc := range added {
			if usedAdded[addedFunc.Name] || addedFunc.Body == "" {
				continue
			}

			similarity := computeSimilarity(removedFunc.Body, addedFunc.Body)
			if similarity > bestSimilarity && similarity >= d.SimilarityThreshold {
				bestSimilarity = similarity
				bestMatch = addedFunc
			}
		}

		if bestMatch != nil {
			usedAdded[bestMatch.Name] = true

			// Create a FUNCTION_RENAMED signal
			var fileRanges []FileRange
			if bestMatch.Node != nil {
				afterRange := parse.GetNodeRange(bestMatch.Node)
				fileRanges = []FileRange{{
					Path:  path,
					Start: afterRange.Start,
					End:   afterRange.End,
				}}
			} else {
				fileRanges = []FileRange{{Path: path}}
			}

			signal := &ChangeSignal{
				Category: FunctionRenamed,
				Evidence: ExtendedEvidence{
					FileRanges: fileRanges,
					Symbols:    []string{"name:" + bestMatch.Name, "oldname:" + removedFunc.Name},
					OldName:    removedFunc.Name,
					NewName:    bestMatch.Name,
				},
				Weight:     0.9,
				Confidence: bestSimilarity, // Use similarity as confidence
				Tags:       []string{"api"}, // Renames are API changes
			}
			signals = append(signals, signal)
		}
	}

	return signals
}

// computeSimilarity computes the Levenshtein-based similarity between two strings.
// Returns a value between 0.0 (completely different) and 1.0 (identical).
func computeSimilarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	if len(a) == 0 || len(b) == 0 {
		return 0.0
	}

	distance := levenshteinDistance(a, b)
	maxLen := len(a)
	if len(b) > maxLen {
		maxLen = len(b)
	}

	return 1.0 - float64(distance)/float64(maxLen)
}

// levenshteinDistance computes the edit distance between two strings.
func levenshteinDistance(a, b string) int {
	if len(a) == 0 {
		return len(b)
	}
	if len(b) == 0 {
		return len(a)
	}

	// Create a matrix to store distances
	m := len(a)
	n := len(b)

	// Use two rows for memory efficiency
	prev := make([]int, n+1)
	curr := make([]int, n+1)

	// Initialize first row
	for j := 0; j <= n; j++ {
		prev[j] = j
	}

	// Fill in the matrix
	for i := 1; i <= m; i++ {
		curr[0] = i
		for j := 1; j <= n; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = minOf3(
				prev[j]+1,      // deletion
				curr[j-1]+1,    // insertion
				prev[j-1]+cost, // substitution
			)
		}
		prev, curr = curr, prev
	}

	return prev[n]
}

// minOf3 returns the minimum of three integers.
func minOf3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

// TokenBasedSimilarity provides an alternative similarity metric based on tokens.
// This can be more robust for code comparison as it ignores whitespace differences.
func TokenBasedSimilarity(a, b string) float64 {
	tokensA := tokenize(a)
	tokensB := tokenize(b)

	if len(tokensA) == 0 && len(tokensB) == 0 {
		return 1.0
	}
	if len(tokensA) == 0 || len(tokensB) == 0 {
		return 0.0
	}

	// Count common tokens
	setA := make(map[string]int)
	for _, t := range tokensA {
		setA[t]++
	}

	common := 0
	for _, t := range tokensB {
		if count, exists := setA[t]; exists && count > 0 {
			common++
			setA[t]--
		}
	}

	// Jaccard-like similarity
	total := len(tokensA) + len(tokensB) - common
	if total == 0 {
		return 1.0
	}
	return float64(common) / float64(total)
}

// tokenize splits code into tokens, ignoring whitespace.
func tokenize(code string) []string {
	var tokens []string
	var current []byte

	for i := 0; i < len(code); i++ {
		c := code[i]
		switch {
		case isWhitespace(c):
			if len(current) > 0 {
				tokens = append(tokens, string(current))
				current = current[:0]
			}
		case isDelimiter(c):
			if len(current) > 0 {
				tokens = append(tokens, string(current))
				current = current[:0]
			}
			tokens = append(tokens, string(c))
		default:
			current = append(current, c)
		}
	}

	if len(current) > 0 {
		tokens = append(tokens, string(current))
	}

	return tokens
}

func isWhitespace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

func isDelimiter(c byte) bool {
	delimiters := "(){}[];,.:+-*/<>=!&|^~?@"
	for i := 0; i < len(delimiters); i++ {
		if c == delimiters[i] {
			return true
		}
	}
	return false
}

// DetectRenamesFromContent is a convenience function that parses content and detects renames.
func (d *RenameDetector) DetectRenamesFromContent(
	path string,
	beforeContent, afterContent []byte,
	parser *parse.Parser,
	lang string,
) ([]*ChangeSignal, error) {
	beforeParsed, err := parser.Parse(beforeContent, lang)
	if err != nil {
		return nil, err
	}

	afterParsed, err := parser.Parse(afterContent, lang)
	if err != nil {
		return nil, err
	}

	beforeFuncs := GetAllFunctions(beforeParsed, beforeContent)
	afterFuncs := GetAllFunctions(afterParsed, afterContent)

	return d.DetectRenames(beforeFuncs, afterFuncs, path), nil
}
