// Package intent provides change clustering for intent generation.
package intent

import (
	"fmt"
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
	IsMixed     bool                   `json:"isMixed"`  // True if cluster has low cohesion
	SubIntents  []string               `json:"subIntents,omitempty"` // For split clusters
}

// CohesionThreshold is the minimum cohesion for a cluster to be considered cohesive.
const CohesionThreshold = 0.5

// MaxClusterSize is the maximum number of signals before considering a split.
const MaxClusterSize = 8

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
			cohesion := computeCohesion(subCluster)
			cluster := &ChangeCluster{
				ID:          generateClusterID(clusterID),
				Signals:     subCluster,
				Files:       extractFiles(subCluster),
				Modules:     []string{module},
				PrimaryArea: determinePrimaryArea(subCluster),
				ClusterType: classifyCluster(subCluster),
				Cohesion:    cohesion,
				IsMixed:     cohesion < CohesionThreshold,
			}
			clusters = append(clusters, cluster)
		}
	}

	// Step 3: Merge small clusters into related larger ones
	clusters = c.mergeSmallClusters(clusters)

	// Step 4: Split large/low-cohesion clusters
	clusters = c.splitLargeClusters(clusters, &clusterID)

	// Step 5: Refresh cohesion/mixed status after merges/splits
	for _, cluster := range clusters {
		cluster.Cohesion = computeCohesion(cluster.Signals)
		cluster.IsMixed = cluster.Cohesion < CohesionThreshold || shouldForceMixed(cluster.Signals)
	}

	// Step 6: Mark mixed clusters and compute sub-intents
	for _, cluster := range clusters {
		if cluster.IsMixed || len(cluster.Signals) > MaxClusterSize {
			cluster.SubIntents = computeSubIntents(cluster.Signals)
		}
	}

	// Sort clusters by importance (cohesion * signal weight)
	sort.Slice(clusters, func(i, j int) bool {
		scoreI := clusters[i].Cohesion * clusters[i].TotalWeight()
		scoreJ := clusters[j].Cohesion * clusters[j].TotalWeight()
		return scoreI > scoreJ
	})

	return clusters
}

// splitLargeClusters breaks up large clusters with low cohesion.
func (c *Clusterer) splitLargeClusters(clusters []*ChangeCluster, clusterID *int) []*ChangeCluster {
	var result []*ChangeCluster

	for _, cluster := range clusters {
		// Only split if cluster is large and has low cohesion
		if len(cluster.Signals) > MaxClusterSize && cluster.Cohesion < CohesionThreshold {
			subClusters := c.splitBySymbolAffinity(cluster, clusterID)
			result = append(result, subClusters...)
		} else {
			result = append(result, cluster)
		}
	}

	return result
}

// splitBySymbolAffinity splits a cluster based on shared symbols between signals.
func (c *Clusterer) splitBySymbolAffinity(cluster *ChangeCluster, clusterID *int) []*ChangeCluster {
	signals := cluster.Signals
	n := len(signals)

	if n <= 2 {
		return []*ChangeCluster{cluster}
	}

	// Build affinity matrix based on shared symbols
	affinity := make([][]float64, n)
	for i := range affinity {
		affinity[i] = make([]float64, n)
	}

	for i := 0; i < n; i++ {
		for j := i + 1; j < n; j++ {
			aff := computeSymbolAffinity(signals[i], signals[j])
			affinity[i][j] = aff
			affinity[j][i] = aff
		}
	}

	// Simple greedy clustering by affinity
	assigned := make([]int, n)
	for i := range assigned {
		assigned[i] = -1
	}

	groupID := 0
	for i := 0; i < n; i++ {
		if assigned[i] == -1 {
			// Start a new group
			assigned[i] = groupID
			// Add all signals with high affinity to this group
			for j := i + 1; j < n; j++ {
				if assigned[j] == -1 && affinity[i][j] > 0.3 {
					assigned[j] = groupID
				}
			}
			groupID++
		}
	}

	// Create new clusters from groups
	groups := make(map[int][]*detect.ChangeSignal)
	for i, gid := range assigned {
		groups[gid] = append(groups[gid], signals[i])
	}

	var result []*ChangeCluster
	for _, groupSignals := range groups {
		*clusterID++
		cohesion := computeCohesion(groupSignals)
		newCluster := &ChangeCluster{
			ID:          generateClusterID(*clusterID),
			Signals:     groupSignals,
			Files:       extractFiles(groupSignals),
			Modules:     cluster.Modules,
			PrimaryArea: determinePrimaryArea(groupSignals),
			ClusterType: classifyCluster(groupSignals),
			Cohesion:    cohesion,
			IsMixed:     cohesion < CohesionThreshold,
		}
		result = append(result, newCluster)
	}

	return result
}

