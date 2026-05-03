package storage

import (
	"encoding/binary"
	"fmt"
	"sort"
)

const (
	NodeTypeLeaf     = 1
	NodeTypeInternal = 2
)

// B+Tree Node Header 在 page.Data() 中的偏移
const (
	offNumKeys      = 0
	offNodeType     = 2
	offRightSibling = 3
	offLeftSibling  = 7
	offParent       = 11
	offIsRoot       = 15
	offFirstChild   = 16 // 内部节点：最左孩子页ID
	offFreeOffset   = 16 // 叶子节点：空闲区起始（与 offFirstChild 复用）

	NodeHeaderSize = 20
)

// KVPair key-value 对
type KVPair struct {
	Key   int
	Value []byte
}

// BTree 基于 Page 的 B+Tree
type BTree struct {
	pager      *Pager
	rootPageID uint32
}

// nodeAccessor 封装对 B+Tree 节点页的操作
type nodeAccessor struct {
	page *Page
	data []byte
}

func newNodeAccessor(page *Page) *nodeAccessor {
	return &nodeAccessor{
		page: page,
		data: page.Data(),
	}
}

// === Node Header 读写 ===

func (na *nodeAccessor) numKeys() int {
	return int(binary.LittleEndian.Uint16(na.data[offNumKeys : offNumKeys+2]))
}
func (na *nodeAccessor) setNumKeys(v int) {
	binary.LittleEndian.PutUint16(na.data[offNumKeys:offNumKeys+2], uint16(v))
}
func (na *nodeAccessor) nodeType() byte     { return na.data[offNodeType] }
func (na *nodeAccessor) setNodeType(t byte) { na.data[offNodeType] = t }
func (na *nodeAccessor) rightSibling() uint32 {
	return binary.LittleEndian.Uint32(na.data[offRightSibling : offRightSibling+4])
}
func (na *nodeAccessor) setRightSibling(v uint32) {
	binary.LittleEndian.PutUint32(na.data[offRightSibling:offRightSibling+4], v)
}
func (na *nodeAccessor) leftSibling() uint32 {
	return binary.LittleEndian.Uint32(na.data[offLeftSibling : offLeftSibling+4])
}
func (na *nodeAccessor) setLeftSibling(v uint32) {
	binary.LittleEndian.PutUint32(na.data[offLeftSibling:offLeftSibling+4], v)
}
func (na *nodeAccessor) parent() uint32 {
	return binary.LittleEndian.Uint32(na.data[offParent : offParent+4])
}
func (na *nodeAccessor) setParent(v uint32) {
	binary.LittleEndian.PutUint32(na.data[offParent:offParent+4], v)
}
func (na *nodeAccessor) isRoot() bool       { return na.data[offIsRoot] != 0 }
func (na *nodeAccessor) setIsRoot(v bool) {
	if v {
		na.data[offIsRoot] = 1
	} else {
		na.data[offIsRoot] = 0
	}
}
func (na *nodeAccessor) firstChild() uint32 {
	return binary.LittleEndian.Uint32(na.data[offFirstChild : offFirstChild+4])
}
func (na *nodeAccessor) setFirstChild(v uint32) {
	binary.LittleEndian.PutUint32(na.data[offFirstChild:offFirstChild+4], v)
}
func (na *nodeAccessor) freeOffset() uint16 {
	return binary.LittleEndian.Uint16(na.data[offFreeOffset : offFreeOffset+2])
}
func (na *nodeAccessor) setFreeOffset(v uint16) {
	binary.LittleEndian.PutUint16(na.data[offFreeOffset:offFreeOffset+2], v)
}

// === 节点初始化 ===

func initLeafNode(page *Page) {
	na := newNodeAccessor(page)
	na.setNumKeys(0)
	na.setNodeType(NodeTypeLeaf)
	na.setRightSibling(0)
	na.setLeftSibling(0)
	na.setParent(0)
	na.setIsRoot(true)
	na.setFreeOffset(NodeHeaderSize)
}

func initInternalNode(page *Page, isRoot bool) {
	na := newNodeAccessor(page)
	na.setNumKeys(0)
	na.setNodeType(NodeTypeInternal)
	na.setRightSibling(0)
	na.setLeftSibling(0)
	na.setParent(0)
	na.setIsRoot(isRoot)
	na.setFirstChild(0)
}

