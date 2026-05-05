package storage

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"strings"
)

// PageTypeTableData 是存行数据的页类型
const PageTypeTableData = 5

// TableManager 管理所有表和行数据
type TableManager struct {
	pager  *Pager
	tables map[string]*Table // 内存中的表定义
}

// NewTableManager 创建表管理器（自动从磁盘加载已有表定义）
func NewTableManager(pager *Pager) *TableManager {
	tm := &TableManager{
		pager:  pager,
		tables: make(map[string]*Table),
	}
	// 自动加载已有的表定义
	_ = tm.LoadTables()
	return tm
}

// CreateTable 创建新表（持久化表元数据 + 创建 B+Tree 聚簇索引）
func (tm *TableManager) CreateTable(table *Table) error {
	if _, exists := tm.tables[table.Name]; exists {
		return fmt.Errorf("table %s already exists", table.Name)
	}

	// 创建 B+Tree 作为聚簇索引
	bt := NewBTree(tm.pager)
	table.RootPageID = bt.rootPageID

	// 持久化表定义到页
	if err := tm.persistTableMeta(table); err != nil {
		return fmt.Errorf("persist table meta: %w", err)
	}

	tm.tables[table.Name] = table
	return nil
}

// persistTableMeta 把表定义序列化存到系统区域
// 方案：用 page 0 的 data 区存所有表定义的 JSON
func (tm *TableManager) persistTableMeta(table *Table) error {
	meta := map[string]interface{}{
		"name":       table.Name,
		"columns":    table.Columns,
		"rootPageID": table.RootPageID,
		"nextRowID":  table.NextRowID,
		"indexes":    table.Indexes,
	}

	// 读出现有表定义列表，追加
	metaPage := tm.pager.GetPage(0)

	// 简单方案：每次重写全部表定义
	allMetas := make([]map[string]interface{}, 0)
	count := binary.LittleEndian.Uint32(metaPage.Data()[0:4])
	if count > 0 {
		// 解析现有的（从 Data()[8:] 开始）
		existing := metaPage.Data()[8:]
		var existingList []map[string]interface{}
		if err := json.Unmarshal(bytes.TrimRight(existing, "\x00"), &existingList); err == nil {
			allMetas = existingList
		}
	}

	// 检查是否已存在，存在则更新
	found := false
	for i, m := range allMetas {
		if m["name"] == table.Name {
			allMetas[i] = meta
			found = true
			break
		}
	}
	if !found {
		allMetas = append(allMetas, meta)
	}

	allJSON, err := json.Marshal(allMetas)
	if err != nil {
		return err
	}

	// page 0 data area layout:
	// Data()[0:4]  = data[16:20] = tableCount
	// Data()[4:8]  = data[20:24] = nextPageID (managed by Pager, DON'T TOUCH)
	// Data()[8:]   = data[24:]   = 表元数据 JSON
	if len(allJSON) > len(metaPage.Data())-8 {
		return fmt.Errorf("table metadata too large for page 0")
	}

	binary.LittleEndian.PutUint32(metaPage.Data()[0:4], uint32(len(allMetas)))
	copy(metaPage.Data()[8:], allJSON)
	metaPage.SetDirty(true)

	return tm.pager.Flush(0)
}

// GetTable 获取表定义
func (tm *TableManager) GetTable(name string) (*Table, bool) {
	t, ok := tm.tables[name]
	return t, ok
}

// ListTables 返回所有表名
func (tm *TableManager) ListTables() []string {
	names := make([]string, 0, len(tm.tables))
	for name := range tm.tables {
		names = append(names, name)
	}
	return names
}

