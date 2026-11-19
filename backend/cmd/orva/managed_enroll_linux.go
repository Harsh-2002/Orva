package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/spf13/cobra"
)

func newManagedCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "managed", Short: "Manage the local instance's offline managed layout (server binary only)"}
	var dataDir, planPath string
	var dryRun, apply, confirm bool
	var exclude []string
	enroll := &cobra.Command{
		Use:   "enroll",
		Short: "Plan or apply offline enrollment of current legacy deployments without importing grants",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if apply {
				if !confirm || planPath == "" {
					return errors.New("--apply requires --plan FILE and --confirm")
				}
				return withEnrollmentPlan(cmd.Context(), dataDir, func(_ string, _ *servingmode.Lock, _ string, plan *enrollmentPlan) error {
					if _, err := validateEnrollmentApply(planPath, plan, exclude); err != nil {
						return err
					}
					return errors.New("enrollment apply is not yet available")
				})
			}
			if !dryRun || confirm || len(exclude) != 0 {
				return errors.New("use --dry-run to inspect; --confirm and --exclude require --apply")
			}
			return planManagedEnrollment(cmd.Context(), dataDir, planPath, func(raw []byte) error {
				_, err := cmd.OutOrStdout().Write(raw)
				return err
			})
		},
	}
	enroll.Flags().StringVar(&dataDir, "data-dir", "", "instance data directory (default ORVA_DATA_DIR)")
	enroll.Flags().StringVar(&planPath, "plan", "", "plan file: create outside the data directory on dry run, verify on apply")
	enroll.Flags().BoolVar(&dryRun, "dry-run", true, "inspect the stopped legacy instance without changing it")
	enroll.Flags().BoolVar(&apply, "apply", false, "apply the matching offline enrollment plan")
	enroll.Flags().BoolVar(&confirm, "confirm", false, "confirm the offline layout conversion")
	enroll.Flags().StringArrayVar(&exclude, "exclude", nil, "explicit legacy function UUID to leave inactive (repeatable, apply only)")
	enroll.MarkFlagsMutuallyExclusive("dry-run", "apply")
	cmd.AddCommand(enroll)
	return cmd
}

func withEnrollmentPlan(ctx context.Context, dataDir string, use func(string, *servingmode.Lock, string, *enrollmentPlan) error) (err error) {
	dataDir, err = managedSetupDataDir(dataDir)
	if err != nil {
		return err
	}
	lock, err := servingmode.AcquireEnrollment(dataDir)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, lock.Close()) }()
	record, root, err := lock.OpenData()
	if err != nil {
		return err
	}
	defer root.Close()
	if record.Mode != servingmode.Legacy {
		return errors.New("enrollment requires an unmarked legacy data directory")
	}
	scratch, err := enrollmentScratch(dataDir)
	if err != nil {
		return err
	}
	defer os.RemoveAll(scratch)
	plan, err := captureEnrollmentPlan(ctx, dataDir, scratch)
	if err != nil {
		return err
	}
	return use(dataDir, lock, scratch, plan)
}

func enrollmentPlanBytes(plan *enrollmentPlan) ([]byte, error) {
	raw, err := json.Marshal(plan)
	return append(raw, '\n'), err
}

func planManagedEnrollment(ctx context.Context, dataDir, planPath string, emit func([]byte) error) error {
	return withEnrollmentPlan(ctx, dataDir, func(root string, _ *servingmode.Lock, _ string, plan *enrollmentPlan) error {
		raw, err := enrollmentPlanBytes(plan)
		if err != nil {
			return err
		}
		if planPath != "" {
			path, err := enrollmentOutside(root, planPath)
			if err != nil {
				return err
			}
			out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, writeErr := out.Write(raw)
			if err := errors.Join(writeErr, out.Sync(), out.Close()); err != nil {
				return err
			}
		}
		return emit(raw)
	})
}

func validateEnrollmentApply(path string, current *enrollmentPlan, exclude []string) (map[string]bool, error) {
	const maxPlan = 16 << 20
	f, err := openEnrollmentFile(path)
	if err != nil {
		return nil, fmt.Errorf("open enrollment plan: %w", err)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxPlan+1))
	if err := errors.Join(err, f.Close()); err != nil {
		return nil, err
	}
	now, err := enrollmentPlanBytes(current)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxPlan || !bytes.Equal(raw, now) {
		return nil, errors.New("enrollment plan differs from the current legacy instance; run a new dry run")
	}
	found := make(map[string]bool, len(current.Functions))
	for _, fn := range current.Functions {
		found[fn.ID] = true
	}
	skipped := make(map[string]bool, len(exclude))
	for _, id := range exclude {
		if !found[id] || skipped[id] {
			return nil, fmt.Errorf("--exclude must list each known function UUID at most once: %q", id)
		}
		skipped[id] = true
	}
	for _, fn := range current.Functions {
		if !fn.Enrollable && !skipped[fn.ID] {
			return nil, fmt.Errorf("unsupported function %s: %s; explicit --exclude %s required", fn.ID, fn.Reason, fn.ID)
		}
	}
	return skipped, nil
}

func enrollmentOutside(dataDir, path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(abs))
	if err != nil {
		return "", err
	}
	path = filepath.Join(parent, filepath.Base(abs))
	rel, err := filepath.Rel(dataDir, path)
	if err != nil {
		return "", err
	}
	if filepath.IsLocal(rel) {
		return "", errors.New("enrollment scratch and plan must be outside the data directory")
	}
	return path, nil
}

func enrollmentScratch(dataDir string) (string, error) {
	temp, err := enrollmentOutside(dataDir, filepath.Join(os.TempDir(), "orva-enroll"))
	if err != nil {
		return "", err
	}
	return os.MkdirTemp(filepath.Dir(temp), "orva-enroll-")
}
