package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/config"
	"github.com/virtualboard/vb-cli/internal/contract"
	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/util"
)

func options() (*config.Options, error) {
	return config.Current()
}

func wrapMutationError(err error) error {
	if errors.Is(err, config.ErrActorRequired) || errors.Is(err, config.ErrActorInvalid) {
		return WrapCLIError(ExitCodeValidation, err)
	}
	if errors.Is(err, feature.ErrDeleteConflict) || errors.Is(err, feature.ErrDependencyBlocked) || errors.Is(err, feature.ErrDependencyCycle) || errors.Is(err, feature.ErrInvalidTransition) {
		return WrapCLIError(ExitCodeValidation, err)
	}
	if errors.Is(err, feature.ErrOwnershipConflict) || errors.Is(err, feature.ErrLockConflict) || errors.Is(err, feature.ErrStaleFeature) {
		return WrapCLIError(ExitCodeLockConflict, err)
	}
	return WrapCLIError(ExitCodeFilesystem, err)
}

func requireWorkspaceActor(opts *config.Options) (string, error) {
	actor, err := opts.RequireActor()
	if err != nil {
		return "", err
	}
	lifecycle, err := contract.Load(opts.RootDir)
	if err != nil {
		return "", err
	}
	if err := lifecycle.ValidateOwner(actor, false); err != nil {
		return "", fmt.Errorf("%w: %v", config.ErrActorInvalid, err)
	}
	return actor, nil
}

func resolveWorkspaceWritePath(root, target string) (string, string, error) {
	if filepath.IsAbs(target) {
		return "", "", fmt.Errorf("output path must be relative to the workspace")
	}
	cleanTarget := filepath.Clean(filepath.FromSlash(target))
	if cleanTarget == "." || cleanTarget == ".." || strings.HasPrefix(cleanTarget, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("output path escapes the workspace: %s", target)
	}
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return "", "", err
	}
	absPath := filepath.Join(rootAbs, cleanTarget)
	rel, err := filepath.Rel(rootAbs, absPath)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("output path escapes the workspace: %s", target)
	}

	resolvedRoot, err := filepath.EvalSymlinks(rootAbs)
	if err != nil {
		return "", "", fmt.Errorf("resolve workspace root: %w", err)
	}
	existingParent := filepath.Dir(absPath)
	for {
		if _, statErr := os.Stat(existingParent); statErr == nil {
			break
		} else if !os.IsNotExist(statErr) {
			return "", "", fmt.Errorf("inspect output parent: %w", statErr)
		}
		parent := filepath.Dir(existingParent)
		if parent == existingParent {
			return "", "", fmt.Errorf("cannot resolve output parent for %s", target)
		}
		existingParent = parent
	}
	resolvedParent, err := filepath.EvalSymlinks(existingParent)
	if err != nil {
		return "", "", fmt.Errorf("resolve output parent: %w", err)
	}
	parentRel, err := filepath.Rel(resolvedRoot, resolvedParent)
	if err != nil || parentRel == ".." || strings.HasPrefix(parentRel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("output path resolves outside the workspace: %s", target)
	}
	return absPath, filepath.ToSlash(rel), nil
}

func respond(cmd *cobra.Command, opts *config.Options, success bool, message string, data interface{}) error {
	if opts.JSONOutput {
		payload := util.StructuredResult(success, message, data)
		return util.PrintJSON(cmd.OutOrStdout(), payload)
	}
	if message != "" {
		fmt.Fprintln(cmd.OutOrStdout(), message)
	}
	return nil
}
