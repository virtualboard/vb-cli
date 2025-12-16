package spec

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/xeipuuv/gojsonschema"

	"github.com/virtualboard/vb-cli/internal/config"
)

// Result represents validation outcome for a single spec.
type Result struct {
	Spec   *Spec
	Errors []string
}

// Summary aggregates validation results.
type Summary struct {
	Total      int               `json:"total"`
	Valid      int               `json:"valid"`
	Invalid    int               `json:"invalid"`
	ErrorCount map[string]int    `json:"error_counts"`
	Results    map[string]Result `json:"results"`
}

// Validator performs schema validation for specs.
type Validator struct {
	mgr          *Manager
	schemaLoader gojsonschema.JSONLoader
	log          *logrus.Entry
}

// New creates a validator configured for the manager.
func NewValidator(opts *config.Options, mgr *Manager) (*Validator, error) {
	schemaPath := mgr.SchemaPath()
	loader := gojsonschema.NewReferenceLoader("file://" + filepath.ToSlash(schemaPath))
	return &Validator{
		mgr:          mgr,
		schemaLoader: loader,
		log:          opts.Logger().WithField("component", "spec-validator"),
	}, nil
}

// ValidateAll runs validations across every spec.
func (v *Validator) ValidateAll() (*Summary, error) {
	specs, err := v.mgr.List()
	if err != nil {
		return nil, err
	}

	results := make(map[string]Result)
	errorCounts := map[string]int{}

	for _, spec := range specs {
		name := filepath.Base(spec.Path)
		res := v.validateSingle(spec)
		results[name] = res
	}

	total := len(results)
	valid := 0
	invalid := 0
	for name, res := range results {
		if len(res.Errors) == 0 {
			valid++
		} else {
			invalid++
			errorCounts[name] = len(res.Errors)
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

// ValidateName runs validation on a specific spec by filename.
func (v *Validator) ValidateName(name string) (Result, error) {
	spec, err := v.mgr.LoadByName(name)
	if err != nil {
		return Result{}, err
	}
	result := v.validateSingle(spec)
	return result, nil
}

func (v *Validator) validateSingle(spec *Spec) Result {
	errors := make([]string, 0)

	// JSON schema validation
	docLoader := gojsonschema.NewGoLoader(spec.FrontMatter)
	result, err := gojsonschema.Validate(v.schemaLoader, docLoader)
	if err != nil {
		errors = append(errors, fmt.Sprintf("schema validation error: %v", err))
	} else if !result.Valid() {
		for _, desc := range result.Errors() {
			errors = append(errors, desc.String())
		}
	}

	// Date format validation
	if _, err := time.Parse("2006-01-02", spec.FrontMatter.LastUpdated); err != nil {
		errors = append(errors, "last_updated must be YYYY-MM-DD")
	}

	// Validate status is one of the allowed values
	validStatuses := map[string]bool{
		"draft":      true,
		"approved":   true,
		"deprecated": true,
	}
	status := strings.ToLower(spec.FrontMatter.Status)
	if !validStatuses[status] {
		errors = append(errors, fmt.Sprintf("status '%s' must be one of: draft, approved, deprecated", spec.FrontMatter.Status))
	}

	// Validate spec_type is one of the known types
	validTypes := map[string]bool{
		"tech-stack":                          true,
		"local-development":                   true,
		"hosting-and-infrastructure":          true,
		"ci-cd-pipeline":                      true,
		"database-schema":                     true,
		"caching-and-performance":             true,
		"security-and-compliance":             true,
		"observability-and-incident-response": true,
	}
	if !validTypes[spec.FrontMatter.SpecType] {
		errors = append(errors, fmt.Sprintf("spec_type '%s' is not a recognized type", spec.FrontMatter.SpecType))
	}

	// Validate applicability has at least one entry
	if len(spec.FrontMatter.Applicability) == 0 {
		errors = append(errors, "applicability must have at least one entry")
	}

	return Result{Spec: spec, Errors: errors}
}

// HasErrors indicates if any validation errors were found.
func (s *Summary) HasErrors() bool {
	return s.Invalid > 0
}

// Error provides a formatted error when summary is invalid.
func (s *Summary) Error() error {
	if !s.HasErrors() {
		return nil
	}
	parts := make([]string, 0, len(s.ErrorCount))
	for name, count := range s.ErrorCount {
		parts = append(parts, fmt.Sprintf("%s (%d errors)", name, count))
	}
	return errors.New(strings.Join(parts, "; "))
}
