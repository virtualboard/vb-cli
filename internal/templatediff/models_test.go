package templatediff

import (
	"testing"
)

func TestTemplateDiff_HasChanges(t *testing.T) {
	tests := []struct {
		name string
		diff *TemplateDiff
		want bool
	}{
		{
			name: "no changes",
			diff: &TemplateDiff{
				Added:    []FileDiff{},
				Modified: []FileDiff{},
				Removed:  []FileDiff{},
			},
			want: false,
		},
		{
			name: "has added files",
			diff: &TemplateDiff{
				Added: []FileDiff{
					{Path: "file1.txt", Status: FileStatusAdded},
				},
				Modified: []FileDiff{},
				Removed:  []FileDiff{},
			},
			want: true,
		},
		{
			name: "has modified files",
			diff: &TemplateDiff{
				Added: []FileDiff{},
				Modified: []FileDiff{
					{Path: "file1.txt", Status: FileStatusModified},
				},
				Removed: []FileDiff{},
			},
			want: true,
		},
		{
			name: "has removed files",
			diff: &TemplateDiff{
				Added:    []FileDiff{},
				Modified: []FileDiff{},
				Removed: []FileDiff{
					{Path: "file1.txt", Status: FileStatusRemoved},
				},
			},
			want: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.diff.HasChanges(); got != tt.want {
				t.Errorf("TemplateDiff.HasChanges() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTemplateDiff_TotalChanges(t *testing.T) {
	tests := []struct {
		name string
		diff *TemplateDiff
		want int
	}{
		{
			name: "no changes",
			diff: &TemplateDiff{
				Added:    []FileDiff{},
				Modified: []FileDiff{},
				Removed:  []FileDiff{},
			},
			want: 0,
		},
		{
			name: "multiple changes",
			diff: &TemplateDiff{
				Added: []FileDiff{
					{Path: "file1.txt", Status: FileStatusAdded},
					{Path: "file2.txt", Status: FileStatusAdded},
				},
				Modified: []FileDiff{
					{Path: "file3.txt", Status: FileStatusModified},
				},
				Removed: []FileDiff{
					{Path: "file4.txt", Status: FileStatusRemoved},
				},
			},
			want: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.diff.TotalChanges(); got != tt.want {
				t.Errorf("TemplateDiff.TotalChanges() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestTemplateDiff_GetFileDiff(t *testing.T) {
	diff := &TemplateDiff{
		Added: []FileDiff{
			{Path: "added.txt", Status: FileStatusAdded},
		},
		Modified: []FileDiff{
			{Path: "modified.txt", Status: FileStatusModified},
		},
		Removed: []FileDiff{
			{Path: "removed.txt", Status: FileStatusRemoved},
		},
	}

	tests := []struct {
		name     string
		path     string
		wantNil  bool
		wantPath string
	}{
		{
			name:     "find added file",
			path:     "added.txt",
			wantNil:  false,
			wantPath: "added.txt",
		},
		{
			name:     "find modified file",
			path:     "modified.txt",
			wantNil:  false,
			wantPath: "modified.txt",
		},
		{
			name:     "find removed file",
			path:     "removed.txt",
			wantNil:  false,
			wantPath: "removed.txt",
		},
		{
			name:    "file not found",
			path:    "nonexistent.txt",
			wantNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := diff.GetFileDiff(tt.path)
			if tt.wantNil {
				if got != nil {
					t.Errorf("TemplateDiff.GetFileDiff() = %v, want nil", got)
				}
			} else {
				if got == nil {
					t.Errorf("TemplateDiff.GetFileDiff() = nil, want non-nil")
				} else if got.Path != tt.wantPath {
					t.Errorf("TemplateDiff.GetFileDiff().Path = %v, want %v", got.Path, tt.wantPath)
				}
			}
		})
	}
}
