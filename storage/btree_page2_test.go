package storage

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestBTreePage2Entries(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	bt := NewBTree(pager)

	// 插入 500 条
	for i := 0; i < 500; i++ {
		if err := bt.Insert(encodeIntKey(i), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	// 打印 Page 2 的所有 entries
	page := bt.pager.GetPage(2)
	na := newNodeAccessor(page)
	entries := na.leafEntries()
	t.Logf("Page 2 has %d entries", len(entries))
	for i, e := range entries {
		k := decodeIntKey(e.key)
		v := string(e.value)
		if i < 5 || i > len(entries)-5 {
			t.Logf("  entry[%d]: key=%d, value=%s", i, k, v)
		} else if i == 5 {
			t.Logf("  ... (%d entries) ...", len(entries)-10)
		}
	}

	// 打印 Page 4
	page4 := bt.pager.GetPage(4)
	na4 := newNodeAccessor(page4)
	entries4 := na4.leafEntries()
	t.Logf("Page 4 has %d entries", len(entries4))
	for i, e := range entries4 {
		k := decodeIntKey(e.key)
		v := string(e.value)
		if i < 5 || i > len(entries4)-5 {
			t.Logf("  entry[%d]: key=%d, value=%s", i, k, v)
		} else if i == 5 {
			t.Logf("  ... (%d entries) ...", len(entries4)-10)
		}
	}
}
