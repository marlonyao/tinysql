# TinySQL 架构详解 —— 事务与 B+Tree 实现

> 文档生成时间：2026-05-04
> 对应版本：commit 待定（事务语法支持 + B+Tree 存储引擎）

---

## 目录

1. [B+Tree 存储引擎实现](#1-btree-存储引擎实现)
2. [事务系统实现](#2-事务系统实现)
3. [两者如何协同工作](#3-两者如何协同工作)
4. [代码路径速查](#4-代码路径速查)

---

## 1. B+Tree 存储引擎实现

### 1.1 为什么选择 B+Tree

TinySQL 使用 B+Tree 作为聚簇索引（Clustered Index），数据按 `_rowid` 顺序存储在叶子节点中。

**优点：**
- 范围查询高效（叶子节点形成有序链表）
- 插入/删除保持 O(log n)
- 磁盘友好（每个节点 = 一个 Page = 4KB）

### 1.2 节点布局

每个 Page（4KB）的前 16 字节是 **PageHeader**，接下来是 **NodeHeader**（32 字节），剩余空间存储数据。

```
Page 结构（4096 字节）：
├─ [0:16]   PageHeader    ── pageID, pageType, checksum
├─ [16:48]  NodeHeader    ── nodeType, numKeys, parentPageID, rightSibling, leftSibling, firstChild
└─ [48:4096] Data Area    ── 存储 entries
```

**NodeHeader 字段（偏移从 page.Data()[0] 开始）：**

| 偏移 | 大小 | 字段 | 说明 |
|------|------|------|------|
| 0 | 1B | nodeType | 1=Leaf, 2=Internal |
| 2 | 2B | numKeys | 当前节点 entry 数量 |
| 4 | 4B | parentPageID | 父节点 pageID（根节点为 0）|
| 8 | 4B | rightSibling | 右兄弟 pageID（叶子链表）|
| 12 | 4B | leftSibling | 左兄弟 pageID |
| 16 | 4B | firstChild | 第一个子节点 pageID（仅内部节点）|

> ⚠️ **关键设计决策**：NodeAccessor 使用 `page.Data()`（即 `page.data[16:]`），而不是 `page.data`，从而避开 PageHeader 占用的前 16 字节。如果直接用 `page.data`，`serializeHeader()` 会把 pageID 写入前 4 字节，覆盖 `nodeType` 和 `numKeys`。

### 1.3 叶子节点 Entry 格式

```
├─ [2B] keyLen
├─ [keyLen] key ([]byte)
├─ [2B] valLen
└─ [valLen] value ([]byte)
```

**INT 主键编码**：8 字节 BigEndian uint64，保证字节序与数值序一致。
```go
func encodeIntKey(v int) []byte {
    b := make([]byte, 8)
    binary.BigEndian.PutUint64(b, uint64(v))
    return b
}
```

### 1.4 B+Tree 核心操作

#### 插入流程

```
Insert(key, value)
  └─ 从根节点开始
     └─ 内部节点：遍历 entries，找到合适的 childPageID
        └─ 前向扫描：for i=0..n-1 { if key >= entry[i].key { pageID = entry[i].childPageID } else { break } }
     └─ 叶子节点：尝试插入 entry
        ├─ 页满 → splitLeaf()
        │   ├─ 将原叶子 entry 分为两半
        │   ├─ 分配新页，写入后半数据
        │   ├─ 设置 rightSibling / leftSibling 指针（维护有序链表）
        │   └─ 向父节点提升 splitKey
        │       └─ 父节点页满 → splitInternal()（递归向上）
        └─ 未满 → 直接插入，setDirty(true)，Flush()
```

#### 分裂时的根节点变化

当根节点分裂时，**创建新的内部节点作为新根**，原根变为子节点。这会导致 `rootPageID` 变化。

```go
// TableManager.Insert 中必须同步 rootPageID
if bt.rootPageID != table.RootPageID {
    table.RootPageID = bt.rootPageID
}
```

如果不同步，下次 Insert 会用旧的 rootPageID，数据被插到错误的子树。

#### 删除（简化版）

当前实现**只做叶子 entry 删除，不做节点合并**。功能正确，但可能导致空间利用率降低。

```
Delete(key)
  └─ 遍历到叶子节点
     └─ 找到 key，删除 entry
        ├─ 找到 → 重写 entries，setDirty，Flush
        └─ 未找到 → 静默成功
```

### 1.5 关键 Bug 与修复

| Bug | 现象 | 根因 | 修复 |
|-----|------|------|------|
| 数据不写入 | Insert 后 SelectAll 返回空 | PageHeader 覆盖 NodeHeader | `nodeAccessor` 使用 `page.Data()` |
| 数据重复 | 同一 key 出现在多个叶子 | 内部节点后向遍历截断 | 改为前向扫描 |
| RangeScan 乱序 | 遍历结果不递增 | sibling 链表顺序错乱 | 修复内部节点选择逻辑 + 同步 rootPageID |

---

## 2. 事务系统实现

### 2.1 WAL（Write-Ahead Logging）

事务系统基于 **WAL** 实现崩溃恢复。所有修改先写日志，再写数据。

#### WAL 文件格式

```
WAL 文件 = [Record] [Record] ...

Record 结构：
├─ [8B] LSN      ── 日志序列号（单调递增）
├─ [8B] TXID     ── 事务ID
├─ [1B] Type     ── BEGIN=1, INSERT=2, COMMIT=3, ROLLBACK=4
├─ [2B] TableNameLen
├─ [N]  TableName
├─ [8B] RowID     ── 被修改的行的 _rowid
├─ [4B] BeforeLen
├─ [N]  BeforeImage（修改前的行数据，用于 UNDO）
├─ [4B] AfterLen
└─ [N]  AfterImage（修改后的行数据，用于 REDO）
```

> Type=BEGIN 时，TableName/RowID/Before/After 都为空。

#### WAL 核心操作

```go
// OpenWAL(path)  → 打开或创建 WAL 文件
// Write(record)   → 追加记录到内存缓冲区
// Flush()         → 刷到磁盘（fsync）
// ReadAll()       → 读取所有记录（用于恢复）
// Truncate()      → 清空 WAL 文件（事务结束后）
```

### 2.2 事务生命周期

```
正常事务流程：
  BEGIN
    └─ WAL.Write({TXID, Type: WALBegin})
    └─ WAL.Flush()  // 确保 BEGIN 记录在磁盘
  INSERT INTO ...
    └─ tm.InsertTx(table, row) → 返回 rowID
    └─ WAL.Write({TXID, Type: WALInsert, TableName, RowID, After: rowData})
  COMMIT
    └─ WAL.Write({TXID, Type: WALCommit})
    └─ WAL.Flush()  // 确保 COMMIT 在磁盘
    └─ Flush 脏页    // 数据刷盘
    └─ WAL.Truncate() // 清理已提交事务的日志

回滚事务流程：
  BEGIN → INSERT → ROLLBACK
    └─ 读取 WAL 中该事务的所有记录
    └─ 倒序 UNDO：对每个 WALInsert，执行 DeleteByRowID()
    └─ WAL.Write({TXID, Type: WALRollback})
    └─ WAL.Truncate()
```

### 2.3 崩溃恢复（Recover）

```
Recover()
  └─ 读取 WAL 所有记录
  └─ 按 TXID 分组
  └─ 检查每个事务是否有 COMMIT 记录
     ├─ 有 COMMIT → 已提交，无需处理（数据已在磁盘）
     └─ 无 COMMIT → 未提交，倒序 UNDO
        └─ 对每个 WALInsert：DeleteByRowID(table, rowID)
  └─ WAL.Truncate() // 清理
```

**关键点**：
- COMMIT 记录在 WAL 中意味着事务已完成，即使数据页还没刷盘，重启后数据仍在（因为 Page 在 Pager 缓存中，或已刷盘）
- 没有 COMMIT 的事务必须 UNDO，保证原子性

### 2.4 SQL 层事务语法

Executor 持有 `currentTx` 字段，表示当前活跃事务：

```go
type Executor struct {
    tm        *storage.TableManager
    txm       *tx.TransactionManager
    currentTx *tx.Transaction
}
```

**执行路由：**
- `BEGIN` → `txm.Begin()`，保存到 `currentTx`
- `INSERT` → 如果 `currentTx != nil`，调用 `tx.Insert()`，否则调用 `tm.Insert()`
- `COMMIT` → `txm.Commit(currentTx)`，清空 `currentTx`
- `ROLLBACK` → `txm.Rollback(currentTx)`，清空 `currentTx`

---

## 3. 两者如何协同工作

### 3.1 事务插入的数据流

```
SQL: BEGIN; INSERT INTO users VALUES (1, 'Alice'); COMMIT;

SQL Parser ──→ Executor.executeBegin()
                  └─ txm.Begin()
                     └─ WAL.Write(BEGIN tx=1) ──→ 磁盘

SQL Parser ──→ Executor.executeInsert()
                  ├─ currentTx.Insert("users", row)
                  │   ├─ tm.InsertTx("users", row)
                  │   │   ├─ table.NextRowID++ → rowID=0
                  │   │   ├─ bt.Insert(encodeIntKey(0), serializedRow)
                  │   │   │   ├─ pager.Allocate()（如需分裂）
                  │   │   │   ├─ na.setLeafEntries(...) // NodeAccessor 操作 page.Data()
                  │   │   │   └─ pager.Flush(pageID)    // PageHeader + NodeHeader 一起刷盘
                  │   │   └─ tm.persistTableMeta(table) // 更新 rootPageID + NextRowID 到 meta page
                  │   └─ WAL.Write(INSERT tx=1, table=users, rowID=0, After=serializedRow)
                  └─ printResult: "Affected 1 row(s)"

SQL Parser ──→ Executor.executeCommit()
                  ├─ txm.Commit(currentTx)
                  │   ├─ WAL.Write(COMMIT tx=1) ──→ 磁盘
                  │   ├─ Flush 所有脏页
                  │   └─ WAL.Truncate()
                  └─ printResult: "Transaction committed"
```

### 3.2 崩溃场景

**场景：BEGIN → INSERT → 崩溃（未 COMMIT）**

重启后：
```
main.go:
  ├─ NewPager(dbPath)          → 打开数据库文件
  ├─ NewTableManager(pager)    → 自动 LoadTables()
  ├─ NewTransactionManager(...)  → 打开 WAL 文件
  └─ txm.Recover()             → 扫描 WAL
       └─ 发现 tx=1: [BEGIN, INSERT]（无 COMMIT）
            └─ UNDO: DeleteByRowID("users", 0)
                 └─ bt.Delete(encodeIntKey(0))
                      └─ 删除叶子 entry，Flush
       └─ WAL.Truncate()
```

重启后 `users` 表为空，原子性保证。

---

## 4. 代码路径速查

### B+Tree 相关

| 功能 | 文件 | 函数 |
|------|------|------|
| 节点访问 | `storage/btree.go` | `nodeAccessor` 结构体及方法 |
| 叶子初始化 | `storage/btree.go` | `initLeafNode()` |
| 内部节点初始化 | `storage/btree.go` | `initInternalNode()` |
| Entry 读写 | `storage/btree.go` | `leafEntries()`, `setLeafEntries()` |
| 插入 | `storage/btree.go` | `BTree.Insert()` |
| 分裂 | `storage/btree.go` | `splitLeaf()`, `splitInternal()` |
| 查找 | `storage/btree.go` | `BTree.Search()` |
| 范围扫描 | `storage/btree.go` | `BTree.RangeScan()` |
| 删除 | `storage/btree.go` | `BTree.Delete()` |
| INT key 编码 | `storage/btree.go` | `encodeIntKey()`, `decodeIntKey()` |
| 聚簇索引 Insert | `storage/table_manager.go` | `Insert()`, `InsertTx()` |
| 聚簇索引 Delete | `storage/table_manager.go` | `DeleteByRowID()` |
| 表元数据持久化 | `storage/table_manager.go` | `persistTableMeta()`, `LoadTables()` |

### 事务相关

| 功能 | 文件 | 函数 |
|------|------|------|
| WAL 打开 | `tx/wal.go` | `OpenWAL()` |
| WAL 写入 | `tx/wal.go` | `WAL.Write()` |
| WAL 读取 | `tx/wal.go` | `WAL.ReadAll()` |
| 开始事务 | `tx/tx.go` | `TransactionManager.Begin()` |
| 提交事务 | `tx/tx.go` | `TransactionManager.Commit()` |
| 回滚事务 | `tx/tx.go` | `TransactionManager.Rollback()` |
| 崩溃恢复 | `tx/tx.go` | `TransactionManager.Recover()` |
| 事务内插入 | `tx/tx.go` | `Transaction.Insert()` |
| SQL BEGIN | `sql/executor.go` | `Executor.executeBegin()` |
| SQL COMMIT | `sql/executor.go` | `Executor.executeCommit()` |
| SQL ROLLBACK | `sql/executor.go` | `Executor.executeRollback()` |
| 事务语法解析 | `sql/parser.go` | `parseBegin()`, `parseCommit()`, `parseRollback()` |

---

## 附录：测试覆盖

```bash
cd tinysql
go test ./...

# 预期结果
ok  tinysql/cmd/tinysql    # CLI 端到端（含事务语法）
ok  tinysql/sql            # 解析器 + 执行器
ok  tinysql/storage        # B+Tree + TableManager + Pager
ok  tinysql/tx             # WAL + TransactionManager
```

---

*文档结束*
