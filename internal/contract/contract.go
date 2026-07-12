// Package contract loads the workspace-level VirtualBoard runtime contract.
package contract

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/virtualboard/vb-cli/internal/version"
)

const fileName = "virtualboard.json"
const defaultIDPattern = `^FTR-[0-9]{4}$`
const contractIntroducedTemplateMajor = 0
const contractIntroducedTemplateMinor = 8

// ErrContractRequired indicates that a workspace cannot safely use the legacy
// built-in lifecycle. Modern workspaces must retain their machine-readable
// contract so deleting it cannot change lock storage or authorization rules.
var ErrContractRequired = errors.New("modern workspace requires virtualboard.json")

// Existing v0.9 workspaces may contain immutable slugs longer than the current
// creation limit. Validation accepts those historical basenames; SlugMaxWords
// still bounds every newly-created feature.
const defaultFilenamePattern = `^FTR-[0-9]{4}-[a-z0-9]+(?:-[a-z0-9]+)*\.md$`
const defaultOwnerPattern = `^(?:unassigned|[A-Za-z0-9][A-Za-z0-9._-]*)$`

var statusSegmentPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)

var canonicalStatuses = []string{"backlog", "in-progress", "blocked", "review", "done"}

var canonicalTransitions = map[string][]string{
	"backlog":     {"in-progress"},
	"in-progress": {"blocked", "review"},
	"blocked":     {"in-progress"},
	"review":      {"in-progress", "done"},
	"done":        {},
}

const (
	// OwnershipAssigned requires a concrete owner for the status.
	OwnershipAssigned = "assigned-required"
	// OwnershipUnassignedAllowed permits the canonical unassigned owner value.
	OwnershipUnassignedAllowed = "unassigned-allowed"
)

// Lifecycle is the validated runtime view of a workspace contract.
type Lifecycle struct {
	root            string
	featuresDir     string
	specsDir        string
	templatesDir    string
	schemasDir      string
	locksDir        string
	auditLog        string
	initial         string
	slugMaxWords    int
	idPattern       *regexp.Regexp
	filenamePattern *regexp.Regexp
	ownerPattern    *regexp.Regexp
	statuses        map[string]struct{}
	transitions     map[string]map[string]struct{}
	ownership       map[string]string
}

type contractFile struct {
	Workspace struct {
		Paths struct {
			Features  string `json:"features"`
			Specs     string `json:"specs"`
			Templates string `json:"templates"`
			Schemas   string `json:"schemas"`
			Locks     string `json:"locks"`
			AuditLog  string `json:"auditLog"`
		} `json:"paths"`
	} `json:"workspace"`
	Feature struct {
		Statuses        []string            `json:"statuses"`
		Transitions     map[string][]string `json:"transitions"`
		Ownership       map[string]string   `json:"ownership"`
		SlugMaxWords    int                 `json:"slugMaxWords"`
		IDPattern       string              `json:"idPattern"`
		FilenamePattern string              `json:"filenamePattern"`
		OwnerPattern    string              `json:"ownerPattern"`
	} `json:"feature"`
}

// Load reads virtualboard.json from workspaceRoot. Only workspaces with an
// explicit pre-v0.8 template marker may use the historical built-in lifecycle;
// every unmarked or modern workspace fails closed when its contract is absent.
func Load(workspaceRoot string) (*Lifecycle, error) {
	path := filepath.Join(workspaceRoot, fileName)
	data, err := readContractFile(workspaceRoot)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			legacy, markerErr := isExplicitLegacyWorkspace(workspaceRoot)
			if markerErr != nil {
				return nil, markerErr
			}
			if !legacy {
				return nil, fmt.Errorf("%w at %s; legacy fallback requires a regular pre-0.8 .template-version or version.txt marker", ErrContractRequired, path)
			}
			lifecycle := Default(workspaceRoot)
			if err := lifecycle.validateResolvedPaths(); err != nil {
				return nil, err
			}
			return lifecycle, nil
		}
		return nil, fmt.Errorf("read lifecycle contract: %w", err)
	}
	if err := validateCanonicalContract(data); err != nil {
		return nil, fmt.Errorf("validate framework contract %s: %w", path, err)
	}

	var raw contractFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse lifecycle contract %s: %w", path, err)
	}
	lifecycle, err := fromFile(workspaceRoot, raw)
	if err != nil {
		return nil, err
	}
	if err := lifecycle.validateResolvedPaths(); err != nil {
		return nil, err
	}
	return lifecycle, nil
}

