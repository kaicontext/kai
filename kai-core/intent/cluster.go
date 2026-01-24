// Package intent provides change clustering for intent generation.
package intent

import (
	"path/filepath"
	"sort"
	"strings"

	"kai-core/detect"
)

// ClusterType represents the type of a change cluster.
type ClusterType string

const (
	ClusterTypeFeature  ClusterType = "feature"
	ClusterTypeRefactor ClusterType = "refactor"
	ClusterTypeBugfix   ClusterType = "bugfix"
	ClusterTypeConfig   ClusterType = "config"
	ClusterTypeTest     ClusterType = "test"
	ClusterTypeDocs     ClusterType = "docs"
	ClusterTypeMixed    ClusterType = "mixed"
)

// ChangeCluster represents a group of related changes.
type ChangeCluster struct {
	ID          string                 `json:"id"`
	Signals     []*detect.ChangeSignal `json:"signals"`
	Files       []string               `json:"files"`
	Modules     []string               `json:"modules"`
	PrimaryArea string                 `json:"primaryArea"`
	ClusterType ClusterType            `json:"clusterType"`
	Cohesion    float64                `json:"cohesion"` // 0.0-1.0 how related changes are
}

// Clusterer groups related changes together.
type Clusterer struct {
	CallGraph map[string][]string // file → imported files
	Modules   map[string]string   // file → module name
}

// NewClusterer creates a new clusterer.
func NewClusterer() *Clusterer {
	return &Clusterer{
		CallGraph: make(map[string][]string),
		Modules:   make(map[string]string),
	}
}

// SetCallGraph sets the import/dependency relationships between files.
func (c *Clusterer) SetCallGraph(graph map[string][]string) {
	c.CallGraph = graph
}

// SetModules sets the file to module mapping.
func (c *Clusterer) SetModules(modules map[string]string) {
	c.Modules = modules
}

// ClusterChanges groups signals into related clusters.
func (c *Clusterer) ClusterChanges(signals []*detect.ChangeSignal, moduleNames []string) []*ChangeCluster {
	if len(signals) == 0 {
		return nil
	}

	// Step 1: Group signals by module
	moduleGroups := c.groupByModule(signals, moduleNames)

	// Step 2: Within each module, group by file dependencies
	var clusters []*ChangeCluster
	clusterID := 0

	for module, moduleSignals := range moduleGroups {
		// Get files from signals
		fileSignals := c.groupByFile(moduleSignals)

		// Create sub-clusters based on file dependencies
		subClusters := c.clusterByDependency(fileSignals)

		for _, subCluster := range subClusters {
			clusterID++
			cluster := &ChangeCluster{
				ID:          generateClusterID(clusterID),
				Signals:     subCluster,
				Files:       extractFiles(subCluster),
				Modules:     []string{module},
				PrimaryArea: determinePrimaryArea(subCluster),
				ClusterType: classifyCluster(subCluster),
				Cohesion:    computeCohesion(subCluster),
			}
			clusters = append(clusters, cluster)
		}
	}

	// Step 3: Merge small clusters into related larger ones
	clusters = c.mergeSmallClusters(clusters)

	// Sort clusters by importance (cohesion * signal count)
	sort.Slice(clusters, func(i, j int) bool {
		scoreI := clusters[i].Cohesion * float64(len(clusters[i].Signals))
		scoreJ := clusters[j].Cohesion * float64(len(clusters[j].Signals))
		return scoreI > scoreJ
	})

	return clusters
}

// groupByModule groups signals by their module.
func (c *Clusterer) groupByModule(signals []*detect.ChangeSignal, moduleNames []string) map[string][]*detect.ChangeSignal {
	groups := make(map[string][]*detect.ChangeSignal)

	// Default module if none specified
	defaultModule := "General"
	if len(moduleNames) > 0 {
		defaultModule = moduleNames[0]
	}

	for _, sig := range signals {
		module := defaultModule

		// Try to determine module from file path
		for _, fr := range sig.Evidence.FileRanges {
			if mod, exists := c.Modules[fr.Path]; exists {
				module = mod
				break
			}
		}

		groups[module] = append(groups[module], sig)
	}

	return groups
}

// groupByFile groups signals by their primary file.
func (c *Clusterer) groupByFile(signals []*detect.ChangeSignal) map[string][]*detect.ChangeSignal {
	groups := make(map[string][]*detect.ChangeSignal)

	for _, sig := range signals {
		file := ""
		if len(sig.Evidence.FileRanges) > 0 {
			file = sig.Evidence.FileRanges[0].Path
		}
		groups[file] = append(groups[file], sig)
	}

	return groups
}