// === 叶子节点 Entry 操作 ===

type leafEntry struct {
	key   int
	value []byte
}

// leafEntries 读取叶子节点所有 entry
func (na *nodeAccessor) leafEntries() []leafEntry {
	n := na.numKeys()
	entries := make([]leafEntry, 0, n)
	offset := NodeHeaderSize
	for i := 0; i < n; i++ {
		key := int(binary.LittleEndian.Uint32(na.data[offset : offset+4]))
		offset += 4
		valLen := int(binary.LittleEndian.Uint16(na.data[offset : offset+2]))
		offset += 2
		value := make([]byte, valLen)
		copy(value, na.data[offset:offset+valLen])
		offset += valLen
		entries = append(entries, leafEntry{key: key, value: value})
	}
	return entries
}

// setLeafEntries 覆盖写入所有 entry
func (na *nodeAccessor) setLeafEntries(entries []leafEntry) {
	na.setNumKeys(len(entries))
	offset := NodeHeaderSize
	for _, e := range entries {
		binary.LittleEndian.PutUint32(na.data[offset:offset+4], uint32(e.key))
		offset += 4
		binary.LittleEndian.PutUint16(na.data[offset:offset+2], uint16(len(e.value)))
		offset += 2
		copy(na.data[offset:], e.value)
		offset += len(e.value)
	}
	na.setFreeOffset(uint16(offset))
}

// tryInsertLeafEntry 尝试插入/替换叶子 entry。返回 false 表示页满
func (na *nodeAccessor) tryInsertLeafEntry(key int, value []byte) bool {
	entries := na.leafEntries()

	// 二分查找位置
	idx := sort.Search(len(entries), func(i int) bool {
		return entries[i].key >= key
	})

	if idx < len(entries) && entries[idx].key == key {
		entries[idx].value = value // 替换
	} else {
		// 插入
		entries = append(entries, leafEntry{})
		copy(entries[idx+1:], entries[idx:])
		entries[idx] = leafEntry{key: key, value: value}
	}

	// 计算所需空间
	size := NodeHeaderSize
	for _, e := range entries {
		size += 4 + 2 + len(e.value)
	}
	if size > len(na.data) {
		return false // 页满
	}

	na.setLeafEntries(entries)
	return true
}

// === 内部节点 Entry 操作 ===

type internalEntry struct {
	key         int
	childPageID uint32
}

// internalEntries 读取内部节点所有 entry（不含 firstChild）
func (na *nodeAccessor) internalEntries() []internalEntry {
	n := na.numKeys()
	entries := make([]internalEntry, 0, n)
	offset := NodeHeaderSize + 4 // 跳过 firstChild
	for i := 0; i < n; i++ {
		key := int(binary.LittleEndian.Uint32(na.data[offset : offset+4]))
		offset += 4
		child := binary.LittleEndian.Uint32(na.data[offset : offset+4])
		offset += 4
		entries = append(entries, internalEntry{key: key, childPageID: child})
	}
	return entries
}

// setInternalEntries 覆盖写入
func (na *nodeAccessor) setInternalEntries(firstChild uint32, entries []internalEntry) {
	na.setNumKeys(len(entries))
	na.setFirstChild(firstChild)
	offset := NodeHeaderSize + 4
	for _, e := range entries {
		binary.LittleEndian.PutUint32(na.data[offset:offset+4], uint32(e.key))
		offset += 4
		binary.LittleEndian.PutUint32(na.data[offset:offset+4], e.childPageID)
		offset += 4
	}
}

// tryInsertInternalEntry 尝试插入内部 entry。返回 false 表示页满
func (na *nodeAccessor) tryInsertInternalEntry(key int, childPageID uint32) bool {
	entries := na.internalEntries()
	firstChild := na.firstChild()

	idx := sort.Search(len(entries), func(i int) bool {
		return entries[i].key >= key
	})

	if idx < len(entries) && entries[idx].key == key {
		entries[idx].childPageID = childPageID // 替换
	} else {
		entries = append(entries, internalEntry{})
		copy(entries[idx+1:], entries[idx:])
		entries[idx] = internalEntry{key: key, childPageID: childPageID}
	}

	// 内部节点每个 entry 8 bytes + firstChild 4 bytes + header 20 bytes
	size := NodeHeaderSize + 4 + len(entries)*8
	if size > len(na.data) {
		return false
	}

	na.setInternalEntries(firstChild, entries)
	return true
}