func isExplicitLegacyWorkspace(workspaceRoot string) (bool, error) {
	found := false
	legacy := true
	for _, name := range []string{".template-version", "version.txt"} {
		path := filepath.Join(workspaceRoot, name)
		data, err := readBoundedWorkspaceRegularFile(workspaceRoot, name, 1, 128)
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			return false, fmt.Errorf("inspect legacy workspace marker %s: %w", path, err)
		}
		found = true
		parsed, err := version.Parse(strings.TrimSpace(string(data)))
		if err != nil || parsed.Major < 0 || parsed.Minor < 0 || parsed.Patch < 0 {
			return false, fmt.Errorf("invalid legacy workspace version in %s: %q", path, strings.TrimSpace(string(data)))
		}
		if parsed.Major > contractIntroducedTemplateMajor ||
			(parsed.Major == contractIntroducedTemplateMajor && parsed.Minor >= contractIntroducedTemplateMinor) {
			legacy = false
		}
	}
	return found && legacy, nil
}

// Default returns the historical lifecycle used by workspaces created before
// virtualboard.json was introduced.
func Default(workspaceRoot string) *Lifecycle {
	return &Lifecycle{
		root:            workspaceRoot,
		featuresDir:     "features",
		specsDir:        "specs",
		templatesDir:    "templates",
		schemasDir:      "schemas",
		locksDir:        "locks",
		auditLog:        "audit.jsonl",
		initial:         "backlog",
		slugMaxWords:    6,
		idPattern:       regexp.MustCompile(defaultIDPattern),
		filenamePattern: regexp.MustCompile(defaultFilenamePattern),
		ownerPattern:    regexp.MustCompile(defaultOwnerPattern),
		statuses: map[string]struct{}{
			"backlog": {}, "in-progress": {}, "blocked": {}, "review": {}, "done": {},
		},
		transitions: map[string]map[string]struct{}{
			"backlog":     {"in-progress": {}},
			"in-progress": {"blocked": {}, "review": {}},
			"blocked":     {"in-progress": {}},
			"review":      {"in-progress": {}, "done": {}},
			"done":        {},
		},
		ownership: map[string]string{
			"backlog":     OwnershipUnassignedAllowed,
			"in-progress": OwnershipAssigned,
			"blocked":     OwnershipAssigned,
			"review":      OwnershipAssigned,
			"done":        OwnershipAssigned,
		},
	}
}

