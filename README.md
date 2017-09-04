# sqli: database/sql improved

`sqli` is a wrapper around `database/sql` with two extras:
- Retryable transaction callbacks
- S/R mapping

## Retryable transaction callbacks
Transaction logic is supplied as a callback function to `db.Do`. The callback should ensure it has no side effects and can be re-executed if the transaction failed because of serializability errors. Non-retryable errors are directly propagated as the return value of `db.Do`.

```golang
var db = sqli.Open(...)

func GetTimeline(userID int) error {
	var data []string
	err := db.Do(func(tx *db.sqli) {
		tx.Exec(`update users set last_access=$2 where user_id=$1`, tx.Now, userID)
		rows := tx.Query("select thing from timeline
			order by id desc limit 250
			where user_id=$1", useRID)

		data = []string{}
		for rows.Next() {
			var thing string
			rows.Scan(&thing)
			data = data.append(thing)
		}
	})
	if err != nil {
		// err might indicate QueryRow failed, Exec failed, sqli.ErrTooMuchAttempts
		return err
	}
	return data
}
```

In case of errors, or calls to `tx.AbortNow()`, the callback's stack is immediately unwound using go's panic mechanism. As long as the callback is indeed side-effect free, this pattern fits the atomicity of database transactions.

## S/R (struct/relational) mapping

Using four simple abstractions that work on `sqli.Record` (interface holding a struct pointer) types: `Get(record, query...)`, `GetAll(singleRecord, query...)`, `Update(record)` and `Insert(record)`. Structs passed should use `db` tags to define their database schema. An `id` field should always be present as primary key.

```
struct User {
	ID int32 `db:"id"`
	Money int32 `db:"money"`
}

var errNoSuchUser = errors.New("no such user")

func UserGiveMoney(id int32) error {
	return db.Do(func(tx *db.sqli) {
		user := tx.Get(new(User), `id=$1`, id).(*User)
		if user == nil {
			tx.AbortNow(errNoSuchUser, false)
		}
		user.Money += 100
		tx.Update(user)
	});
}
```

Table names are derived from the struct's name and appending "s", but can be overridden by calling `SetTableName(record, sqlName)`.

## Work in progress

This code is used in a production system that has a lot of postgres interaction, and `sqli` feels like a productive (and simple!) abstraction to use.

There are some things that might be improved in the future. Notably:

- Documentation
- Tests
- Have a good long look at retry semantics of postgres and see if we're retrying all possible serialization errors.
- Support the `context` package, even though I personally don't like it. At least `db.Do` should probably have a `DoContext` variant.
- `QueryRow` is currently not implemented because that API delays passing down the error until `Scan()`. We would need to implement a custom `sql.Result` to make it work.
- Have a better look at (recursively) embedded structs for records. We should follow `encoding/json` semantics.

# License

(C) 2017 Staying BV

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