// LoadTables 从磁盘恢复所有表定义（含 B+Tree rootPageID）
func (tm *TableManager) LoadTables() error {
	metaPage := tm.pager.GetPage(0)
	count := binary.LittleEndian.Uint32(metaPage.Data()[0:4])
	if count == 0 {
		return nil
	}

	data := bytes.TrimRight(metaPage.Data()[8:], "\x00")
	var allMetas []map[string]interface{}
	if err := json.Unmarshal(data, &allMetas); err != nil {
		return fmt.Errorf("parse table metadata: %w", err)
	}

	for _, m := range allMetas {
		name, _ := m["name"].(string)

		// 解析 columns
		colsRaw, _ := json.Marshal(m["columns"])
		var columns []Column
		json.Unmarshal(colsRaw, &columns)

		// 解析 rootPageID
		var rootPageID uint32
		switch v := m["rootPageID"].(type) {
		case float64:
			rootPageID = uint32(v)
		case uint32:
			rootPageID = v
		}

		// 解析 nextRowID
		var nextRowID int
		switch v := m["nextRowID"].(type) {
		case float64:
			nextRowID = int(v)
		case int:
			nextRowID = v
		}

		// 解析 indexes
		var indexes []Index
		if idxRaw, ok := m["indexes"]; ok {
			raw, _ := json.Marshal(idxRaw)
			json.Unmarshal(raw, &indexes)
		}

		tm.tables[name] = &Table{
			Name:       name,
			Columns:    columns,
			RootPageID: rootPageID,
			NextRowID:  nextRowID,
			Indexes:    indexes,
		}
	}

	return nil
}

// Insert 向表中插入一行（B+Tree 聚簇索引 + 二级索引）
func (tm *TableManager) Insert(tableName string, row *Row) error {
	table, ok := tm.tables[tableName]
	if !ok {
		return fmt.Errorf("table %s not found", tableName)
	}

	rowData, err := table.SerializeRow(row)
	if err != nil {
		return fmt.Errorf("serialize row: %w", err)
	}

	// 使用自增 _rowid 作为 B+Tree key
	rowID := table.NextRowID
	table.NextRowID++

	bt := LoadBTree(tm.pager, table.RootPageID)
	if err := bt.Insert(EncodeIntKey(rowID), rowData); err != nil {
		return fmt.Errorf("btree insert: %w", err)
	}
	// BTree 分裂可能导致根节点变化，需要同步
	if bt.rootPageID != table.RootPageID {
		table.RootPageID = bt.rootPageID
	}

	// 维护二级索引
	for i := range table.Indexes {
		idx := &table.Indexes[i]
		idxBT := LoadBTree(tm.pager, idx.RootPageID)
		key, err := tm.buildIndexKey(table, *idx, row, rowID)
		if err != nil {
			return fmt.Errorf("build index key for %s: %w", idx.Name, err)
		}
		if err := idxBT.Insert(key, EncodeIntKey(rowID)); err != nil {
			return fmt.Errorf("index %s insert: %w", idx.Name, err)
		}
		if idxBT.rootPageID != idx.RootPageID {
			idx.RootPageID = idxBT.rootPageID
		}
	}

	// 更新表元数据（nextRowID、rootPageID、索引 rootPageID 变化）
	return tm.persistTableMeta(table)
}

// InsertTx 事务版插入，返回 (rowID, error)
func (tm *TableManager) InsertTx(tableName string, row *Row) (int, error) {
	table, ok := tm.tables[tableName]
	if !ok {
		return 0, fmt.Errorf("table %s not found", tableName)
	}

	rowData, err := table.SerializeRow(row)
	if err != nil {
		return 0, fmt.Errorf("serialize row: %w", err)
	}

	rowID := table.NextRowID
	table.NextRowID++

	bt := LoadBTree(tm.pager, table.RootPageID)
	if err := bt.Insert(EncodeIntKey(rowID), rowData); err != nil {
		return 0, fmt.Errorf("btree insert: %w", err)
	}
	// BTree 分裂可能导致根节点变化，需要同步
	if bt.rootPageID != table.RootPageID {
		table.RootPageID = bt.rootPageID
	}

	// 维护二级索引
	for i := range table.Indexes {
		idx := &table.Indexes[i]
		idxBT := LoadBTree(tm.pager, idx.RootPageID)
		key, err := tm.buildIndexKey(table, *idx, row, rowID)
		if err != nil {
			return 0, fmt.Errorf("build index key for %s: %w", idx.Name, err)
		}
		if err := idxBT.Insert(key, EncodeIntKey(rowID)); err != nil {
			return 0, fmt.Errorf("index %s insert: %w", idx.Name, err)
		}
		if idxBT.rootPageID != idx.RootPageID {
			idx.RootPageID = idxBT.rootPageID
		}
	}

	return rowID, tm.persistTableMeta(table)
}

// DeleteSlot 标记删除指定位置的行（B+Tree 暂不支持物理删除，这里是兼容接口）
func (tm *TableManager) DeleteSlot(tableName string, pageID uint32, slotIdx int) error {
	return fmt.Errorf("delete not supported with btree storage")
}

