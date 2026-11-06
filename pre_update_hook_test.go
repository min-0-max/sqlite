package sqlite_test

import (
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/min-0-max/sqlite"
	sqlite3 "github.com/min-0-max/sqlite/lib"
)

func TestPreUpdateHook(t *testing.T) {
	connStr := ":memory:"
	driverName := "sqlite_pre_update_hook_test"

	var (
		insertCount     int
		insertNewValues []any
		updateCount     int
		updateOldValues []any
		updateNewValues []any
		deleteCount     int
		deleteOldValues []any
		commitCount     int
		rollbackCount   int
	)
	var testDriver sqlite.Driver
	testDriver.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
		if hooker, ok := conn.(sqlite.HookRegisterer); ok {
			hooker.RegisterPreUpdateHook(func(data sqlite.SQLitePreUpdateData) {
				switch data.Op {
				case sqlite3.SQLITE_INSERT:
					insertCount++
					insertNewValues = make([]any, data.Count())
					err := data.New(insertNewValues...)
					if err != nil {
						t.Fatal(err)
					}
				case sqlite3.SQLITE_UPDATE:
					updateCount++
					updateOldValues = make([]any, data.Count())
					err := data.Old(updateOldValues...)
					if err != nil {
						t.Fatal(err)
					}
					updateNewValues = make([]any, data.Count())
					err = data.New(updateNewValues...)
					if err != nil {
						t.Fatal(err)
					}
				case sqlite3.SQLITE_DELETE:
					deleteCount++
					deleteOldValues = make([]any, data.Count())
					err := data.Old(deleteOldValues...)
					if err != nil {
						t.Fatal(err)
					}
				}
			})
			hooker.RegisterCommitHook(func() int32 {
				commitCount++
				return 0
			})
			hooker.RegisterRollbackHook(func() {
				rollbackCount++
			})
		}
		return nil
	})

	sql.Register(driverName, &testDriver)

	db, err := sql.Open(driverName, connStr)
	if err != nil {
		t.Fatal(err)
	}

	expectInsertValues := []any{int64(42), 3.1415, "Test", "will be nil"}
	expectUpdateValues := []any{int64(43), 1.5, "Test update", nil}
	_, err = db.Exec(`
	CREATE TABLE in_memory_test(id INTEGER PRIMARY KEY, f FLOAT, t TEXT, x ANY);
	INSERT INTO in_memory_test VALUES(42, 3.1415, 'Test', 'will be nil');
	UPDATE in_memory_test SET id = 43, f = 1.5, t = 'Test update', x = null;
	DELETE FROM in_memory_test;
	SELECT 1;
	`)
	if err != nil {
		t.Fatal(err)
	}
	if insertCount != 1 {
		t.Errorf("pre update hook: expect %d inserts call, got %d", 1, insertCount)
	}
	if !slices.Equal(insertNewValues, expectInsertValues) {
		t.Errorf("pre update hook: expect %v as inserted new values, got %v", expectInsertValues, insertNewValues)
	}
	if updateCount != 1 {
		t.Errorf("pre update hook: expect %d updates call, got %d", 1, updateCount)
	}
	if !slices.Equal(updateOldValues, expectInsertValues) {
		t.Errorf("pre update hook: expect %v as updated old values, got %v", expectInsertValues, updateOldValues)
	}
	if !slices.Equal(updateNewValues, expectUpdateValues) {
		t.Errorf("pre update hook: expect %v as updated new values, got %v", expectUpdateValues, updateNewValues)
	}
	if deleteCount != 1 {
		t.Errorf("pre update hook: expect %d deletes call, got %d", 1, deleteCount)
	}
	if !slices.Equal(deleteOldValues, expectUpdateValues) {
		t.Errorf("pre update hook: expect %v as deleted old values, got %v", expectUpdateValues, deleteOldValues)
	}
	if commitCount != 4 {
		t.Errorf("commit hook: expect %d, got %d", 4, commitCount)
	}
	if rollbackCount != 0 {
		t.Errorf("rollback hook: expect %d, got %d", 0, rollbackCount)
	}

}

