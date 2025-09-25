package template

import (
	"fmt"
	"os"

	"github.com/virtualboard/vb-cli/internal/feature"
)

// Processor applies the canonical template to feature specs.
type Processor struct {
	templateFeature *feature.Feature
	sectionOrder    []string
	sectionDefaults map[string]string
}

// NewProcessor loads the template file via the feature manager.
func NewProcessor(mgr *feature.Manager) (*Processor, error) {
	templatePath := mgr.TemplatePath()
	// #nosec G304 -- template path is derived from validated configuration
	data, err := os.ReadFile(templatePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read template: %w", err)
	}
	tmpl, err := feature.Parse(templatePath, data)
	if err != nil {
		return nil, err
	}
	order, defaults := feature.ExtractSections(tmpl.Body)
	return &Processor{
		templateFeature: tmpl,
		sectionOrder:    order,
		sectionDefaults: defaults,
	}, nil
}

// Apply updates the target feature with any missing sections or defaults.
func (p *Processor) Apply(target *feature.Feature) error {
	if target == nil {
		return fmt.Errorf("nil feature provided")
	}

	target.AddMissingSections(p.sectionOrder, p.sectionDefaults)

	if target.FrontMatter.Priority == "" {
		target.FrontMatter.Priority = p.templateFeature.FrontMatter.Priority
	}
	if target.FrontMatter.Complexity == "" {
		target.FrontMatter.Complexity = p.templateFeature.FrontMatter.Complexity
	}
	if target.FrontMatter.Status == "" {
		target.FrontMatter.Status = p.templateFeature.FrontMatter.Status
	}
	if target.FrontMatter.Owner == "" {
		target.FrontMatter.Owner = p.templateFeature.FrontMatter.Owner
	}
	if target.FrontMatter.Labels == nil {
		target.FrontMatter.Labels = []string{}
	}
	if target.FrontMatter.Dependencies == nil {
		target.FrontMatter.Dependencies = []string{}
	}

	return nil
}
