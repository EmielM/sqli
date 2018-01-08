package sqli

import (
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Queryer is implemented by both *sql.DB and *sql.Tx
type queryer interface {
	Query(string, ...interface{}) (*sql.Rows, error)
	QueryRow(string, ...interface{}) *sql.Row
	Exec(string, ...interface{}) (sql.Result, error)
}

func getRecord(q queryer, r interface{}, query string, args ...interface{}) error {
	rt := reflect.TypeOf(r)
	if rt.Kind() != reflect.Ptr && rt.Elem().Kind() != reflect.Struct {
		return errors.New("sqli: Get needs a pointer to a struct: *User")
	}

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

func getAllRecords(q queryer, rs interface{}, query string, args ...interface{}) error {
	rt := reflect.TypeOf(rs) // *[]*MyType
	if rt.Kind() != reflect.Ptr || rt.Elem().Kind() != reflect.Slice || rt.Elem().Elem().Kind() != reflect.Ptr || rt.Elem().Elem().Elem().Kind() != reflect.Struct {
		return errors.New("sqli: GetAll needs a pointer to a slice: *[]*MyType")
	}

	rt = rt.Elem().Elem() // *MyType

	rows, err := selectQuery(q, GetTable(reflect.New(rt.Elem()).Interface()), query, args)
	if err != nil {
		return err
	}
	defer rows.Close()

	sv := reflect.MakeSlice(reflect.SliceOf(rt), 0, 0)

	for rows.Next() {
		rv := reflect.New(rt.Elem())
		err = Hydrate(rv.Interface(), rows)
		if err != nil {
			return err
		}
		sv = reflect.Append(sv, rv)
	}
	reflect.ValueOf(rs).Elem().Set(sv)
	return nil
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

func insertRecord(q queryer, r interface{}) error {
	rt := reflect.TypeOf(r)
	if rt.Kind() != reflect.Ptr || rt.Elem().Kind() != reflect.Struct {
		return errors.New("sqli: Insert needs a pointer to a struct: *MyType")
	}

	table := GetTable(r)

	fields := []string{}
	exprs := []string{}
	args := []interface{}{}
	i := 1
	walkTags(r, func(field string, flags tagFlags, value reflect.Value) {
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
	return Hydrate(r, rows)
}

func updateRecord(q queryer, r interface{}) error {
	rt := reflect.TypeOf(r)
	if rt.Kind() != reflect.Ptr || rt.Elem().Kind() != reflect.Struct {
		return errors.New("sqli: Update needs a pointer to a struct: *MyType")
	}

	exprs := []string{}
	args := []interface{}{GetID(r)}
	i := 2
	walkTags(r, func(field string, flags tagFlags, value reflect.Value) {
		if flags.NoWrite {
			return
		}
		exprs = append(exprs, fmt.Sprintf("\"%s\"=$%d", field, i))
		args = append(args, value.Interface())
		i++
	})
	rows, err := q.Query(fmt.Sprintf(`update "%s" set %s where id=$1 returning *`,
		GetTable(r), strings.Join(exprs, ", ")), args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	if !rows.Next() {
		return fmt.Errorf("sqli: update failed, did you mean to insert instead? record=%s", GetPKString(r))
	}
	return Hydrate(r, rows)
}

func GetID(r interface{}) int64 {
	rv := reflect.ValueOf(r)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Struct {
		return 0
	}
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

func SetTableName(r interface{}, tableName string) {
	rt := reflect.TypeOf(r)
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}
	tableNames[rt] = tableName
}

func GetTable(r interface{}) string {
	rt := reflect.TypeOf(r)
	if rt.Kind() == reflect.Ptr {
		rt = rt.Elem()
	}
	if n, _ := tableNames[rt]; n != "" {
		return n
	}
	if rt.Kind() == reflect.Struct {
		return strings.ToLower(rt.Name()) + "s"
	}
	return ""
}

func GetPKString(r interface{}) string {
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

func walkTags(r interface{}, cb func(string, tagFlags, reflect.Value)) {
	// r should be pointer type: *MyRecord

	v := reflect.ValueOf(r).Elem()
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
func Hydrate(r interface{}, rows *sql.Rows) error {

	rt := reflect.TypeOf(r)
	if rt.Kind() != reflect.Ptr || rt.Elem().Kind() != reflect.Struct {
		return errors.New("sqli: Hydrate needs a pointer to a struct: *MyType")
	}

	columns, err := rows.Columns()
	if err != nil {
		return err
	}

	values := make(map[string]interface{})
	walkTags(r, func(field string, flags tagFlags, value reflect.Value) {
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
