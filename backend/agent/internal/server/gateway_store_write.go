package server

import (
	"database/sql"
	"fmt"
)

// gatewayExecExactlyOne preserves compare-and-set behavior without SQL RETURNING.
// Use it only for updates that must change the matched row (not idempotent SETs).
func gatewayExecExactlyOne(tx *sql.Tx, query string, args ...any) error {
	result, err := tx.Exec(query, args...)
	if err != nil {
		return err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	switch n {
	case 0:
		return sql.ErrNoRows
	case 1:
		return nil
	default:
		return fmt.Errorf("gateway update affected %d rows; expected one", n)
	}
}

// gatewayInsertID returns the auto-generated key from the same insert and connection.
func gatewayInsertID(tx *sql.Tx, query string, args ...any) (int64, error) {
	result, err := tx.Exec(query, args...)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}
