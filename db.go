package sqli

import "database/sql"

type DB struct {
	sql.DB
}

func Open(driverName, dataSourceName string) (*DB, error) {
	db, err := sql.Open(driverName, dataSourceName)
	if err != nil {
		return nil, err
	}
	// is it ok to deref db here?
	return &DB{*db}, nil
}

func (db *DB) Get(r Record, query string, args ...interface{}) error {
	return getRecord(db, r, query, args...)
}

func (db *DB) GetAll(r Record, query string, args ...interface{}) (interface{}, error) {
	return getAllRecords(db, r, query, args...)
}

func (db *DB) Update(record Record) error {
	return updateRecord(db, record)
}

func (db *DB) Insert(record Record) error {
	return insertRecord(db, record)
}
