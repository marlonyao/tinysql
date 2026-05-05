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

func TestExecutorDelete(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	exec := NewExecutor(tm)

	exec.Execute(MustParse("CREATE TABLE users (id INT, name VARCHAR);"))
	exec.Execute(MustParse("INSERT INTO users (id, name) VALUES (1, 'Alice');"))
	exec.Execute(MustParse("INSERT INTO users (id, name) VALUES (2, 'Bob');"))
	exec.Execute(MustParse("INSERT INTO users (id, name) VALUES (3, 'Charlie');"))

	// DELETE WHERE id = 2
	res, err := exec.Execute(MustParse("DELETE FROM users WHERE id = 2;"))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	del := res.(*DeleteResult)
	if del.RowsAffected != 1 {
		t.Fatalf("expected 1 row deleted, got %d", del.RowsAffected)
	}

	// 验证只剩 2 行
	res, err = exec.Execute(MustParse("SELECT * FROM users;"))
	if err != nil {
		t.Fatalf("select after delete: %v", err)
	}
	sel := res.(*SelectResult)
	if len(sel.Rows) != 2 {
		t.Fatalf("expected 2 rows after delete, got %d", len(sel.Rows))
	}
}

func TestExecutorUpdate(t *testing.T) {
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
	exec.Execute(MustParse("INSERT INTO users (id, name, age) VALUES (1, 'Alice', 25);"))
	exec.Execute(MustParse("INSERT INTO users (id, name, age) VALUES (2, 'Bob', 30);"))

	// UPDATE WHERE id = 1
	res, err := exec.Execute(MustParse("UPDATE users SET name = 'Alicia', age = 26 WHERE id = 1;"))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	upd := res.(*UpdateResult)
	if upd.RowsAffected != 1 {
		t.Fatalf("expected 1 row updated, got %d", upd.RowsAffected)
	}

	// 验证更新结果
	res, err = exec.Execute(MustParse("SELECT * FROM users WHERE id = 1;"))
	if err != nil {
		t.Fatalf("select after update: %v", err)
	}
	sel := res.(*SelectResult)
	if len(sel.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(sel.Rows))
	}
	if sel.Rows[0][1] != "Alicia" || sel.Rows[0][2] != 26 {
		t.Fatalf("unexpected updated values: %v", sel.Rows[0])
	}
}

func TestExecutorDeleteNoWhere(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	exec := NewExecutor(tm)

	exec.Execute(MustParse("CREATE TABLE t (id INT);"))
	exec.Execute(MustParse("INSERT INTO t (id) VALUES (1);"))
	exec.Execute(MustParse("INSERT INTO t (id) VALUES (2);"))

	// DELETE 不带 WHERE = 删除所有
	res, err := exec.Execute(MustParse("DELETE FROM t;"))
	if err != nil {
		t.Fatalf("delete all: %v", err)
	}
	del := res.(*DeleteResult)
	if del.RowsAffected != 2 {
		t.Fatalf("expected 2 rows deleted, got %d", del.RowsAffected)
	}

	res, _ = exec.Execute(MustParse("SELECT * FROM t;"))
	sel := res.(*SelectResult)
	if len(sel.Rows) != 0 {
		t.Fatalf("expected 0 rows after delete all, got %d", len(sel.Rows))
	}
}

func TestExecutorCreateIndex(t *testing.T) {
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
	exec.Execute(MustParse("INSERT INTO users (id, name, age) VALUES (1, 'Alice', 25);"))
	exec.Execute(MustParse("INSERT INTO users (id, name, age) VALUES (2, 'Bob', 30);"))
	exec.Execute(MustParse("INSERT INTO users (id, name, age) VALUES (3, 'Alice', 35);"))

	// CREATE INDEX
	res, err := exec.Execute(MustParse("CREATE INDEX idx_name ON users (name);"))
	if err != nil {
		t.Fatalf("create index: %v", err)
	}
	ci := res.(*CreateIndexResult)
	if ci.Message != "Index idx_name created on users" {
		t.Fatalf("unexpected message: %s", ci.Message)
	}

	// 验证索引存在
	table, _ := tm.GetTable("users")
	if len(table.Indexes) != 1 {
		t.Fatalf("expected 1 index, got %d", len(table.Indexes))
	}
	if table.Indexes[0].Name != "idx_name" {
		t.Fatalf("unexpected index name: %s", table.Indexes[0].Name)
	}

	// 删除后重新加载，验证索引持久化
	pager.Close()
	pager2, _ := storage.NewPager(dbPath)
	defer pager2.Close()
	tm2 := storage.NewTableManager(pager2)
	table2, _ := tm2.GetTable("users")
	if len(table2.Indexes) != 1 {
		t.Fatalf("expected 1 index after reload, got %d", len(table2.Indexes))
	}
}

func TestExecutorDeleteWithIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	exec := NewExecutor(tm)

	exec.Execute(MustParse("CREATE TABLE users (id INT, name VARCHAR);"))
	exec.Execute(MustParse("INSERT INTO users (id, name) VALUES (1, 'Alice');"))
	exec.Execute(MustParse("INSERT INTO users (id, name) VALUES (2, 'Bob');"))
	exec.Execute(MustParse("CREATE INDEX idx_name ON users (name);"))

	// DELETE Alice
	res, err := exec.Execute(MustParse("DELETE FROM users WHERE name = 'Alice';"))
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	del := res.(*DeleteResult)
	if del.RowsAffected != 1 {
		t.Fatalf("expected 1 deleted, got %d", del.RowsAffected)
	}

	// 验证只剩 Bob
	res, _ = exec.Execute(MustParse("SELECT * FROM users;"))
	sel := res.(*SelectResult)
	if len(sel.Rows) != 1 || sel.Rows[0][1] != "Bob" {
		t.Fatalf("expected only Bob, got %v", sel.Rows)
	}
}

func TestExecutorUpdateWithIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	exec := NewExecutor(tm)

	exec.Execute(MustParse("CREATE TABLE users (id INT, name VARCHAR);"))
	exec.Execute(MustParse("INSERT INTO users (id, name) VALUES (1, 'Alice');"))
	exec.Execute(MustParse("CREATE INDEX idx_name ON users (name);"))

	// UPDATE Alice -> Alicia
	res, err := exec.Execute(MustParse("UPDATE users SET name = 'Alicia' WHERE id = 1;"))
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	upd := res.(*UpdateResult)
	if upd.RowsAffected != 1 {
		t.Fatalf("expected 1 updated, got %d", upd.RowsAffected)
	}

	// 验证更新成功
	res, _ = exec.Execute(MustParse("SELECT * FROM users WHERE id = 1;"))
	sel := res.(*SelectResult)
	if len(sel.Rows) != 1 || sel.Rows[0][1] != "Alicia" {
		t.Fatalf("expected Alicia, got %v", sel.Rows)
	}
}

func MustParse(sql string) Statement {
	stmt, err := ParseSQL(sql)
	if err != nil {
		panic(fmt.Sprintf("parse %s: %v", sql, err))
	}
	return stmt
}

func TestExecutorCreateUniqueIndex(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	exec := NewExecutor(tm)

	_, err = exec.Execute(MustParse("CREATE TABLE users (id INT, name VARCHAR(20), email VARCHAR(50))"))
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	_, err = exec.Execute(MustParse("INSERT INTO users VALUES (1, 'Alice', 'alice@example.com')"))
	if err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	_, err = exec.Execute(MustParse("INSERT INTO users VALUES (2, 'Bob', 'bob@example.com')"))
	if err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	_, err = exec.Execute(MustParse("CREATE UNIQUE INDEX idx_email ON users (email)"))
	if err != nil {
		t.Fatalf("create unique index: %v", err)
	}

	_, err = exec.Execute(MustParse("INSERT INTO users VALUES (3, 'Charlie', 'alice@example.com')"))
	if err == nil {
		t.Fatalf("expected unique constraint violation, got nil")
	}
	if err != nil && !contains(err.Error(), "unique constraint violation") {
		t.Fatalf("expected unique constraint error, got: %v", err)
	}

	_, err = exec.Execute(MustParse("INSERT INTO users VALUES (3, 'Charlie', 'charlie@example.com')"))
	if err != nil {
		t.Fatalf("insert 3: %v", err)
	}

	res, err := exec.Execute(MustParse("SELECT * FROM users"))
	if err != nil {
		t.Fatalf("select: %v", err)
	}
	selectRes := res.(*SelectResult)
	if len(selectRes.Rows) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(selectRes.Rows))
	}
}

func TestExecutorUniqueIndexUpdateConflict(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	exec := NewExecutor(tm)

	_, err = exec.Execute(MustParse("CREATE TABLE users (id INT, name VARCHAR(20), email VARCHAR(50))"))
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	_, err = exec.Execute(MustParse("INSERT INTO users VALUES (1, 'Alice', 'alice@example.com')"))
	if err != nil {
		t.Fatalf("insert 1: %v", err)
	}
	_, err = exec.Execute(MustParse("INSERT INTO users VALUES (2, 'Bob', 'bob@example.com')"))
	if err != nil {
		t.Fatalf("insert 2: %v", err)
	}

	_, err = exec.Execute(MustParse("CREATE UNIQUE INDEX idx_email ON users (email)"))
	if err != nil {
		t.Fatalf("create unique index: %v", err)
	}

	_, err = exec.Execute(MustParse("UPDATE users SET email = 'alice@example.com' WHERE name = 'Bob'"))
	if err == nil {
		t.Fatalf("expected unique constraint violation on update, got nil")
	}
	if err != nil && !contains(err.Error(), "unique constraint violation") {
		t.Fatalf("expected unique constraint error, got: %v", err)
	}

	_, err = exec.Execute(MustParse("UPDATE users SET email = 'alice@example.com' WHERE name = 'Alice'"))
	if err != nil {
		t.Fatalf("update same value: %v", err)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsInternal(s, substr))
}

func containsInternal(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

