package cmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/virtualboard/vb-cli/internal/lock"
)

func newLockCommand() *cobra.Command {
	var ttl int
	var owner string
	var release bool
	var status bool
	var force bool

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

			mgr := lock.NewManager(opts)

			if status {
				info, err := mgr.Load(id)
				if err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}
				if info == nil {
					message := fmt.Sprintf("No active lock for %s", id)
					return respond(cmd, opts, false, message, map[string]interface{}{
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
				data := lockPayload(info)
				return respond(cmd, opts, !expired, message, data)
			}

			if release {
				if err := mgr.Release(id); err != nil {
					return WrapCLIError(ExitCodeFilesystem, err)
				}
				message := fmt.Sprintf("Released lock for %s", id)
				data := map[string]interface{}{
					"id":       id,
					"released": true,
				}
				return respond(cmd, opts, true, message, data)
			}

			if ttl <= 0 {
				return WrapCLIError(ExitCodeValidation, fmt.Errorf("ttl must be positive"))
			}

			info, err := mgr.Acquire(id, owner, ttl, force)
			if err != nil {
				if errors.Is(err, lock.ErrActiveLock) {
					return WrapCLIError(ExitCodeLockConflict, err)
				}
				return WrapCLIError(ExitCodeFilesystem, err)
			}

			data := lockPayload(info)
			message := fmt.Sprintf("Lock acquired for %s (owner %s, ttl %dm)", id, info.Owner, info.TTLMinutes)
			return respond(cmd, opts, true, message, data)
		},
	}

	cmd.Flags().IntVar(&ttl, "ttl", 30, "Lock TTL in minutes")
	cmd.Flags().StringVar(&owner, "owner", "", "Owner acquiring the lock")
	cmd.Flags().BoolVar(&release, "release", false, "Release the lock")
	cmd.Flags().BoolVar(&status, "status", false, "Show lock status")
	cmd.Flags().BoolVar(&force, "force", false, "Override an active lock")
	return cmd
}

func lockPayload(info *lock.Info) map[string]interface{} {
	expiresAt := info.ExpiresAt().Format(time.RFC3339)
	return map[string]interface{}{
		"id":          info.ID,
		"owner":       info.Owner,
		"ttl_minutes": info.TTLMinutes,
		"started_at":  info.StartedAt.Format(time.RFC3339),
		"expires_at":  expiresAt,
		"expired":     info.Expired(),
	}
}
