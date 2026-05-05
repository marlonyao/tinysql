package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sort"
)

const NodeTypeLeaf byte = 1
const NodeTypeInternal byte = 2

// NodeHeader 布局 (32 bytes)
// [0:1]   nodeType (1=leaf, 2=internal)
// [1:2]   flags
// [2:4]   numKeys (uint16)
// [4:8]   parentPageID (uint32)
// [8:12]  rightSibling (uint32)
// [12:16] leftSibling (uint32)
// [16:20] firstChild (uint32) — 仅内部节点使用
// [20:32] reserved

const NodeHeaderSize = 32

type leafEntry struct {
	key   []byte
	value []byte
}

type internalEntry struct {
	key         []byte
	childPageID uint32
}

type nodeAccessor struct {
	page *Page
	data []byte
}

func newNodeAccessor(page *Page) *nodeAccessor {
	return &nodeAccessor{page: page, data: page.Data()}
}

func (na *nodeAccessor) nodeType() byte     { return na.data[0] }
func (na *nodeAccessor) numKeys() uint16     { return binary.LittleEndian.Uint16(na.data[2:4]) }
func (na *nodeAccessor) parent() uint32      { return binary.LittleEndian.Uint32(na.data[4:8]) }
func (na *nodeAccessor) rightSibling() uint32 { return binary.LittleEndian.Uint32(na.data[8:12]) }
func (na *nodeAccessor) leftSibling() uint32  { return binary.LittleEndian.Uint32(na.data[12:16]) }
func (na *nodeAccessor) firstChild() uint32    { return binary.LittleEndian.Uint32(na.data[16:20]) }
func (na *nodeAccessor) isRoot() bool         { return na.parent() == 0 }

func (na *nodeAccessor) setNodeType(t byte)      { na.data[0] = t }
func (na *nodeAccessor) setNumKeys(n uint16)     { binary.LittleEndian.PutUint16(na.data[2:4], n) }
func (na *nodeAccessor) setParent(p uint32)      { binary.LittleEndian.PutUint32(na.data[4:8], p) }
func (na *nodeAccessor) setRightSibling(r uint32) { binary.LittleEndian.PutUint32(na.data[8:12], r) }
func (na *nodeAccessor) setLeftSibling(l uint32)  { binary.LittleEndian.PutUint32(na.data[12:16], l) }
func (na *nodeAccessor) setFirstChild(f uint32)   { binary.LittleEndian.PutUint32(na.data[16:20], f) }

func initLeafNode(page *Page) {
	na := newNodeAccessor(page)
	na.setNodeType(NodeTypeLeaf)
	na.setNumKeys(0)
	na.setParent(0)
	na.setRightSibling(0)
	na.setLeftSibling(0)
	na.setFirstChild(0)
}

func initInternalNode(page *Page, isRoot bool) {
	na := newNodeAccessor(page)
	na.setNodeType(NodeTypeInternal)
	na.setNumKeys(0)
	if isRoot {
		na.setParent(0)
	}
	na.setRightSibling(0)
	na.setLeftSibling(0)
	na.setFirstChild(0)
}

// leafEntries 读取所有叶子 entry
func (na *nodeAccessor) leafEntries() []leafEntry {
	count := int(na.numKeys())
	entries := make([]leafEntry, 0, count)
	offset := NodeHeaderSize
	for i := 0; i < count; i++ {
		keyLen := binary.LittleEndian.Uint16(na.data[offset : offset+2])
		offset += 2
		key := make([]byte, keyLen)
		copy(key, na.data[offset:offset+int(keyLen)])
		offset += int(keyLen)
		valLen := binary.LittleEndian.Uint16(na.data[offset : offset+2])
		offset += 2
		value := make([]byte, valLen)
		copy(value, na.data[offset:offset+int(valLen)])
		offset += int(valLen)
		entries = append(entries, leafEntry{key: key, value: value})
	}
	return entries
}

