// Package classify re-exports change detection from kai-core.
package classify

import (
	"kai-core/detect"
	coregraph "kai-core/graph"
)

// Re-export types from kai-core/detect
type ChangeCategory = detect.ChangeCategory
type FileRange = detect.FileRange
type Evidence = detect.Evidence
type ChangeType = detect.ChangeType
type JSONSymbol = detect.JSONSymbol

// Detector wraps kai-core/detect.Detector to use local graph.Node type
type Detector struct {
	inner *detect.Detector
}

// NewDetector creates a new change detector.
func NewDetector() *Detector {
	return &Detector{inner: detect.NewDetector()}
}

// SetSymbols sets the symbols for a file (used for mapping changes to symbols).
func (d *Detector) SetSymbols(fileID string, symbols []*coregraph.Node) {
	d.inner.SetSymbols(fileID, symbols)
}

// DetectChanges detects all change types between two versions of a file.
func (d *Detector) DetectChanges(path string, beforeContent, afterContent []byte, fileID string) ([]*ChangeType, error) {
	return d.inner.DetectChanges(path, beforeContent, afterContent, fileID)
}

// DetectFileChange creates a FILE_CONTENT_CHANGED for non-parseable files.
func (d *Detector) DetectFileChange(path string, lang string) *ChangeType {
	return d.inner.DetectFileChange(path, lang)
}

// Re-export constants from kai-core/detect
const (
	ConditionChanged   = detect.ConditionChanged
	ConstantUpdated    = detect.ConstantUpdated
	APISurfaceChanged  = detect.APISurfaceChanged
	FunctionAdded      = detect.FunctionAdded
	FunctionRemoved    = detect.FunctionRemoved
	FileContentChanged = detect.FileContentChanged
	FileAdded          = detect.FileAdded
	FileDeleted        = detect.FileDeleted
	JSONFieldAdded     = detect.JSONFieldAdded
	JSONFieldRemoved   = detect.JSONFieldRemoved
	JSONValueChanged   = detect.JSONValueChanged
	JSONArrayChanged   = detect.JSONArrayChanged
	YAMLKeyAdded       = detect.YAMLKeyAdded
	YAMLKeyRemoved     = detect.YAMLKeyRemoved
	YAMLValueChanged   = detect.YAMLValueChanged
)

// Re-export functions from kai-core/detect
var (
	GetCategoryPayload = detect.GetCategoryPayload
	NewFileChange      = detect.NewFileChange
	IsParseable        = detect.IsParseable
	ExtractJSONSymbols = detect.ExtractJSONSymbols
	DetectJSONChanges  = detect.DetectJSONChanges
	FormatJSONPath     = detect.FormatJSONPath
	IsPackageJSON      = detect.IsPackageJSON
	IsTSConfig         = detect.IsTSConfig
)
