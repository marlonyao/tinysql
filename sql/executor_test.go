package sql

import (
	"fmt"
	"path/filepath"
	"testing"

	"tinysql/storage"
)

func TestExecutorCreateAndInsert(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	exec := NewExecutor(tm)

	// CREATE TABLE
	stmt, _ := ParseSQL("CREATE TABLE users (id INT, name VARCHAR, age INT);")
	res, err := exec.Execute(stmt)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	msg := res.(*CreateTableResult)
	if msg.Message != "Table users created" {
		t.Fatalf("unexpected message: %s", msg.Message)
	}

	// INSERT
	stmt, _ = ParseSQL("INSERT INTO users (id, name, age) VALUES (1, 'Alice', 25);")
	res, err = exec.Execute(stmt)
	if err != nil {
		t.Fatalf("insert: %v", err)
	}
	ins := res.(*InsertResult)
	if ins.RowsAffected != 1 {
		t.Fatalf("expected 1 row affected, got %d", ins.RowsAffected)
	}

	// SELECT *
	stmt, _ = ParseSQL("SELECT * FROM users;")
	res, err = exec.Execute(stmt)
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	sel := res.(*SelectResult)
	if len(sel.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(sel.Rows))
	}
	if sel.Rows[0][0] != 1 || sel.Rows[0][1] != "Alice" || sel.Rows[0][2] != 25 {
		t.Fatalf("unexpected row: %v", sel.Rows[0])
	}
}

func TestExecutorWhereClause(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	exec := NewExecutor(tm)

	// CREATE + INSERT 3 rows
	exec.Execute(MustParse("CREATE TABLE nums (id INT, val INT);"))
	exec.Execute(MustParse("INSERT INTO nums (id, val) VALUES (1, 10);"))
	exec.Execute(MustParse("INSERT INTO nums (id, val) VALUES (2, 20);"))
	exec.Execute(MustParse("INSERT INTO nums (id, val) VALUES (3, 30);"))

	// SELECT WHERE val > 15
	res, err := exec.Execute(MustParse("SELECT * FROM nums WHERE val > 15;"))
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	sel := res.(*SelectResult)
	if len(sel.Rows) != 2 {
		t.Fatalf("expected 2 rows (val>15), got %d", len(sel.Rows))
	}

	// SELECT WHERE id = 2
	res, err = exec.Execute(MustParse("SELECT * FROM nums WHERE id = 2;"))
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	sel = res.(*SelectResult)
	if len(sel.Rows) != 1 {
		t.Fatalf("expected 1 row (id=2), got %d", len(sel.Rows))
	}
	if sel.Rows[0][0] != 2 {
		t.Fatalf("expected id=2, got %v", sel.Rows[0][0])
	}
}

func TestExecutorSelectColumns(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	exec := NewExecutor(tm)

	exec.Execute(MustParse("CREATE TABLE users (id INT, name VARCHAR, age INT);"))
	exec.Execute(MustParse("INSERT INTO users (id, name, age) VALUES (1, 'Bob', 30);"))

	// SELECT name, age
	res, err := exec.Execute(MustParse("SELECT name, age FROM users;"))
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	sel := res.(*SelectResult)
	if len(sel.Columns) != 2 || sel.Columns[0] != "name" || sel.Columns[1] != "age" {
		t.Fatalf("unexpected columns: %v", sel.Columns)
	}
	if len(sel.Rows) != 1 || len(sel.Rows[0]) != 2 {
		t.Fatalf("unexpected row shape")
	}
	if sel.Rows[0][0] != "Bob" || sel.Rows[0][1] != 30 {
		t.Fatalf("unexpected values: %v", sel.Rows[0])
	}
}

func TestExecutorNullValue(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	exec := NewExecutor(tm)

	exec.Execute(MustParse("CREATE TABLE t (id INT, name VARCHAR);"))
	exec.Execute(MustParse("INSERT INTO t (id, name) VALUES (1, NULL);"))

	res, err := exec.Execute(MustParse("SELECT * FROM t;"))
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	sel := res.(*SelectResult)
	if len(sel.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(sel.Rows))
	}
	if sel.Rows[0][1] != nil {
		t.Fatalf("expected NULL, got %v", sel.Rows[0][1])
	}
}

func MustParse(sql string) Statement {
	stmt, err := ParseSQL(sql)
	if err != nil {
		panic(fmt.Sprintf("parse %s: %v", sql, err))
	}
	return stmt
}
