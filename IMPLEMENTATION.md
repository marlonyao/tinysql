# TinySQL 实现详解

## 项目总览

TinySQL 是一个从零实现的极简关系型数据库，Go 语言编写，标准库 only。

**已完成的 3 个 Phase：**
- **Phase 1**: 存储引擎骨架（Page、Pager、TableManager）
- **Phase 2**: B+Tree 索引
- **Phase 3**: SQL 解析器 + 执行器

**总计 25 个测试全部通过。**

```
tinysql/
├── go.mod
├── storage/              # Phase 1-2: 存储引擎
│   ├── page.go          # 4KB Page 结构 + header + CRC32
│   ├── pager.go         # 页分配/缓存/刷盘
│   ├── pager_test.go    # 3 个测试
│   ├── table.go         # 表元数据 + 行格式序列化
│   ├── table_test.go    # 4 个测试
│   ├── table_manager.go # 表管理器（创建/插入/扫描/恢复）
│   ├── table_manager_test.go  # 5 个测试
│   ├── btree.go         # Phase 2: B+Tree 索引
│   └── btree_test.go    # 4 个测试
└── sql/                 # Phase 3: SQL 层
    ├── parser.go        # 分词器 + 递归下降 Parser
    ├── parser_test.go   # 6 个测试
    ├── executor.go      # SQL 执行器
    └── executor_test.go # 4 个测试
```

---

## Phase 1: 存储引擎骨架

### 1.1 Page —— 磁盘页的基本单位

**文件**: `storage/page.go`

```go
const PageSize = 4096        // 4KB，与 OS 页对齐
const PageHeaderSize = 16    // 页头占前 16 字节
```

**页结构**（4096 bytes = 16B header + 4080B data）：

```
[0:4]   pageID        — 页编号
[4:8]   pageType      — 类型：Meta/Table/Index/Free
[8:12]  checksum      — CRC32 校验（仅 data 区）
[12:16] reserved      — 保留
[16:4096] data        — 实际数据
```

**核心方法**:

```go
type Page struct {
    id       uint32
    data     [PageSize]byte
    dirty    bool
    mu       sync.Mutex  // 并发安全
}

func (p *Page) Data() []byte      { return p.data[PageHeaderSize:] }  // 返回 4080 bytes
func (p *Page) SetDirty(v bool) { p.dirty = v }
func (p *Page) ComputeChecksum() uint32 { return crc32.ChecksumIEEE(p.data[PageHeaderSize:]) }
```

**设计要点**:
- 页头存在 `data[0:16]`，对外暴露 `Data()` 返回 `data[16:]`，避免用户代码直接操作 header
- CRC32 校验写时计算、读时验证，发现损坏可报错
- `sync.Mutex` 保护页内数据操作

---

### 1.2 Pager —— 页管理器

**文件**: `storage/pager.go`

负责**分配新页、缓存、刷盘、重启恢复**。

```go
type Pager struct {
    file      *os.File          // 数据库文件
    pages     map[uint32]*Page   // 页缓存
    nextPageID uint32            // 下一个待分配的页编号
}
```

**关键流程**:

#### 分配新页
```go
func (p *Pager) Allocate() (uint32, error) {
    pageID := p.nextPageID
    p.nextPageID++
    
    page := &Page{id: pageID, dirty: true}
    page.setHeaderType(PageTypeFree)
    page.ComputeAndStoreChecksum()
    
    p.pages[pageID] = page
    
    // 立即写入文件（预分配空间）
    offset := int64(pageID) * PageSize
    p.file.WriteAt(page.data[:], offset)
    
    return pageID, nil
}
```

#### 读取页（带缓存）
```go
func (p *Pager) GetPage(pageID uint32) *Page {
    if page, ok := p.pages[pageID]; ok {
        return page  // 缓存命中
    }
    // 从磁盘读
    page := &Page{id: pageID}
    offset := int64(pageID) * PageSize
    p.file.ReadAt(page.data[:], offset)
    p.pages[pageID] = page
    return page
}
```

#### 刷盘
```go
func (p *Pager) Flush(pageID uint32) error {
    page := p.GetPage(pageID)
    page.mu.Lock()
    defer page.mu.Unlock()
    
    page.ComputeAndStoreChecksum()
    offset := int64(pageID) * PageSize
    _, err := p.file.WriteAt(page.data[:], offset)
    page.dirty = false
    return err
}
```

