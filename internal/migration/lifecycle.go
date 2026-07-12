// Package migration provides explicit, preflighted workspace migrations.
package migration

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/contract"
	"github.com/virtualboard/vb-cli/internal/feature"
)

// ErrPreflight indicates that no feature files were written because one or
// more lifecycle metadata decisions were missing or unsafe.
var ErrPreflight = errors.New("lifecycle metadata migration preflight failed")

// ErrConcurrentChange indicates that a feature changed after migration
// preflight. Apply fails closed rather than overwriting the newer bytes.
var ErrConcurrentChange = errors.New("feature changed after lifecycle migration preflight")

var replaceMigrationFile = func(batch *feature.MutationBatch, change Change, expected, replacement []byte) error {
	return batch.CompareAndSwap(change.Feature.FrontMatter.ID, change.Feature.Path, expected, replacement, change.Mode)
}

// Assignments contains explicit per-feature lifecycle provenance.
type Assignments struct {
	ImplementationOwner map[string]string
	StatusChanged       map[string]string
	AllowOwnerOverride  bool
}

// Change describes one preflighted feature rewrite.
type Change struct {
	Feature      *feature.Feature
	OriginalData []byte
	PlannedData  []byte
	Mode         os.FileMode
	Fields       []string
}

// Plan is a complete migration plan. It is safe to apply only after every
// workspace feature and every supplied mapping has passed preflight.
type Plan struct {
	Total   int
	Changes []Change
	mgr     *feature.Manager
	force   bool
}

// IDs returns changed feature IDs in deterministic order.
func (p *Plan) IDs() []string {
	ids := make([]string, 0, len(p.Changes))
	for _, change := range p.Changes {
		ids = append(ids, change.Feature.FrontMatter.ID)
	}
	sort.Strings(ids)
	return ids
}

