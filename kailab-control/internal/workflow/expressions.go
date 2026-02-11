package workflow

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode"
)

// ExprContext holds all variables available during expression evaluation.
type ExprContext struct {
	GitHub  map[string]interface{} // github.* context
	Env     map[string]string      // env.* context
	Secrets map[string]string      // secrets.* context
	Matrix  map[string]interface{} // matrix.* context
	Steps   map[string]StepResult  // steps.* context
	Needs   map[string]JobResult   // needs.* context
	Runner  map[string]string      // runner.* context
	Inputs  map[string]string      // inputs.* context
	Vars    map[string]string      // vars.* context

	// HashFilesFunc is called by hashFiles() to compute a SHA-256 hash of files
	// matching the given glob patterns. The runner sets this to execute inside the
	// job pod where the workspace files live. Returns hex-encoded hash or empty string.
	HashFilesFunc func(patterns []string) string
}

// StepResult holds the result of a completed step.
type StepResult struct {
	Outputs    map[string]string `json:"outputs"`
	Outcome    string            `json:"outcome"`    // success, failure, cancelled, skipped
	Conclusion string            `json:"conclusion"` // success, failure, cancelled, skipped
}

// JobResult holds the result of a completed job.
type JobResult struct {
	Outputs map[string]string `json:"outputs"`
	Result  string            `json:"result"` // success, failure, cancelled, skipped
}

// NewExprContext creates an ExprContext with initialized maps.
func NewExprContext() *ExprContext {
	return &ExprContext{
		GitHub:  make(map[string]interface{}),
		Env:     make(map[string]string),
		Secrets: make(map[string]string),
		Matrix:  make(map[string]interface{}),
		Steps:   make(map[string]StepResult),
		Needs:   make(map[string]JobResult),
		Runner:  make(map[string]string),
		Inputs:  make(map[string]string),
		Vars:    make(map[string]string),
	}
}

// Interpolate replaces all ${{ expr }} occurrences in a string with evaluated values.
func Interpolate(s string, ctx *ExprContext) string {
	var result strings.Builder
	i := 0
	for i < len(s) {
		// Look for ${{
		idx := strings.Index(s[i:], "${{")
		if idx == -1 {
			result.WriteString(s[i:])
			break
		}
		result.WriteString(s[i : i+idx])
		i += idx + 3 // skip past ${{

		// Find matching }}
		end := strings.Index(s[i:], "}}")
		if end == -1 {
			// No closing }}, write literally
			result.WriteString("${{")
			continue
		}
		expr := strings.TrimSpace(s[i : i+end])
		i += end + 2 // skip past }}

		val := EvalExpr(expr, ctx)
		result.WriteString(exprToString(val))
	}
	return result.String()
}

// EvalExpr evaluates a GitHub Actions expression and returns the result.
func EvalExpr(expr string, ctx *ExprContext) interface{} {
	p := &exprParser{input: expr, pos: 0, ctx: ctx}
	return p.parseOr()
}

// EvalExprBool evaluates an expression and coerces the result to bool.
// Used for `if:` conditions.
func EvalExprBool(expr string, ctx *ExprContext) bool {
	// GitHub Actions if: conditions have implicit ${{ }} wrapping
	expr = strings.TrimSpace(expr)
	expr = strings.TrimPrefix(expr, "${{")
	expr = strings.TrimSuffix(expr, "}}")
	expr = strings.TrimSpace(expr)

	val := EvalExpr(expr, ctx)
	return toBool(val)
}

// exprParser is a recursive descent parser for GitHub Actions expressions.
type exprParser struct {
	input string
	pos   int
	ctx   *ExprContext
}

func (p *exprParser) peek() byte {
	p.skipWhitespace()
	if p.pos >= len(p.input) {
		return 0
	}
	return p.input[p.pos]
}

func (p *exprParser) skipWhitespace() {
	for p.pos < len(p.input) && (p.input[p.pos] == ' ' || p.input[p.pos] == '\t') {
		p.pos++
	}
}

func (p *exprParser) matchStr(s string) bool {
	p.skipWhitespace()
	if p.pos+len(s) <= len(p.input) && p.input[p.pos:p.pos+len(s)] == s {
		p.pos += len(s)
		return true
	}
	return false
}

// parseOr handles: expr || expr
func (p *exprParser) parseOr() interface{} {
	left := p.parseAnd()
	for {
		p.skipWhitespace()
		if p.pos+2 <= len(p.input) && p.input[p.pos:p.pos+2] == "||" {
			p.pos += 2
			right := p.parseAnd()
			if toBool(left) {
				return left
			}
			left = right
		} else {
			break
		}
	}
	return left
}