**注意**: `Flush` 内部会 `GetPage`，而 `GetPage` 可能返回已缓存的页。如果调用方已持有 `page.mu`，这里会**死锁**。修复方案是在调用 `Flush` 前先 `Unlock`。

---

### 1.3 Table —— 表元数据 + 行格式

**文件**: `storage/table.go`

#### 列定义

```go
type ColumnType int
const (
    TypeInt     ColumnType = 1
    TypeVarchar ColumnType = 2
    TypeBool    ColumnType = 3
)

type Column struct {
    Name string
    Type ColumnType
}
```

#### 行格式（紧凑二进制）

**序列化格式 v3**:

```
[null bitmap]        — 每个 bit 表示一列是否为 NULL（字节对齐）
[定长字段区]          — INT(4B) / BOOL(1B)。VARCHAR 写 2B offset 占位
[变长字段区]          — VARCHAR: [2B length][N B data]
```

**VARCHAR null 标记**: 如果 VARCHAR 为 NULL，在定长区写 `0xFFFF` 表示 offset 无效。

```go
func (t *Table) SerializeRow(row *Row) ([]byte, error) {
    n := len(t.Columns)
    
    // 1. null bitmap
    bitmapBytes := (n + 7) / 8
    bitmap := make([]byte, bitmapBytes)
    fixedSize := 0
    for i, col := range t.Columns {
        if row.Values[i] == nil {
            bitmap[i/8] |= 1 << (i % 8)
        } else {
            switch col.Type {
            case TypeInt: fixedSize += 4
            case TypeBool: fixedSize += 1
            case TypeVarchar: fixedSize += 2 // offset 占位
            }
        }
    }
    
    // 2. 计算变长区
    var varcharData []byte
    var varcharOffsets []int
    for i, col := range t.Columns {
        if col.Type == TypeVarchar && row.Values[i] != nil {
            varcharOffsets = append(varcharOffsets, len(varcharData))
            s := row.Values[i].(string)
            varcharData = append(varcharData, byte(len(s)>>8), byte(len(s)))
            varcharData = append(varcharData, []byte(s)...)
        }
    }
    
    // 3. 组装
    result := make([]byte, 0, bitmapBytes+fixedSize+len(varcharData))
    result = append(result, bitmap...)
    
    // 定长区
    varcharIdx := 0
    for i, col := range t.Columns {
        if row.Values[i] == nil {
            continue
        }
        switch col.Type {
        case TypeInt:
            v := row.Values[i].(int)
            result = append(result, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
        case TypeBool:
            if row.Values[i].(bool) { result = append(result, 1) } else { result = append(result, 0) }
        case TypeVarchar:
            if row.Values[i] == nil {
                result = append(result, 0xFF, 0xFF)
            } else {
                off := bitmapBytes + fixedSize + varcharOffsets[varcharIdx]
                result = append(result, byte(off), byte(off>>8))
                varcharIdx++
            }
        }
    }
    
    result = append(result, varcharData...)
    return result, nil
}
```

---

### 1.4 TableManager —— 表级操作

**文件**: `storage/table_manager.go`

管理**所有表的元数据**和**行数据存储**。

```go
type TableManager struct {
    pager  *Pager
    tables map[string]*Table   // 内存中的表定义缓存
}
```

#### Meta Page（page 0）布局

```
Data()[0:4]   = nextPageID    — Pager 管理，不要碰
Data()[4:8]   = tableCount    — 表数量
Data()[8:]    = JSON 元数据   — 所有表的定义
```

JSON 格式:
```json
[
  {"name": "users", "columns": [{"Name":"id","Type":1}, {"Name":"name","Type":2}], "firstDataPage": 1},
  {"name": "orders", "columns": [...], "firstDataPage": 5}
]
```

#### 创建表

```go
func (tm *TableManager) CreateTable(table *Table) error {
    // 1. 分配首数据页
    pageID, _ := tm.pager.Allocate()
    page := tm.pager.GetPage(pageID)
    page.setHeaderType(PageTypeTableData)
    setRecordCount(page.data, 0)
    setFreeOffset(page.data, 32)  // header 之后从 32 开始存数据
    setNextPageID(page.data, 0)
    page.SetDirty(true)
    tm.pager.Flush(pageID)
    
    // 2. 表定义 + firstDataPage 写入 Meta Page
    table.FirstDataPage = pageID
    tm.tables[table.Name] = table
    tm.persistTableMeta()  // 重写 page 0 的 JSON
    
    return nil
}
```

