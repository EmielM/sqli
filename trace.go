package sqli

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

type tracedTx struct {
	*sql.Tx
	t time.Duration
	q []string
}

func (tx *tracedTx) Query(query string, args ...interface{}) (*sql.Rows, error) {
	defer tx.trace(query, args)()
	return tx.Tx.Query(query, args...)
}

func (tx *tracedTx) QueryRow(query string, args ...interface{}) *sql.Row {
	defer tx.trace(query, args)()
	return tx.Tx.QueryRow(query, args...)
}

func (tx *tracedTx) Exec(query string, args ...interface{}) (sql.Result, error) {
	defer tx.trace(query, args)()
	return tx.Tx.Exec(query, args...)
}

func (tx *tracedTx) trace(query string, args []interface{}) func() {
	// todo: better log query/args
	start := time.Now()
	tx.q = append(tx.q, strings.Replace(query, "\n", " ", -1))

	return func() {
		tx.t += time.Since(start)
	}
}

func (tx *tracedTx) stats() string {
	return fmt.Sprintf("tx=%p dbTime=%v\n    %s", tx, tx.t, strings.Join(tx.q, "\n    "))
}