func fromFile(root string, raw contractFile) (*Lifecycle, error) {
	featuresDir, err := validateRelativePath("workspace.paths.features", raw.Workspace.Paths.Features)
	if err != nil {
		return nil, err
	}
	specsDir, err := validateOptionalRelativePath("workspace.paths.specs", raw.Workspace.Paths.Specs, "specs")
	if err != nil {
		return nil, err
	}
	templatesDir, err := validateOptionalRelativePath("workspace.paths.templates", raw.Workspace.Paths.Templates, "templates")
	if err != nil {
		return nil, err
	}
	schemasDir, err := validateOptionalRelativePath("workspace.paths.schemas", raw.Workspace.Paths.Schemas, "schemas")
	if err != nil {
		return nil, err
	}
	locksDir, err := validateRelativePath("workspace.paths.locks", raw.Workspace.Paths.Locks)
	if err != nil {
		return nil, err
	}
	auditLog, err := validateRelativePath("workspace.paths.auditLog", raw.Workspace.Paths.AuditLog)
	if err != nil {
		return nil, err
	}
	if len(raw.Feature.Statuses) == 0 {
		return nil, errors.New("lifecycle contract feature.statuses must not be empty")
	}
	for name, actual := range map[string]string{
		"workspace.paths.features":  featuresDir,
		"workspace.paths.specs":     specsDir,
		"workspace.paths.templates": templatesDir,
		"workspace.paths.schemas":   schemasDir,
		"workspace.paths.locks":     locksDir,
		"workspace.paths.auditLog":  auditLog,
	} {
		expected := map[string]string{
			"workspace.paths.features":  "features",
			"workspace.paths.specs":     "specs",
			"workspace.paths.templates": "templates",
			"workspace.paths.schemas":   "schemas",
			"workspace.paths.locks":     ".state/locks",
			"workspace.paths.auditLog":  "audit.jsonl",
		}[name]
		if filepath.ToSlash(actual) != expected {
			return nil, fmt.Errorf("%s must be the canonical path %q for this CLI release", name, expected)
		}
	}

	lifecycle := &Lifecycle{
		root:            root,
		featuresDir:     featuresDir,
		specsDir:        specsDir,
		templatesDir:    templatesDir,
		schemasDir:      schemasDir,
		locksDir:        locksDir,
		auditLog:        auditLog,
		statuses:        make(map[string]struct{}, len(raw.Feature.Statuses)),
		transitions:     make(map[string]map[string]struct{}, len(raw.Feature.Statuses)),
		ownership:       make(map[string]string, len(raw.Feature.Statuses)),
		slugMaxWords:    6,
		idPattern:       regexp.MustCompile(defaultIDPattern),
		filenamePattern: regexp.MustCompile(defaultFilenamePattern),
		ownerPattern:    regexp.MustCompile(defaultOwnerPattern),
	}
	if raw.Feature.IDPattern != "" {
		if raw.Feature.IDPattern != defaultIDPattern {
			return nil, fmt.Errorf("feature.idPattern must be the canonical %q for this CLI release", defaultIDPattern)
		}
		pattern, compileErr := regexp.Compile(raw.Feature.IDPattern)
		if compileErr != nil {
			return nil, fmt.Errorf("invalid feature.idPattern: %w", compileErr)
		}
		lifecycle.idPattern = pattern
	}
	if raw.Feature.FilenamePattern != "" {
		if raw.Feature.FilenamePattern != defaultFilenamePattern {
			return nil, fmt.Errorf("feature.filenamePattern must be the canonical %q for this CLI release", defaultFilenamePattern)
		}
		pattern, compileErr := regexp.Compile(raw.Feature.FilenamePattern)
		if compileErr != nil {
			return nil, fmt.Errorf("invalid feature.filenamePattern: %w", compileErr)
		}
		lifecycle.filenamePattern = pattern
	}
	if raw.Feature.OwnerPattern != "" {
		if raw.Feature.OwnerPattern != defaultOwnerPattern {
			return nil, fmt.Errorf("feature.ownerPattern must be the canonical %q for this CLI release", defaultOwnerPattern)
		}
		pattern, compileErr := regexp.Compile(raw.Feature.OwnerPattern)
		if compileErr != nil {
			return nil, fmt.Errorf("invalid feature.ownerPattern: %w", compileErr)
		}
		lifecycle.ownerPattern = pattern
	}
	if raw.Feature.SlugMaxWords != 0 && raw.Feature.SlugMaxWords != 6 {
		return nil, errors.New("lifecycle contract feature.slugMaxWords must be 6 for this CLI release")
	}

	if len(raw.Feature.Statuses) != len(canonicalStatuses) {
		return nil, fmt.Errorf("feature.statuses must be the canonical lifecycle %v", canonicalStatuses)
	}
	for index, status := range raw.Feature.Statuses {
		status = normalizeStatus(status)
		if !statusSegmentPattern.MatchString(status) {
			return nil, fmt.Errorf("invalid lifecycle status %q", status)
		}
		if status != canonicalStatuses[index] {
			return nil, fmt.Errorf("feature.statuses must be ordered exactly as %v", canonicalStatuses)
		}
		if _, exists := lifecycle.statuses[status]; exists {
			return nil, fmt.Errorf("duplicate lifecycle status %q", status)
		}
		lifecycle.statuses[status] = struct{}{}
		lifecycle.transitions[status] = map[string]struct{}{}
		if lifecycle.initial == "" {
			lifecycle.initial = status
		}
	}

	for from, targets := range raw.Feature.Transitions {
		from = normalizeStatus(from)
		if _, ok := lifecycle.statuses[from]; !ok {
			return nil, fmt.Errorf("transition source %q is not a declared status", from)
		}
		for _, target := range targets {
			target = normalizeStatus(target)
			if _, ok := lifecycle.statuses[target]; !ok {
				return nil, fmt.Errorf("transition target %q is not a declared status", target)
			}
			lifecycle.transitions[from][target] = struct{}{}
		}
	}
	if len(raw.Feature.Transitions) != len(canonicalTransitions) {
		return nil, fmt.Errorf("feature.transitions must be the canonical lifecycle map")
	}
	for status, expectedTargets := range canonicalTransitions {
		actualTargets, ok := raw.Feature.Transitions[status]
		if !ok || len(actualTargets) != len(expectedTargets) {
			return nil, fmt.Errorf("feature.transitions[%s] must be %v", status, expectedTargets)
		}
		actualSet := map[string]bool{}
		for _, target := range actualTargets {
			actualSet[normalizeStatus(target)] = true
		}
		for _, target := range expectedTargets {
			if !actualSet[target] {
				return nil, fmt.Errorf("feature.transitions[%s] must be %v", status, expectedTargets)
			}
		}
	}

	for status := range lifecycle.statuses {
		policy, ok := raw.Feature.Ownership[status]
		if !ok {
			return nil, fmt.Errorf("ownership policy missing for status %q", status)
		}
		if policy != OwnershipAssigned && policy != OwnershipUnassignedAllowed {
			return nil, fmt.Errorf("invalid ownership policy %q for status %q", policy, status)
		}
		lifecycle.ownership[status] = policy
	}
	for _, status := range canonicalStatuses {
		expected := OwnershipAssigned
		if status == "backlog" {
			expected = OwnershipUnassignedAllowed
		}
		if lifecycle.ownership[status] != expected {
			return nil, fmt.Errorf("feature.ownership[%s] must be %q", status, expected)
		}
	}

	return lifecycle, nil
}