// === BTree 对外接口 ===

// NewBTree 创建新的 B+Tree（分配根叶子节点）
func NewBTree(pager *Pager) *BTree {
	pageID, _ := pager.Allocate()
	page := pager.GetPage(pageID)
	initLeafNode(page)
	page.SetDirty(true)
	pager.Flush(pageID)
	return &BTree{pager: pager, rootPageID: pageID}
}

// LoadBTree 从已有根页加载
func LoadBTree(pager *Pager, rootPageID uint32) *BTree {
	return &BTree{pager: pager, rootPageID: rootPageID}
}

// Search 查找 key
func (bt *BTree) Search(key int) ([]byte, bool, error) {
	pageID := bt.rootPageID
	for {
		page := bt.pager.GetPage(pageID)
		na := newNodeAccessor(page)

		if na.nodeType() == NodeTypeLeaf {
			entries := na.leafEntries()
			idx := sort.Search(len(entries), func(i int) bool {
				return entries[i].key >= key
			})
			if idx < len(entries) && entries[idx].key == key {
				return entries[idx].value, true, nil
			}
			return nil, false, nil
		}

		// 内部节点：选择子节点
		entries := na.internalEntries()
		firstChild := na.firstChild()

		if len(entries) == 0 {
			pageID = firstChild
			continue
		}

		if key < entries[0].key {
			pageID = firstChild
		} else {
			pageID = entries[len(entries)-1].childPageID
			for i := len(entries) - 2; i >= 0; i-- {
				if key >= entries[i].key {
					pageID = entries[i].childPageID
					break
				}
			}
		}
	}
}

// RangeScan 范围扫描 [start, end]
func (bt *BTree) RangeScan(start, end int) ([]KVPair, error) {
	// 找到包含 start 的叶子节点
	pageID := bt.rootPageID
	for {
		page := bt.pager.GetPage(pageID)
		na := newNodeAccessor(page)
		if na.nodeType() == NodeTypeLeaf {
			break
		}
		entries := na.internalEntries()
		firstChild := na.firstChild()
		if len(entries) == 0 || start < entries[0].key {
			pageID = firstChild
		} else {
			pageID = entries[len(entries)-1].childPageID
			for i := len(entries) - 2; i >= 0; i-- {
				if start >= entries[i].key {
					pageID = entries[i].childPageID
					break
				}
			}
		}
	}

	var results []KVPair
	for pageID != 0 {
		page := bt.pager.GetPage(pageID)
		na := newNodeAccessor(page)
		entries := na.leafEntries()

		for _, e := range entries {
			if e.key < start {
				continue
			}
			if e.key > end {
				return results, nil
			}
			results = append(results, KVPair{Key: e.key, Value: e.value})
		}

		pageID = na.rightSibling()
	}
	return results, nil
}

// Insert 插入 key-value
func (bt *BTree) Insert(key int, value []byte) error {
	rootPage := bt.pager.GetPage(bt.rootPageID)
	newRoot, err := bt.insertIntoNode(rootPage, key, value)
	if err != nil {
		return err
	}
	if newRoot != 0 {
		bt.rootPageID = newRoot
	}
	return nil
}