// DeleteByRowID 按 RowID 删除（聚簇索引 + 二级索引）
func (tm *TableManager) DeleteByRowID(tableName string, rowID int) error {
	table, ok := tm.tables[tableName]
	if !ok {
		return fmt.Errorf("table %s not found", tableName)
	}

	// 先读出该行数据，用于删除二级索引
	bt := LoadBTree(tm.pager, table.RootPageID)
	rowData, found, err := bt.Search(EncodeIntKey(rowID))
	if err != nil {
		return fmt.Errorf("btree search: %w", err)
	}
	if !found {
		return nil // 已经不存在，无需删除
	}
	row, err := table.DeserializeRow(rowData)
	if err != nil {
		return fmt.Errorf("deserialize row: %w", err)
	}

	// 删除二级索引
	for i := range table.Indexes {
		idx := &table.Indexes[i]
		idxBT := LoadBTree(tm.pager, idx.RootPageID)
		key, err := tm.buildIndexKey(table, *idx, row, rowID)
		if err != nil {
			return fmt.Errorf("build index key for %s: %w", idx.Name, err)
		}
		if err := idxBT.Delete(key); err != nil {
			return fmt.Errorf("index %s delete: %w", idx.Name, err)
		}
		if idxBT.rootPageID != idx.RootPageID {
			idx.RootPageID = idxBT.rootPageID
		}
	}

	// 删除聚簇索引
	if err := bt.Delete(EncodeIntKey(rowID)); err != nil {
		return fmt.Errorf("btree delete: %w", err)
	}
	// BTree 合并可能导致根节点变化（简化版暂不合并，但保留检查）
	if bt.rootPageID != table.RootPageID {
		table.RootPageID = bt.rootPageID
	}
	return tm.persistTableMeta(table)
}

// GetPager 返回底层 Pager
func (tm *TableManager) GetPager() *Pager {
	return tm.pager
}

// SelectAllWithRowID 返回所有行和对应的 rowID
func (tm *TableManager) SelectAllWithRowID(tableName string) ([]*Row, []int, error) {
	table, ok := tm.tables[tableName]
	if !ok {
		return nil, nil, fmt.Errorf("table %s not found", tableName)
	}

	bt := LoadBTree(tm.pager, table.RootPageID)
	pairs, err := bt.RangeScan(EncodeIntKey(0), EncodeIntKey(table.NextRowID))
	if err != nil {
		return nil, nil, fmt.Errorf("btree scan: %w", err)
	}

	var rows []*Row
	var rowIDs []int
	for _, pair := range pairs {
		row, err := table.DeserializeRow(pair.Value)
		if err != nil {
			return nil, nil, fmt.Errorf("deserialize row %d: %w", pair.Key, err)
		}
		rows = append(rows, row)
		rowIDs = append(rowIDs, DecodeIntKey(pair.Key))
	}
	return rows, rowIDs, nil
}

// PersistTableMeta 公开持久化表元数据接口
func (tm *TableManager) PersistTableMeta(table *Table) error {
	return tm.persistTableMeta(table)
}

// CreateIndex 创建二级索引：分配 BTree，遍历全表数据填充索引
func (tm *TableManager) CreateIndex(tableName, indexName string, columns []string, unique bool) error {
	table, ok := tm.tables[tableName]
	if !ok {
		return fmt.Errorf("table %s not found", tableName)
	}
	// 检查索引名是否已存在
	for _, idx := range table.Indexes {
		if idx.Name == indexName {
			return fmt.Errorf("index %s already exists", indexName)
		}
	}

	// 创建新 BTree 作为索引
	bt := NewBTree(tm.pager)
	idx := Index{
		Name:       indexName,
		Columns:    columns,
		RootPageID: bt.rootPageID,
		Unique:     unique,
	}
	table.Indexes = append(table.Indexes, idx)

	// 遍历全表数据，为每一行构建索引 key 并插入
	rows, rowIDs, err := tm.SelectAllWithRowID(tableName)
	if err != nil {
		return fmt.Errorf("scan table for index: %w", err)
	}

	for i, row := range rows {
		rowID := rowIDs[i]
		key, err := tm.buildIndexKey(table, idx, row, rowID)
		if err != nil {
			return err
		}
		// 二级索引 value = rowID
		if err := bt.Insert(key, EncodeIntKey(rowID)); err != nil {
			return fmt.Errorf("index insert: %w", err)
		}
	}
	// 更新索引的根页ID（分裂后可能变化）
	table.Indexes[len(table.Indexes)-1].RootPageID = bt.rootPageID

	return tm.persistTableMeta(table)
}