// setLeafEntries 覆盖写入
func (na *nodeAccessor) setLeafEntries(entries []leafEntry) {
	na.setNumKeys(uint16(len(entries)))
	offset := NodeHeaderSize
	for _, e := range entries {
		binary.LittleEndian.PutUint16(na.data[offset:offset+2], uint16(len(e.key)))
		offset += 2
		copy(na.data[offset:], e.key)
		offset += len(e.key)
		binary.LittleEndian.PutUint16(na.data[offset:offset+2], uint16(len(e.value)))
		offset += 2
		copy(na.data[offset:], e.value)
		offset += len(e.value)
	}
}

// tryInsertLeafEntry 尝试插入/更新。返回 false 表示页满
func (na *nodeAccessor) tryInsertLeafEntry(key []byte, value []byte) bool {
	entries := na.leafEntries()

	idx := sort.Search(len(entries), func(i int) bool {
		return bytes.Compare(entries[i].key, key) >= 0
	})

	if idx < len(entries) && bytes.Equal(entries[idx].key, key) {
		entries[idx].value = value
	} else {
		entries = append(entries, leafEntry{})
		copy(entries[idx+1:], entries[idx:])
		entries[idx] = leafEntry{key: key, value: value}
	}

	size := NodeHeaderSize
	for _, e := range entries {
		size += 2 + len(e.key) + 2 + len(e.value)
	}
	if size > len(na.data) {
		return false
	}

	na.setLeafEntries(entries)
	return true
}

// internalEntries 读取所有内部 entry
func (na *nodeAccessor) internalEntries() []internalEntry {
	count := int(na.numKeys())
	entries := make([]internalEntry, 0, count)
	offset := NodeHeaderSize + 4
	for i := 0; i < count; i++ {
		keyLen := binary.LittleEndian.Uint16(na.data[offset : offset+2])
		offset += 2
		key := make([]byte, keyLen)
		copy(key, na.data[offset:offset+int(keyLen)])
		offset += int(keyLen)
		childPageID := binary.LittleEndian.Uint32(na.data[offset : offset+4])
		offset += 4
		entries = append(entries, internalEntry{key: key, childPageID: childPageID})
	}
	return entries
}

// setInternalEntries 覆盖写入
func (na *nodeAccessor) setInternalEntries(firstChild uint32, entries []internalEntry) {
	na.setNumKeys(uint16(len(entries)))
	na.setFirstChild(firstChild)
	offset := NodeHeaderSize + 4
	for _, e := range entries {
		binary.LittleEndian.PutUint16(na.data[offset:offset+2], uint16(len(e.key)))
		offset += 2
		copy(na.data[offset:], e.key)
		offset += len(e.key)
		binary.LittleEndian.PutUint32(na.data[offset:offset+4], e.childPageID)
		offset += 4
	}
}

// tryInsertInternalEntry 尝试插入内部 entry。返回 false 表示页满
func (na *nodeAccessor) tryInsertInternalEntry(key []byte, childPageID uint32) bool {
	entries := na.internalEntries()
	firstChild := na.firstChild()

	idx := sort.Search(len(entries), func(i int) bool {
		return bytes.Compare(entries[i].key, key) >= 0
	})

	if idx < len(entries) && bytes.Equal(entries[idx].key, key) {
		entries[idx].childPageID = childPageID // 替换
	} else {
		entries = append(entries, internalEntry{})
		copy(entries[idx+1:], entries[idx:])
		entries[idx] = internalEntry{key: key, childPageID: childPageID}
	}

	size := NodeHeaderSize + 4
	for _, e := range entries {
		size += 2 + len(e.key) + 4
	}
	if size > len(na.data) {
		return false
	}

	na.setInternalEntries(firstChild, entries)
	return true
}

// KVPair key-value 对
type KVPair struct {
	Key   []byte
	Value []byte
}