// insertIntoNode 递归插入。返回 (newRootPageID, error)
func (bt *BTree) insertIntoNode(page *Page, key int, value []byte) (uint32, error) {
	na := newNodeAccessor(page)

	if na.nodeType() == NodeTypeLeaf {
		// 尝试直接插入
		if na.tryInsertLeafEntry(key, value) {
			na.page.SetDirty(true)
			return 0, bt.pager.Flush(page.ID())
		}
		// 叶子满了，分裂
		return bt.splitLeaf(page, key, value)
	}

	// 内部节点：找到合适的子节点
	entries := na.internalEntries()
	firstChild := na.firstChild()

	var childPageID uint32
	if len(entries) == 0 || key < entries[0].key {
		childPageID = firstChild
	} else {
		childPageID = entries[len(entries)-1].childPageID
		for i := len(entries) - 2; i >= 0; i-- {
			if key >= entries[i].key {
				childPageID = entries[i].childPageID
				break
			}
		}
	}

	childPage := bt.pager.GetPage(childPageID)
	newChildPageID, err := bt.insertIntoNode(childPage, key, value)
	if err != nil {
		return 0, err
	}
	if newChildPageID == 0 {
		return 0, nil // 子节点未分裂
	}

	// 子节点分裂了，需要把 newChildPageID 和它的最小 key 插入当前内部节点
	newChildNa := newNodeAccessor(bt.pager.GetPage(newChildPageID))
	var splitKey int
	if newChildNa.nodeType() == NodeTypeLeaf {
		childEntries := newChildNa.leafEntries()
		if len(childEntries) > 0 {
			splitKey = childEntries[0].key
		}
	} else {
		childEntries := newChildNa.internalEntries()
		if len(childEntries) > 0 {
			splitKey = childEntries[0].key
		} else {
			splitKey = int(newChildPageID) // fallback
		}
	}

	// 尝试插入当前内部节点
	if na.tryInsertInternalEntry(splitKey, newChildPageID) {
		na.page.SetDirty(true)
		return 0, bt.pager.Flush(page.ID())
	}

	// 内部节点也满了，分裂
	return bt.splitInternal(page, splitKey, newChildPageID)
}

// splitLeaf 分裂叶子节点。返回 (newRootPageID, error)
func (bt *BTree) splitLeaf(page *Page, key int, value []byte) (uint32, error) {
	na := newNodeAccessor(page)
	entries := na.leafEntries()

	// 找到插入位置
	idx := sort.Search(len(entries), func(i int) bool {
		return entries[i].key >= key
	})
	if idx < len(entries) && entries[idx].key == key {
		entries[idx].value = value
	} else {
		entries = append(entries, leafEntry{})
		copy(entries[idx+1:], entries[idx:])
		entries[idx] = leafEntry{key: key, value: value}
	}

	// 分成两半
	mid := (len(entries) + 1) / 2
	leftEntries := entries[:mid]
	rightEntries := entries[mid:]

	// 分配新页给右半
	newPageID, err := bt.pager.Allocate()
	if err != nil {
		return 0, fmt.Errorf("allocate leaf: %w", err)
	}
	newPage := bt.pager.GetPage(newPageID)
	initLeafNode(newPage)
	newNa := newNodeAccessor(newPage)
	newNa.setLeafEntries(rightEntries)
	newNa.setLeftSibling(page.ID())
	newNa.setRightSibling(na.rightSibling())
	newNa.setParent(na.parent())
	newNa.page.SetDirty(true)

	// 更新原页
	na.setLeafEntries(leftEntries)
	na.setRightSibling(newPageID)
	na.page.SetDirty(true)

	// 更新右兄弟的左指针
	if newNa.rightSibling() != 0 {
		rsPage := bt.pager.GetPage(newNa.rightSibling())
		rsNa := newNodeAccessor(rsPage)
		rsNa.setLeftSibling(newPageID)
		rsPage.SetDirty(true)
		bt.pager.Flush(newNa.rightSibling())
	}

	if err := bt.pager.Flush(page.ID()); err != nil {
		return 0, err
	}
	if err := bt.pager.Flush(newPageID); err != nil {
		return 0, err
	}

	// 将新节点的最小 key 提升到父节点
	splitKey := rightEntries[0].key
	if na.isRoot() {
		// 创建新的根内部节点
		return bt.createNewRoot(page.ID(), splitKey, newPageID)
	}

	// 递归插入父节点
	parentPage := bt.pager.GetPage(na.parent())
	return bt.insertIntoNode(parentPage, splitKey, nil) // value=nil 表示内部节点插入
}

