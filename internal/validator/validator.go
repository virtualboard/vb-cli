package validator

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/xeipuuv/gojsonschema"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/contract"
	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/frameworkschema"
)

var replaceValidatedFix = func(batch *feature.MutationBatch, plan plannedFix, expected, replacement []byte) error {
	return batch.CompareAndSwap(plan.feature.FrontMatter.ID, plan.feature.Path, expected, replacement, plan.mode)
}

type plannedFix struct {
	target   *feature.Feature
	feature  *feature.Feature
	original []byte
	planned  []byte
	mode     os.FileMode
}

// maxDependencyDepth mirrors the immutable dependency policy in
// templates/rules.yml. Depth counts dependency edges, so a root plus ten
// reachable dependency nodes is valid and the eleventh edge is rejected.
const maxDependencyDepth = 10

// Result represents validation outcome for a single feature.
type Result struct {
	Feature *feature.Feature
	Errors  []string
}

// Summary aggregates validation results.
type Summary struct {
	Total      int               `json:"total"`
	Valid      int               `json:"valid"`
	Invalid    int               `json:"invalid"`
	ErrorCount map[string]int    `json:"error_counts"`
	Results    map[string]Result `json:"results"`
}

// Validator performs schema and workflow checks.
type Validator struct {
	mgr          *feature.Manager
	lifecycle    *contract.Lifecycle
	root         string
	schemaLoader gojsonschema.JSONLoader
	log          *logrus.Entry
}

// New creates a validator configured for the manager.
func New(opts *config.Options, mgr *feature.Manager) (*Validator, error) {
	lifecycle, err := mgr.Lifecycle()
	if err != nil {
		return nil, err
	}
	schemaData, err := frameworkschema.Feature(opts.RootDir, mgr.SchemaPath())
	if err != nil {
		return nil, err
	}
	loader := gojsonschema.NewStringLoader(string(schemaData))
	return &Validator{
		mgr:          mgr,
		lifecycle:    lifecycle,
		root:         opts.RootDir,
		schemaLoader: loader,
		log:          opts.Logger().WithField("component", "validator"),
	}, nil
}

// ValidateAll runs validations across every feature.
func (v *Validator) ValidateAll() (*Summary, error) {
	features, err := v.mgr.List()
	if err != nil {
		return nil, err
	}
	results := make(map[string]Result)
	errorCounts := map[string]int{}
	idToFeature := map[string]*feature.Feature{}

	for _, feat := range features {
		if _, exists := idToFeature[feat.FrontMatter.ID]; exists {
			res := v.validateSingle(feat)
			res.Errors = append(res.Errors, fmt.Sprintf("duplicate ID detected for %s", feat.FrontMatter.ID))
			results[feat.FrontMatter.ID] = res
			continue
		}
		idToFeature[feat.FrontMatter.ID] = feat
		res := v.validateSingle(feat)
		results[feat.FrontMatter.ID] = res
	}

	v.applyDependencyChecks(idToFeature, results)

	total := len(results)
	valid := 0
	invalid := 0
	for id, res := range results {
		if len(res.Errors) == 0 {
			valid++
		} else {
			invalid++
			errorCounts[id] = len(res.Errors)
		}
	}

	if invalid > 0 {
		v.log.WithField("invalid", invalid).Warn("Validation found issues")
	} else {
		v.log.WithField("total", total).Info("Validation passed")
	}

	return &Summary{
		Total:      total,
		Valid:      valid,
		Invalid:    invalid,
		ErrorCount: errorCounts,
		Results:    results,
	}, nil
}

// ValidateID runs validation on a specific feature.
func (v *Validator) ValidateID(id string) (Result, error) {
	feat, err := v.mgr.LoadByID(id)
	if err != nil {
		return Result{}, err
	}
	result := v.validateSingle(feat)
	all, err := v.mgr.List()
	if err != nil {
		return Result{}, err
	}
	board := make(map[string]*feature.Feature, len(all))
	for _, listed := range all {
		board[listed.FrontMatter.ID] = listed
	}
	if depthError := dependencyDepthViolations(board)[feat.FrontMatter.ID]; depthError != "" {
		result.Errors = appendUniqueErrors(result.Errors, depthError)
	}
	result.Errors = appendUniqueErrors(result.Errors, v.validateReachableDependencies(feat.FrontMatter.ID, board)...)
	return result, nil
}

