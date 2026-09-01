package store

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// Deleting a catalog entity and detaching it from every tenant.
//
// These ran as separate statements with their errors discarded, so a cleanup
// that failed left the entity gone but still named in tenants' entitlement
// arrays and in per-tenant enablement rows — an inconsistency the caller was
// never told about, because the delete reported success. That is the same shape
// as the wiring-without-dependencies records that had to be repaired by
// migration later.
//
// Running them in one transaction makes the delete all-or-nothing, and returning
// the error means a failure is reported rather than discovered.

// deleteCascade deletes a row from `table` by id and runs its detach statements
// atomically. Returns ErrNotFound when the entity does not exist.
func (s *Store) deleteCascade(ctx context.Context, table, id string, detach ...string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tag, err := tx.Exec(ctx, `DELETE FROM `+table+` WHERE id = $1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	for _, stmt := range detach {
		if _, err := tx.Exec(ctx, stmt, id); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

// txFor is a small helper for callers that need the transaction itself.
var _ = pgx.Tx(nil)
