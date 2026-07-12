package feature

import (
	"fmt"
	"slices"
	"strings"
)

func cloneFeatureGraphFields(feat *Feature) *Feature {
	if feat == nil {
		return nil
	}
	return &Feature{FrontMatter: FrontMatter{
		ID:           feat.FrontMatter.ID,
		Status:       feat.FrontMatter.Status,
		Dependencies: append([]string(nil), feat.FrontMatter.Dependencies...),
	}}
}

func featureGraphRelevantChange(previous, candidate *Feature) bool {
	if previous == nil || candidate == nil {
		return true
	}
	if !strings.EqualFold(strings.TrimSpace(previous.FrontMatter.Status), strings.TrimSpace(candidate.FrontMatter.Status)) {
		return true
	}
	left := normalizedDependencySet(previous.FrontMatter.Dependencies)
	right := normalizedDependencySet(candidate.FrontMatter.Dependencies)
	return !slices.Equal(left, right)
}

func normalizedDependencySet(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	slices.Sort(result)
	return result
}

// validateFeatureGraphCandidate re-enumerates the complete board while the
// caller holds the global board-graph guard, substitutes candidate, and rejects
// missing dependencies, dependency-state violations, and cycles before bytes
// are published.
func (m *Manager) validateFeatureGraphCandidate(candidate *Feature) error {
	if candidate == nil {
		return fmt.Errorf("%w: candidate is nil", ErrIdentityMismatch)
	}
	features, err := m.List()
	if err != nil {
		return err
	}
	graph := make(map[string]*Feature, len(features)+1)
	for _, feat := range features {
		graph[feat.FrontMatter.ID] = feat
	}
	graph[candidate.FrontMatter.ID] = candidate

	for id, feat := range graph {
		seen := map[string]struct{}{}
		for _, rawDependency := range feat.FrontMatter.Dependencies {
			dependency := strings.TrimSpace(rawDependency)
			if dependency == "" {
				continue
			}
			if _, duplicate := seen[dependency]; duplicate {
				return fmt.Errorf("%w: %s repeats dependency %s", ErrDependencyBlocked, id, dependency)
			}
			seen[dependency] = struct{}{}
			dependencyFeature, exists := graph[dependency]
			if !exists {
				return fmt.Errorf("%w: %s references missing dependency %s", ErrDependencyBlocked, id, dependency)
			}
			if strings.EqualFold(strings.TrimSpace(feat.FrontMatter.Status), "in-progress") && !strings.EqualFold(strings.TrimSpace(dependencyFeature.FrontMatter.Status), "done") {
				return fmt.Errorf("%w: dependency %s of %s is not done", ErrDependencyBlocked, dependency, id)
			}
		}
	}

	visiting := map[string]bool{}
	visited := map[string]bool{}
	var visit func(string) error
	visit = func(id string) error {
		if visiting[id] {
			return fmt.Errorf("%w: cycle reaches %s", ErrDependencyCycle, id)
		}
		if visited[id] {
			return nil
		}
		visiting[id] = true
		feat := graph[id]
		for _, rawDependency := range feat.FrontMatter.Dependencies {
			dependency := strings.TrimSpace(rawDependency)
			if dependency == "" {
				continue
			}
			if err := visit(dependency); err != nil {
				return err
			}
		}
		visiting[id] = false
		visited[id] = true
		return nil
	}
	for id := range graph {
		if err := visit(id); err != nil {
			return err
		}
	}
	return nil
}

func (m *Manager) validateDeletePolicy(target *Feature) error {
	if target == nil {
		return fmt.Errorf("%w: target is nil", ErrDeleteConflict)
	}
	if !strings.EqualFold(strings.TrimSpace(target.FrontMatter.Status), m.lifecycle.InitialStatus()) {
		return fmt.Errorf("%w: only %s features may be deleted (got %s)", ErrDeleteConflict, m.lifecycle.InitialStatus(), target.FrontMatter.Status)
	}
	features, err := m.List()
	if err != nil {
		return err
	}
	targetID := strings.TrimSpace(target.FrontMatter.ID)
	for _, feat := range features {
		if feat.FrontMatter.ID == targetID {
			continue
		}
		for _, rawDependency := range feat.FrontMatter.Dependencies {
			if strings.TrimSpace(rawDependency) == targetID {
				return fmt.Errorf("%w: %s is referenced by %s", ErrDeleteConflict, targetID, feat.FrontMatter.ID)
			}
		}
	}
	return nil
}