// PreflightLifecycleMetadata validates and plans the entire workspace without
// writing any feature file.
func PreflightLifecycleMetadata(opts *config.Options, mgr *feature.Manager, assignments Assignments) (*Plan, error) {
	lifecycle, err := mgr.Lifecycle()
	if err != nil {
		return nil, err
	}
	features, err := mgr.List()
	if err != nil {
		return nil, err
	}
	implementationAssignments := normalizeAssignments(assignments.ImplementationOwner)
	statusAssignments := normalizeAssignments(assignments.StatusChanged)
	seen := map[string]bool{}
	issues := []string{}
	plan := &Plan{Total: len(features), mgr: mgr, force: assignments.AllowOwnerOverride}
	ids := make([]string, 0, len(features))
	for _, listed := range features {
		id := strings.ToUpper(strings.TrimSpace(listed.FrontMatter.ID))
		if seen[id] {
			issues = append(issues, fmt.Sprintf("%s: duplicate feature ID must be resolved before migration", id))
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	sort.Strings(ids)

	if err := mgr.WithFeatureMutationBatch(ids, func(batch *feature.MutationBatch) error {
		for _, id := range ids {
			snapshot, loadErr := batch.Load(id)
			if loadErr != nil {
				issues = append(issues, fmt.Sprintf("%s: load guarded feature: %v", id, loadErr))
				continue
			}
			feat := snapshot.Feature

			changedFields := []string{}
			implementationValue, implementationChanged, implementationIssue := resolveImplementationOwner(feat, lifecycle, implementationAssignments[id])
			if implementationIssue != "" {
				issues = append(issues, fmt.Sprintf("%s: %s", id, implementationIssue))
			} else if implementationChanged {
				feat.FrontMatter.ImplementationOwner = implementationValue
				changedFields = append(changedFields, "implementation_owner")
			}

			statusValue, statusChanged, statusIssue := resolveStatusChanged(feat, lifecycle, statusAssignments[id])
			if statusIssue != "" {
				issues = append(issues, fmt.Sprintf("%s: %s", id, statusIssue))
			} else if statusChanged {
				feat.FrontMatter.StatusChanged = statusValue
				changedFields = append(changedFields, "status_changed")
			}

			if len(changedFields) == 0 || implementationIssue != "" || statusIssue != "" {
				continue
			}
			if authErr := batch.Authorize(feat, assignments.AllowOwnerOverride); authErr != nil {
				issues = append(issues, fmt.Sprintf("%s: mutation authorization failed: %v", id, authErr))
				continue
			}
			feat.UpdateTimestamp()
			plannedData, encodeErr := feat.Encode()
			if encodeErr != nil {
				issues = append(issues, fmt.Sprintf("%s: encode planned feature: %v", id, encodeErr))
				continue
			}
			plan.Changes = append(plan.Changes, Change{
				Feature: feat, OriginalData: append([]byte(nil), snapshot.Data...),
				PlannedData: plannedData, Mode: snapshot.Mode.Perm(), Fields: changedFields,
			})
		}
		return nil
	}); err != nil {
		return nil, err
	}

	for id := range implementationAssignments {
		if !seen[id] {
			issues = append(issues, fmt.Sprintf("%s: implementation-owner mapping references no feature", id))
		}
	}
	for id := range statusAssignments {
		if !seen[id] {
			issues = append(issues, fmt.Sprintf("%s: status-changed mapping references no feature", id))
		}
	}
	if len(issues) > 0 {
		sort.Strings(issues)
		return nil, fmt.Errorf("%w:\n  - %s", ErrPreflight, strings.Join(issues, "\n  - "))
	}
	sort.Slice(plan.Changes, func(i, j int) bool {
		return plan.Changes[i].Feature.FrontMatter.ID < plan.Changes[j].Feature.FrontMatter.ID
	})
	_ = opts // retained for future audit-policy migrations
	return plan, nil
}

func resolveImplementationOwner(feat *feature.Feature, lifecycle *contract.Lifecycle, explicit string) (string, bool, string) {
	current := strings.TrimSpace(feat.FrontMatter.ImplementationOwner)
	status := strings.ToLower(strings.TrimSpace(feat.FrontMatter.Status))
	if explicit != "" {
		allowUnassigned := status == lifecycle.InitialStatus()
		if err := lifecycle.ValidateOwner(explicit, allowUnassigned); err != nil {
			return "", false, "invalid --implementation-owner mapping: " + err.Error()
		}
		if current != "" && !strings.EqualFold(current, "unassigned") && current != explicit {
			return "", false, fmt.Sprintf("mapping %q conflicts with existing implementation_owner %q", explicit, current)
		}
		return explicit, current != explicit, ""
	}
	if current != "" {
		if err := lifecycle.ValidateOwner(current, status == lifecycle.InitialStatus()); err == nil {
			return current, false, ""
		}
	}
	if status == lifecycle.InitialStatus() {
		return "unassigned", current != "unassigned", ""
	}
	return "", false, "implementation_owner cannot be inferred safely for a non-backlog state; provide --implementation-owner " + feat.FrontMatter.ID + "=<actor>"
}

func resolveStatusChanged(feat *feature.Feature, lifecycle *contract.Lifecycle, explicit string) (string, bool, string) {
	current := strings.TrimSpace(feat.FrontMatter.StatusChanged)
	if explicit != "" {
		if !validDate(explicit) {
			return "", false, "invalid --status-changed date; expected YYYY-MM-DD"
		}
		if validDate(current) && current != explicit {
			return "", false, fmt.Sprintf("mapping %q conflicts with existing status_changed %q", explicit, current)
		}
		return explicit, current != explicit, ""
	}
	if validDate(current) {
		return current, false, ""
	}
	if strings.EqualFold(strings.TrimSpace(feat.FrontMatter.Status), lifecycle.InitialStatus()) && validDate(feat.FrontMatter.Created) {
		return feat.FrontMatter.Created, current != feat.FrontMatter.Created, ""
	}
	return "", false, "status_changed cannot be inferred safely for a non-backlog state; provide --status-changed " + feat.FrontMatter.ID + "=YYYY-MM-DD"
}

func validDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", strings.TrimSpace(value))
	return err == nil && parsed.Format("2006-01-02") == strings.TrimSpace(value)
}

func normalizeAssignments(input map[string]string) map[string]string {
	result := make(map[string]string, len(input))
	for id, value := range input {
		result[strings.ToUpper(strings.TrimSpace(id))] = strings.TrimSpace(value)
	}
	return result
}

// Apply writes a fully preflighted plan. It verifies every original before the
// first write and again immediately before each write. If any write fails,
// already-written files are restored only when they still contain exactly the
// planned bytes, so rollback never destroys a concurrent edit.
func (p *Plan) Apply(dryRun bool) error {
	if dryRun {
		return nil
	}
	if p.mgr == nil {
		return errors.New("lifecycle metadata migration plan has no feature manager")
	}
	ids := p.IDs()
	return p.mgr.WithFeatureMutationBatch(ids, func(batch *feature.MutationBatch) error {
		// Re-authorize and verify the complete plan before the first write.
		// All per-feature/user-lock guards remain held through apply and rollback.
		for _, change := range p.Changes {
			current, err := batch.Load(change.Feature.FrontMatter.ID)
			if err != nil {
				return fmt.Errorf("verify lifecycle metadata migration input %s: %w", change.Feature.FrontMatter.ID, err)
			}
			if err := batch.Authorize(current.Feature, p.force); err != nil {
				return err
			}
			if filepath.Clean(current.Feature.Path) != filepath.Clean(change.Feature.Path) ||
				!bytes.Equal(current.Data, change.OriginalData) || current.Mode.Perm() != change.Mode.Perm() {
				return fmt.Errorf("%w: %s", ErrConcurrentChange, change.Feature.FrontMatter.ID)
			}
		}

		applied := make([]Change, 0, len(p.Changes))
		for _, change := range p.Changes {
			err := replaceMigrationFile(batch, change, change.OriginalData, change.PlannedData)
			if errors.Is(err, feature.ErrStaleFeature) {
				err = fmt.Errorf("%w: %s", ErrConcurrentChange, change.Feature.FrontMatter.ID)
			}
			if err != nil {
				rollbackErrors := rollbackApplied(batch, applied)
				if len(rollbackErrors) > 0 {
					return fmt.Errorf("apply lifecycle metadata migration: %w (rollback failures: %s)", err, strings.Join(rollbackErrors, "; "))
				}
				return fmt.Errorf("apply lifecycle metadata migration: %w", err)
			}
			applied = append(applied, change)
		}
		return nil
	})
}

func rollbackApplied(batch *feature.MutationBatch, applied []Change) []string {
	rollbackErrors := []string{}
	for i := len(applied) - 1; i >= 0; i-- {
		previous := applied[i]
		if err := replaceMigrationFile(batch, previous, previous.PlannedData, previous.OriginalData); err != nil {
			if errors.Is(err, feature.ErrStaleFeature) {
				rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s: concurrent content preserved; manual reconciliation required", previous.Feature.FrontMatter.ID))
				continue
			}
			rollbackErrors = append(rollbackErrors, fmt.Sprintf("%s: %v", previous.Feature.FrontMatter.ID, err))
		}
	}
	return rollbackErrors
}
