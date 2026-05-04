package storage

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestBTreeInternalStructure(t *testing.T) {
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

	// 打印所有 page 的结构
	for pid := uint32(1); pid <= 10; pid++ {
		page := bt.pager.GetPage(pid)
		na := newNodeAccessor(page)
		t.Logf("Page %d: type=%d, numKeys=%d, isRoot=%v, parent=%d, left=%d, right=%d, firstChild=%d",
			pid, na.nodeType(), na.numKeys(), na.isRoot(), na.parent(), na.leftSibling(), na.rightSibling(), na.firstChild())
		if na.nodeType() == NodeTypeInternal {
			entries := na.internalEntries()
			for i, e := range entries {
				t.Logf("  entry[%d]: key=%d, child=%d", i, decodeIntKey(e.key), e.childPageID)
			}
		}
	}
}
