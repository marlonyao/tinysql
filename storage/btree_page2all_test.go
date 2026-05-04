package storage

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestBTreePage2AllEntries(t *testing.T) {
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

	// 打印 Page 2 的所有 entries（不省略）
	page := bt.pager.GetPage(2)
	na := newNodeAccessor(page)
	entries := na.leafEntries()
	for i, e := range entries {
		k := decodeIntKey(e.key)
		// 只打印不连续的
		if i > 0 {
			prev := decodeIntKey(entries[i-1].key)
			if k != prev+1 {
				t.Logf("  GAP! entry[%d]: key=%d (prev=%d, diff=%d)", i, k, prev, k-prev)
			}
		}
		if i < 10 || i > len(entries)-10 {
			t.Logf("  entry[%d]: key=%d", i, k)
		}
	}
}
