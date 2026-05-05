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

func TestMVCCReadView(t *testing.T) {
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

	// 创建表
	if err := tm.CreateTable(&storage.Table{
		Name: "users",
		Columns: []storage.Column{
			{Name: "id", Type: storage.TypeInt},
			{Name: "name", Type: storage.TypeVarchar},
		},
	}); err != nil {
		t.Fatalf("CreateTable: %v", err)
	}

	// 插入初始数据（TrxID=0 表示旧数据，对任何 ReadView 可见）
	if err := tm.Insert("users", &storage.Row{Values: []interface{}{1, "Alice"}}); err != nil {
		t.Fatalf("Insert: %v", err)
	}

	// 事务 T1 开始，创建 ReadView
	tx1, err := txm.Begin()
	if err != nil {
		t.Fatalf("Begin: %v", err)
	}
	readView := tx1.GetReadView(txm)

	// T1 第一次读取，应看到 Alice
	rows1, _, err := tm.SelectAllWithReadView("users", readView)
	if err != nil {
		t.Fatalf("SelectAllWithReadView: %v", err)
	}
	if len(rows1) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows1))
	}
	if rows1[0].Values[1] != "Alice" {
		t.Fatalf("expected Alice, got %v", rows1[0].Values[1])
	}

	// 模拟另一个事务 T2 修改这行
	tx2, err := txm.Begin()
	if err != nil {
		t.Fatalf("Begin tx2: %v", err)
	}

	// tx2 UPDATE: Alice → Bob
	allRows, allIDs, _ := tm.SelectAllWithRowID("users")
	if len(allRows) != 1 {
		t.Fatalf("expected 1 row for update, got %d", len(allRows))
	}
	rowID := allIDs[0]
	row := allRows[0]

	// 写入 undo log（保存旧值 Alice）
	oldValues := []interface{}{row.Values[0], row.Values[1]}
	undoID, err := tm.WriteUndoRecord(tx2.ID, "users", rowID, oldValues, row.TrxID, row.RollPtr, 2)
	if err != nil {
		t.Fatalf("WriteUndoRecord: %v", err)
	}

	// 修改行值
	row.Values[1] = "Bob"
	row.TrxID = tx2.ID
	row.RollPtr = undoID

	// 覆盖写回聚簇索引
	usersTable, ok := tm.GetTable("users")
	if !ok {
		t.Fatalf("users table not found")
	}
	bt := storage.LoadBTree(pager, usersTable.RootPageID)
	rowData, _ := usersTable.SerializeRow(row)
	bt.Insert(storage.EncodeIntKey(rowID), rowData)

	// T2 未提交！

	// T1 再次用同一个 ReadView 读取，应该仍然看到 Alice（通过 undo 链回溯）
	rows2, _, err := tm.SelectAllWithReadView("users", readView)
	if err != nil {
		t.Fatalf("SelectAllWithReadView 2nd: %v", err)
	}
	if len(rows2) != 1 {
		t.Fatalf("expected 1 row on 2nd read, got %d", len(rows2))
	}
	if rows2[0].Values[1] != "Alice" {
		t.Fatalf("T1 should still see Alice (undo chain), got %v", rows2[0].Values[1])
	}

	// T2 提交后，T1 的新 ReadView 应该看到 Bob（Read Committed 语义）
	if err := txm.Commit(tx2); err != nil {
		t.Fatalf("Commit tx2: %v", err)
	}

	// T1 创建新的 ReadView（强制新快照）
	readViewNew := txm.NewReadView(tx1.ID)
	rows3, _, err := tm.SelectAllWithReadView("users", readViewNew)
	if err != nil {
		t.Fatalf("SelectAllWithReadView 3rd: %v", err)
	}
	if len(rows3) != 1 {
		t.Fatalf("expected 1 row on 3rd read, got %d", len(rows3))
	}
	if rows3[0].Values[1] != "Bob" {
		t.Fatalf("after T2 commit, T1 should see Bob, got %v", rows3[0].Values[1])
	}

	t.Logf("MVCC test passed: T1 sees Alice before T2 commit, Bob after")
}