// clusterByDependency clusters files based on their import relationships.
func (c *Clusterer) clusterByDependency(fileSignals map[string][]*detect.ChangeSignal) [][]*detect.ChangeSignal {
	if len(fileSignals) == 0 {
		return nil
	}

	// Build union-find structure for clustering
	files := make([]string, 0, len(fileSignals))
	for f := range fileSignals {
		files = append(files, f)
	}

	// Find connected components based on call graph
	fileIndex := make(map[string]int)
	for i, f := range files {
		fileIndex[f] = i
	}

	parent := make([]int, len(files))
	for i := range parent {
		parent[i] = i
	}

	var find func(int) int
	find = func(x int) int {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}

	union := func(x, y int) {
		px, py := find(x), find(y)
		if px != py {
			parent[px] = py
		}
	}

	// Union files that are related by imports
	for file, imports := range c.CallGraph {
		if idx, exists := fileIndex[file]; exists {
			for _, importedFile := range imports {
				if impIdx, impExists := fileIndex[importedFile]; impExists {
					union(idx, impIdx)
				}
			}
		}
	}

	// Also union files in the same directory
	for i, fileA := range files {
		dirA := filepath.Dir(fileA)
		for j := i + 1; j < len(files); j++ {
			fileB := files[j]
			dirB := filepath.Dir(fileB)
			if dirA == dirB {
				union(i, j)
			}
		}
	}

	// Group signals by component
	components := make(map[int][]*detect.ChangeSignal)
	for file, sigs := range fileSignals {
		if idx, exists := fileIndex[file]; exists {
			root := find(idx)
			components[root] = append(components[root], sigs...)
		}
	}

	// Convert to slice of clusters
	var result [][]*detect.ChangeSignal
	for _, sigs := range components {
		result = append(result, sigs)
	}

	return result
}

// mergeSmallClusters merges clusters with only 1 signal into related larger ones.
func (c *Clusterer) mergeSmallClusters(clusters []*ChangeCluster) []*ChangeCluster {
	if len(clusters) <= 1 {
		return clusters
	}

	const minClusterSize = 2

	var large, small []*ChangeCluster
	for _, cluster := range clusters {
		if len(cluster.Signals) >= minClusterSize {
			large = append(large, cluster)
		} else {
			small = append(small, cluster)
		}
	}

	// Try to merge small clusters into large ones by module
	for _, smallCluster := range small {
		merged := false
		for _, largeCluster := range large {
			// Check if they share a module
			if hasCommonModule(smallCluster.Modules, largeCluster.Modules) {
				largeCluster.Signals = append(largeCluster.Signals, smallCluster.Signals...)
				largeCluster.Files = unique(append(largeCluster.Files, smallCluster.Files...))
				largeCluster.Cohesion = computeCohesion(largeCluster.Signals)
				merged = true
				break
			}
		}
		if !merged {
			// Keep as separate cluster
			large = append(large, smallCluster)
		}
	}

	return large
}

// generateClusterID generates a unique cluster ID.
func generateClusterID(n int) string {
	return strings.ToUpper(string(rune('A' + (n-1)%26)))
}

// extractFiles extracts unique file paths from signals.
func extractFiles(signals []*detect.ChangeSignal) []string {
	seen := make(map[string]bool)
	var files []string

	for _, sig := range signals {
		for _, fr := range sig.Evidence.FileRanges {
			if !seen[fr.Path] {
				seen[fr.Path] = true
				files = append(files, fr.Path)
			}
		}
	}

	return files
}

// determinePrimaryArea determines the primary area name from signals.
func determinePrimaryArea(signals []*detect.ChangeSignal) string {
	// Count function names
	funcNames := make(map[string]int)
	for _, sig := range signals {
		for _, sym := range sig.Evidence.Symbols {
			if strings.HasPrefix(sym, "name:") {
				name := strings.TrimPrefix(sym, "name:")
				funcNames[name]++
			}
		}
	}

	// Find most common function name
	var bestName string
	var bestCount int
	for name, count := range funcNames {
		if count > bestCount {
			bestName = name
			bestCount = count
		}
	}

	if bestName != "" {
		return bestName
	}

	// Fall back to common file path
	files := extractFiles(signals)
	if len(files) == 1 {
		base := filepath.Base(files[0])
		ext := filepath.Ext(base)
		return strings.TrimSuffix(base, ext)
	}
	if len(files) > 0 {
		return getCommonDir(files)
	}

	return "codebase"
}

// getCommonDir finds the common directory among file paths.
func getCommonDir(paths []string) string {
	if len(paths) == 0 {
		return "codebase"
	}

	dirs := make([][]string, len(paths))
	minLen := -1
	for i, p := range paths {
		dirs[i] = strings.Split(filepath.Dir(p), string(filepath.Separator))
		if minLen == -1 || len(dirs[i]) < minLen {
			minLen = len(dirs[i])
		}
	}

	var common []string
	for i := 0; i < minLen; i++ {
		val := dirs[0][i]
		allMatch := true
		for j := 1; j < len(dirs); j++ {
			if dirs[j][i] != val {
				allMatch = false
				break
			}
		}
		if allMatch {
			common = append(common, val)
		} else {
			break
		}
	}

	if len(common) > 0 {
		for i := len(common) - 1; i >= 0; i-- {
			if common[i] != "" && common[i] != "." {
				return common[i]
			}
		}
	}

	return "codebase"
}

