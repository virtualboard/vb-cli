package cmd

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

const initDirName = ".virtualboard"
const templateZipURL = "https://github.com/virtualboard/template-base/archive/refs/heads/main.zip"
const maxTemplateBytes int64 = 50 * 1024 * 1024

type fetchTemplateFunc func(workdir, dest string) error

var fetchTemplate fetchTemplateFunc = func(workdir, dest string) error {
	resp, err := http.Get(templateZipURL)
	if err != nil {
		return fmt.Errorf("failed to download template archive: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d fetching template", resp.StatusCode)
	}

	tmpFile, err := os.CreateTemp("", "vb-template-*.zip")
	if err != nil {
		return fmt.Errorf("failed to create temp file: %w", err)
	}
	defer func() {
		if cerr := tmpFile.Close(); cerr != nil {
			fmt.Fprintf(os.Stderr, "vb-cli: failed to close temp file: %v\n", cerr)
		}
		if remErr := os.Remove(tmpFile.Name()); remErr != nil {
			fmt.Fprintf(os.Stderr, "vb-cli: failed to remove temp file: %v\n", remErr)
		}
	}()

	if _, err := io.Copy(tmpFile, resp.Body); err != nil {
		return fmt.Errorf("failed to save template archive: %w", err)
	}

	if _, err := tmpFile.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to rewind template archive: %w", err)
	}

	stat, err := tmpFile.Stat()
	if err != nil {
		return fmt.Errorf("failed to stat template archive: %w", err)
	}

	archive, err := zip.NewReader(tmpFile, stat.Size())
	if err != nil {
		return fmt.Errorf("failed to open template archive: %w", err)
	}

	targetRoot := filepath.Join(workdir, dest)
	if err := os.MkdirAll(targetRoot, 0o750); err != nil {
		return fmt.Errorf("failed to create target directory: %w", err)
	}

	for _, file := range archive.File {
		parts := strings.SplitN(file.Name, "/", 2)
		if len(parts) < 2 || parts[1] == "" {
			continue
		}
		relative := parts[1]
		outPath := filepath.Join(targetRoot, relative)
		clean := filepath.Clean(outPath)
		if !strings.HasPrefix(clean, targetRoot) {
			return fmt.Errorf("archive entry escapes target directory: %s", file.Name)
		}

		if file.UncompressedSize64 > uint64(maxTemplateBytes) {
			return fmt.Errorf("archive entry too large: %s", file.Name)
		}

		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(clean, 0o750); err != nil {
				return fmt.Errorf("failed to create directory %s: %w", clean, err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(clean), 0o750); err != nil {
			return fmt.Errorf("failed to ensure parent directory: %w", err)
		}

		src, err := file.Open()
		if err != nil {
			return fmt.Errorf("failed to open archive entry %s: %w", file.Name, err)
		}
		if err := func() error {
			defer src.Close()
			out, err := os.OpenFile(clean, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
			if err != nil {
				return fmt.Errorf("failed to create file %s: %w", clean, err)
			}
			defer out.Close()
			if _, err := io.CopyN(out, src, int64(file.UncompressedSize64)); err != nil {
				return fmt.Errorf("failed to write file %s: %w", clean, err)
			}
			return nil
		}(); err != nil {
			return err
		}
	}
	return nil
}

func newInitCommand() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialise a VirtualBoard workspace in the current directory",
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}

			projectRoot := opts.RootDir
			if filepath.Base(projectRoot) == initDirName {
				projectRoot = filepath.Dir(projectRoot)
			}

			targetPath := filepath.Join(projectRoot, initDirName)

			if exists, err := pathExists(targetPath); err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			} else if exists && !force {
				detail := fmt.Sprintf("VirtualBoard workspace already initialised at %s. Use --force to re-create it. We recommend managing this directory with git.", initDirName)
				if opts.JSONOutput {
					if respErr := respond(cmd, opts, false, detail, map[string]interface{}{
						"path":          initDirName,
						"force_hint":    true,
						"recommend_git": true,
					}); respErr != nil {
						return respErr
					}
					return WrapCLIError(ExitCodeValidation, fmt.Errorf("virtualboard workspace already initialised"))
				}
				return WrapCLIError(ExitCodeValidation, errors.New(detail))
			}

			if force {
				if err := os.RemoveAll(targetPath); err != nil {
					return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to remove existing workspace: %w", err))
				}
			}

			if err := fetchTemplate(projectRoot, initDirName); err != nil {
				return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("failed to prepare template: %w", err))
			}

			msg := fmt.Sprintf("VirtualBoard project initialised in %s. Review the files under %s.", initDirName, initDirName)
			return respond(cmd, opts, true, msg, map[string]interface{}{
				"path":   initDirName,
				"source": templateZipURL,
			})
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "Recreate the VirtualBoard workspace even if it already exists")
	cmd.SilenceUsage = true
	return cmd
}

func pathExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
