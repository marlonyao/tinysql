# TinySQL

一个极简的 SQL 数据库实现，用于学习关系型数据库的核心原理。

## 功能特性

- **存储引擎**：4KB 分页 + B+Tree 聚簇索引
- **SQL 解析器**：支持 `CREATE TABLE`、`INSERT`、`SELECT`、`DELETE`、`UPDATE`、`CREATE INDEX`
- **二级索引**：独立 BTree，INSERT/DELETE/UPDATE 自动维护
- **唯一索引**：`CREATE UNIQUE INDEX`，INSERT/UPDATE 时约束检查
- **索引扫描**：WHERE 等值条件自动选择最优索引（`name = 'Alice'`）
- **事务**：WAL + 崩溃恢复 + 事务 BEGIN/COMMIT/ROLLBACK
- **MVCC**：快照读（Repeatable Read 语义）+ Undo Log 链回溯
- **并发安全**：ReadView 四段式可见性判断，防止脏读/不可重复读

## 架构

```
┌─────────────┐
│  SQL Parser │  ← 词法分析 + 递归下降语法分析
├─────────────┤
│   Executor  │  ← SQL 执行、WHERE 过滤、索引扫描路由
├─────────────┤
│  TableMgr   │  ← 表元数据、BTree 操作、行格式序列化
├─────────────┤
│    BTree    │  ← B+Tree 实现（节点分裂、RangeScan）
├─────────────┤
│    Pager    │  ← 4KB 页缓存、LRU、FlushAll
├─────────────┤
│     WAL     │  ← 事务日志、崩溃恢复、UNDO 回滚
├─────────────┤
│    MVCC     │  ← ReadView、Undo Log、可见性判断
└─────────────┘
```

## 快速开始

### 依赖

- Go 1.22+

### 运行测试

```bash
# 全部测试
go test ./...

# 各包独立测试
go test ./sql -v        # SQL 解析器 + 执行器
go test ./storage -v    # 存储层（BTree、Page、TableManager）
go test ./tx -v         # 事务 + MVCC
```

### 交互式 CLI

```bash
go run ./cmd/tinysql mydb.db
```

示例会话：

```sql
tinysql> CREATE TABLE users (id INT, name VARCHAR(20), email VARCHAR(50));
tinysql> INSERT INTO users VALUES (1, 'Alice', 'alice@example.com');
tinysql> INSERT INTO users VALUES (2, 'Bob', 'bob@example.com');
tinysql> SELECT * FROM users;
id | name | email
---|------|------------------
1  | Alice | alice@example.com
2  | Bob   | bob@example.com
(2 row(s))

tinysql> CREATE INDEX idx_name ON users (name);
tinysql> SELECT * FROM users WHERE name = 'Bob';
1 row(s)

tinysql> CREATE UNIQUE INDEX idx_email ON users (email);
tinysql> INSERT INTO users VALUES (3, 'Charlie', 'alice@example.com');
Error: unique constraint violation on idx_email: row 1

tinysql> .tables
users
tinysql> .schema
  CREATE TABLE users (id INT, name VARCHAR, email VARCHAR);
tinysql> .quit
```

多行 SQL 支持（未输入分号时提示继续）：

```sql
tinysql> CREATE TABLE t (
   ...> id INT,
   ...> name VARCHAR(10)
   ...> );
```

数据文件 `mydb.db` 持久化到磁盘，WAL 日志在 `mydb.db.wal`。下次用相同路径打开，数据自动加载。

## 支持的 SQL

