package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/Harsh-2002/Orva/backend/internal/servingmode"
	"github.com/spf13/cobra"
)

func newManagedCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "managed", Short: "Manage the local instance's offline managed layout (server binary only)"}
	var dataDir, planPath string
	var dryRun bool
	enroll := &cobra.Command{
		Use:   "enroll",
		Short: "Plan offline enrollment of current legacy deployments without importing grants",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !dryRun {
				return errors.New("use --dry-run to inspect the legacy instance")
			}
			return planManagedEnrollment(cmd.Context(), dataDir, planPath, func(raw []byte) error {
				_, err := cmd.OutOrStdout().Write(raw)
				return err
			})
		},
	}
	enroll.Flags().StringVar(&dataDir, "data-dir", "", "instance data directory (default ORVA_DATA_DIR)")
	enroll.Flags().StringVar(&planPath, "plan", "", "create a new plan file outside the data directory (also printed to stdout)")
	enroll.Flags().BoolVar(&dryRun, "dry-run", true, "inspect the stopped legacy instance without changing it")
	cmd.AddCommand(enroll)
	return cmd
}

func planManagedEnrollment(ctx context.Context, dataDir, planPath string, emit func([]byte) error) (err error) {
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
	raw, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if planPath != "" {
		path, err := enrollmentOutside(dataDir, planPath)
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