#### 插入行

```go
func (tm *TableManager) Insert(tableName string, row *Row) error {
    table := tm.tables[tableName]
    rowData, _ := table.SerializeRow(row)
    
    firstPageID := tm.getFirstDataPage(tableName)
    return tm.insertIntoPage(firstPageID, rowData)
}

func (tm *TableManager) insertIntoPage(pageID uint32, rowData []byte) error {
    page := tm.pager.GetPage(pageID)
    page.Lock()
    defer page.Unlock()
    
    freeOffset := getFreeOffset(page.data)
    recordCount := getRecordCount(page.data)
    
    // 行格式: [2B len][rowData]
    rowSize := 2 + len(rowData)
    
    // slot directory 从页尾往前，每个 slot 2B
    slotDirStart := PageSize - (recordCount+1)*2
    available := slotDirStart - int(freeOffset)
    
    if available >= rowSize {
        // 写入
        offset := int(freeOffset)
        binary.LittleEndian.PutUint16(page.data[offset:offset+2], uint16(len(rowData)))
        copy(page.data[offset+2:], rowData)
        
        // 更新 slot directory
        slotOffset := PageSize - (recordCount+1)*2
        binary.LittleEndian.PutUint16(page.data[slotOffset:slotOffset+2], uint16(offset))
        
        // 更新 header
        setRecordCount(page.data, recordCount+1)
        setFreeOffset(page.data, uint16(offset+rowSize))
        page.SetDirty(true)
        
        // 先解锁再 Flush，避免死锁
        page.Unlock()
        err := tm.pager.Flush(pageID)
        page.Lock()
        return err
    }
    
    // 页满了，分配新页
    newPageID, _ := tm.pager.Allocate()
    // ... 初始化新页，链到当前页
    // 递归插入新页
    return tm.insertIntoPage(newPageID, rowData)
}
```

**数据页布局**:

```
[0:16]     Page Header (type=TableData)
[16:20]    recordCount    — 记录数
[20:24]    freeOffset       — 空闲区起始
[24:28]    nextPageID       — 下一页（链表）
[28:32]    prevPageID       — 上一页
[32:...]   行数据区        — [2B len][rowData]
[...:4096] slot directory  — 从页尾往前，每个 2B 存行偏移
```

**Slot Directory** 的作用：行数据可能变长，通过 slot directory 可以 O(1) 定位任意行。

#### 全表扫描

```go
func (tm *TableManager) SelectAll(tableName string) ([]*Row, error) {
    firstPageID := tm.getFirstDataPage(tableName)
    
    var rows []*Row
    pageID := firstPageID
    for pageID != 0 {
        page := tm.pager.GetPage(pageID)
        rows = append(rows, tm.readRowsFromPage(page, table)...)
        pageID = getNextPageID(page.data)  // 沿链表遍历
    }
    return rows, nil
}
```

#### 重启恢复

```go
func (tm *TableManager) LoadTables() error {
    metaPage := tm.pager.GetPage(0)
    count := binary.LittleEndian.Uint32(metaPage.Data()[4:8])
    if count == 0 { return nil }
    
    data := bytes.TrimRight(metaPage.Data()[8:], "\x00")
    var allMetas []map[string]interface{}
    json.Unmarshal(data, &allMetas)
    
    for _, m := range allMetas {
        name := m["name"].(string)
        // 解析 columns...
        tm.tables[name] = &Table{...}
    }
    return nil
}
```

---

## Phase 2: B+Tree 索引

**文件**: `storage/btree.go`

### 节点页布局

```
B+Tree Node Header (20 bytes):
[0:2]    numKeys       — key 数量
[2]      nodeType      — 1=Leaf, 2=Internal
[3:7]    rightSibling  — 右兄弟页ID（叶子链表）
[7:11]   leftSibling   — 左兄弟页ID
[11:15]  parent        — 父节点页ID
[15]     isRoot        — 是否为根
[16:20]  firstChild    — 内部节点：最左孩子
          或 freeOffset — 叶子节点：复用此字段（未用）

Entry 区 (20 bytes 之后):
叶子节点: [key: 4B][valLen: 2B][value: N B] — 变长
内部节点: [key: 4B][childPageID: 4B] — 定长 8B
```

