package sqlite // import "modernc.org/sqlite"

import (
	"database/sql"
	"reflect"
	"testing"
	"time"
)

// TestDeclTime pins _decltime: by default the TEXT of a column declared DATE, DATETIME or TIMESTAMP is parsed as a
// time.Time; with _decltime=0 it is the stored string byte for byte, and ColumnTypeScanType reports string for it even
// under _texttotime.
func TestDeclTime(t *testing.T) {
	stored := []struct{ decl, text string }{
		{"DATE", "2026-10-22"},
		{"DATETIME", "2020-01-02 03:04:05.5"},
		{"DATETIME", "2020-01-02 03:04:05.500000"},
		{"TIMESTAMP", "2020-01-02T03:04:05Z"},
		{"DATE", "not a date"},
		{"TEXT", "2026-10-22"},
	}
	for _, tc := range []struct {
		name   string
		query  string
		parsed bool
	}{
		{"default", "", true},
		{"on", "?_decltime=1", true},
		{"off", "?_decltime=0", false},
		{"off under texttotime", "?_decltime=0&_texttotime=1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open(driverName, "file::memory:"+tc.query)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			db.SetMaxOpenConns(1)
			for i, s := range stored {
				if _, err := db.Exec("CREATE TABLE t" + string(rune('a'+i)) + " (v " + s.decl + ")"); err != nil {
					t.Fatal(err)
				}
				table := "t" + string(rune('a'+i))
				if _, err := db.Exec("INSERT INTO "+table+" VALUES (?)", s.text); err != nil {
					t.Fatal(err)
				}
				rows, err := db.Query("SELECT v FROM " + table)
				if err != nil {
					t.Fatal(err)
				}
				types, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if !rows.Next() {
					t.Fatalf("%s %q: no row", s.decl, s.text)
				}
				var got any
				if err := rows.Scan(&got); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				_, isTime := got.(time.Time)
				temporal := s.decl != "TEXT" && s.text != "not a date"
				if want := tc.parsed && temporal; isTime != want {
					t.Errorf("%s %q: read %T %v, want a time %v", s.decl, s.text, got, got, want)
				}
				if !isTime && got != s.text {
					t.Errorf("%s %q: read %q, want the stored text", s.decl, s.text, got)
				}
				if !tc.parsed && types[0].ScanType() != reflect.TypeOf("") {
					t.Errorf("%s %q: scan type %v, want string", s.decl, s.text, types[0].ScanType())
				}
			}
		})
	}
}

func TestDeclTimeBad(t *testing.T) {
	db, err := sql.Open(driverName, "file::memory:?_decltime=foobar")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec("select 1")
	want := `unknown _decltime "foobar", must be 1, t, T, TRUE, true, True, 0, f, F, FALSE, false, False`
	if err == nil || err.Error() != want {
		t.Fatalf("got error %v, want %q", err, want)
	}
}
