package storage

import (
	"fmt"
	"path/filepath"
	"testing"
)

func TestBTreeNodeTypes(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	pager, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager: %v", err)
	}
	defer pager.Close()

	bt := NewBTree(pager)

	for i := 0; i < 500; i++ {
		if err := bt.Insert(EncodeIntKey(i), []byte(fmt.Sprintf("v%d", i))); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}

	// 检查 page 1-10 的 nodeType
	for pid := uint32(1); pid <= 10; pid++ {
		page := bt.pager.GetPage(pid)
		na := newNodeAccessor(page)
		nt := na.nodeType()
		if nt == NodeTypeLeaf {
			entries := na.leafEntries()
			if len(entries) > 0 {
				first := DecodeIntKey(entries[0].key)
				last := DecodeIntKey(entries[len(entries)-1].key)
				t.Logf("Page %d: LEAF, keys=[%d..%d] (%d entries), rs=%d", pid, first, last, len(entries), na.rightSibling())
			}
		} else {
			entries := na.internalEntries()
			fc := na.firstChild()
			t.Logf("Page %d: INTERNAL, firstChild=%d, entries=%d", pid, fc, len(entries))
			for _, e := range entries {
				t.Logf("  key=%d child=%d", DecodeIntKey(e.key), e.childPageID)
			}
		}
	}
}