// parseAnd handles: expr && expr
func (p *exprParser) parseAnd() interface{} {
	left := p.parseComparison()
	for {
		p.skipWhitespace()
		if p.pos+2 <= len(p.input) && p.input[p.pos:p.pos+2] == "&&" {
			p.pos += 2
			right := p.parseComparison()
			if !toBool(left) {
				return left
			}
			left = right
		} else {
			break
		}
	}
	return left
}

// parseComparison handles: ==, !=, <, >, <=, >=
func (p *exprParser) parseComparison() interface{} {
	left := p.parseNot()
	p.skipWhitespace()

	if p.pos+2 <= len(p.input) {
		op := p.input[p.pos:min(p.pos+2, len(p.input))]
		switch op {
		case "==":
			p.pos += 2
			right := p.parseNot()
			return exprEquals(left, right)
		case "!=":
			p.pos += 2
			right := p.parseNot()
			return !exprEquals(left, right)
		case "<=":
			p.pos += 2
			right := p.parseNot()
			return exprCompare(left, right) <= 0
		case ">=":
			p.pos += 2
			right := p.parseNot()
			return exprCompare(left, right) >= 0
		}
	}
	if p.pos < len(p.input) {
		switch p.input[p.pos] {
		case '<':
			// Make sure it's not <= (already handled)
			if p.pos+1 < len(p.input) && p.input[p.pos+1] == '=' {
				break
			}
			p.pos++
			right := p.parseNot()
			return exprCompare(left, right) < 0
		case '>':
			if p.pos+1 < len(p.input) && p.input[p.pos+1] == '=' {
				break
			}
			p.pos++
			right := p.parseNot()
			return exprCompare(left, right) > 0
		}
	}

	return left
}

// parseNot handles: !expr
func (p *exprParser) parseNot() interface{} {
	p.skipWhitespace()
	if p.pos < len(p.input) && p.input[p.pos] == '!' {
		p.pos++
		val := p.parseNot()
		return !toBool(val)
	}
	return p.parsePrimary()
}

// parsePrimary handles: literals, context lookups, function calls, parenthesized expressions
func (p *exprParser) parsePrimary() interface{} {
	p.skipWhitespace()

	if p.pos >= len(p.input) {
		return nil
	}

	ch := p.input[p.pos]

	// Parenthesized expression
	if ch == '(' {
		p.pos++
		val := p.parseOr()
		p.skipWhitespace()
		if p.pos < len(p.input) && p.input[p.pos] == ')' {
			p.pos++
		}
		return val
	}

	// String literal
	if ch == '\'' {
		return p.parseString()
	}

	// Number
	if ch >= '0' && ch <= '9' {
		return p.parseNumber()
	}

	// Boolean/null literals or identifier (context lookup / function call)
	return p.parseIdentifier()
}

func (p *exprParser) parseString() interface{} {
	p.pos++ // skip opening '
	var s strings.Builder
	for p.pos < len(p.input) {
		if p.input[p.pos] == '\'' {
			if p.pos+1 < len(p.input) && p.input[p.pos+1] == '\'' {
				// Escaped single quote
				s.WriteByte('\'')
				p.pos += 2
			} else {
				p.pos++ // skip closing '
				return s.String()
			}
		} else {
			s.WriteByte(p.input[p.pos])
			p.pos++
		}
	}
	return s.String()
}

func (p *exprParser) parseNumber() interface{} {
	start := p.pos
	isFloat := false
	for p.pos < len(p.input) && (p.input[p.pos] >= '0' && p.input[p.pos] <= '9' || p.input[p.pos] == '.') {
		if p.input[p.pos] == '.' {
			isFloat = true
		}
		p.pos++
	}
	numStr := p.input[start:p.pos]
	if isFloat {
		var f float64
		fmt.Sscanf(numStr, "%f", &f)
		return f
	}
	var n int64
	fmt.Sscanf(numStr, "%d", &n)
	return n
}

func (p *exprParser) parseIdentifier() interface{} {
	start := p.pos
	for p.pos < len(p.input) {
		ch := p.input[p.pos]
		if ch == '.' || ch == '_' || ch == '-' || (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
			p.pos++
		} else {
			break
		}
	}
	name := p.input[start:p.pos]

	if name == "" {
		return nil
	}

	// Check for boolean/null literals
	switch strings.ToLower(name) {
	case "true":
		return true
	case "false":
		return false
	case "null":
		return nil
	}

	// Check for function call
	p.skipWhitespace()
	if p.pos < len(p.input) && p.input[p.pos] == '(' {
		return p.parseFunction(name)
	}

	// Context property lookup
	return p.lookupContext(name)
}