// validateReachableDependencies validates the complete dependency closure of
// rootID. Errors from transitive features retain the first deterministic chain
// from the requested feature, while missing nodes and cycles include the full
// chain that exposed them.
func (v *Validator) validateReachableDependencies(rootID string, board map[string]*feature.Feature) []string {
	root, exists := board[rootID]
	if !exists {
		return []string{"requested feature is absent from the validated board: " + rootID}
	}
	validationErrors := []string{}
	state := make(map[string]uint8, len(board))
	validated := map[string]bool{rootID: true}
	var walk func(*feature.Feature, []string)
	walk = func(current *feature.Feature, chain []string) {
		currentID := current.FrontMatter.ID
		state[currentID] = 1
		for _, rawDependency := range current.FrontMatter.Dependencies {
			dependencyID := strings.TrimSpace(rawDependency)
			if dependencyID == "" {
				continue
			}
			dependencyChain := append(append([]string(nil), chain...), dependencyID)
			dependency, found := board[dependencyID]
			if !found {
				validationErrors = append(validationErrors, fmt.Sprintf("dependency chain %s: dependency %s not found", strings.Join(dependencyChain, " -> "), dependencyID))
				continue
			}
			if strings.EqualFold(strings.TrimSpace(current.FrontMatter.Status), "in-progress") && !strings.EqualFold(strings.TrimSpace(dependency.FrontMatter.Status), "done") {
				validationErrors = append(validationErrors, fmt.Sprintf("dependency chain %s: dependency %s must be done before %s can be in-progress", strings.Join(dependencyChain, " -> "), dependencyID, currentID))
			}
			if state[dependencyID] == 1 {
				validationErrors = append(validationErrors, "circular dependency detected in reachable chain: "+strings.Join(dependencyChain, " -> "))
				continue
			}
			if !validated[dependencyID] {
				validated[dependencyID] = true
				dependencyResult := v.validateSingle(dependency)
				for _, dependencyError := range dependencyResult.Errors {
					validationErrors = append(validationErrors, fmt.Sprintf("dependency chain %s: %s", strings.Join(dependencyChain, " -> "), dependencyError))
				}
			}
			if state[dependencyID] == 0 {
				walk(dependency, dependencyChain)
			}
		}
		state[currentID] = 2
	}
	walk(root, []string{rootID})
	return appendUniqueErrors(nil, validationErrors...)
}

func appendUniqueErrors(existing []string, additions ...string) []string {
	seen := make(map[string]struct{}, len(existing)+len(additions))
	for _, message := range existing {
		seen[message] = struct{}{}
	}
	for _, message := range additions {
		if _, duplicate := seen[message]; duplicate {
			continue
		}
		seen[message] = struct{}{}
		existing = append(existing, message)
	}
	return existing
}

