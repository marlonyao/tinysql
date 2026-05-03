package tx

import (
	"path/filepath"
	"testing"

	"tinysql/storage"
)

func TestTxCommit(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	walPath := filepath.Join(dir, "test.wal")

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	txm, err := NewTransactionManager(tm, walPath)
	if err != nil {
		t.Fatalf("NewTransactionManager: %v", err)
	}

	// 创建表（非事务）
	if err := tm.CreateTable(&storage.Table{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
			{Name: "name", Type: storage.TypeVarchar},
		},
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// BEGIN → INSERT → COMMIT
	tx, err := txm.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}

	if err := tx.Insert("users", &storage.Row{Values: []interface{}{1, "Alice"}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	if err := txm.Commit(tx); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// 验证数据存在
	rows, err := tm.SelectAll("users")
	if err != nil {
		t.Fatalf("SelectAll: %v", err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after commit, got %d", len(rows))
	}
	if rows[0].Values[0] != 1 || rows[0].Values[1] != "Alice" {
		t.Fatalf("unexpected data: %v", rows[0].Values)
	}
}

func TestTxRollback(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	walPath := filepath.Join(dir, "test.wal")

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	txm, err := NewTransactionManager(tm, walPath)
	if err != nil {
		t.Fatalf("NewTransactionManager: %v", err)
	}

	// 创建表 + 预置 1 行
	tm.CreateTable(&storage.Table{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
			{Name: "name", Type: storage.TypeVarchar},
		},
	})
	tm.Insert("users", &storage.Row{Values: []interface{}{1, "Alice"}})

	// BEGIN → INSERT → ROLLBACK
	tx, _ := txm.Begin()
	tx.Insert("users", &storage.Row{Values: []interface{}{2, "Bob"}})

	if err := txm.Rollback(tx); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// 验证只保留 Alice，Bob 被回滚
	rows, _ := tm.SelectAll("users")
	if len(rows) != 1 {
		t.Fatalf("expected 1 row after rollback, got %d", len(rows))
	}
	if rows[0].Values[1] != "Alice" {
		t.Fatalf("expected Alice, got %v", rows[0].Values[1])
	}
}

func TestTxRecover(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	walPath := filepath.Join(dir, "test.wal")

	// 阶段 1：创建表 + 开始事务 + 插入（但不提交）
	{
		pager, _ := storage.NewPager(dbPath)
		tm := storage.NewTableManager(pager)
		txm, _ := NewTransactionManager(tm, walPath)

		tm.CreateTable(&storage.Table{
			Name: "users",
			Columns: []storage.Column{
				{Name: "id", Type: storage.TypeInt},
				{Name: "name", Type: storage.TypeVarchar},
			},
		})

		tx, _ := txm.Begin()
		tx.Insert("users", &storage.Row{Values: []interface{}{1, "Crash"}})
		// 故意不 COMMIT/ROLLBACK，模拟崩溃
		pager.Close()
	}

	// 阶段 2：重启，恢复
	{
		pager, _ := storage.NewPager(dbPath)
		tm := storage.NewTableManager(pager)
		txm, _ := NewTransactionManager(tm, walPath)

		// 恢复
		if err := txm.Recover(); err != nil {
			t.Fatalf("Recover: %v", err)
		}

		// 验证未提交事务已回滚
		rows, _ := tm.SelectAll("users")
		if len(rows) != 0 {
			t.Fatalf("expected 0 rows after recovery, got %d", len(rows))
		}
	}
}
