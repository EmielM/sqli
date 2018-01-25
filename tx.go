package sqli

import (
	"database/sql"
	"errors"
	"log"
	"reflect"
	"time"

	"math/rand"

	"github.com/lib/pq"
)

type txer interface {
	queryer
	Rollback() error
	Commit() error
}

type Tx struct {
	tx       txer
	Now      time.Time
	Attempt  int
	onCommit []func()
	onDone   []func()
	onFail   []func(bool)
}

func (tx *Tx) OnCommit(f func()) {
	tx.onCommit = append(tx.onCommit, f)
}

func (tx *Tx) OnDone(f func()) {
	tx.onDone = append(tx.onDone, f)
}

func (tx *Tx) OnFail(f func(bool)) {
	tx.onFail = append(tx.onFail, f)
}

var ErrTooMuchAttempts = errors.New("sqli: too much tx attempts")
var TxTrace bool

func (db *DB) Do(cb func(*Tx)) error {
	tx := &Tx{Attempt: 1}

	for {
		tx.onCommit = nil
		tx.onDone = nil
		tx.onFail = nil

		tx0, err := db.Begin()
		if err != nil {
			return err
		}

		_, err = tx0.Exec("set transaction isolation level repeatable read")
		if err != nil {
			log.Print("sqli: could not 'set transaction isolation level repeatable read', ignoring err=", err)
		}

		tx.tx = tx0
		if TxTrace {
			tx.tx = &tracedTx{tx: tx.tx}
		}

		tx.Now = time.Now().UTC().Truncate(time.Millisecond) // work with milliseconds

		txErr := tx.runAndCommit(cb)
		if txErr.err == nil {
			// all done!
			for _, f := range tx.onDone {
				f()
			}
			return nil
		}

		if txErr.retry && tx.Attempt > 3 {
			txErr.err = ErrTooMuchAttempts
			txErr.retry = false
		}

		for _, f := range tx.onFail {
			f(txErr.retry)
		}

		if !txErr.retry {
			log.Print("sqli: txFail err=", txErr.err)
			return txErr.err
		}

		tx.Attempt++
		log.Print("sqli: txRetry=", tx.Attempt, " err=", txErr.err)
		time.Sleep(time.Duration(tx.Attempt*tx.Attempt) * time.Duration(50+rand.Intn(100)) * time.Millisecond)
	}
	// never reaches
}

// DoContext should perhaps be exposed in the future, but the truth is
// I really don't like the whole context API.
//func (db *DB) DoContext(ctx context.Context, cb func(*Tx)) error {
//}

// runAndCommit runs the cb func, the tx.onCommit handlers and then commits the transaction.
// If any txErrors are thrown during execution,
func (tx *Tx) runAndCommit(cb func(*Tx)) (txErr txError) {

	defer func() {
		if rvr := recover(); rvr != nil {
			tx.tx.Rollback()
			if e, ok := rvr.(txError); ok {
				txErr = e
				return
			}
			// re-panic
			panic(rvr)
		}
	}()

	cb(tx)

	for _, f := range tx.onCommit {
		f() // could still call tx.AbortNow() if they want to transaction to fail
	}

	txErr.err = tx.tx.Commit()

	txErr.retry = true // error on commit means we should retry
	return
}

var errTxAbort = errors.New("tx abort")

// AbortNow immediately stops the transaction flow (unwinds stack)
// and cancels the transaction
func (tx *Tx) AbortNow(err error) {
	panic(txError{err: err})
}

// Retry immediately stops the transaction flow (unwinds stack)
// and schedules a tx retry.
func (tx *Tx) RetryNow(err error) {
	panic(txError{err: err, retry: true})
}

type txError struct {
	err   error
	retry bool
}

func checkTxError(err error) {
	if err != nil {
		txErr := txError{err: err}
		if pqErr, ok := err.(*pq.Error); ok {
			if pqErr.Code.Class() == "40" {
				txErr.retry = true
			}
		}
		panic(txErr)
	}
}

func (tx *Tx) Query(query string, args ...interface{}) *sql.Rows {
	r, err := tx.tx.Query(query, args...)
	checkTxError(err)
	return r
}

func (tx *Tx) Exec(query string, args ...interface{}) sql.Result {
	r, err := tx.tx.Exec(query, args...)
	checkTxError(err)
	return r
}

type Row struct {
	row *sql.Row
}

func (r *Row) Scan(dest ...interface{}) {
	err := r.row.Scan(dest...)
	checkTxError(err)
}

func (tx *Tx) QueryRow(query string, args ...interface{}) *Row {
	return &Row{row: tx.tx.QueryRow(query, args...)}
}

// Get a single row from the database. r should be a pointer to a new struct that we
// will populate when the query matches. query should be the part in sql query after
// where `, or should be prefixed with `:`.
// When there are zero results for the query, an empty pointer of the same pointer
// type as r is returned
func (tx *Tx) Get(r interface{}, query string, args ...interface{}) interface{} {
	err := getRecord(tx.tx, r, query, args...)
	checkTxError(err)
	if GetID(r) == 0 {
		// special case, return nil pointer of record type for syntactic sugar:
		// user := tx.Get(new(User), "id=$1", "nonexist").(*User) ==> nil pointer instead of the empty user
		return reflect.Zero(reflect.TypeOf(r)).Interface()
	}
	return r
}

// GetAll fetches a slice of all records returned for a query into rs.
// rs should be a pointer to a slice that we'll fill and update like db.GetAll.
// GetAll returns the unpointed slice.
// As a special case, rs could be a pointer to a record type only to indicate type.
// GetAll will then allocate the slice and you should use the return value.
func (tx *Tx) GetAll(rs interface{}, query string, args ...interface{}) interface{} {
	rt := reflect.TypeOf(rs)
	if rt.Kind() == reflect.Ptr && rt.Elem().Kind() == reflect.Struct {
		// special case: invoked with new(MyType), so we can allocate the slice (and return it)
		rs = reflect.New(reflect.SliceOf(rt)).Interface()
	}
	err := getAllRecords(tx.tx, rs, query, args...)
	checkTxError(err)
	return reflect.ValueOf(rs).Elem().Interface()
}

// Update a record that should exist in the database. The updated record is
// saved back in the struct using `update ... returning *`.
//
// `db:"xx,nowrite"` fields are never written to the database; `db:"xx,nullempty"`
// fields will not be written if their value looks empty (0, false, "", etc).
func (tx *Tx) Update(r interface{}) {
	err := updateRecord(tx.tx, r)
	checkTxError(err)
}

// Insert a new record in the database. The newly saved record is saved back in
// the struct (using `insert ... returning *`).
//
// `db:"xx,nowrite"` fields will not be written to the database.
func (tx *Tx) Insert(r interface{}) {
	err := insertRecord(tx.tx, r)
	checkTxError(err)
}

func (tx *Tx) NextSeq(seqName string) int {
	var s struct {
		NextVal int `db:"nextval"`
	}
	tx.Get(&s, `:select nextval($1)`, seqName)
	return s.NextVal
}

func (tx Tx) TraceStats() string {
	if tt, ok := tx.tx.(*tracedTx); ok {
		return tt.stats()
	}
	return ""
}