### 核心结构

```go
type BTree struct {
    pager      *Pager
    rootPageID uint32
}

// 叶子节点 entry
type leafEntry struct {
    key   int
    value []byte
}

// 内部节点 entry  
type internalEntry struct {
    key         int
    childPageID uint32
}
```

### 查找 Search

```go
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
        
        // 内部节点：二分查找子节点
        entries := na.internalEntries()
        firstChild := na.firstChild()
        
        pageID = firstChild
        for _, e := range entries {
            if key >= e.key {
                pageID = e.childPageID
            } else {
                break
            }
        }
    }
}
```

### 插入 Insert

```go
func (bt *BTree) Insert(key int, value []byte) error {
    rootPage := bt.pager.GetPage(bt.rootPageID)
    newRoot, err := bt.insertIntoNode(rootPage, key, value)
    if newRoot != 0 {
        bt.rootPageID = newRoot  // 根分裂，更新根
    }
    return err
}
```

**插入流程**:
1. 从根开始递归找到叶子节点
2. 尝试直接插入叶子
3. 叶子满了 → **分裂**:
   - 把 entry 平分成两半
   - 原页保留左半，新页存右半
   - 右半的最小 key 提到父节点
   - 维护叶子链表（left/right sibling）
4. 父节点也满了 → **递归分裂**
5. 根分裂 → **树高 +1**，创建新根内部节点

### 叶子分裂

```go
func (bt *BTree) splitLeaf(page *Page, key int, value []byte) (uint32, error) {
    na := newNodeAccessor(page)
    entries := na.leafEntries()
    
    // 找到插入位置并插入
    idx := sort.Search(...)
    // ... 插入到 entries ...
    
    // 平分成两半
    mid := (len(entries) + 1) / 2
    leftEntries := entries[:mid]
    rightEntries := entries[mid:]
    
    // 分配新页
    newPageID, _ := bt.pager.Allocate()
    newPage := bt.pager.GetPage(newPageID)
    initLeafNode(newPage)
    newNa := newNodeAccessor(newPage)
    newNa.setLeafEntries(rightEntries)
    newNa.setLeftSibling(page.ID())
    newNa.setRightSibling(na.rightSibling())
    
    // 更新原页
    na.setLeafEntries(leftEntries)
    na.setRightSibling(newPageID)
    
    // 如果是根，创建新根
    if na.isRoot() {
        return bt.createNewRoot(page.ID(), rightEntries[0].key, newPageID)
    }
    
    // 递归插入父节点
    parentPage := bt.pager.GetPage(na.parent())
    return bt.insertIntoNode(parentPage, rightEntries[0].key, nil)
}
```

### 范围扫描 RangeScan

利用**叶子节点的链表**实现高效范围扫描：

```go
func (bt *BTree) RangeScan(start, end int) ([]KVPair, error) {
    // 1. 找到包含 start 的叶子
    pageID := bt.rootPageID
    for {
        page := bt.pager.GetPage(pageID)
        na := newNodeAccessor(page)
        if na.nodeType() == NodeTypeLeaf { break }
        // ... 找到子节点 ...
    }
    
    // 2. 沿叶子链表遍历
    var results []KVPair
    for pageID != 0 {
        page := bt.pager.GetPage(pageID)
        na := newNodeAccessor(page)
        entries := na.leafEntries()
        
        for _, e := range entries {
            if e.key < start { continue }
            if e.key > end { return results, nil }
            results = append(results, KVPair{e.key, e.value})
        }
        
        pageID = na.rightSibling()  // 跳到右兄弟
    }
    return results, nil
}
```

---

## Phase 3: SQL 解析器 + 执行器

### 3.1 分词器 Tokenizer

**文件**: `sql/parser.go`

```go
type TokenType int
const (
    TokenEOF TokenType = iota
    TokenKeyword      // CREATE, TABLE, SELECT, ...
    TokenIdentifier   // 表名、列名
    TokenNumber       // 123
    TokenString       // 'hello'
    TokenSymbol       // (, ), ,, ;, =, <, >, ...
)
```

分词逻辑：
1. 跳过空白字符
2. `'` 开头 → 读到下一个 `'` → TokenString
3. 数字开头 → 连续数字 → TokenNumber
4. 字母开头 → 连续字母/数字/下划线 → 检查是否在 keyword 表 → TokenKeyword/TokenIdentifier
5. 单个字符 → 检查是否是多字符符号（`<>`, `<=`, `>=`）→ TokenSymbol