// classifyCluster determines the cluster type based on signals.
func classifyCluster(signals []*detect.ChangeSignal) ClusterType {
	var hasTest, hasConfig, hasDocs bool
	var hasAdd, hasRemove, hasChange bool

	for _, sig := range signals {
		// Check tags
		for _, tag := range sig.Tags {
			switch tag {
			case "test":
				hasTest = true
			case "config":
				hasConfig = true
			}
		}

		// Check category
		switch sig.Category {
		case detect.FunctionAdded, detect.FileAdded, detect.DependencyAdded, detect.ImportAdded:
			hasAdd = true
		case detect.FunctionRemoved, detect.FileDeleted, detect.DependencyRemoved, detect.ImportRemoved:
			hasRemove = true
		case detect.FunctionBodyChanged, detect.FunctionRenamed, detect.ConditionChanged, detect.ConstantUpdated:
			hasChange = true
		}

		// Check file paths for docs
		for _, fr := range sig.Evidence.FileRanges {
			if isDocFile(fr.Path) {
				hasDocs = true
			}
		}
	}

	// Determine cluster type
	if hasTest && !hasAdd && !hasRemove {
		return ClusterTypeTest
	}
	if hasDocs && !hasAdd && !hasRemove {
		return ClusterTypeDocs
	}
	if hasConfig && !hasAdd && !hasRemove {
		return ClusterTypeConfig
	}
	if hasAdd && hasRemove {
		return ClusterTypeRefactor
	}
	if hasAdd && !hasRemove && !hasChange {
		return ClusterTypeFeature
	}
	if hasChange && !hasAdd && !hasRemove {
		return ClusterTypeBugfix
	}

	return ClusterTypeMixed
}

// isDocFile checks if a file is documentation.
func isDocFile(path string) bool {
	docExts := []string{".md", ".txt", ".rst", ".adoc"}
	for _, ext := range docExts {
		if strings.HasSuffix(path, ext) {
			return true
		}
	}
	return false
}

// computeCohesion computes how related the signals in a cluster are.
// Higher cohesion means more related changes.
func computeCohesion(signals []*detect.ChangeSignal) float64 {
	if len(signals) <= 1 {
		return 1.0
	}

	// Factors that increase cohesion:
	// 1. All signals have the same category
	// 2. All signals are in the same file
	// 3. Signals share common tags
	// 4. Signals have high confidence

	var score float64 = 0

	// Category consistency
	categories := make(map[detect.ChangeCategory]int)
	for _, sig := range signals {
		categories[sig.Category]++
	}
	maxCategoryCount := 0
	for _, count := range categories {
		if count > maxCategoryCount {
			maxCategoryCount = count
		}
	}
	score += 0.3 * float64(maxCategoryCount) / float64(len(signals))

	// File consistency
	files := extractFiles(signals)
	fileScore := 1.0 / float64(len(files))
	if fileScore > 1 {
		fileScore = 1
	}
	score += 0.3 * fileScore

	// Tag overlap
	allTags := make(map[string]int)
	for _, sig := range signals {
		for _, tag := range sig.Tags {
			allTags[tag]++
		}
	}
	maxTagCount := 0
	for _, count := range allTags {
		if count > maxTagCount {
			maxTagCount = count
		}
	}
	if len(signals) > 0 {
		score += 0.2 * float64(maxTagCount) / float64(len(signals))
	}

	// Average confidence
	var totalConf float64
	for _, sig := range signals {
		totalConf += sig.Confidence
	}
	score += 0.2 * (totalConf / float64(len(signals)))

	return score
}

// hasCommonModule checks if two module lists share any module.
func hasCommonModule(a, b []string) bool {
	set := make(map[string]bool)
	for _, m := range a {
		set[m] = true
	}
	for _, m := range b {
		if set[m] {
			return true
		}
	}
	return false
}

// unique returns unique strings from a slice.
func unique(strs []string) []string {
	seen := make(map[string]bool)
	var result []string
	for _, s := range strs {
		if !seen[s] {
			seen[s] = true
			result = append(result, s)
		}
	}
	return result
}

// TotalWeight returns the sum of all signal weights in the cluster.
func (c *ChangeCluster) TotalWeight() float64 {
	var total float64
	for _, sig := range c.Signals {
		total += sig.Weight
	}
	return total
}

// AverageConfidence returns the average confidence of signals in the cluster.
func (c *ChangeCluster) AverageConfidence() float64 {
	if len(c.Signals) == 0 {
		return 0
	}
	var total float64
	for _, sig := range c.Signals {
		total += sig.Confidence
	}
	return total / float64(len(c.Signals))
}

// HasCategory checks if the cluster contains a signal with the given category.
func (c *ChangeCluster) HasCategory(category detect.ChangeCategory) bool {
	for _, sig := range c.Signals {
		if sig.Category == category {
			return true
		}
	}
	return false
}

// CategoryCount returns the count of signals with the given category.
func (c *ChangeCluster) CategoryCount(category detect.ChangeCategory) int {
	count := 0
	for _, sig := range c.Signals {
		if sig.Category == category {
			count++
		}
	}
	return count
}
