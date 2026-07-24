package cmd

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/feature"
	"github.com/virtualboard/vb-cli/internal/lock"
)

func newLockCommand() *cobra.Command {
	var ttl int
	var owner string
	var release bool
	var status bool
	var force bool
	var token string
	var tokenOnly bool

	cmd := &cobra.Command{
		Use:   "lock <id>",
		Short: "Manage feature locks",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts, err := options()
			if err != nil {
				return err
			}
			id := args[0]

			if status && release {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("--status and --release cannot be combined"))
			}
			if token != "" && !release {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("--token requires --release"))
			}
			if token != "" && force {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("--token and --force cannot be combined"))
			}
			if tokenOnly && (status || release || opts.JSONOutput || opts.DryRun) {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("--token-only is valid only for a non-dry-run plain-text acquisition"))
			}
			featureMgr := feature.NewManager(opts)
			lifecycle, err := featureMgr.Lifecycle()
			if err != nil {
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			if err := lifecycle.ValidateFeatureID(id); err != nil {
				return WrapCLIError(ExitCodeValidation, err)
			}
			actor := ""
			if !status {
				actor, err = requireWorkspaceActor(opts)
				if err != nil {
					return wrapMutationError(err)
				}
			}
			mgr := lock.NewManager(opts)

			if status {
				info, err := mgr.Load(id)
				if err != nil {
					if errors.Is(err, lock.ErrLockStorageConflict) {
						return WrapCLIError(ExitCodeLockConflict, err)
					}
					return WrapCLIError(ExitCodeFilesystem, err)
				}
				if info == nil {
					message := fmt.Sprintf("No active lock for %s", id)
					return respond(cmd, opts, true, message, map[string]interface{}{
						"id":      id,
						"locked":  false,
						"expired": false,
					})
				}
				expired := info.Expired()
				state := "active"
				if expired {
					state = "expired"
				}
				expires := info.ExpiresAt().Format(time.RFC3339)
				message := fmt.Sprintf("Lock %s for %s (owner %s, expires %s)", state, id, info.Owner, expires)
				data := lockPayload(info, false)
				return respond(cmd, opts, true, message, data)
			}

			if release {
				if token != "" {
					if err := lock.ValidateToken(token); err != nil {
						return WrapCLIError(ExitCodeValidation, err)
					}
					err = mgr.Release(id, token)
				} else {
					err = mgr.ReleaseOwned(id, actor, force)
				}
				if err != nil {
					if errors.Is(err, lock.ErrLockOwner) || errors.Is(err, lock.ErrLockStorageConflict) || errors.Is(err, lock.ErrLockChanged) || errors.Is(err, lock.ErrLockTokenRequired) {
						return WrapCLIError(ExitCodeLockConflict, err)
					}
					return WrapCLIError(ExitCodeFilesystem, err)
				}
				message := fmt.Sprintf("Released lock for %s", id)
				if opts.DryRun {
					message = fmt.Sprintf("Dry-run: would release lock for %s", id)
				}
				data := map[string]interface{}{
					"id":       id,
					"released": !opts.DryRun,
					"dry_run":  opts.DryRun,
				}
				return respond(cmd, opts, true, message, data)
			}

			if ttl <= 0 {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("ttl must be positive"))
			}
			if ttl > lock.MaxTTLMinutes {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("ttl must not exceed %d minutes", lock.MaxTTLMinutes))
			}
			feat, err := featureMgr.LoadByID(id)
			if err != nil {
				if errors.Is(err, feature.ErrNotFound) {
					return WrapCLIError(ExitCodeNotFound, err)
				}
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			if owner == "" {
				owner = actor
			}
			if err := lifecycle.ValidateOwner(owner, false); err != nil {
				return WrapCLIError(ExitCodeValidation, err)
			}
			if owner != actor && !force {
				return WrapCLIError(ExitCodeLockConflict, fmt.Errorf("lock owner %s does not match actor %s", owner, actor))
			}
			featureOwner := strings.TrimSpace(feat.FrontMatter.Owner)
			if featureOwner != "" && !strings.EqualFold(featureOwner, "unassigned") && featureOwner != actor && !force {
				return WrapCLIError(ExitCodeLockConflict, fmt.Errorf("feature %s is owned by %s; actor %s cannot acquire its lock", id, featureOwner, actor))
			}

			info, err := mgr.Acquire(id, owner, ttl, force)
			if err != nil {
				if errors.Is(err, lock.ErrActiveLock) || errors.Is(err, lock.ErrLockStorageConflict) || errors.Is(err, lock.ErrLockChanged) {
					return WrapCLIError(ExitCodeLockConflict, err)
				}
				return WrapCLIError(ExitCodeFilesystem, err)
			}
			if !force {
				current, reloadErr := featureMgr.LoadByID(id)
				if reloadErr != nil {
					if releaseErr := mgr.Release(id, info.Token); releaseErr != nil {
						return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("feature changed before lock acquisition: %v; release acquired lock: %w", reloadErr, releaseErr))
					}
					if errors.Is(reloadErr, feature.ErrNotFound) {
						return WrapCLIError(ExitCodeNotFound, reloadErr)
					}
					return WrapCLIError(ExitCodeFilesystem, reloadErr)
				}
				currentOwner := strings.TrimSpace(current.FrontMatter.Owner)
				if currentOwner != "" && !strings.EqualFold(currentOwner, "unassigned") && currentOwner != actor {
					if releaseErr := mgr.Release(id, info.Token); releaseErr != nil {
						return WrapCLIError(ExitCodeFilesystem, fmt.Errorf("feature owner changed to %s before lock acquisition; release acquired lock: %w", currentOwner, releaseErr))
					}
					return WrapCLIError(ExitCodeLockConflict, fmt.Errorf("feature %s is now owned by %s; actor %s cannot acquire its lock", id, currentOwner, actor))
				}
			}

			data := lockPayload(info, !opts.DryRun)
			data["dry_run"] = opts.DryRun
			data["written"] = !opts.DryRun
			message := fmt.Sprintf("Lock acquired for %s (owner %s, ttl %dm)", id, info.Owner, info.TTLMinutes)
			if opts.DryRun {
				message = fmt.Sprintf("Dry-run: would acquire lock for %s (owner %s, ttl %dm)", id, info.Owner, info.TTLMinutes)
			} else {
				message = fmt.Sprintf("%s; release token %s", message, info.Token)
			}
			if tokenOnly {
				fmt.Fprintln(cmd.OutOrStdout(), info.Token)
				return nil
			}
			return respond(cmd, opts, true, message, data)
		},
	}

	cmd.Flags().IntVar(&ttl, "ttl", 30, "Lock TTL in minutes")
	cmd.Flags().StringVar(&owner, "owner", "", "Owner acquiring the lock")
	cmd.Flags().BoolVar(&release, "release", false, "Release the lock")
	cmd.Flags().BoolVar(&status, "status", false, "Show lock status")
	cmd.Flags().BoolVar(&force, "force", false, "Override an active lock")
	cmd.Flags().StringVar(&token, "token", "", "Exact acquisition token required to release a current lock")
	cmd.Flags().BoolVar(&tokenOnly, "token-only", false, "Print only the acquisition token for safe shell capture")
	return cmd
}

func lockPayload(info *lock.Info, includeToken bool) map[string]interface{} {
	expiresAt := info.ExpiresAt().Format(time.RFC3339)
	payload := map[string]interface{}{
		"id":          info.ID,
		"owner":       info.Owner,
		"ttl_minutes": info.TTLMinutes,
		"started_at":  info.StartedAt.Format(time.RFC3339),
		"expires_at":  expiresAt,
		"expired":     info.Expired(),
	}
	if includeToken {
		payload["token"] = info.Token
	}
	return payload
}