### 3.2 AST 定义

```go
type Statement interface { stmtNode() }

type CreateTableStmt struct {
    TableName string
    Columns   []ColumnDef      // {Name, Type, Nullable}
}

type InsertStmt struct {
    TableName string
    Columns   []string         // 指定列
    Values    []Expr           // 值表达式
}

type SelectStmt struct {
    Columns   []string         // "*" 或 ["id", "name"]
    TableName string
    Where     Expr             // 可为 nil
}

type Expr interface { exprNode(); String() string }

type BinaryExpr struct { Op string; Left, Right Expr }   // AND, OR, =, <>, <, >, <=, >=
type Identifier struct { Name string }                    // 列引用
type Literal   struct { Value interface{} }               // 数字、字符串、NULL、true/false
```

### 3.3 递归下降 Parser

**优先级**（从高到低）:
1. `parsePrimaryExpr` — 数字、字符串、标识符、NULL
2. `parseComparison` — `=`, `<>`, `<`, `>`, `<=`, `>=`
3. `parseExpr` — `AND`, `OR`

```go
func (p *Parser) parseExpr() (Expr, error) {
    // AND/OR 优先级最低
    left, _ := p.parseComparison()
    for peek() 是 AND/OR {
        op := advance()
        right, _ := p.parseComparison()
        left = &BinaryExpr{Op: op, Left: left, Right: right}
    }
    return left, nil
}

func (p *Parser) parseComparison() (Expr, error) {
    left, _ := p.parsePrimaryExpr()
    if peek() 是 =, <>, <, >, <=, >= {
        op := advance()
        right, _ := p.parsePrimaryExpr()
        return &BinaryExpr{Op: op, Left: left, Right: right}, nil
    }
    return left, nil
}
```

**解析示例**:
```sql
SELECT * FROM users WHERE age >= 18 AND name = 'Bob';
```

AST:
```
SelectStmt
  Columns: ["*"]
  TableName: "users"
  Where: BinaryExpr("AND")
    Left:  BinaryExpr(">=")
      Left:  Identifier("age")
      Right: Literal(18)
    Right: BinaryExpr("=")
      Left:  Identifier("name")
      Right: Literal("Bob")
```

### 3.4 Executor 执行器

**文件**: `sql/executor.go`

```go
type Executor struct {
    tm *storage.TableManager  // 依赖存储引擎
}

func (e *Executor) Execute(stmt Statement) (Result, error) {
    switch s := stmt.(type) {
    case *CreateTableStmt: return e.executeCreateTable(s)
    case *InsertStmt:      return e.executeInsert(s)
    case *SelectStmt:      return e.executeSelect(s)
    }
}
```

#### 执行 CREATE TABLE

```go
func (e *Executor) executeCreateTable(stmt *CreateTableStmt) (Result, error) {
    columns := make([]storage.Column, len(stmt.Columns))
    for i, col := range stmt.Columns {
        columns[i] = storage.Column{
            Name: col.Name,
            Type: parseColumnType(col.Type),  // INT→TypeInt, VARCHAR→TypeVarchar
        }
    }
    table := &storage.Table{Name: stmt.TableName, Columns: columns}
    e.tm.CreateTable(table)
    return &CreateTableResult{Message: "Table created"}, nil
}
```

#### 执行 INSERT

```go
func (e *Executor) executeInsert(stmt *InsertStmt) (Result, error) {
    table := e.tm.GetTable(stmt.TableName)
    
    // 构建 Row，按表定义的列顺序
    row := &storage.Row{Values: make([]interface{}, len(table.Columns))}
    for i := range row.Values { row.Values[i] = nil }  // 默认 NULL
    
    for i, colName := range stmt.Columns {
        colIdx := findColumnIndex(table, colName)
        val := evalExpr(stmt.Values[i], table.Columns[colIdx].Type)
        row.Values[colIdx] = val
    }
    
    e.tm.Insert(stmt.TableName, row)
    return &InsertResult{RowsAffected: 1}, nil
}
```

#### 执行 SELECT

