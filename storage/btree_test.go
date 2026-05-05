package storage

import (
	"bytes"
	"fmt"
	"path/filepath"
	"testing"
)

func TestBTreeInsertAndSearch(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	bt := NewBTree(pager)

	// 插入几个 key-value
	if err := bt.Insert(EncodeIntKey(10), []byte("ten")); err != nil {
		t.Fatalf("insert 10: %v", err)
	}
	if err := bt.Insert(EncodeIntKey(5), []byte("five")); err != nil {
		t.Fatalf("insert 5: %v", err)
	}
	if err := bt.Insert(EncodeIntKey(20), []byte("twenty")); err != nil {
		t.Fatalf("insert 20: %v", err)
	}

	// 查找
	val, found, err := bt.Search(EncodeIntKey(10))
	if err != nil {
		t.Fatalf("search 10: %v", err)
	}
	if !found || string(val) != "ten" {
		t.Fatalf("search 10: expected 'ten', got '%s' (found=%v)", string(val), found)
	}

	val, found, err = bt.Search(EncodeIntKey(5))
	if err != nil {
		t.Fatalf("search 5: %v", err)
	}
	if !found || string(val) != "five" {
		t.Fatalf("search 5: expected 'five', got '%s'", string(val))
	}

	val, found, err = bt.Search(EncodeIntKey(20))
	if err != nil {
		t.Fatalf("search 20: %v", err)
	}
	if !found || string(val) != "twenty" {
		t.Fatalf("search 20: expected 'twenty', got '%s'", string(val))
	}

	// 查找不存在的 key
	val, found, err = bt.Search(EncodeIntKey(99))
	if err != nil {
		t.Fatalf("search 99: %v", err)
	}
	if found {
		t.Fatalf("search 99: should not be found, got '%s'", string(val))
	}
}

func TestBTreeInsertMany(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	bt := NewBTree(pager)

	// 插入大量 key，触发分裂
	count := 100
	for i := 0; i < count; i++ {
		key := i * 2 // 偶数，避免冲突
		val := fmt.Sprintf("value-%d", key)
		if err := bt.Insert(EncodeIntKey(key), []byte(val)); err != nil {
			t.Fatalf("insert %d: %v", key, err)
		}
	}

	// 验证每个 key 都能查到
	for i := 0; i < count; i++ {
		key := i * 2
		expected := fmt.Sprintf("value-%d", key)
		val, found, err := bt.Search(EncodeIntKey(key))
		if err != nil {
			t.Fatalf("search %d: %v", key, err)
		}
		if !found {
			t.Fatalf("search %d: not found", key)
		}
		if string(val) != expected {
			t.Fatalf("search %d: expected '%s', got '%s'", key, expected, string(val))
		}
	}

	// 验证不存在的 key 查不到
	_, found, err := bt.Search(EncodeIntKey(101))
	if err != nil {
		t.Fatalf("search 101: %v", err)
	}
	if found {
		t.Fatalf("search 101: should not be found")
	}
}

func TestBTreeRangeScan(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	bt := NewBTree(pager)

	// 插入 0, 10, 20, 30, 40, 50
	keys := []int{0, 10, 20, 30, 40, 50}
	for _, k := range keys {
		if err := bt.Insert(EncodeIntKey(k), []byte(fmt.Sprintf("v%d", k))); err != nil {
			t.Fatalf("insert %d: %v", k, err)
		}
	}

	// 范围扫描 [15, 35]
	results, err := bt.RangeScan(EncodeIntKey(15), EncodeIntKey(35))
	if err != nil {
		t.Fatalf("range scan: %v", err)
	}

	// 应该返回 20, 30
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if !bytes.Equal(results[0].Key, EncodeIntKey(20)) || string(results[0].Value) != "v20" {
		t.Fatalf("result[0]: expected {20, v20}, got %v", results[0])
	}
	if !bytes.Equal(results[1].Key, EncodeIntKey(30)) || string(results[1].Value) != "v30" {
		t.Fatalf("result[1]: expected {30, v30}, got %v", results[1])
	}
}

func TestBTreeUpdate(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	bt := NewBTree(pager)

	// 插入
	if err := bt.Insert(EncodeIntKey(1), []byte("old")); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// 更新同一 key
	if err := bt.Insert(EncodeIntKey(1), []byte("new")); err != nil {
		t.Fatalf("update: %v", err)
	}

	val, found, err := bt.Search(EncodeIntKey(1))
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !found || string(val) != "new" {
		t.Fatalf("expected 'new', got '%s'", string(val))
	}
}
