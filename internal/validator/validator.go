package validator

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/xeipuuv/gojsonschema"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/util"
)

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
	schemaLoader gojsonschema.JSONLoader
	log          *logrus.Entry
}

// New creates a validator configured for the manager.
func New(opts *config.Options, mgr *feature.Manager) (*Validator, error) {
	schemaPath := mgr.SchemaPath()
	loader := gojsonschema.NewReferenceLoader("file://" + filepath.ToSlash(schemaPath))
	return &Validator{
		mgr:          mgr,
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

	deps := map[string]*feature.Feature{id: feat}
	for _, dep := range feat.FrontMatter.Dependencies {
		dep = strings.TrimSpace(dep)
		if dep == "" {
			continue
		}
		depFeat, depErr := v.mgr.LoadByID(dep)
		if depErr == nil {
			deps[dep] = depFeat
		}
	}
	results := map[string]Result{id: result}
	v.applyDependencyChecks(deps, results)

	return results[id], nil
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

	dir := feature.DirectoryForStatus(feat.FrontMatter.Status)
	statusSubdir := strings.TrimPrefix(dir, "features/")
	expectedDir := filepath.Join(v.mgr.FeaturesDir(), statusSubdir)
	actualDir := filepath.Dir(feat.Path)
	if dir == "" {
		errors = append(errors, fmt.Sprintf("invalid status %s", feat.FrontMatter.Status))
	} else {
		expectedClean := filepath.Clean(expectedDir)
		actualClean := filepath.Clean(actualDir)
		if !strings.EqualFold(expectedClean, actualClean) {
			errors = append(errors, fmt.Sprintf("status '%s' requires directory %s", feat.FrontMatter.Status, expectedDir))
		}
	}

	expectedName := fmt.Sprintf("%s-%s.md", feat.FrontMatter.ID, util.Slugify(feat.FrontMatter.Title))
	if base := filepath.Base(feat.Path); !strings.EqualFold(base, expectedName) {
		errors = append(errors, fmt.Sprintf("filename '%s' should be '%s'", base, expectedName))
	}

	if _, err := time.Parse("2006-01-02", feat.FrontMatter.Created); err != nil {
		errors = append(errors, "created date must be YYYY-MM-DD")
	}
	if _, err := time.Parse("2006-01-02", feat.FrontMatter.Updated); err != nil {
		errors = append(errors, "updated date must be YYYY-MM-DD")
	}

	return Result{Feature: feat, Errors: errors}
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

// ApplyFixes optionally applies non-destructive fixes (currently template re-application).
func (v *Validator) ApplyFixes(features map[string]*feature.Feature, processor func(*feature.Feature) error) error {
	for _, feat := range features {
		if err := processor(feat); err != nil {
			return err
		}
		if err := v.mgr.Save(feat); err != nil {
			return err
		}
	}
	return nil
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