```go
func (e *Executor) executeSelect(stmt *SelectStmt) (Result, error) {
    table := e.tm.GetTable(stmt.TableName)
    rows, _ := e.tm.SelectAll(stmt.TableName)
    
    // WHERE 过滤
    var filtered []*storage.Row
    for _, row := range rows {
        if stmt.Where == nil || evalWhere(stmt.Where, row, table) {
            filtered = append(filtered, row)
        }
    }
    
    // 投影列
    colNames := expandColumns(stmt.Columns, table)  // "*" → 所有列
    result := projectRows(filtered, colNames, table)
    
    return &SelectResult{Columns: colNames, Rows: result}, nil
}
```

#### WHERE 条件求值

```go
func evalWhere(expr Expr, row *storage.Row, table *storage.Table) bool {
    switch e := expr.(type) {
    case *BinaryExpr:
        left := evalExpr(e.Left, row, table)   // 取值
        right := evalExpr(e.Right, row, table)
        
        switch e.Op {
        case "AND": return toBool(left) && toBool(right)
        case "OR":  return toBool(left) || toBool(right)
        case "=":   return compareEqual(left, right)
        case "<>":  return !compareEqual(left, right)
        case "<":   return compareLess(left, right)
        case ">":   return compareLess(right, left)
        // ...
        }
    }
}

func evalExpr(expr Expr, row *storage.Row, table *storage.Table) interface{} {
    switch e := expr.(type) {
    case *Identifier:
        // 从 row 中取列值
        for i, col := range table.Columns {
            if col.Name == e.Name { return row.Values[i] }
        }
    case *Literal:
        return e.Value
    }
}
```

---

## 测试覆盖

### storage 包（15 个测试）

| 测试 | 验证内容 |
|------|---------|
| TestPagerAllocateAndRead | 分配页、写数据、读回验证 |
| TestPagerMultiplePages | 多页分配、独立读写 |
| TestPagerPageSize | 页大小 = 4096 |
| TestTableSerializeDeserialize | 行格式序列化/反序列化 |
| TestTableWithNulls | NULL 值处理 |
| TestTableRowSize | 行大小计算 |
| TestColumnTypeString | 类型字符串 |
| TestTableManagerCreateAndInsert | 创建表、插入行、读取 |
| TestTableManagerRecovery | 重启后恢复表结构和数据 |
| TestTableManagerMultiPage | 多页扩展、页满自动分配 |
| TestTableManagerDuplicateTable | 重复表名报错 |
| TestBTreeInsertAndSearch | B+Tree 插入/查找 |
| TestBTreeInsertMany | 100 条数据触发分裂 |
| TestBTreeRangeScan | 范围扫描 |
| TestBTreeUpdate | 同一 key 覆盖更新 |

### sql 包（10 个测试）

| 测试 | 验证内容 |
|------|---------|
| TestParseCreateTable | CREATE TABLE 解析（列名、类型） |
| TestParseInsert | INSERT 解析（列、值） |
| TestParseSelect | SELECT + WHERE 解析 |
| TestParseSelectStar | SELECT * |
| TestParseSelectWhereAnd | AND 条件 |
| TestParseInsertNull | NULL 值 |
| TestExecutorCreateAndInsert | 端到端：建表→插入→查询 |
| TestExecutorWhereClause | WHERE 过滤（>、=） |
| TestExecutorSelectColumns | 指定列投影 |
| TestExecutorNullValue | NULL 值处理 |

---

## 关键设计决策

1. **Page 0 是 Meta Page**：存 `nextPageID` + 表定义 JSON。重启时从 page 0 恢复所有表结构。

2. **数据页用 Slot Directory**：行数据变长，slot directory（页尾偏移数组）支持 O(1) 随机访问任意行。

3. **行格式紧凑二进制**：null bitmap + 定长区 + 变长区。比 JSON/CSV 节省 50%+ 空间。

4. **B+Tree 而非 B-Tree**：叶子节点链表支持高效范围扫描，所有数据在叶子，内部节点只存 key。

5. **锁策略**：页级 `sync.Mutex`。`Flush` 时先 `Unlock` 避免死锁。

6. **SQL 解析器手写**：递归下降，无 yacc/ANTLR 依赖。支持 `CREATE`, `INSERT`, `SELECT` + `WHERE`。

---

## 下一步（Phase 4-5）

- **Phase 4**: 事务 — WAL（Write-Ahead Log）+ 简单行级锁（两阶段锁）
- **Phase 5**: CLI 客户端 — 交互式命令行，读取用户输入 SQL，打印结果表格