// dependencyDepthViolations returns one deterministic, representative
// over-depth chain per affected root. The graph is required to be acyclic by a
// separate validation rule; back-edges are ignored here so cycles do not turn
// this bounded longest-path calculation into recursion without an endpoint.
func dependencyDepthViolations(features map[string]*feature.Feature) map[string]string {
	ids := make([]string, 0, len(features))
	for id := range features {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	state := make(map[string]uint8, len(features))
	memo := make(map[string][]string, len(features))
	var longestChain func(string) []string
	longestChain = func(id string) []string {
		if state[id] == 1 {
			return nil
		}
		if state[id] == 2 {
			return memo[id]
		}
		current, exists := features[id]
		if !exists {
			return nil
		}
		state[id] = 1
		best := []string{id}
		for _, rawDependency := range current.FrontMatter.Dependencies {
			dependencyID := strings.TrimSpace(rawDependency)
			if dependencyID == "" {
				continue
			}
			suffix := longestChain(dependencyID)
			if len(suffix) == 0 {
				continue
			}
			candidate := make([]string, 1, min(maxDependencyDepth+2, len(suffix)+1))
			candidate[0] = id
			candidate = append(candidate, suffix...)
			if len(candidate) > maxDependencyDepth+2 {
				candidate = candidate[:maxDependencyDepth+2]
			}
			if len(candidate) > len(best) {
				best = candidate
			}
		}
		state[id] = 2
		memo[id] = best
		return best
	}

	violations := make(map[string]string)
	for _, id := range ids {
		chain := longestChain(id)
		if len(chain)-1 > maxDependencyDepth {
			violations[id] = fmt.Sprintf("dependency chain %s exceeds maximum dependency depth %d", strings.Join(chain, " -> "), maxDependencyDepth)
		}
	}
	return violations
}

// ValidateCandidate validates a complete in-memory feature against the schema
// and the prospective board graph, replacing any on-disk feature with the same
// ID for relationship checks. It never writes.
func (v *Validator) ValidateCandidate(candidate *feature.Feature) error {
	features, err := v.mgr.List()
	if err != nil {
		return err
	}
	board := make(map[string]*feature.Feature, len(features)+1)
	for _, feat := range features {
		board[feat.FrontMatter.ID] = feat
	}
	board[candidate.FrontMatter.ID] = candidate
	results := make(map[string]Result, len(board))
	for id, feat := range board {
		results[id] = v.validateSingle(feat)
	}
	v.applyDependencyChecks(board, results)
	result := results[candidate.FrontMatter.ID]
	if len(result.Errors) == 0 {
		return nil
	}
	return fmt.Errorf("candidate feature %s is invalid: %s", candidate.FrontMatter.ID, strings.Join(result.Errors, "; "))
}

func (v *Validator) validateSingle(feat *feature.Feature) Result {
	errors := make([]string, 0)

	docLoader := gojsonschema.NewGoLoader(feat.FrontMatter)
	result, err := gojsonschema.Validate(v.schemaLoader, docLoader)
	if err != nil {
		errors = append(errors, fmt.Sprintf("schema validation error: %v", err))
	} else if !result.Valid() {
		for _, desc := range result.Errors() {
			errors = append(errors, desc.String())
		}
	}

	expectedDir, statusOK := v.lifecycle.DirectoryForStatus(feat.FrontMatter.Status)
	actualDir := filepath.Dir(feat.Path)
	if !statusOK {
		errors = append(errors, fmt.Sprintf("invalid status %s", feat.FrontMatter.Status))
	} else {
		expectedClean := filepath.Clean(expectedDir)
		actualClean := filepath.Clean(actualDir)
		if !strings.EqualFold(expectedClean, actualClean) {
			errors = append(errors, fmt.Sprintf("status '%s' requires directory %s", feat.FrontMatter.Status, expectedDir))
		}
	}

	base := filepath.Base(feat.Path)
	if err := v.lifecycle.ValidateFilename(base); err != nil {
		errors = append(errors, err.Error())
	}
	if !strings.HasPrefix(strings.ToUpper(base), strings.ToUpper(feat.FrontMatter.ID)+"-") {
		errors = append(errors, fmt.Sprintf("filename '%s' must retain immutable ID prefix %s-", base, feat.FrontMatter.ID))
	}

	created, createdOK := parseExactDate(feat.FrontMatter.Created)
	if !createdOK {
		errors = append(errors, "created date must be YYYY-MM-DD")
	}
	updated, updatedOK := parseExactDate(feat.FrontMatter.Updated)
	if !updatedOK {
		errors = append(errors, "updated date must be YYYY-MM-DD")
	}
	statusChanged, statusChangedOK := parseExactDate(feat.FrontMatter.StatusChanged)
	if !statusChangedOK {
		errors = append(errors, "status_changed date must be YYYY-MM-DD")
	}
	today, _ := parseExactDate(time.Now().Format("2006-01-02"))
	if createdOK && created.After(today) {
		errors = append(errors, "created date cannot be in the future")
	}
	if statusChangedOK && statusChanged.After(today) {
		errors = append(errors, "status_changed date cannot be in the future")
	}
	if updatedOK && updated.After(today) {
		errors = append(errors, "updated date cannot be in the future")
	}
	if createdOK && statusChangedOK && created.After(statusChanged) {
		errors = append(errors, "date provenance requires created <= status_changed")
	}
	if statusChangedOK && updatedOK && statusChanged.After(updated) {
		errors = append(errors, "date provenance requires status_changed <= updated")
	}
	implementationOwner := strings.TrimSpace(feat.FrontMatter.ImplementationOwner)
	if implementationOwner == "" {
		errors = append(errors, "implementation_owner is required")
	} else if err := v.lifecycle.ValidateOwner(implementationOwner, !v.lifecycle.RequiresAssignedOwner(feat.FrontMatter.Status)); err != nil {
		errors = append(errors, "invalid implementation_owner: "+err.Error())
	}
	owner := strings.TrimSpace(feat.FrontMatter.Owner)
	if owner == "" {
		errors = append(errors, "owner is required")
	} else if err := v.lifecycle.ValidateOwner(owner, !v.lifecycle.RequiresAssignedOwner(feat.FrontMatter.Status)); err != nil {
		errors = append(errors, "invalid owner: "+err.Error())
	}
	if v.lifecycle.RequiresAssignedOwner(feat.FrontMatter.Status) {
		if owner == "" || strings.EqualFold(owner, "unassigned") {
			errors = append(errors, fmt.Sprintf("status '%s' requires an assigned owner", feat.FrontMatter.Status))
		}
		if implementationOwner == "" || strings.EqualFold(implementationOwner, "unassigned") {
			errors = append(errors, fmt.Sprintf("status '%s' requires an assigned implementation_owner", feat.FrontMatter.Status))
		}
	}
	status := strings.ToLower(strings.TrimSpace(feat.FrontMatter.Status))
	if (status == "review" || status == "done") && owner != "" && implementationOwner != "" &&
		!strings.EqualFold(owner, "unassigned") && !strings.EqualFold(implementationOwner, "unassigned") &&
		strings.EqualFold(owner, implementationOwner) {
		errors = append(errors, fmt.Sprintf("status '%s' requires reviewer owner to differ from implementation_owner", status))
	}
	errors = append(errors, feature.ValidateBodyForStatus(feat.Body, feat.FrontMatter.Status)...)
	errors = append(errors, v.validateInternalLinks(feat)...)
	if worktreeFileChanged(feat.Path) && feat.FrontMatter.Updated != time.Now().Format("2006-01-02") {
		errors = append(errors, "updated date must be today when the feature file has uncommitted changes")
	}

	return Result{Feature: feat, Errors: errors}
}

func parseExactDate(value string) (time.Time, bool) {
	trimmed := strings.TrimSpace(value)
	parsed, err := time.Parse("2006-01-02", trimmed)
	return parsed, err == nil && parsed.Format("2006-01-02") == trimmed
}

func (v *Validator) applyDependencyChecks(features map[string]*feature.Feature, results map[string]Result) {
	for id, feat := range features {
		res := results[id]
		for _, dep := range feat.FrontMatter.Dependencies {
			dep = strings.TrimSpace(dep)
			if dep == "" {
				continue
			}
			depFeat, ok := features[dep]
			if !ok {
				res.Errors = append(res.Errors, fmt.Sprintf("dependency %s not found", dep))
				continue
			}
			if strings.ToLower(feat.FrontMatter.Status) == "in-progress" && strings.ToLower(depFeat.FrontMatter.Status) != "done" {
				res.Errors = append(res.Errors, fmt.Sprintf("dependency %s must be done before moving to in-progress", dep))
			}
		}
		results[id] = res
	}

	cycles := findCycles(features)
	for _, cycle := range cycles {
		message := "circular dependency detected: " + strings.Join(cycle, " -> ")
		for _, id := range cycle {
			res := results[id]
			res.Errors = append(res.Errors, message)
			results[id] = res
		}
	}

	for id, message := range dependencyDepthViolations(features) {
		res := results[id]
		res.Errors = appendUniqueErrors(res.Errors, message)
		results[id] = res
	}
}

func findCycles(features map[string]*feature.Feature) [][]string {
	graph := make(map[string][]string)
	for id, feat := range features {
		deps := make([]string, 0, len(feat.FrontMatter.Dependencies))
		for _, dep := range feat.FrontMatter.Dependencies {
			dep = strings.TrimSpace(dep)
			if dep != "" {
				deps = append(deps, dep)
			}
		}
		graph[id] = deps
	}

	visited := map[string]bool{}
	onStack := map[string]bool{}
	stack := make([]string, 0)
	cycles := [][]string{}

	var dfs func(string)
	dfs = func(node string) {
		visited[node] = true
		onStack[node] = true
		stack = append(stack, node)

		for _, dep := range graph[node] {
			if !visited[dep] {
				dfs(dep)
			} else if onStack[dep] {
				cycle := extractCycle(stack, dep)
				if len(cycle) > 0 {
					cycles = append(cycles, cycle)
				}
			}
		}

		onStack[node] = false
		stack = stack[:len(stack)-1]
	}

	for node := range graph {
		if !visited[node] {
			dfs(node)
		}
	}

	return dedupeCycles(cycles)
}

func extractCycle(stack []string, start string) []string {
	idx := -1
	for i := len(stack) - 1; i >= 0; i-- {
		if stack[i] == start {
			idx = i
			break
		}
	}
	if idx == -1 {
		return nil
	}
	cycle := append([]string{}, stack[idx:]...)
	cycle = append(cycle, start)
	return cycle
}

func dedupeCycles(cycles [][]string) [][]string {
	unique := [][]string{}
	seen := map[string]struct{}{}
	for _, cycle := range cycles {
		if len(cycle) == 0 {
			continue
		}
		key := strings.Join(cycle, "->")
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, cycle)
	}
	return unique
}

