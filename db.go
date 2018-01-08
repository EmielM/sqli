package sqli

import "database/sql"

type queryer interface {
	Query(string, ...interface{}) (*sql.Rows, error)
	QueryRow(string, ...interface{}) *sql.Row
	Exec(string, ...interface{}) (sql.Result, error)
}

type DB struct {
	*sql.DB
}

func Open(driverName, dataSourceName string) (*DB, error) {
	db, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}
	return &DB{db}, nil
}

func (db *DB) Get(r interface{}, query string, args ...interface{}) error {
	return getRecord(db, r, query, args...)
}

func (db *DB) GetAll(rs interface{}, query string, args ...interface{}) error {
	return getAllRecords(db, rs, query, args...)
}

func (db *DB) Update(r interface{}) error {
	return updateRecord(db, r)
}

func (db *DB) Insert(r interface{}) error {
	return insertRecord(db, r)
}
