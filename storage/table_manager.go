package storage

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"fmt"
)

// PageTypeTableData 是存行数据的页类型
const PageTypeTableData = 5

// TableManager 管理所有表和行数据
type TableManager struct {
	pager  *Pager
	tables map[string]*Table // 内存中的表定义
}

// NewTableManager 创建表管理器
func NewTableManager(pager *Pager) *TableManager {
	return &TableManager{
		pager:  pager,
		tables: make(map[string]*Table),
	}
}

// CreateTable 创建新表（持久化表元数据 + 分配首数据页）
func (tm *TableManager) CreateTable(table *Table) error {
	if _, exists := tm.tables[table.Name]; exists {
		return fmt.Errorf("table %s already exists", table.Name)
	}

	// 分配首数据页
	firstPageID, err := tm.pager.Allocate()
	if err != nil {
		return fmt.Errorf("allocate first data page: %w", err)
	}

	// 初始化数据页
	dataPage := tm.pager.GetPage(firstPageID)
	dataPage.setHeaderType(PageTypeTableData)
	setRecordCount(dataPage.data, 0)
	setFreeOffset(dataPage.data, 32) // 记录区从 32 开始
	setNextPageID(dataPage.data, 0)
	setPrevPageID(dataPage.data, 0)
	setFirstDataPage(dataPage.data, firstPageID)
	dataPage.SetDirty(true)

	// 持久化表定义到页
	if err := tm.persistTableMeta(table, firstPageID); err != nil {
		return fmt.Errorf("persist table meta: %w", err)
	}

	tm.tables[table.Name] = table
	return tm.pager.Flush(firstPageID)
}

// persistTableMeta 把表定义序列化存到系统区域
// 方案：用 page 0 的 data 区存所有表定义的 JSON
func (tm *TableManager) persistTableMeta(table *Table, firstDataPage uint32) error {
	meta := map[string]interface{}{
		"name":          table.Name,
		"columns":       table.Columns,
		"firstDataPage": firstDataPage,
	}

	// 读出现有表定义列表，追加
	metaPage := tm.pager.GetPage(0)
	
	// 简单方案：每次重写全部表定义
	allMetas := make([]map[string]interface{}, 0)
	count := binary.LittleEndian.Uint32(metaPage.Data()[4:8])
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
	// Data()[0:4]  = data[16:20] = nextPageID (managed by Pager, DON'T TOUCH)
	// Data()[4:8]  = data[20:24] = tableCount
	// Data()[8:]   = data[24:]   = 表元数据 JSON
	if len(allJSON) > len(metaPage.Data())-8 {
		return fmt.Errorf("table metadata too large for page 0")
	}
	
	binary.LittleEndian.PutUint32(metaPage.Data()[4:8], uint32(len(allMetas)))
	copy(metaPage.Data()[8:], allJSON)
	metaPage.SetDirty(true)
	
	return tm.pager.Flush(0)
}

// GetTable 获取表定义
func (tm *TableManager) GetTable(name string) (*Table, bool) {
	t, ok := tm.tables[name]
	return t, ok
}

// LoadTables 从磁盘恢复所有表定义
func (tm *TableManager) LoadTables() error {
	metaPage := tm.pager.GetPage(0)
	count := binary.LittleEndian.Uint32(metaPage.Data()[4:8])
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
		
		tm.tables[name] = &Table{
			Name:    name,
			Columns: columns,
		}
	}
	
	return nil
}

// Insert 向表中插入一行（非事务，立即刷盘）
func (tm *TableManager) Insert(tableName string, row *Row) error {
	table, ok := tm.tables[tableName]
	if !ok {
		return fmt.Errorf("table %s not found", tableName)
	}

	rowData, err := table.SerializeRow(row)
	if err != nil {
		return fmt.Errorf("serialize row: %w", err)
	}

	firstPageID := tm.getFirstDataPage(tableName)
	if firstPageID == 0 {
		return fmt.Errorf("table %s has no data page", tableName)
	}

	_, _, err = tm.insertIntoPageInternal(firstPageID, rowData, false)
	return err
}

// InsertTx 事务版插入，不刷盘，返回 (pageID, slotIdx, error)
func (tm *TableManager) InsertTx(tableName string, row *Row) (uint32, int, error) {
	table, ok := tm.tables[tableName]
	if !ok {
		return 0, 0, fmt.Errorf("table %s not found", tableName)
	}

	rowData, err := table.SerializeRow(row)
	if err != nil {
		return 0, 0, fmt.Errorf("serialize row: %w", err)
	}

	firstPageID := tm.getFirstDataPage(tableName)
	if firstPageID == 0 {
		return 0, 0, fmt.Errorf("table %s has no data page", tableName)
	}

	return tm.insertIntoPageInternal(firstPageID, rowData, true)
}

// DeleteSlot 标记删除指定位置的行（slot directory offset 设为 0xFFFF）
func (tm *TableManager) DeleteSlot(tableName string, pageID uint32, slotIdx int) error {
	page := tm.pager.GetPage(pageID)
	page.Lock()
	defer page.Unlock()

	recordCount := getRecordCount(page.data)
	if slotIdx < 0 || slotIdx >= recordCount {
		return fmt.Errorf("invalid slot index %d", slotIdx)
	}

	slotOffset := PageSize - (slotIdx+1)*2
	binary.LittleEndian.PutUint16(page.data[slotOffset:slotOffset+2], 0xFFFF)
	page.SetDirty(true)
	return nil
}

// GetPager 返回底层 Pager
func (tm *TableManager) GetPager() *Pager {
	return tm.pager
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

	firstPageID := tm.getFirstDataPage(tableName)
	if firstPageID == 0 {
		return nil, nil
	}

	var rows []*Row
	pageID := firstPageID
	for pageID != 0 {
		page := tm.pager.GetPage(pageID)
		pageRows, err := tm.readRowsFromPage(page, table)
		if err != nil {
			return nil, err
		}
		rows = append(rows, pageRows...)
		pageID = getNextPageID(page.data)
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
	count := binary.LittleEndian.Uint32(metaPage.Data()[4:8])
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
