package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
)

const PortableFunctionLimit = 256

type PortableDeployment struct {
	Owner, Deployment, Runtime, Entrypoint string
	Output                                 WorkloadBuildOutput
}

// PortableInventory reads current associations from a checkpoint-clean
// snapshot, not from the live registry, Factory, or historical deploy list.
func PortableInventory(ctx context.Context, path, owner string) (int, []PortableDeployment, error) {
	conn, err := portableReadDB(path)
	if err != nil {
		return 0, nil, err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return 0, nil, err
	}
	defer tx.Rollback()
	return portableInventoryTx(ctx, tx, owner)
}

func portableReadDB(path string) (*sql.DB, error) {
	uri := url.URL{Scheme: "file", Path: path, RawQuery: "mode=ro&immutable=1&_pragma=trusted_schema(OFF)"}
	return sql.Open("sqlite", uri.String())
}

func CheckPortableMasterKey(ctx context.Context, path string, size int64) error {
	if size == 32 {
		return nil
	}
	if size != 0 {
		return errors.New("invalid managed master key size")
	}
	conn, err := portableReadDB(path)
	if err != nil {
		return err
	}
	defer conn.Close()
	var encrypted bool
	if err := conn.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM function_secrets) OR
		EXISTS(SELECT 1 FROM ai_provider_configs WHERE coalesce(api_key_encrypted,'')<>'')`).Scan(&encrypted); err != nil {
		return err
	}
	if encrypted {
		return errors.New("encrypted state requires the instance master key")
	}
	return nil
}

func portableInventoryTx(ctx context.Context, tx *sql.Tx, owner string) (int, []PortableDeployment, error) {
	bound, err := checkManagedOwnerBinding(ctx, tx, owner)
	if err != nil || !bound {
		return 0, nil, errors.Join(ErrManagedOwnerInvalid, err)
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,coalesce(active_deployment_id,''),runtime,entrypoint,
		coalesce(run_entrypoint,''),coalesce(code_hash,''),status FROM functions ORDER BY id LIMIT ?`, PortableFunctionLimit+1)
	if err != nil {
		return 0, nil, err
	}
	type current struct {
		PortableDeployment
		run, hash, status string
	}
	var all []current
	for rows.Next() {
		var f current
		if err := rows.Scan(&f.Owner, &f.Deployment, &f.Runtime, &f.Entrypoint, &f.run, &f.hash, &f.status); err != nil {
			rows.Close()
			return 0, nil, err
		}
		all = append(all, f)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(all) > PortableFunctionLimit {
		return 0, nil, errors.Join(ErrWorkloadArtifactInvalid, err)
	}
	var out []PortableDeployment
	for _, f := range all {
		if !validManagedOwnerID(f.Owner) || (f.status != "inactive" && f.status != "active") {
			return 0, nil, ErrWorkloadArtifactInvalid
		}
		if f.Deployment == "" && f.hash == "" && f.status == "inactive" {
			continue
		}
		if f.status == "inactive" {
			// Explicit enrollment exclusions have no managed seal to export.
			var table, excluded bool
			if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM sqlite_schema
				WHERE name='workload_enrollment_functions' AND type='table')`).Scan(&table); err != nil {
				return 0, nil, err
			}
			if table {
				err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM workload_enrollment_functions e
					WHERE e.function_id=? AND e.excluded=1 AND NOT EXISTS(SELECT 1 FROM workload_deployment_capture
					WHERE function_id=e.function_id AND deployment_id=?))`, f.Owner, f.Deployment).Scan(&excluded)
				if err != nil {
					return 0, nil, err
				}
			}
			if excluded {
				continue
			}
		}
		if !validManagedOwnerID(f.Deployment) || (f.run != "" && f.run != f.Entrypoint) {
			return 0, nil, ErrWorkloadArtifactConflict
		}
		a, err := readRollbackAssociationTx(ctx, tx, f.Owner, f.Deployment)
		if err != nil || a.runtime != f.Runtime || a.authored != f.Entrypoint ||
			a.output.CodeHash != f.hash || a.output.Entrypoint != f.Entrypoint {
			return 0, nil, errors.Join(fmt.Errorf("current deployment %s: %w", f.Deployment, ErrWorkloadArtifactConflict), err)
		}
		f.Output = a.output
		out = append(out, f.PortableDeployment)
	}
	return len(all), out, nil
}
