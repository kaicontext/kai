// Package parse re-exports tree-sitter parsing from kai-core.
package parse

import (
	coreparse "kai-core/parse"
)

// Re-export types from kai-core/parse
type Range = coreparse.Range
type Symbol = coreparse.Symbol
type ParsedFile = coreparse.ParsedFile
type Parser = coreparse.Parser

// Re-export functions from kai-core/parse
var (
	NewParser      = coreparse.NewParser
	GetNodeRange   = coreparse.GetNodeRange
	GetNodeContent = coreparse.GetNodeContent
	RangesOverlap  = coreparse.RangesOverlap
)
