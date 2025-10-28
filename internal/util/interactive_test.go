package util

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestPromptUser(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantChoice PromptChoice
		wantErr    bool
	}{
		{
			name:       "yes response",
			input:      "y\n",
			wantChoice: PromptChoiceYes,
			wantErr:    false,
		},
		{
			name:       "yes full response",
			input:      "yes\n",
			wantChoice: PromptChoiceYes,
			wantErr:    false,
		},
		{
			name:       "no response",
			input:      "n\n",
			wantChoice: PromptChoiceNo,
			wantErr:    false,
		},
		{
			name:       "no full response",
			input:      "no\n",
			wantChoice: PromptChoiceNo,
			wantErr:    false,
		},
		{
			name:       "all response",
			input:      "a\n",
			wantChoice: PromptChoiceAll,
			wantErr:    false,
		},
		{
			name:       "all full response",
			input:      "all\n",
			wantChoice: PromptChoiceAll,
			wantErr:    false,
		},
		{
			name:       "quit response",
			input:      "q\n",
			wantChoice: PromptChoiceQuit,
			wantErr:    false,
		},
		{
			name:       "quit full response",
			input:      "quit\n",
			wantChoice: PromptChoiceQuit,
			wantErr:    false,
		},
		{
			name:       "unknown response",
			input:      "invalid\n",
			wantChoice: PromptChoiceUnknown,
			wantErr:    false,
		},
		{
			name:       "uppercase response",
			input:      "Y\n",
			wantChoice: PromptChoiceYes,
			wantErr:    false,
		},
		{
			name:       "whitespace response",
			input:      "  yes  \n",
			wantChoice: PromptChoiceYes,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Replace stdin
			oldStdin := os.Stdin
			r, w, _ := os.Pipe()
			os.Stdin = r

			// Replace stderr to capture output
			oldStderr := os.Stderr
			os.Stderr = os.NewFile(0, os.DevNull)

			defer func() {
				os.Stdin = oldStdin
				os.Stderr = oldStderr
			}()

			// Write input
			go func() {
				defer w.Close()
				io.WriteString(w, tt.input)
			}()

			got, err := PromptUser("test question")
			if (err != nil) != tt.wantErr {
				t.Errorf("PromptUser() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.wantChoice {
				t.Errorf("PromptUser() = %v, want %v", got, tt.wantChoice)
			}
		})
	}
}

func TestPromptYesNo(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    bool
		wantErr bool
	}{
		{
			name:    "yes response",
			input:   "y\n",
			want:    true,
			wantErr: false,
		},
		{
			name:    "yes full response",
			input:   "yes\n",
			want:    true,
			wantErr: false,
		},
		{
			name:    "no response",
			input:   "n\n",
			want:    false,
			wantErr: false,
		},
		{
			name:    "no full response",
			input:   "no\n",
			want:    false,
			wantErr: false,
		},
		{
			name:    "unknown response defaults to no",
			input:   "invalid\n",
			want:    false,
			wantErr: false,
		},
		{
			name:    "uppercase response",
			input:   "YES\n",
			want:    true,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Replace stdin
			oldStdin := os.Stdin
			r, w, _ := os.Pipe()
			os.Stdin = r

			// Replace stderr to capture output
			oldStderr := os.Stderr
			os.Stderr = os.NewFile(0, os.DevNull)

			defer func() {
				os.Stdin = oldStdin
				os.Stderr = oldStderr
			}()

			// Write input
			go func() {
				defer w.Close()
				io.WriteString(w, tt.input)
			}()

			got, err := PromptYesNo("test question")
			if (err != nil) != tt.wantErr {
				t.Errorf("PromptYesNo() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("PromptYesNo() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPromptUser_StderrOutput(t *testing.T) {
	// Replace stdin
	oldStdin := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r

	// Capture stderr
	oldStderr := os.Stderr
	stderr := &bytes.Buffer{}
	rStderr, wStderr, _ := os.Pipe()
	os.Stderr = wStderr

	defer func() {
		os.Stdin = oldStdin
		os.Stderr = oldStderr
	}()

	// Read stderr in background
	done := make(chan bool)
	go func() {
		io.Copy(stderr, rStderr)
		done <- true
	}()

	// Write input
	go func() {
		defer w.Close()
		io.WriteString(w, "y\n")
	}()

	_, _ = PromptUser("test question")

	wStderr.Close()
	<-done

	output := stderr.String()
	if output == "" {
		t.Error("Expected prompt to be written to stderr")
	}
	if !bytes.Contains([]byte(output), []byte("test question")) {
		t.Errorf("Expected prompt to contain 'test question', got: %s", output)
	}
}
