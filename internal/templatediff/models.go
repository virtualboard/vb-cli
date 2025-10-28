package templatediff

// FileStatus represents the status of a file in the comparison
type FileStatus string

const (
	FileStatusAdded     FileStatus = "added"
	FileStatusModified  FileStatus = "modified"
	FileStatusRemoved   FileStatus = "removed"
	FileStatusUnchanged FileStatus = "unchanged"
)

// FileDiff represents the difference for a single file
type FileDiff struct {
	Path          string // Relative path from .virtualboard/
	Status        FileStatus
	UnifiedDiff   string // Unified diff output (empty for added/removed)
	LocalContent  []byte // Content of local file (nil if added)
	RemoteContent []byte // Content of remote file (nil if removed)
}

// TemplateDiff represents all differences between local and remote templates
type TemplateDiff struct {
	Added     []FileDiff
	Modified  []FileDiff
	Removed   []FileDiff
	Unchanged []FileDiff
}

// HasChanges returns true if there are any changes
func (td *TemplateDiff) HasChanges() bool {
	return len(td.Added) > 0 || len(td.Modified) > 0 || len(td.Removed) > 0
}

// TotalChanges returns the total number of changes
func (td *TemplateDiff) TotalChanges() int {
	return len(td.Added) + len(td.Modified) + len(td.Removed)
}

// GetFileDiff returns the FileDiff for a specific path, or nil if not found
func (td *TemplateDiff) GetFileDiff(path string) *FileDiff {
	for i := range td.Added {
		if td.Added[i].Path == path {
			return &td.Added[i]
		}
	}
	for i := range td.Modified {
		if td.Modified[i].Path == path {
			return &td.Modified[i]
		}
	}
	for i := range td.Removed {
		if td.Removed[i].Path == path {
			return &td.Removed[i]
		}
	}
	return nil
}