| 语句 | 示例 | 状态 |
|------|------|------|
| CREATE TABLE | `CREATE TABLE t (id INT, name VARCHAR(20));` | ✅ |
| INSERT | `INSERT INTO t VALUES (1, 'Alice');` | ✅ |
| SELECT | `SELECT * FROM t WHERE id = 1;` | ✅ |
| DELETE | `DELETE FROM t WHERE id = 1;` | ✅ |
| UPDATE | `UPDATE t SET name = 'Bob' WHERE id = 1;` | ✅ |
| CREATE INDEX | `CREATE INDEX idx ON t (name);` | ✅ |
| CREATE UNIQUE INDEX | `CREATE UNIQUE INDEX uidx ON t (email);` | ✅ |
| BEGIN | `BEGIN;` | ✅ |
| COMMIT | `COMMIT;` | ✅ |
| ROLLBACK | `ROLLBACK;` | ✅ |
| WHERE | `WHERE name = 'Alice' AND age > 25` | ✅（AND / OR / = / <> / < / > / <= / >=） |

## 行格式

```
[null bitmap] [trx_id: 8 bytes] [roll_ptr: 8 bytes] [定长区] [变长区]
```

- `null bitmap`：1 bit 每列，标记 NULL
- `trx_id`：创建/最后修改该版本的事务 ID
- `roll_ptr`：指向 Undo Log（0 = 无旧版本）
- 定长区：INT（4 bytes）、BOOL（1 byte）、VARCHAR offset（2 bytes）
- 变长区：VARCHAR 实际数据

## 索引格式

- **聚簇索引**：BTree key = `_rowid`（自增），value = 完整行数据
- **二级索引**：独立 BTree
  - 非唯一索引 key = `[col1][col2]...[rowid]`，value = `rowid`
  - 唯一索引 key = `[col1][col2]...`（纯列值），value = `rowid`

## MVCC 可见性规则

对 ReadView `(creatorTrxID, minTrxID, maxTrxID, activeIDs)`：

```
1. TrxID == 0          → 可见（旧数据）
2. TrxID == Creator    → 可见（自己创建）
3. TrxID < MinTrxID    → 可见（ReadView 创建前已提交）
4. TrxID >= MaxTrxID    → 不可见（ReadView 创建后启动）
5. TrxID in ActiveIDs   → 不可见（活跃未提交）
6. 其他                 → 可见（已提交）
```

不可见的行沿 `RollPtr` 查 Undo Log 链回溯旧版本，直到找到可见版本或链结束。

## 项目结构

```
tinysql/
├── cmd/tinysql/        # 交互式 CLI
├── sql/
│   ├── parser.go       # SQL 解析器（Tokenizer + 递归下降）
│   ├── executor.go     # SQL 执行器（SELECT/INSERT/DELETE/UPDATE/索引扫描）
│   └── *_test.go       # 解析器/执行器测试
├── storage/
│   ├── pager.go        # 页缓存管理器（4KB 页、LRU）
│   ├── btree.go        # B+Tree 实现（插入、删除、RangeScan）
│   ├── table.go        # 表元数据、行格式序列化/反序列化
│   └── table_manager.go # 表管理、索引维护、MVCC 查询
└── tx/
    ├── tx.go           # 事务管理器 + MVCC（ReadView、UndoLog）
    ├── wal.go          # WAL 日志 + 崩溃恢复
    └── *_test.go       # 事务/MVCC 测试
```

## 实现阶段

| 阶段 | 内容 | 提交 |
|------|------|------|
| Phase 1 | Page 管理器 + B+Tree + 表元数据持久化 | 早期提交 |
| Phase 2 | DELETE/UPDATE + 二级索引自动维护 | `407cea0` |
| Phase 3 | MVCC 快照读 + Undo 链 + 并发事务测试 | `5d17bb4` |
| - | 唯一索引约束检查 | `fbd2bb4` |
| - | 索引扫描（等值查询） | `bb0e5a5` |

## 学习路线

本项目按渐进式难度实现数据库核心组件：

1. **存储层** → 理解页式存储、B+Tree 索引、行格式
2. **执行层** → 理解 SQL 解析、执行计划、索引扫描
3. **事务层** → 理解 WAL、ACID、MVCC 可见性
4. **优化层** → 理解索引选择、覆盖索引、范围查询（待实现）

## License

MIT