// ApplyFixes applies non-destructive template repairs while preserving the
// immutable feature basename.
func (v *Validator) ApplyFixes(features map[string]*feature.Feature, processor func(*feature.Feature) error) error {
	ids := make([]string, 0, len(features))
	for id := range features {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return v.mgr.WithFeatureMutationBatch(ids, func(batch *feature.MutationBatch) error {
		snapshots := make(map[string]*feature.GuardedFeatureSnapshot, len(ids))
		for _, id := range ids {
			snapshot, err := batch.Load(id)
			if err != nil {
				return err
			}
			if err := batch.Authorize(snapshot.Feature, false); err != nil {
				return err
			}
			snapshots[id] = snapshot
		}

		plans := make([]plannedFix, 0, len(ids))
		for _, id := range ids {
			snapshot := snapshots[id]
			feat := cloneFeature(snapshot.Feature)
			if err := processor(feat); err != nil {
				return err
			}
			feat.UpdateTimestamp()
			planned, err := feat.Encode()
			if err != nil {
				return err
			}
			plans = append(plans, plannedFix{
				target: features[id], feature: feat,
				original: append([]byte(nil), snapshot.Data...), planned: planned,
				mode: snapshot.Mode.Perm(),
			})
		}

		// Re-read every planned input before the first write. Guards prevent
		// cooperating CLI processes from changing them; this second pass also
		// catches direct filesystem edits made during planning.
		for _, plan := range plans {
			current, err := batch.Load(plan.feature.FrontMatter.ID)
			if err != nil {
				return err
			}
			if filepath.Clean(current.Feature.Path) != filepath.Clean(plan.feature.Path) ||
				!bytes.Equal(current.Data, plan.original) || current.Mode.Perm() != plan.mode.Perm() {
				return fmt.Errorf("%w: feature %s changed while fixes were being planned", feature.ErrStaleFeature, plan.feature.FrontMatter.ID)
			}
		}
		if len(plans) == 0 || v.mgr.DryRun() {
			return nil
		}

		applied := make([]plannedFix, 0, len(plans))
		for _, plan := range plans {
			if err := replaceValidatedFix(batch, plan, plan.original, plan.planned); err != nil {
				return rollbackValidatedFixes(batch, applied, err)
			}
			applied = append(applied, plan)
		}
		for _, plan := range plans {
			*plan.target = *cloneFeature(plan.feature)
			v.mgr.RecordAudit("validate-fix", plan.feature.FrontMatter.ID, "template repair")
		}
		return nil
	})
}

func cloneFeature(source *feature.Feature) *feature.Feature {
	clone := *source
	clone.FrontMatter.Labels = append([]string{}, source.FrontMatter.Labels...)
	clone.FrontMatter.Dependencies = append([]string{}, source.FrontMatter.Dependencies...)
	return &clone
}

func rollbackValidatedFixes(batch *feature.MutationBatch, applied []plannedFix, applyErr error) error {
	rollbackErrors := []string{}
	for i := len(applied) - 1; i >= 0; i-- {
		plan := applied[i]
		if err := replaceValidatedFix(batch, plan, plan.planned, plan.original); err != nil {
			if errors.Is(err, feature.ErrStaleFeature) {
				rollbackErrors = append(rollbackErrors, plan.feature.FrontMatter.ID+": concurrent content preserved")
				continue
			}
			rollbackErrors = append(rollbackErrors, plan.feature.FrontMatter.ID+": "+err.Error())
		}
	}
	if len(rollbackErrors) > 0 {
		return fmt.Errorf("apply fixes: %w (rollback failures: %s)", applyErr, strings.Join(rollbackErrors, "; "))
	}
	return fmt.Errorf("apply fixes: %w", applyErr)
}

// CollectFeatures returns a map of ID to feature for fix workflows.
func (v *Validator) CollectFeatures(ids ...string) (map[string]*feature.Feature, error) {
	features := map[string]*feature.Feature{}
	if len(ids) == 0 {
		all, err := v.mgr.List()
		if err != nil {
			return nil, err
		}
		for _, feat := range all {
			features[feat.FrontMatter.ID] = feat
		}
		return features, nil
	}
	for _, id := range ids {
		feat, err := v.mgr.LoadByID(id)
		if err != nil {
			return nil, err
		}
		features[feat.FrontMatter.ID] = feat
	}
	return features, nil
}

// HasErrors indicates if any validation errors were found.
func (s *Summary) HasErrors() bool {
	return s.Invalid > 0
}

// Error provides a formatted error when summary invalid.
func (s *Summary) Error() error {
	if !s.HasErrors() {
		return nil
	}
	parts := make([]string, 0, len(s.ErrorCount))
	for id, count := range s.ErrorCount {
		parts = append(parts, fmt.Sprintf("%s (%d errors)", id, count))
	}
	return errors.New(strings.Join(parts, "; "))
}