// splitInternal 分裂内部节点。返回 (newRootPageID, error)
func (bt *BTree) splitInternal(page *Page, key int, childPageID uint32) (uint32, error) {
	na := newNodeAccessor(page)
	entries := na.internalEntries()
	firstChild := na.firstChild()

	// 在合适位置插入新 entry
	idx := sort.Search(len(entries), func(i int) bool {
		return entries[i].key >= key
	})
	if idx < len(entries) && entries[idx].key == key {
		entries[idx].childPageID = childPageID
	} else {
		entries = append(entries, internalEntry{})
		copy(entries[idx+1:], entries[idx:])
		entries[idx] = internalEntry{key: key, childPageID: childPageID}
	}

	// 内部节点的完整 key 列表：firstChild 不算在 entries 中
	// 分裂时，把 entries 分成两半
	mid := len(entries) / 2
	leftEntries := entries[:mid]
	promoteEntry := entries[mid]          // 提升到父节点的 key
	rightEntries := entries[mid+1:]       // 右半部分
	rightFirstChild := promoteEntry.childPageID // 右节点的 firstChild

	// 分配新页
	newPageID, err := bt.pager.Allocate()
	if err != nil {
		return 0, fmt.Errorf("allocate internal: %w", err)
	}
	newPage := bt.pager.GetPage(newPageID)
	initInternalNode(newPage, false)
	newNa := newNodeAccessor(newPage)
	newNa.setInternalEntries(rightFirstChild, rightEntries)
	newNa.setParent(na.parent())
	newNa.page.SetDirty(true)

	// 更新原页
	na.setInternalEntries(firstChild, leftEntries)
	na.page.SetDirty(true)

	// 更新所有被移动子节点的 parent 指针
	bt.updateParentPointers(rightFirstChild, rightEntries, newPageID)

	if err := bt.pager.Flush(page.ID()); err != nil {
		return 0, err
	}
	if err := bt.pager.Flush(newPageID); err != nil {
		return 0, err
	}

	// 提升 promoteEntry.key 到父节点
	promoteKey := promoteEntry.key
	if na.isRoot() {
		return bt.createNewRoot(page.ID(), promoteKey, newPageID)
	}

	parentPage := bt.pager.GetPage(na.parent())
	// 这里有个问题：insertIntoNode 期望插入 key-value，但内部节点插入是 key-child
	// 我需要修改内部节点的处理...
	
	// 简化：直接递归调用，但用特殊标记？
	// 实际上不应该递归到 insertIntoNode，因为父节点也是内部节点
	// 应该直接在父节点插入 (promoteKey, newPageID)
	
	parentNa := newNodeAccessor(parentPage)
	if parentNa.tryInsertInternalEntry(promoteKey, newPageID) {
		parentNa.page.SetDirty(true)
		return 0, bt.pager.Flush(parentPage.ID())
	}
	
	// 父节点也满了，递归分裂
	return bt.splitInternal(parentPage, promoteKey, newPageID)
}

// createNewRoot 创建新的根内部节点
func (bt *BTree) createNewRoot(leftPageID uint32, key int, rightPageID uint32) (uint32, error) {
	newRootID, err := bt.pager.Allocate()
	if err != nil {
		return 0, fmt.Errorf("allocate new root: %w", err)
	}
	newRootPage := bt.pager.GetPage(newRootID)
	initInternalNode(newRootPage, true)
	newNa := newNodeAccessor(newRootPage)
	newNa.setFirstChild(leftPageID)
	newNa.setInternalEntries(leftPageID, []internalEntry{{key: key, childPageID: rightPageID}})
	newNa.page.SetDirty(true)

	// 更新左右子节点的 parent
	leftPage := bt.pager.GetPage(leftPageID)
	leftNa := newNodeAccessor(leftPage)
	leftNa.setParent(newRootID)
	leftNa.setIsRoot(false)
	leftPage.SetDirty(true)

	rightPage := bt.pager.GetPage(rightPageID)
	rightNa := newNodeAccessor(rightPage)
	rightNa.setParent(newRootID)
	rightNa.setIsRoot(false)
	rightPage.SetDirty(true)

	if err := bt.pager.Flush(newRootID); err != nil {
		return 0, err
	}
	if err := bt.pager.Flush(leftPageID); err != nil {
		return 0, err
	}
	if err := bt.pager.Flush(rightPageID); err != nil {
		return 0, err
	}

	return newRootID, nil
}

// updateParentPointers 更新子节点的 parent 指针
func (bt *BTree) updateParentPointers(firstChild uint32, entries []internalEntry, newParent uint32) {
	childIDs := []uint32{firstChild}
	for _, e := range entries {
		childIDs = append(childIDs, e.childPageID)
	}
	for _, cid := range childIDs {
		childPage := bt.pager.GetPage(cid)
		na := newNodeAccessor(childPage)
		na.setParent(newParent)
		childPage.SetDirty(true)
		bt.pager.Flush(cid)
	}
}