func TestPreUpdateHookLargeRowID(t *testing.T) {
	driverName := fmt.Sprintf("sqlite_preupdate_large_rowid_%p", t)
	var gotOldRowID, gotNewRowID int64
	var gotOp int32

	var testDriver sqlite.Driver
	testDriver.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
		if hooker, ok := conn.(sqlite.HookRegisterer); ok {
			hooker.RegisterPreUpdateHook(func(data sqlite.SQLitePreUpdateData) {
				gotOp = data.Op
				gotOldRowID = data.OldRowID
				gotNewRowID = data.NewRowID
			})
		}
		return nil
	})
	sql.Register(driverName, &testDriver)

	db, err := sql.Open(driverName, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE t(id INTEGER PRIMARY KEY, v TEXT)`)
	if err != nil {
		t.Fatal(err)
	}

	const largeRowID = int64(1) << 40 // 1 TiB — well beyond int32 range
	_, err = db.Exec(`INSERT INTO t VALUES(?, 'hello')`, largeRowID)
	if err != nil {
		t.Fatal(err)
	}

	if gotOp != sqlite3.SQLITE_INSERT {
		t.Fatalf("expected INSERT op, got %d", gotOp)
	}
	if gotNewRowID != largeRowID {
		t.Errorf("NewRowID: got %d, want %d", gotNewRowID, largeRowID)
	}

	// Also test UPDATE with large rowids
	const newLargeRowID = int64(1)<<40 + 1
	gotOldRowID = 0
	gotNewRowID = 0
	_, err = db.Exec(`UPDATE t SET id = ?, v = 'updated' WHERE id = ?`, newLargeRowID, largeRowID)
	if err != nil {
		t.Fatal(err)
	}

	if gotOp != sqlite3.SQLITE_UPDATE {
		t.Fatalf("expected UPDATE op, got %d", gotOp)
	}
	if gotOldRowID != largeRowID {
		t.Errorf("OldRowID after UPDATE: got %d, want %d", gotOldRowID, largeRowID)
	}
	if gotNewRowID != newLargeRowID {
		t.Errorf("NewRowID after UPDATE: got %d, want %d", gotNewRowID, newLargeRowID)
	}
}

// TestPreUpdateHookTextByLength pins that the old and new values of a text the pre-update hook reports are read by
// their length, so a text holding a NUL, an empty text and a text of many bytes come whole.
func TestPreUpdateHookTextByLength(t *testing.T) {
	texts := []string{"a\x00b", "\x00", "", "plain", strings.Repeat("x\x00", 4096)}
	var olds, news []any
	var testDriver sqlite.Driver
	testDriver.RegisterConnectionHook(func(conn sqlite.ExecQuerierContext, dsn string) error {
		hooker, ok := conn.(sqlite.HookRegisterer)
		if !ok {
			t.Fatal("the connection registers no hook")
		}
		hooker.RegisterPreUpdateHook(func(data sqlite.SQLitePreUpdateData) {
			if data.Op != sqlite3.SQLITE_UPDATE {
				return
			}
			old, new := make([]any, data.Count()), make([]any, data.Count())
			if err := data.Old(old...); err != nil {
				t.Error(err)
			}
			if err := data.New(new...); err != nil {
				t.Error(err)
			}
			olds, news = append(olds, old[0]), append(news, new[0])
		})
		return nil
	})
	sql.Register("sqlite_pre_update_hook_text_test", &testDriver)
	db, err := sql.Open("sqlite_pre_update_hook_text_test", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("CREATE TABLE t (v TEXT)"); err != nil {
		t.Fatal(err)
	}
	for _, text := range texts {
		olds, news = nil, nil
		if _, err := db.Exec("DELETE FROM t"); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("INSERT INTO t (v) VALUES (?)", text); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec("UPDATE t SET v = v || ?", text); err != nil {
			t.Fatal(err)
		}
		if len(olds) != 1 || len(news) != 1 {
			t.Fatalf("the update of a text of %d bytes reports %d old and %d new values", len(text), len(olds), len(news))
		}
		if olds[0] != text || news[0] != text+text {
			old, _ := olds[0].(string)
			new, _ := news[0].(string)
			t.Errorf("the update of a text of %d bytes reports an old text of %d bytes and a new one of %d, want %d", len(text), len(old), len(new), 2*len(text))
		}
	}
}
