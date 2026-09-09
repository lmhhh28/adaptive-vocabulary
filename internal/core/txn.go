package core

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// TxnContext wraps a database transaction and provides helper methods
// so operations across modules can participate in the same atomic transaction.
type TxnContext struct {
	ctx context.Context
	tx  *sql.Tx
}

func NewTxnContext(ctx context.Context, tx *sql.Tx) *TxnContext {
	return &TxnContext{
		ctx: ctx,
		tx:  tx,
	}
}

func (tc *TxnContext) Context() context.Context {
	if tc.ctx == nil {
		return context.Background()
	}
	return tc.ctx
}

func (tc *TxnContext) Tx() *sql.Tx {
	return tc.tx
}

func (tc *TxnContext) Exec(query string, args ...any) (sql.Result, error) {
	return tc.tx.ExecContext(tc.Context(), query, args...)
}

func (tc *TxnContext) Query(query string, args ...any) (*sql.Rows, error) {
	return tc.tx.QueryContext(tc.Context(), query, args...)
}

func (tc *TxnContext) QueryRow(query string, args ...any) *sql.Row {
	return tc.tx.QueryRowContext(tc.Context(), query, args...)
}

// RunInTx executes a function within an atomic transaction.
// If fn returns an error or panics, the transaction is rolled back.
func RunInTx(ctx context.Context, db *sql.DB, fn func(tc *TxnContext) error) (err error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}

	tc := NewTxnContext(ctx, tx)

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			rollbackErr := tx.Rollback()
			if rollbackErr != nil && !errors.Is(rollbackErr, sql.ErrTxDone) {
				err = fmt.Errorf("tx err: %v, rollback err: %w", err, rollbackErr)
			}
		} else {
			commitErr := tx.Commit()
			if commitErr != nil {
				err = fmt.Errorf("commit transaction: %w", commitErr)
			}
		}
	}()

	return fn(tc)
}
