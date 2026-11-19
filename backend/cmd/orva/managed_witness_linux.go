package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Harsh-2002/Orva/backend/internal/artifactfs"
	"github.com/Harsh-2002/Orva/backend/internal/config"
)

const (
	managedWitnessEnv     = "ORVA_MANAGED_WITNESS_PATH"
	managedBackupEnv      = "ORVA_MANAGED_BACKUP_DIR"
	managedParentEnv      = "ORVA_MANAGED_WORKLOAD_PARENT"
	defaultManagedWitness = "/etc/orva/host-witness/witness"
)

func managedBackupRoots(dataDir string) []string {
	roots := []string{dataDir}
	if dir, set := os.LookupEnv(managedBackupEnv); set {
		roots = append(roots, dir)
	}
	return roots
}

func setupManagedWitness(path, dataDir string) error {
	dataDir, err := managedSetupDataDir(dataDir)
	if err != nil {
		return err
	}
	if err := artifactfs.ProvisionOwnerWitness(path, managedBackupRoots(dataDir)); err != nil {
		return fmt.Errorf("provision managed host witness: %w", err)
	}
	return nil
}

func managedSetupDataDir(dataDir string) (string, error) {
	if dataDir == "" {
		cfg, err := config.Load()
		if err != nil {
			return "", err
		}
		dataDir = cfg.Data.Dir
	}
	return filepath.Abs(dataDir)
}