// BTree B+Tree 索引
type BTree struct {
	pager      *Pager
	rootPageID uint32
}

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
func (bt *BTree) Search(key []byte) ([]byte, bool, error) {
	pageID := bt.rootPageID
	for {
		page := bt.pager.GetPage(pageID)
		na := newNodeAccessor(page)

		if na.nodeType() == NodeTypeLeaf {
			entries := na.leafEntries()
			idx := sort.Search(len(entries), func(i int) bool {
				return bytes.Compare(entries[i].key, key) >= 0
			})
			if idx < len(entries) && bytes.Equal(entries[idx].key, key) {
				return entries[idx].value, true, nil
			}
			return nil, false, nil
		}

		// 内部节点：选择子节点
		entries := na.internalEntries()
		firstChild := na.firstChild()

		pageID = firstChild
		for i := 0; i < len(entries); i++ {
			if bytes.Compare(key, entries[i].key) >= 0 {
				pageID = entries[i].childPageID
			} else {
				break
			}
		}
	}
}

// Delete 删除 key
func (bt *BTree) Delete(key []byte) error {
	pageID := bt.rootPageID
	for {
		page := bt.pager.GetPage(pageID)
		na := newNodeAccessor(page)

		if na.nodeType() == NodeTypeLeaf {
			entries := na.leafEntries()
			idx := sort.Search(len(entries), func(i int) bool {
				return bytes.Compare(entries[i].key, key) >= 0
			})
			if idx < len(entries) && bytes.Equal(entries[idx].key, key) {
				// 删除 entry
				entries = append(entries[:idx], entries[idx+1:]...)
				na.setLeafEntries(entries)
				na.page.SetDirty(true)
				return bt.pager.Flush(page.ID())
			}
			return nil // key not found, nothing to delete
		}

		// 内部节点：选择子节点
		entries := na.internalEntries()
		firstChild := na.firstChild()

		pageID = firstChild
		for i := 0; i < len(entries); i++ {
			if bytes.Compare(key, entries[i].key) >= 0 {
				pageID = entries[i].childPageID
			} else {
				break
			}
		}
	}
}

// RangeScan 范围扫描 [start, end]（字节序比较）
func (bt *BTree) RangeScan(start, end []byte) ([]KVPair, error) {
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
		pageID = firstChild
		for i := 0; i < len(entries); i++ {
			if bytes.Compare(start, entries[i].key) >= 0 {
				pageID = entries[i].childPageID
			} else {
				break
			}
		}
	}

	var results []KVPair
	for pageID != 0 {
		page := bt.pager.GetPage(pageID)
		na := newNodeAccessor(page)
		entries := na.leafEntries()
		for _, e := range entries {
			if bytes.Compare(e.key, start) < 0 {
				continue
			}
			if bytes.Compare(e.key, end) > 0 {
				return results, nil
			}
			results = append(results, KVPair{Key: e.key, Value: e.value})
		}
		pageID = na.rightSibling()
	}
	return results, nil
}

// Insert 插入 key-value
func (bt *BTree) Insert(key []byte, value []byte) error {
	page := bt.pager.GetPage(bt.rootPageID)
	newRootID, err := bt.insertIntoNode(page, key, value)
	if err != nil {
		return err
	}
	if newRootID != 0 {
		bt.rootPageID = newRootID
	}
	return nil
}