func (p *exprParser) parseFunction(name string) interface{} {
	p.pos++ // skip (
	var args []interface{}
	for {
		p.skipWhitespace()
		if p.pos >= len(p.input) || p.input[p.pos] == ')' {
			break
		}
		if len(args) > 0 {
			if p.pos < len(p.input) && p.input[p.pos] == ',' {
				p.pos++
			}
		}
		args = append(args, p.parseOr())
	}
	if p.pos < len(p.input) && p.input[p.pos] == ')' {
		p.pos++
	}

	return p.callFunction(name, args)
}

func (p *exprParser) callFunction(name string, args []interface{}) interface{} {
	switch strings.ToLower(name) {
	case "contains":
		if len(args) >= 2 {
			return exprContains(args[0], args[1])
		}
	case "startswith":
		if len(args) >= 2 {
			s := exprToString(args[0])
			prefix := exprToString(args[1])
			return strings.HasPrefix(strings.ToLower(s), strings.ToLower(prefix))
		}
	case "endswith":
		if len(args) >= 2 {
			s := exprToString(args[0])
			suffix := exprToString(args[1])
			return strings.HasSuffix(strings.ToLower(s), strings.ToLower(suffix))
		}
	case "format":
		if len(args) >= 1 {
			return exprFormat(args)
		}
	case "join":
		if len(args) >= 1 {
			sep := ","
			if len(args) >= 2 {
				sep = exprToString(args[1])
			}
			return exprJoin(args[0], sep)
		}
	case "tojson":
		if len(args) >= 1 {
			b, _ := json.Marshal(args[0])
			return string(b)
		}
	case "fromjson":
		if len(args) >= 1 {
			s := exprToString(args[0])
			var v interface{}
			json.Unmarshal([]byte(s), &v)
			return v
		}
	case "hashfiles":
		if p.ctx.HashFilesFunc != nil && len(args) >= 1 {
			var patterns []string
			for _, a := range args {
				patterns = append(patterns, exprToString(a))
			}
			return p.ctx.HashFilesFunc(patterns)
		}
		return ""
	case "success":
		return p.statusCheck("success")
	case "failure":
		return p.statusCheck("failure")
	case "cancelled":
		return p.statusCheck("cancelled")
	case "always":
		return true
	}
	return nil
}

func (p *exprParser) statusCheck(check string) bool {
	// Check the job status from context
	if status, ok := p.ctx.GitHub["job_status"].(string); ok {
		switch check {
		case "success":
			return status == "success"
		case "failure":
			return status == "failure"
		case "cancelled":
			return status == "cancelled"
		}
	}
	// Default: success() returns true if no failures yet
	if check == "success" {
		return true
	}
	return false
}

// lookupContext resolves a dotted path like "matrix.os" or "github.event.inputs.tag"
func (p *exprParser) lookupContext(path string) interface{} {
	parts := strings.SplitN(path, ".", 2)
	if len(parts) == 0 {
		return nil
	}

	root := parts[0]
	var rest string
	if len(parts) > 1 {
		rest = parts[1]
	}

	switch root {
	case "github":
		return deepLookup(p.ctx.GitHub, rest)
	case "env":
		if rest != "" {
			return p.ctx.Env[rest]
		}
		return p.ctx.Env
	case "secrets":
		if rest != "" {
			return p.ctx.Secrets[rest]
		}
		return p.ctx.Secrets
	case "matrix":
		if rest != "" {
			return deepLookup(p.ctx.Matrix, rest)
		}
		return p.ctx.Matrix
	case "steps":
		return p.lookupSteps(rest)
	case "needs":
		return p.lookupNeeds(rest)
	case "runner":
		if rest != "" {
			return p.ctx.Runner[rest]
		}
		return p.ctx.Runner
	case "inputs":
		if rest != "" {
			return p.ctx.Inputs[rest]
		}
		return p.ctx.Inputs
	case "vars":
		if rest != "" {
			return p.ctx.Vars[rest]
		}
		return p.ctx.Vars
	default:
		// Could be a bare identifier that's really a context lookup
		// e.g., just "matrix" without a dot
		return nil
	}
}