func validateRelativePath(name, value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s must not be empty", name)
	}
	if filepath.IsAbs(value) {
		return "", fmt.Errorf("%s must be relative", name)
	}
	clean := filepath.Clean(value)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s escapes the workspace", name)
	}
	return clean, nil
}

func validateOptionalRelativePath(name, value, fallback string) (string, error) {
	if strings.TrimSpace(value) == "" {
		value = fallback
	}
	return validateRelativePath(name, value)
}

func (l *Lifecycle) validateResolvedPaths() error {
	for name, target := range map[string]string{
		"workspace.paths.features":  l.FeaturesDir(),
		"workspace.paths.specs":     l.SpecsDir(),
		"workspace.paths.templates": l.TemplatesDir(),
		"workspace.paths.schemas":   l.SchemasDir(),
		"workspace.paths.locks":     l.LocksDir(),
		"workspace.paths.auditLog":  l.AuditLogPath(),
	} {
		if err := validateResolvedWithinRoot(l.root, target); err != nil {
			return fmt.Errorf("%s resolves outside workspace: %w", name, err)
		}
	}
	for _, status := range l.Statuses() {
		target, _ := l.DirectoryForStatus(status)
		if err := validateLexicallyWithinBase(l.FeaturesDir(), target); err != nil {
			return fmt.Errorf("feature status directory %q escapes configured features directory: %w", status, err)
		}
		if err := validateResolvedWithinRoot(l.root, target); err != nil {
			return fmt.Errorf("feature status directory %q resolves outside workspace: %w", status, err)
		}
	}
	return nil
}

func validateLexicallyWithinBase(base, target string) error {
	rel, err := filepath.Rel(filepath.Clean(base), filepath.Clean(target))
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s", target)
	}
	return nil
}

func validateResolvedWithinRoot(root, target string) error {
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	existing := target
	for {
		if _, statErr := os.Lstat(existing); statErr == nil {
			break
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return fmt.Errorf("no existing ancestor for %s", target)
		}
		existing = parent
	}
	resolvedExisting, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return err
	}
	rel, err := filepath.Rel(resolvedRoot, resolvedExisting)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("%s", target)
	}
	return nil
}

func normalizeStatus(status string) string {
	return strings.ToLower(strings.TrimSpace(status))
}

// FeaturesDir returns the configured absolute feature directory.
func (l *Lifecycle) FeaturesDir() string { return filepath.Join(l.root, l.featuresDir) }

