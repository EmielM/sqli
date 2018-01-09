package sqli

import (
	"database/sql"
	"fmt"
	"regexp"
	"strconv"
	"time"
)

type tracedTx struct {
	tx  txer
	log string
}

func (tx *tracedTx) Query(query string, args ...interface{}) (*sql.Rows, error) {
	defer tx.trace(query, args...)()
	return tx.tx.Query(query, args...)
}

func (tx *tracedTx) QueryRow(query string, args ...interface{}) *sql.Row {
	defer tx.trace(query, args...)()
	return tx.tx.QueryRow(query, args...)
}

func (tx *tracedTx) Exec(query string, args ...interface{}) (sql.Result, error) {
	defer tx.trace(query, args...)()
	return tx.tx.Exec(query, args...)
}

func (tx *tracedTx) Commit() error {
	defer tx.trace("commit")()
	return tx.tx.Commit()
}

func (tx *tracedTx) Rollback() error {
	return tx.tx.Rollback()
}

var traceSpaceRE = regexp.MustCompile(`\s+`)
var traceParamsRE = regexp.MustCompile(`\$([0-9]+)`)

func (tx *tracedTx) trace(query string, args ...interface{}) func() {

	if tx.log == "" {
		var id string
		tx.tx.QueryRow("select txid_current()").Scan(&id)
		tx.log = fmt.Sprintf("tx=%s\n", id)
	}

	query = traceSpaceRE.ReplaceAllString(query, " ")
	query = traceParamsRE.ReplaceAllStringFunc(query, func(m string) string {
		i, _ := strconv.Atoi(m[1:])
		if i > 0 && i <= len(args) {
			return fmt.Sprintf("%s:%#v", m, args[i-1])
		}
		return m
	})
	start := time.Now()
	return func() {
		took := int64(time.Since(start).Truncate(time.Microsecond) / time.Microsecond)
		tx.log += fmt.Sprintf("%8dµs %s\n", took, query)
	}
}

func (tx *tracedTx) stats() string {
	return tx.log
}