// buildIndexKey 根据索引列从行数据构建索引 key
// 格式: [col1_value][col2_value]...[rowid] — 确保唯一性
func (tm *TableManager) buildIndexKey(table *Table, idx Index, row *Row, rowID int) ([]byte, error) {
	var buf []byte
	for _, colName := range idx.Columns {
		colIdx := -1
		for j, col := range table.Columns {
			if strings.EqualFold(col.Name, colName) {
				colIdx = j
				break
			}
		}
		if colIdx == -1 {
			return nil, fmt.Errorf("column %s not found", colName)
		}
		val := row.Values[colIdx]
		if val == nil {
			// NULL 用特殊标记
			buf = append(buf, 0x00)
			continue
		}
		buf = append(buf, 0x01) // non-NULL marker
		switch table.Columns[colIdx].Type {
		case TypeInt:
			v, _ := val.(int)
			b := make([]byte, 8)
			binary.BigEndian.PutUint64(b, uint64(v))
			buf = append(buf, b...)
		case TypeVarchar:
			v, _ := val.(string)
			buf = append(buf, []byte(v)...)
			buf = append(buf, 0x00) // string terminator
		case TypeBool:
			v, _ := val.(bool)
			if v {
				buf = append(buf, 0x01)
			} else {
				buf = append(buf, 0x00)
			}
		}
	}
	// 最后追加 rowID，确保即使索引列值相同也能区分不同行
	buf = append(buf, EncodeIntKey(rowID)...)
	return buf, nil
}

// insertIntoPageInternal 核心插入逻辑
// noFlush=true 时事务模式：只改内存，不刷盘，返回 slotIdx
func (tm *TableManager) insertIntoPageInternal(pageID uint32, rowData []byte, noFlush bool) (uint32, int, error) {
	page := tm.pager.GetPage(pageID)
	page.Lock()
	defer page.Unlock()

	freeOffset := getFreeOffset(page.data)
	recordCount := getRecordCount(page.data)

	rowSize := 2 + len(rowData)
	slotDirStart := PageSize - (recordCount+1)*2
	available := slotDirStart - int(freeOffset)

	if available >= rowSize {
		offset := int(freeOffset)
		binary.LittleEndian.PutUint16(page.data[offset:offset+2], uint16(len(rowData)))
		copy(page.data[offset+2:offset+2+len(rowData)], rowData)

		slotIdx := recordCount
		slotOffset := PageSize - (slotIdx+1)*2
		binary.LittleEndian.PutUint16(page.data[slotOffset:slotOffset+2], uint16(offset))

		setRecordCount(page.data, recordCount+1)
		setFreeOffset(page.data, uint16(offset+rowSize))
		page.SetDirty(true)

		if !noFlush {
			page.Unlock()
			err := tm.pager.Flush(pageID)
			page.Lock()
			if err != nil {
				return pageID, slotIdx, err
			}
		}
		return pageID, slotIdx, nil
	}

	// 当前页满了，看看有没有下一页
	nextPageID := getNextPageID(page.data)
	if nextPageID != 0 {
		return tm.insertIntoPageInternal(nextPageID, rowData, noFlush)
	}

	// 分配新页
	newPageID, err := tm.pager.Allocate()
	if err != nil {
		return 0, 0, err
	}

	newPage := tm.pager.GetPage(newPageID)
	newPage.setHeaderType(PageTypeTableData)
	setRecordCount(newPage.data, 0)
	setFreeOffset(newPage.data, 32)
	setNextPageID(newPage.data, 0)
	setPrevPageID(newPage.data, pageID)
	newPage.SetDirty(true)

	setNextPageID(page.data, newPageID)
	page.SetDirty(true)

	if !noFlush {
		page.Unlock()
		err = tm.pager.Flush(pageID)
		page.Lock()
		if err != nil {
			return pageID, 0, err
		}
	}

	return tm.insertIntoPageInternal(newPageID, rowData, noFlush)
}

