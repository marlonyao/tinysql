package storage

import (
	"path/filepath"
	"testing"
)

func TestTableManagerCreateAndInsert(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// 创建 Pager 和 TableManager
	pager, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager failed: %v", err)
	}

	tm := NewTableManager(pager)

	// 创建表
	table := &Table{
		Name: "users",
		Columns: []Column{
			{Name: "id", Type: TypeInt, Primary: true},
			{Name: "name", Type: TypeVarchar, Length: 50},
			{Name: "age", Type: TypeInt},
		},
	}

	if err := tm.CreateTable(table); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 插入几行
	rows := []*Row{
		{Values: []interface{}{1, "alice", 30}},
		{Values: []interface{}{2, "bob", 25}},
		{Values: []interface{}{3, "charlie", 35}},
	}

	for _, row := range rows {
		if err := tm.Insert("users", row); err != nil {
			t.Fatalf("Insert failed: %v", err)
		}
	}

	// 查询所有行
	result, err := tm.SelectAll("users")
	if err != nil {
		t.Fatalf("SelectAll failed: %v", err)
	}
	if len(result) != 3 {
		t.Fatalf("expected 3 rows, got %d", len(result))
	}

	// 验证数据
	if result[0].Values[0].(int) != 1 || result[0].Values[1].(string) != "alice" || result[0].Values[2].(int) != 30 {
		t.Fatalf("row 0 mismatch: %v", result[0].Values)
	}
	if result[1].Values[0].(int) != 2 || result[1].Values[1].(string) != "bob" || result[1].Values[2].(int) != 25 {
		t.Fatalf("row 1 mismatch: %v", result[1].Values)
	}
	if result[2].Values[0].(int) != 3 || result[2].Values[1].(string) != "charlie" || result[2].Values[2].(int) != 35 {
		t.Fatalf("row 2 mismatch: %v", result[2].Values)
	}

	// 关闭
	if err := pager.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}
}

func TestTableManagerRecovery(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// === 第一轮：创建 + 写入 ===
	pager1, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager failed: %v", err)
	}

	tm1 := NewTableManager(pager1)
	
	table := &Table{
		Name: "users",
		Columns: []Column{
			{Name: "id", Type: TypeInt},
			{Name: "name", Type: TypeVarchar, Length: 50},
		},
	}
	
	if err := tm1.CreateTable(table); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}
	
	if err := tm1.Insert("users", &Row{Values: []interface{}{42, "alice"}}); err != nil {
		t.Fatalf("Insert failed: %v", err)
	}
	
	if err := pager1.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// === 第二轮：重启恢复 ===
	pager2, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	defer pager2.Close()

	tm2 := NewTableManager(pager2)
	if err := tm2.LoadTables(); err != nil {
		t.Fatalf("LoadTables failed: %v", err)
	}

	// 查询恢复的数据
	result, err := tm2.SelectAll("users")
	if err != nil {
		t.Fatalf("SelectAll failed: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 row after recovery, got %d", len(result))
	}
	if result[0].Values[0].(int) != 42 || result[0].Values[1].(string) != "alice" {
		t.Fatalf("recovered row mismatch: %v", result[0].Values)
	}
}

func TestTableManagerMultiPage(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager failed: %v", err)
	}
	defer pager.Close()

	tm := NewTableManager(pager)

	// 创建表（小行，方便触发多页）
	table := &Table{
		Name: "numbers",
		Columns: []Column{
			{Name: "n", Type: TypeInt},
		},
	}
	if err := tm.CreateTable(table); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 插入大量行，触发页分裂
	// 每行大约 4(null bitmap) + 4(int) = 8 bytes，加 2B 前缀 = 10 bytes
	// 一页可用约 4080 - 32 = 4048 bytes，slot directory 每项 2B
	// 大约能存 300+ 行
	count := 500
	for i := 0; i < count; i++ {
		if err := tm.Insert("numbers", &Row{Values: []interface{}{i}}); err != nil {
			t.Fatalf("Insert failed at %d: %v", i, err)
		}
	}

	// 查询验证
	result, err := tm.SelectAll("numbers")
	if err != nil {
		t.Fatalf("SelectAll failed: %v", err)
	}
	if len(result) != count {
		t.Fatalf("expected %d rows, got %d", count, len(result))
	}

	// 验证值连续
	for i := 0; i < count; i++ {
		if result[i].Values[0].(int) != i {
			t.Fatalf("row %d: expected %d, got %v", i, i, result[i].Values[0])
		}
	}

	// 验证使用了多页
	firstPage := tm.getFirstDataPage("numbers")
	if firstPage == 0 {
		t.Fatalf("no data page")
	}
	page := pager.GetPage(firstPage)
	nextPageID := getNextPageID(page.data)
	if nextPageID == 0 {
		t.Fatalf("expected multiple pages, got single page")
	}
}

func TestTableManagerDuplicateTable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager failed: %v", err)
	}
	defer pager.Close()

	tm := NewTableManager(pager)

	table := &Table{Name: "test", Columns: []Column{{Name: "id", Type: TypeInt}}}
	if err := tm.CreateTable(table); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	// 重复创建应该失败
	if err := tm.CreateTable(table); err == nil {
		t.Fatalf("expected error for duplicate table, got nil")
	}
}