// insertIntoNode 递归插入。返回 (newRootPageID, error)
func (bt *BTree) insertIntoNode(page *Page, key []byte, value []byte) (uint32, error) {
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

	childPageID := firstChild
	for i := 0; i < len(entries); i++ {
		if bytes.Compare(key, entries[i].key) >= 0 {
			childPageID = entries[i].childPageID
		} else {
			break
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
	var splitKey []byte
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
			splitKey = EncodeIntKey(int(newChildPageID)) // fallback
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
func (bt *BTree) splitLeaf(page *Page, key []byte, value []byte) (uint32, error) {
	na := newNodeAccessor(page)
	entries := na.leafEntries()

	// 找到插入位置
	idx := sort.Search(len(entries), func(i int) bool {
		return bytes.Compare(entries[i].key, key) >= 0
	})
	if idx < len(entries) && bytes.Equal(entries[idx].key, key) {
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

	// 写入右半数据
	newNa := newNodeAccessor(newPage)
	newNa.setLeafEntries(rightEntries)
	newNa.setRightSibling(na.rightSibling()) // 新节点的右兄弟 = 原节点的右兄弟
	newNa.setLeftSibling(page.ID())          // 新节点的左兄弟 = 原节点
	newNa.setParent(na.parent())
	newNa.page.SetDirty(true)

	// 更新原页
	na.setLeafEntries(leftEntries)
	na.setRightSibling(newPageID) // 原节点的右兄弟 = 新节点
	na.page.SetDirty(true)

	// 更新原节点的右兄弟的左兄弟指针
	if newNa.rightSibling() != 0 {
		rightSiblingPage := bt.pager.GetPage(newNa.rightSibling())
		rightSiblingNa := newNodeAccessor(rightSiblingPage)
		rightSiblingNa.setLeftSibling(newPageID)
		rightSiblingPage.SetDirty(true)
		if err := bt.pager.Flush(rightSiblingPage.ID()); err != nil {
			return 0, err
		}
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

	// 直接在父节点插入 internal entry
	parentPage := bt.pager.GetPage(na.parent())
	parentNa := newNodeAccessor(parentPage)
	if parentNa.tryInsertInternalEntry(splitKey, newPageID) {
		parentNa.page.SetDirty(true)
		return 0, bt.pager.Flush(parentPage.ID())
	}

	// 父节点也满了，分裂
	return bt.splitInternal(parentPage, splitKey, newPageID)
}

// splitInternal 分裂内部节点。返回 (newRootPageID, error)
func (bt *BTree) splitInternal(page *Page, key []byte, childPageID uint32) (uint32, error) {
	na := newNodeAccessor(page)
	entries := na.internalEntries()
	firstChild := na.firstChild()

	// 在合适位置插入新 entry
	idx := sort.Search(len(entries), func(i int) bool {
		return bytes.Compare(entries[i].key, key) >= 0
	})
	if idx < len(entries) && bytes.Equal(entries[idx].key, key) {
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
	parentNa := newNodeAccessor(parentPage)
	if parentNa.tryInsertInternalEntry(promoteKey, newPageID) {
		parentNa.page.SetDirty(true)
		return 0, bt.pager.Flush(parentPage.ID())
	}

	// 祖父节点也满了，递归分裂
	return bt.splitInternal(parentPage, promoteKey, newPageID)
}

// createNewRoot 创建新的根节点
func (bt *BTree) createNewRoot(leftPageID uint32, key []byte, rightPageID uint32) (uint32, error) {
	newRootID, err := bt.pager.Allocate()
	if err != nil {
		return 0, fmt.Errorf("allocate root: %w", err)
	}
	newPage := bt.pager.GetPage(newRootID)
	initInternalNode(newPage, true)
	newNa := newNodeAccessor(newPage)
	newNa.setFirstChild(leftPageID)
	newNa.setInternalEntries(leftPageID, []internalEntry{{key: key, childPageID: rightPageID}})
	newNa.page.SetDirty(true)

	// 更新左右子节点的 parent
	leftPage := bt.pager.GetPage(leftPageID)
	leftNa := newNodeAccessor(leftPage)
	leftNa.setParent(newRootID)
	leftPage.SetDirty(true)

	rightPage := bt.pager.GetPage(rightPageID)
	rightNa := newNodeAccessor(rightPage)
	rightNa.setParent(newRootID)
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

// EncodeIntKey 将 int 编码为 8 字节 BigEndian []byte，用于 BTree key
func EncodeIntKey(v int) []byte {
	b := make([]byte, 8)
	binary.BigEndian.PutUint64(b, uint64(v))
	return b
}

// DecodeIntKey 从 8 字节 BigEndian []byte 解码为 int
func DecodeIntKey(b []byte) int {
	if len(b) != 8 {
		return 0
	}
	return int(binary.BigEndian.Uint64(b))
}

// RootPageID 返回根页 ID
func (bt *BTree) RootPageID() uint32 {
	return bt.rootPageID
}