// insertIntoPage 兼容旧接口，调用内部方法
func (tm *TableManager) insertIntoPage(pageID uint32, rowData []byte) error {
	_, _, err := tm.insertIntoPageInternal(pageID, rowData, false)
	return err
}
func (tm *TableManager) SelectAll(tableName string) ([]*Row, error) {
	table, ok := tm.tables[tableName]
	if !ok {
		return nil, fmt.Errorf("table %s not found", tableName)
	}

	bt := LoadBTree(tm.pager, table.RootPageID)
	pairs, err := bt.RangeScan(EncodeIntKey(0), EncodeIntKey(table.NextRowID))
	if err != nil {
		return nil, fmt.Errorf("btree scan: %w", err)
	}

	var rows []*Row
	for _, pair := range pairs {
		row, err := table.DeserializeRow(pair.Value)
		if err != nil {
			return nil, fmt.Errorf("deserialize row %d: %w", pair.Key, err)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// readRowsFromPage 从单个页读取所有行（跳过已删除的 slot）
func (tm *TableManager) readRowsFromPage(page *Page, table *Table) ([]*Row, error) {
	recordCount := getRecordCount(page.data)
	if recordCount == 0 {
		return nil, nil
	}

	var rows []*Row
	for i := 0; i < recordCount; i++ {
		// 从 slot directory 读偏移
		slotOffset := PageSize - (i+1)*2
		rowOffset := int(binary.LittleEndian.Uint16(page.data[slotOffset : slotOffset+2]))

		// 跳过已删除的行（offset == 0xFFFF）
		if rowOffset == 0xFFFF {
			continue
		}

		// 读长度前缀
		rowLen := int(binary.LittleEndian.Uint16(page.data[rowOffset : rowOffset+2]))
		rowData := page.data[rowOffset+2 : rowOffset+2+rowLen]

		row, err := table.DeserializeRow(rowData)
		if err != nil {
			return nil, fmt.Errorf("deserialize row %d: %w", i, err)
		}
		rows = append(rows, row)
	}

	return rows, nil
}

// getFirstDataPage 从表元数据获取首数据页ID（简化：从 page 0 的 JSON 中解析）
func (tm *TableManager) getFirstDataPage(tableName string) uint32 {
	metaPage := tm.pager.GetPage(0)
	count := binary.LittleEndian.Uint32(metaPage.Data()[0:4])
	if count == 0 {
		return 0
	}

	data := bytes.TrimRight(metaPage.Data()[8:], "\x00")
	var allMetas []map[string]interface{}
	json.Unmarshal(data, &allMetas)

	for _, m := range allMetas {
		if m["name"] == tableName {
			// 解析 firstDataPage (JSON number -> float64)
			switch v := m["firstDataPage"].(type) {
			case float64:
				return uint32(v)
			case uint32:
				return v
			case json.Number:
				f, _ := v.Float64()
				return uint32(f)
			}
		}
	}
	return 0
}

// === Page 行存储辅助函数 ===

// [16:18] recordCount
func getRecordCount(data []byte) int {
	return int(binary.LittleEndian.Uint16(data[16:18]))
}
func setRecordCount(data []byte, count int) {
	binary.LittleEndian.PutUint16(data[16:18], uint16(count))
}

// [18:20] freeOffset
func getFreeOffset(data []byte) uint16 {
	return binary.LittleEndian.Uint16(data[18:20])
}
func setFreeOffset(data []byte, offset uint16) {
	binary.LittleEndian.PutUint16(data[18:20], offset)
}

// [20:24] nextPageID
func getNextPageID(data []byte) uint32 {
	return binary.LittleEndian.Uint32(data[20:24])
}
func setNextPageID(data []byte, id uint32) {
	binary.LittleEndian.PutUint32(data[20:24], id)
}

// [24:28] prevPageID
func getPrevPageID(data []byte) uint32 {
	return binary.LittleEndian.Uint32(data[24:28])
}
func setPrevPageID(data []byte, id uint32) {
	binary.LittleEndian.PutUint32(data[24:28], id)
}

// [28:32] firstDataPage（首数据页ID，冗余存储方便恢复）
func getFirstDataPage(data []byte) uint32 {
	return binary.LittleEndian.Uint32(data[28:32])
}
func setFirstDataPage(data []byte, id uint32) {
	binary.LittleEndian.PutUint32(data[28:32], id)
}