// computeSymbolAffinity computes how related two signals are based on shared symbols.
func computeSymbolAffinity(a, b *detect.ChangeSignal) float64 {
	// Check for same file
	filesA := extractFilesFromSignal(a)
	filesB := extractFilesFromSignal(b)
	sameFile := hasOverlap(filesA, filesB)

	// Check for shared symbols
	symA := extractSymbolNames(a)
	symB := extractSymbolNames(b)
	sharedSymbols := countOverlap(symA, symB)

	// Check for same category
	sameCategory := a.Category == b.Category

	// Check for same tags
	tagA := a.Tags
	tagB := b.Tags
	sharedTags := countOverlap(tagA, tagB)

	// Compute weighted affinity
	affinity := 0.0
	if sameFile {
		affinity += 0.4
	}
	if sharedSymbols > 0 {
		affinity += 0.3 * float64(sharedSymbols) / float64(max(len(symA), len(symB), 1))
	}
	if sameCategory {
		affinity += 0.2
	}
	if sharedTags > 0 {
		affinity += 0.1 * float64(sharedTags) / float64(max(len(tagA), len(tagB), 1))
	}

	return affinity
}

// extractFilesFromSignal extracts file paths from a single signal.
func extractFilesFromSignal(sig *detect.ChangeSignal) []string {
	var files []string
	for _, fr := range sig.Evidence.FileRanges {
		files = append(files, fr.Path)
	}
	return files
}

// extractSymbolNames extracts symbol names from a signal.
func extractSymbolNames(sig *detect.ChangeSignal) []string {
	var names []string
	for _, sym := range sig.Evidence.Symbols {
		if strings.HasPrefix(sym, "name:") {
			names = append(names, strings.TrimPrefix(sym, "name:"))
		}
	}
	return names
}

// hasOverlap checks if two string slices have any common elements.
func hasOverlap(a, b []string) bool {
	set := make(map[string]bool)
	for _, s := range a {
		set[s] = true
	}
	for _, s := range b {
		if set[s] {
			return true
		}
	}
	return false
}

// countOverlap counts common elements between two string slices.
func countOverlap(a, b []string) int {
	set := make(map[string]bool)
	for _, s := range a {
		set[s] = true
	}
	count := 0
	for _, s := range b {
		if set[s] {
			count++
		}
	}
	return count
}

// max returns the maximum of variadic ints.
func max(vals ...int) int {
	if len(vals) == 0 {
		return 0
	}
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}

// computeSubIntents generates sub-intent descriptions for mixed clusters.
func computeSubIntents(signals []*detect.ChangeSignal) []string {
	// Group signals by category
	byCategory := make(map[detect.ChangeCategory][]*detect.ChangeSignal)
	for _, sig := range signals {
		byCategory[sig.Category] = append(byCategory[sig.Category], sig)
	}

	var subIntents []string
	for cat, sigs := range byCategory {
		if len(sigs) > 0 {
			desc := describeSignalGroup(cat, sigs)
			if desc != "" {
				subIntents = append(subIntents, desc)
			}
		}
	}

	return subIntents
}

// describeSignalGroup creates a brief description for a group of signals.
func describeSignalGroup(cat detect.ChangeCategory, signals []*detect.ChangeSignal) string {
	count := len(signals)
	switch cat {
	case detect.FunctionAdded:
		return formatCount(count, "function added", "functions added")
	case detect.FunctionRemoved:
		return formatCount(count, "function removed", "functions removed")
	case detect.FunctionRenamed:
		return formatCount(count, "function renamed", "functions renamed")
	case detect.FunctionBodyChanged:
		return formatCount(count, "function modified", "functions modified")
	case detect.DependencyAdded, detect.DependencyRemoved, detect.DependencyUpdated:
		return formatCount(count, "dependency change", "dependency changes")
	case detect.JSONValueChanged, detect.YAMLValueChanged:
		return formatCount(count, "config value changed", "config values changed")
	case detect.SchemaFieldAdded, detect.SchemaFieldRemoved, detect.SchemaFieldChanged:
		return formatCount(count, "schema change", "schema changes")
	default:
		return ""
	}
}

// formatCount formats a count with singular/plural forms.
func formatCount(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
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

// shouldForceMixed returns true when category diversity suggests mixed intent.
func shouldForceMixed(signals []*detect.ChangeSignal) bool {
	if len(signals) == 0 {
		return false
	}

	categories := make(map[detect.ChangeCategory]bool)
	hasConfig := false
	hasCode := false
	hasSchema := false
	hasDependency := false

	for _, sig := range signals {
		categories[sig.Category] = true

		switch sig.Category {
		case detect.JSONValueChanged, detect.JSONFieldAdded, detect.JSONFieldRemoved,
			detect.YAMLValueChanged, detect.YAMLKeyAdded, detect.YAMLKeyRemoved,
			detect.FeatureFlagChanged, detect.TimeoutChanged, detect.LimitChanged,
			detect.RetryConfigChanged, detect.EndpointChanged, detect.CredentialChanged:
			hasConfig = true
		case detect.SchemaFieldAdded, detect.SchemaFieldRemoved, detect.SchemaFieldChanged, detect.MigrationAdded:
			hasSchema = true
		case detect.DependencyAdded, detect.DependencyRemoved, detect.DependencyUpdated:
			hasDependency = true
		default:
			hasCode = true
		}
	}

	if len(categories) >= 3 {
		return true
	}

	if hasConfig && (hasCode || hasSchema || hasDependency) {
		return true
	}

	return false
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
