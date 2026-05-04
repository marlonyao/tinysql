package storage

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestBTreeStructure(t *testing.T) {
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

	// 打印叶子节点链表
	t.Log("Leaf node chain via rightSibling:")
	pageID := bt.rootPageID
	for {
		page := bt.pager.GetPage(pageID)
		na := newNodeAccessor(page)
		if na.nodeType() == NodeTypeLeaf {
			break
		}
		pageID = na.firstChild()
	}

	count := 0
	for pageID != 0 {
		page := bt.pager.GetPage(pageID)
		na := newNodeAccessor(page)
		entries := na.leafEntries()
		if len(entries) > 0 {
			firstKey := decodeIntKey(entries[0].key)
			lastKey := decodeIntKey(entries[len(entries)-1].key)
			t.Logf("Page %d: keys %d..%d (entries=%d, right=%d)", pageID, firstKey, lastKey, len(entries), na.rightSibling())
		} else {
			t.Logf("Page %d: EMPTY (right=%d)", pageID, na.rightSibling())
		}
		count += len(entries)
		pageID = na.rightSibling()
		if pageID > 0 && count > 600 {
			t.Fatalf("possible cycle, breaking")
		}
	}
	t.Logf("Total entries in leaf chain: %d", count)
}