// SpecsDir returns the configured absolute system-spec directory.
func (l *Lifecycle) SpecsDir() string { return filepath.Join(l.root, l.specsDir) }

// TemplatesDir returns the configured absolute template directory.
func (l *Lifecycle) TemplatesDir() string { return filepath.Join(l.root, l.templatesDir) }

// SchemasDir returns the configured absolute schema directory.
func (l *Lifecycle) SchemasDir() string { return filepath.Join(l.root, l.schemasDir) }

// IndexPath returns the canonical feature-index path.
func (l *Lifecycle) IndexPath() string { return filepath.Join(l.FeaturesDir(), "INDEX.md") }

// LocksDir returns the configured absolute lock directory.
func (l *Lifecycle) LocksDir() string { return filepath.Join(l.root, l.locksDir) }

// AuditLogPath returns the configured absolute audit log path.
func (l *Lifecycle) AuditLogPath() string { return filepath.Join(l.root, l.auditLog) }

// InitialStatus returns the first declared status in the workspace contract.
func (l *Lifecycle) InitialStatus() string { return l.initial }

// SlugMaxWords returns the maximum number of words allowed in feature slugs.
func (l *Lifecycle) SlugMaxWords() int { return l.slugMaxWords }

// Statuses returns declared lifecycle statuses in deterministic order.
func (l *Lifecycle) Statuses() []string {
	statuses := make([]string, 0, len(l.statuses))
	for status := range l.statuses {
		statuses = append(statuses, status)
	}
	sort.Strings(statuses)
	return statuses
}

// ValidateFeatureID verifies a stable feature ID against the workspace contract.
func (l *Lifecycle) ValidateFeatureID(id string) error {
	if !l.idPattern.MatchString(strings.TrimSpace(id)) {
		return fmt.Errorf("feature ID %q does not match workspace feature.idPattern", id)
	}
	return nil
}

// ValidateFilename verifies the immutable feature basename against the contract.
func (l *Lifecycle) ValidateFilename(name string) error {
	if !l.filenamePattern.MatchString(name) {
		return fmt.Errorf("filename %q does not match workspace feature.filenamePattern", name)
	}
	return nil
}

// ValidateOwner verifies a coordination actor/owner against the contract.
func (l *Lifecycle) ValidateOwner(owner string, allowUnassigned bool) error {
	owner = strings.TrimSpace(owner)
	if owner == "" || !l.ownerPattern.MatchString(owner) {
		return fmt.Errorf("invalid actor or owner %q", owner)
	}
	if !allowUnassigned && (strings.EqualFold(owner, "unassigned") || strings.EqualFold(owner, "unknown")) {
		return fmt.Errorf("actor must be explicit and cannot be %q", owner)
	}
	return nil
}

// DirectoryForStatus returns the configured absolute directory for status.
func (l *Lifecycle) DirectoryForStatus(status string) (string, bool) {
	status = normalizeStatus(status)
	if _, ok := l.statuses[status]; !ok {
		return "", false
	}
	return filepath.Join(l.FeaturesDir(), status), true
}

// ValidateStatus verifies that status is declared by the workspace contract.
func (l *Lifecycle) ValidateStatus(status string) error {
	status = normalizeStatus(status)
	if _, ok := l.statuses[status]; !ok {
		return fmt.Errorf("invalid status %q", status)
	}
	return nil
}

// ValidateTransition verifies a directed transition from the contract.
func (l *Lifecycle) ValidateTransition(current, target string) error {
	current = normalizeStatus(current)
	target = normalizeStatus(target)
	if err := l.ValidateStatus(target); err != nil {
		return err
	}
	allowed, ok := l.transitions[current]
	if !ok {
		return fmt.Errorf("status %q cannot transition", current)
	}
	if _, ok := allowed[target]; ok {
		return nil
	}
	if len(allowed) == 0 {
		return fmt.Errorf("cannot transition from %s", current)
	}
	return fmt.Errorf("cannot transition from %s to %s", current, target)
}

// RequiresAssignedOwner reports whether status requires a concrete owner.
func (l *Lifecycle) RequiresAssignedOwner(status string) bool {
	return l.ownership[normalizeStatus(status)] == OwnershipAssigned
}