func (p *exprParser) lookupSteps(path string) interface{} {
	if path == "" {
		return p.ctx.Steps
	}
	parts := strings.SplitN(path, ".", 2)
	stepID := parts[0]
	step, ok := p.ctx.Steps[stepID]
	if !ok {
		return nil
	}
	if len(parts) == 1 {
		return step
	}
	rest := parts[1]
	switch {
	case rest == "outcome":
		return step.Outcome
	case rest == "conclusion":
		return step.Conclusion
	case strings.HasPrefix(rest, "outputs."):
		outputKey := strings.TrimPrefix(rest, "outputs.")
		return step.Outputs[outputKey]
	case rest == "outputs":
		return step.Outputs
	}
	return nil
}

func (p *exprParser) lookupNeeds(path string) interface{} {
	if path == "" {
		return p.ctx.Needs
	}
	parts := strings.SplitN(path, ".", 2)
	jobID := parts[0]
	job, ok := p.ctx.Needs[jobID]
	if !ok {
		return nil
	}
	if len(parts) == 1 {
		return job
	}
	rest := parts[1]
	switch {
	case rest == "result":
		return job.Result
	case strings.HasPrefix(rest, "outputs."):
		outputKey := strings.TrimPrefix(rest, "outputs.")
		return job.Outputs[outputKey]
	case rest == "outputs":
		return job.Outputs
	}
	return nil
}

// deepLookup traverses a map by dotted path.
func deepLookup(m map[string]interface{}, path string) interface{} {
	if path == "" {
		return m
	}
	parts := strings.SplitN(path, ".", 2)
	val, ok := m[parts[0]]
	if !ok {
		return nil
	}
	if len(parts) == 1 {
		return val
	}
	if sub, ok := val.(map[string]interface{}); ok {
		return deepLookup(sub, parts[1])
	}
	return nil
}

// Helper functions for expression evaluation

func toBool(v interface{}) bool {
	if v == nil {
		return false
	}
	switch val := v.(type) {
	case bool:
		return val
	case string:
		return val != ""
	case int64:
		return val != 0
	case int:
		return val != 0
	case float64:
		return val != 0
	default:
		return true
	}
}

func exprToString(v interface{}) string {
	if v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return val
	case bool:
		if val {
			return "true"
		}
		return "false"
	case int64:
		return fmt.Sprintf("%d", val)
	case int:
		return fmt.Sprintf("%d", val)
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	default:
		b, _ := json.Marshal(val)
		return string(b)
	}
}

func exprEquals(a, b interface{}) bool {
	// Coerce to same type for comparison
	aStr := strings.ToLower(exprToString(a))
	bStr := strings.ToLower(exprToString(b))
	return aStr == bStr
}

func exprCompare(a, b interface{}) int {
	aFloat := toFloat(a)
	bFloat := toFloat(b)
	if aFloat < bFloat {
		return -1
	}
	if aFloat > bFloat {
		return 1
	}
	return 0
}

func toFloat(v interface{}) float64 {
	switch val := v.(type) {
	case int64:
		return float64(val)
	case int:
		return float64(val)
	case float64:
		return val
	case float32:
		return float64(val)
	case string:
		var f float64
		fmt.Sscanf(val, "%f", &f)
		return f
	default:
		return 0
	}
}

func exprContains(haystack, needle interface{}) bool {
	h := strings.ToLower(exprToString(haystack))
	n := strings.ToLower(exprToString(needle))
	return strings.Contains(h, n)
}

func exprFormat(args []interface{}) string {
	if len(args) == 0 {
		return ""
	}
	template := exprToString(args[0])
	// Replace {0}, {1}, etc.
	for i := 1; i < len(args); i++ {
		placeholder := fmt.Sprintf("{%d}", i-1)
		template = strings.ReplaceAll(template, placeholder, exprToString(args[i]))
	}
	return template
}

func exprJoin(val interface{}, sep string) string {
	switch v := val.(type) {
	case []interface{}:
		parts := make([]string, len(v))
		for i, item := range v {
			parts[i] = exprToString(item)
		}
		return strings.Join(parts, sep)
	case []string:
		return strings.Join(v, sep)
	default:
		return exprToString(val)
	}
}

// min returns the smaller of two ints.
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// InterpolateMap interpolates all values in a map.
func InterpolateMap(m map[string]string, ctx *ExprContext) map[string]string {
	if m == nil {
		return nil
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = Interpolate(v, ctx)
	}
	return result
}

// isAlphaNumeric checks if a rune is alphanumeric or underscore.
func isAlphaNumeric(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_'
}
