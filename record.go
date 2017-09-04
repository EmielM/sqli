package sqli

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
)

// Record should be a pointer to a struct with an "ID" field (in itself or any embedded structs)
// type MyRecord struct {
//     ID int `db:"id"`
//     someField string `db:"some_field"`
// }
type Record interface{}

// queryer is implemented by both *sql.DB and *sql.Tx
type queryer interface {
	Query(string, ...interface{}) (*sql.Rows, error)
	QueryRow(string, ...interface{}) *sql.Row
	Exec(string, ...interface{}) (sql.Result, error)
}

func getRecord(q queryer, r Record, query string, args ...interface{}) error {
	rows, err := selectQuery(q, GetTable(r), query, args)
	if err != nil {
		return err
	}
	defer rows.Close()
	ok := rows.Next()
	if !ok {
		return nil
	}

	err = Hydrate(r, rows)
	if err != nil {
		return err
	}

	return nil
}

func getAllRecords(q queryer, r Record, query string, args ...interface{}) (interface{}, error) {
	rows, err := selectQuery(q, GetTable(r), query, args)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	rt := reflect.TypeOf(r) // *User
	rsv := reflect.MakeSlice(reflect.SliceOf(rt), 0, 0)

	for rows.Next() {
		r := reflect.New(rt.Elem())
		err = Hydrate(r.Interface(), rows)
		if err != nil {
			return nil, err
		}
		rsv = reflect.Append(rsv, r)
	}
	return rsv.Interface(), nil
}

func isEmpty(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Map, reflect.Slice, reflect.Ptr:
		return v.IsNil()
	case reflect.Struct:
		return v.Interface() == reflect.Zero(v.Type()).Interface()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.String:
		return v.String() == ""
	// no bools: it's stupid to insert them as empty
	default:
		return false
	}
}

func insertRecord(q queryer, record Record) error {
	table := GetTable(record)

	fields := []string{}
	exprs := []string{}
	args := []interface{}{}
	i := 1
	walkTags(record, func(field string, flags tagFlags, value reflect.Value) {
		nullEmpty := flags.NullEmpty || value.Kind() == reflect.Ptr
		if flags.NoWrite || (nullEmpty && isEmpty(value)) {
			return
		}
		fields = append(fields, fmt.Sprintf("\"%s\"", field))
		exprs = append(exprs, fmt.Sprintf("$%d", i))
		args = append(args, value.Interface())
		i++
	})

	rows, err := q.Query(fmt.Sprintf(`insert into "%s" (%s) values (%s) returning *`,
		table, strings.Join(fields, ", "), strings.Join(exprs, ", ")), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	rows.Next() // assert: returns true
	return Hydrate(record, rows)
}

func updateRecord(q queryer, record Record) error {
	exprs := []string{}
	args := []interface{}{GetID(record)}
	i := 2
	walkTags(record, func(field string, flags tagFlags, value reflect.Value) {
		if flags.NoWrite {
			return
		}
		exprs = append(exprs, fmt.Sprintf("\"%s\"=$%d", field, i))
		args = append(args, value.Interface())
		i++
	})
	rows, err := q.Query(fmt.Sprintf(`update "%s" set %s where id=$1 returning *`,
		GetTable(record), strings.Join(exprs, ", ")), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return fmt.Errorf("update failed, did you mean to insert instead? record=%s", GetPKString(record))
	}
	return Hydrate(record, rows)
}

func GetID(r Record) int64 {
	rv := reflect.ValueOf(r).Elem()
	idv := rv.FieldByNameFunc(func(name string) bool {
		return name == "ID"
	})
	if !idv.IsValid() {
		return 0
		//panic(fmt.Errorf("record %q has no ID field", rv.Type()))
	}
	return idv.Int()
}

var tableNames = map[reflect.Type]string{}

func SetTableName(r Record, tableName string) {
	tableNames[reflect.TypeOf(r).Elem()] = tableName
}

func GetTable(r Record) string {
	t := reflect.TypeOf(r)
	// assert t.Kind() == reflect.Ptr
	t = t.Elem()
	// assert t.Kind() == reflect.Struct
	if n, _ := tableNames[t]; n != "" {
		return n
	}
	return strings.ToLower(t.Name()) + "s"
}

func GetPKString(r Record) string {
	return fmt.Sprintf("%s:%d", GetTable(r), GetID(r))
}

func selectQuery(q queryer, table string, query string, args []interface{}) (*sql.Rows, error) {
	if strings.HasPrefix(query, ":") {
		// complete query, including "select * from ..."
		query = query[1:]
	} else {
		query = fmt.Sprintf(`select * from "%s" where %s`, table, query)
	}
	return q.Query(query, args...)
}

type tagFlags struct {
	NullEmpty bool
	NoWrite   bool
}

func stringInSlice(slice []string, needle string) bool {
	for _, s := range slice {
		if s == needle {
			return true
		}
	}
	return false
}

func walkTags(record interface{}, cb func(string, tagFlags, reflect.Value)) {
	// record should be pointer type: *MyRecord

	v := reflect.ValueOf(record).Elem()
	t := v.Type()

	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.Anonymous {
			// recurse
			walkTags(v.Field(i).Addr().Interface(), cb)
		} else if dbTag, ok := f.Tag.Lookup("db"); ok {
			s := strings.Split(dbTag, ",")
			dbTag = s[0]
			f := tagFlags{
				NullEmpty: stringInSlice(s[1:], "nullempty"),
				NoWrite:   stringInSlice(s[1:], "nowrite"),
			}
			cb(dbTag, f, v.Field(i))
		}
	}
}

var hydrateToVoid interface{}

// Hydrate reads the next record in rows into struct Record
func Hydrate(record Record, rows *sql.Rows) error {

	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	values := make(map[string]interface{})
	walkTags(record, func(field string, flags tagFlags, value reflect.Value) {
		values[field] = value.Addr().Interface() // pointer to the field
	})

	scanArgs := make([]interface{}, len(columns))
	for i, field := range columns {
		if ptr, ok := values[field]; ok {
			scanArgs[i] = ptr
		} else {
			scanArgs[i] = &hydrateToVoid
		}
	}

	return rows.Scan(scanArgs...)
}
