package sql

import (
	"fmt"
	"strconv"
	"strings"

	"tinysql/storage"
	"tinysql/tx"
)

// Executor SQL 执行器（支持事务上下文）
type Executor struct {
	tm        *storage.TableManager
	txm       *tx.TransactionManager
	currentTx *tx.Transaction // 当前活跃事务（如果有）
}

// NewExecutor 创建执行器（无事务支持）
func NewExecutor(tm *storage.TableManager) *Executor {
	return &Executor{tm: tm}
}

// NewExecutorWithTx 创建支持事务的执行器
func NewExecutorWithTx(tm *storage.TableManager, txm *tx.TransactionManager) *Executor {
	return &Executor{tm: tm, txm: txm}
}

// currentTrxID 返回当前事务 ID，无事务时返回 0
func (e *Executor) currentTrxID() uint64 {
	if e.currentTx != nil {
		return e.currentTx.ID
	}
	return 0
}

// Execute 执行 SQL 语句
func (e *Executor) Execute(stmt Statement) (Result, error) {
	switch s := stmt.(type) {
	case *CreateTableStmt:
		return e.executeCreateTable(s)
	case *InsertStmt:
		return e.executeInsert(s)
	case *SelectStmt:
		return e.executeSelect(s)
	case *DeleteStmt:
		return e.executeDelete(s)
	case *UpdateStmt:
		return e.executeUpdate(s)
	case *CreateIndexStmt:
		return e.executeCreateIndex(s)
	case *TxBeginStmt:
		return e.executeBegin()
	case *TxCommitStmt:
		return e.executeCommit()
	case *TxRollbackStmt:
		return e.executeRollback()
	default:
		return nil, fmt.Errorf("unsupported statement type")
	}
}

// Result 执行结果接口
type Result interface {
	resultNode()
}

type CreateTableResult struct {
	Message string
}
func (r *CreateTableResult) resultNode() {}

type InsertResult struct {
	RowsAffected int
}
func (r *InsertResult) resultNode() {}

type SelectResult struct {
	Columns []string
	Rows    [][]interface{}
}
func (r *SelectResult) resultNode() {}

type TxResult struct {
	Message string
}
func (r *TxResult) resultNode() {}

// DELETE / UPDATE / CREATE INDEX 结果
type DeleteResult struct {
	RowsAffected int
}
func (r *DeleteResult) resultNode() {}

type UpdateResult struct {
	RowsAffected int
}
func (r *UpdateResult) resultNode() {}

type CreateIndexResult struct {
	Message string
}
func (r *CreateIndexResult) resultNode() {}

// === 事务执行 ===

func (e *Executor) executeBegin() (Result, error) {
	if e.txm == nil {
		return nil, fmt.Errorf("transaction manager not available")
	}
	if e.currentTx != nil {
		return nil, fmt.Errorf("transaction already in progress")
	}
	tx, err := e.txm.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	e.currentTx = tx
	return &TxResult{Message: "Transaction started"}, nil
}

func (e *Executor) executeCommit() (Result, error) {
	if e.txm == nil {
		return nil, fmt.Errorf("transaction manager not available")
	}
	if e.currentTx == nil {
		return nil, fmt.Errorf("no active transaction")
	}
	if err := e.txm.Commit(e.currentTx); err != nil {
		return nil, fmt.Errorf("commit: %w", err)
	}
	e.currentTx = nil
	return &TxResult{Message: "Transaction committed"}, nil
}

func (e *Executor) executeRollback() (Result, error) {
	if e.txm == nil {
		return nil, fmt.Errorf("transaction manager not available")
	}
	if e.currentTx == nil {
		return nil, fmt.Errorf("no active transaction")
	}
	if err := e.txm.Rollback(e.currentTx); err != nil {
		return nil, fmt.Errorf("rollback: %w", err)
	}
	e.currentTx = nil
	return &TxResult{Message: "Transaction rolled back"}, nil
}

// === 执行 CREATE TABLE ===

func (e *Executor) executeCreateTable(stmt *CreateTableStmt) (Result, error) {
	columns := make([]storage.Column, len(stmt.Columns))
	for i, col := range stmt.Columns {
		colType, err := parseColumnType(col.Type)
		if err != nil {
			return nil, err
		}
		columns[i] = storage.Column{
			Name: col.Name,
			Type: colType,
		}
	}

	table := &storage.Table{
		Name:    stmt.TableName,
		Columns: columns,
	}

	if err := e.tm.CreateTable(table); err != nil {
		return nil, err
	}

	return &CreateTableResult{Message: fmt.Sprintf("Table %s created", stmt.TableName)}, nil
}

func parseColumnType(s string) (storage.ColumnType, error) {
	switch strings.ToUpper(s) {
	case "INT":
		return storage.TypeInt, nil
	case "VARCHAR", "TEXT":
		return storage.TypeVarchar, nil
	case "BOOL":
		return storage.TypeBool, nil
	default:
		return 0, fmt.Errorf("unknown column type: %s", s)
	}
}

// === 执行 INSERT ===

func (e *Executor) executeInsert(stmt *InsertStmt) (Result, error) {
	table, ok := e.tm.GetTable(stmt.TableName)
	if !ok {
		return nil, fmt.Errorf("table %s not found", stmt.TableName)
	}

	// 构建 Row：按表定义的列顺序填充
	row := &storage.Row{
		Values: make([]interface{}, len(table.Columns)),
	}

	// 默认全部 NULL
	for i := range row.Values {
		row.Values[i] = nil
	}

	// 未指定列名时，默认按表列顺序
	columns := stmt.Columns
	if len(columns) == 0 {
		for _, col := range table.Columns {
			columns = append(columns, col.Name)
		}
	}

	if len(stmt.Values) != len(columns) {
		return nil, fmt.Errorf("value count mismatch: expected %d, got %d", len(columns), len(stmt.Values))
	}

	for i, colName := range columns {
		// 找到列索引
		colIdx := -1
		for j, col := range table.Columns {
			if strings.EqualFold(col.Name, colName) {
				colIdx = j
				break
			}
		}
		if colIdx == -1 {
			return nil, fmt.Errorf("column %s not found in table %s", colName, stmt.TableName)
		}

		// 计算值
		val, err := evalExpr(stmt.Values[i], table.Columns[colIdx].Type)
		if err != nil {
			return nil, err
		}
		row.Values[colIdx] = val
	}

	// 事务路径 vs 非事务路径
	if e.currentTx != nil {
		if err := e.currentTx.Insert(stmt.TableName, row); err != nil {
			return nil, err
		}
	} else {
		if err := e.tm.Insert(stmt.TableName, row); err != nil {
			return nil, err
		}
	}

	return &InsertResult{RowsAffected: 1}, nil
}

func evalExpr(expr Expr, colType storage.ColumnType) (interface{}, error) {
	switch e := expr.(type) {
	case *Literal:
		return convertValue(e.Value, colType)
	default:
		return nil, fmt.Errorf("unsupported expression in INSERT value")
	}
}

func convertValue(val interface{}, colType storage.ColumnType) (interface{}, error) {
	if val == nil {
		return nil, nil
	}

	switch colType {
	case storage.TypeInt:
		switch v := val.(type) {
		case int:
			return v, nil
		case string:
			i, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("cannot convert string '%s' to int: %w", v, err)
			}
			return i, nil
		default:
			return nil, fmt.Errorf("cannot convert %T to int", val)
		}
	case storage.TypeVarchar:
		switch v := val.(type) {
		case string:
			return v, nil
		case int:
			return fmt.Sprintf("%d", v), nil
		default:
			return nil, fmt.Errorf("cannot convert %T to string", val)
		}
	case storage.TypeBool:
		switch v := val.(type) {
		case bool:
			return v, nil
		default:
			return nil, fmt.Errorf("cannot convert %T to bool", val)
		}
	default:
		return nil, fmt.Errorf("unknown column type")
	}
}

// === 执行 SELECT ===

func (e *Executor) executeSelect(stmt *SelectStmt) (Result, error) {
	table, ok := e.tm.GetTable(stmt.TableName)
	if !ok {
		return nil, fmt.Errorf("table %s not found", stmt.TableName)
	}

	var rows []*storage.Row
	var rowIDs []int
	var err error

	// 尝试索引扫描：从 WHERE 中提取等值条件，找到匹配的索引
	if stmt.Where != nil {
		indexName, values := e.findBestIndex(table, stmt.Where)
		if indexName != "" {
			rows, rowIDs, err = e.tm.SelectByIndex(stmt.TableName, indexName, values)
			if err != nil {
				return nil, err
			}
		}
	}

	// 无法走索引时全表扫描
	if rows == nil {
		rows, rowIDs, err = e.tm.SelectAllWithRowID(stmt.TableName)
		if err != nil {
			return nil, err
		}
	}

	// WHERE 过滤
	if stmt.Where != nil {
		var filtered []*storage.Row
		var filteredIDs []int
		for i, row := range rows {
			match, err := evalWhere(stmt.Where, row, table.Columns)
			if err != nil {
				return nil, err
			}
			if match {
				filtered = append(filtered, row)
				filteredIDs = append(filteredIDs, rowIDs[i])
			}
		}
		rows = filtered
		rowIDs = filteredIDs
	}

	// 列选择
	var columns []string
	var colIndices []int
	if len(stmt.Columns) == 1 && stmt.Columns[0] == "*" {
		for i, col := range table.Columns {
			columns = append(columns, col.Name)
			colIndices = append(colIndices, i)
		}
	} else {
		for _, colName := range stmt.Columns {
			found := false
			for i, col := range table.Columns {
				if strings.EqualFold(col.Name, colName) {
					columns = append(columns, col.Name)
					colIndices = append(colIndices, i)
					found = true
					break
				}
			}
			if !found {
				return nil, fmt.Errorf("column %s not found in table %s", colName, stmt.TableName)
			}
		}
	}

	// 构建结果
	var resultRows [][]interface{}
	for _, row := range rows {
		var vals []interface{}
		for _, idx := range colIndices {
			vals = append(vals, row.Values[idx])
		}
		resultRows = append(resultRows, vals)
	}

	return &SelectResult{
		Columns: columns,
		Rows:    resultRows,
	}, nil
}

// findBestIndex 从 WHERE 条件中提取等值条件，找到最匹配的索引
// 返回 (索引名, 索引列的值列表)。未找到返回 ("", nil)
func (e *Executor) findBestIndex(table *storage.Table, where Expr) (string, []interface{}) {
	// 提取所有 column = literal 的等值条件
	eqConds := make(map[string]interface{})
	extractEqConditions(where, eqConds)
	if len(eqConds) == 0 {
		return "", nil
	}

	// 遍历所有索引，找最长前缀匹配
	bestIdx := ""
	bestValues := []interface{}{}
	bestMatch := 0

	for _, idx := range table.Indexes {
		var values []interface{}
		matched := 0
		for _, col := range idx.Columns {
			val, ok := eqConds[strings.ToLower(col)]
			if !ok {
				break
			}
			values = append(values, val)
			matched++
		}
		if matched > bestMatch {
			bestMatch = matched
			bestIdx = idx.Name
			bestValues = values
		}
	}

	if bestMatch == 0 {
		return "", nil
	}
	return bestIdx, bestValues
}

// extractEqConditions 从 WHERE 表达式中提取 column = literal 的等值条件
func extractEqConditions(expr Expr, out map[string]interface{}) {
	switch e := expr.(type) {
	case *BinaryExpr:
		if e.Op == "=" {
			if id, ok := e.Left.(*Identifier); ok {
				if lit, ok := e.Right.(*Literal); ok {
					out[strings.ToLower(id.Name)] = lit.Value
				}
			} else if id, ok := e.Right.(*Identifier); ok {
				if lit, ok := e.Left.(*Literal); ok {
					out[strings.ToLower(id.Name)] = lit.Value
				}
			}
		} else if e.Op == "AND" {
			extractEqConditions(e.Left, out)
			extractEqConditions(e.Right, out)
		}
	}
}

func evalWhere(expr Expr, row *storage.Row, columns []storage.Column) (bool, error) {
	switch e := expr.(type) {
	case *BinaryExpr:
		left, err := evalExprForWhere(e.Left, row, columns)
		if err != nil {
			return false, err
		}
		right, err := evalExprForWhere(e.Right, row, columns)
		if err != nil {
			return false, err
		}
		switch e.Op {
		case "=":
			return fmt.Sprintf("%v", left) == fmt.Sprintf("%v", right), nil
		case "<>":
			return fmt.Sprintf("%v", left) != fmt.Sprintf("%v", right), nil
		case "<":
			return compareValues(left, right) < 0, nil
		case ">":
			return compareValues(left, right) > 0, nil
		case "<=":
			return compareValues(left, right) <= 0, nil
		case ">=":
			return compareValues(left, right) >= 0, nil
		case "AND":
			return left.(bool) && right.(bool), nil
		case "OR":
			return left.(bool) || right.(bool), nil
		default:
			return false, fmt.Errorf("unknown operator: %s", e.Op)
		}
	case *Literal:
		return e.Value.(bool), nil
	default:
		return false, fmt.Errorf("unsupported WHERE expression")
	}
}

func evalExprForWhere(expr Expr, row *storage.Row, columns []storage.Column) (interface{}, error) {
	switch e := expr.(type) {
	case *Identifier:
		for i, col := range columns {
			if strings.EqualFold(col.Name, e.Name) {
				return row.Values[i], nil
			}
		}
		return nil, fmt.Errorf("column %s not found", e.Name)
	case *Literal:
		return e.Value, nil
	case *BinaryExpr:
		return evalWhere(e, row, columns)
	default:
		return nil, fmt.Errorf("unsupported expression in WHERE")
	}
}

func compareValues(a, b interface{}) int {
	// 尝试数值比较
	ia, aok := toInt64(a)
	ib, bok := toInt64(b)
	if aok && bok {
		if ia < ib { return -1 }
		if ia > ib { return 1 }
		return 0
	}
	// 回退到字符串比较
	sa := fmt.Sprintf("%v", a)
	sb := fmt.Sprintf("%v", b)
	if sa < sb { return -1 }
	if sa > sb { return 1 }
	return 0
}

func toInt64(v interface{}) (int64, bool) {
	switch val := v.(type) {
	case int:
		return int64(val), true
	case int32:
		return int64(val), true
	case int64:
		return val, true
	case string:
		i, err := strconv.ParseInt(val, 10, 64)
		return i, err == nil
	default:
		return 0, false
	}
}

// === 执行 DELETE ===

func (e *Executor) executeDelete(stmt *DeleteStmt) (Result, error) {
	table, ok := e.tm.GetTable(stmt.TableName)
	if !ok {
		return nil, fmt.Errorf("table %s not found", stmt.TableName)
	}

	// 全表扫描获取所有行（需要 rowID 才能删除）
	rows, rowIDs, err := e.tm.SelectAllWithRowID(stmt.TableName)
	if err != nil {
		return nil, err
	}

	// WHERE 过滤，收集要删除的 rowID
	var toDelete []int
	for i, row := range rows {
		if stmt.Where != nil {
			match, err := evalWhere(stmt.Where, row, table.Columns)
			if err != nil {
				return nil, err
			}
			if !match {
				continue
			}
		}
		toDelete = append(toDelete, rowIDs[i])
	}

	// 执行删除
	for _, rowID := range toDelete {
		if err := e.tm.DeleteByRowID(stmt.TableName, rowID); err != nil {
			return nil, fmt.Errorf("delete row %d: %w", rowID, err)
		}
	}

	return &DeleteResult{RowsAffected: len(toDelete)}, nil
}

// === 执行 UPDATE ===

func (e *Executor) executeUpdate(stmt *UpdateStmt) (Result, error) {
	table, ok := e.tm.GetTable(stmt.TableName)
	if !ok {
		return nil, fmt.Errorf("table %s not found", stmt.TableName)
	}

	// 全表扫描获取所有行
	rows, rowIDs, err := e.tm.SelectAllWithRowID(stmt.TableName)
	if err != nil {
		return nil, err
	}

	// WHERE 过滤
	var toUpdateRows []*storage.Row
	var toUpdateIDs []int
	for i, row := range rows {
		if stmt.Where != nil {
			match, err := evalWhere(stmt.Where, row, table.Columns)
			if err != nil {
				return nil, err
			}
			if !match {
				continue
			}
		}
		toUpdateRows = append(toUpdateRows, row)
		toUpdateIDs = append(toUpdateIDs, rowIDs[i])
	}

	// 解析 SET 表达式为具体值（按列类型转换）
	setValues := make(map[string]interface{})
	for colName, expr := range stmt.Set {
		// 找到列定义以获取类型
		var colType storage.ColumnType
		for _, col := range table.Columns {
			if strings.EqualFold(col.Name, colName) {
				colType = col.Type
				break
			}
		}
		val, err := evalExpr(expr, colType)
		if err != nil {
			return nil, err
		}
		setValues[colName] = val
	}

	// 逐行更新：先删旧索引，再更新聚簇索引，再插新索引
	updated := 0
	pager := e.tm.GetPager()
	for i, row := range toUpdateRows {
		rowID := toUpdateIDs[i]

		// 删除旧二级索引（需要旧行数据构建旧 key）
		for j := range table.Indexes {
			idx := &table.Indexes[j]
			idxBT := storage.LoadBTree(pager, idx.RootPageID)
			oldKey, err := e.tm.BuildIndexKey(table, *idx, row, rowID)
			if err != nil {
				return nil, fmt.Errorf("build old index key: %w", err)
			}
			if err := idxBT.Delete(oldKey); err != nil {
				return nil, fmt.Errorf("delete old index key: %w", err)
			}
			if idxBT.RootPageID() != idx.RootPageID {
				idx.RootPageID = idxBT.RootPageID()
			}
		}

		// 写入 undo log（保存修改前的完整值）
		var oldValues []interface{}
		for _, v := range row.Values {
			oldValues = append(oldValues, v)
		}
		var rollPtr uint64
		if row.TrxID != 0 {
			rollPtr = row.RollPtr
		}
		undoID, err := e.tm.WriteUndoRecord(e.currentTrxID(), stmt.TableName, rowID, oldValues, row.TrxID, rollPtr, 2) // 2=UPDATE
		if err != nil {
			return nil, fmt.Errorf("write undo: %w", err)
		}

		// 应用 SET
		for colName, val := range setValues {
			for j, col := range table.Columns {
				if strings.EqualFold(col.Name, colName) {
					converted, err := convertValue(val, col.Type)
					if err != nil {
						return nil, err
					}
					row.Values[j] = converted
					break
				}
			}
		}

		// 更新 MVCC 元信息
		row.TrxID = e.currentTrxID()
		row.RollPtr = undoID

		// 重新序列化并覆盖聚簇索引
		rowData, err := table.SerializeRow(row)
		if err != nil {
			return nil, fmt.Errorf("serialize row: %w", err)
		}
		bt := storage.LoadBTree(pager, table.RootPageID)
		if err := bt.Insert(storage.EncodeIntKey(rowID), rowData); err != nil {
			return nil, fmt.Errorf("btree update: %w", err)
		}
		if bt.RootPageID() != table.RootPageID {
			table.RootPageID = bt.RootPageID()
		}

		// 插入新二级索引
		for j := range table.Indexes {
			idx := &table.Indexes[j]
			idxBT := storage.LoadBTree(pager, idx.RootPageID)
			newKey, err := e.tm.BuildIndexKey(table, *idx, row, rowID)
			if err != nil {
				return nil, fmt.Errorf("build new index key: %w", err)
			}
			if idx.Unique {
				if conflictRowID, conflict := e.tm.CheckUniqueConflict(idxBT, newKey, rowID); conflict {
					return nil, fmt.Errorf("unique constraint violation on %s: row %d", idx.Name, conflictRowID)
				}
			}
			if err := idxBT.Insert(newKey, storage.EncodeIntKey(rowID)); err != nil {
				return nil, fmt.Errorf("insert new index key: %w", err)
			}
			if idxBT.RootPageID() != idx.RootPageID {
				idx.RootPageID = idxBT.RootPageID()
			}
		}
		updated++
	}

	if updated > 0 {
		if err := e.tm.PersistTableMeta(table); err != nil {
			return nil, err
		}
	}

	return &UpdateResult{RowsAffected: updated}, nil
}

// === 执行 CREATE INDEX ===

func (e *Executor) executeCreateIndex(stmt *CreateIndexStmt) (Result, error) {
	// 校验表存在
	table, ok := e.tm.GetTable(stmt.TableName)
	if !ok {
		return nil, fmt.Errorf("table %s not found", stmt.TableName)
	}

	// 校验列存在
	for _, colName := range stmt.Columns {
		found := false
		for _, col := range table.Columns {
			if strings.EqualFold(col.Name, colName) {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("column %s not found in table %s", colName, stmt.TableName)
		}
	}

	// 创建二级索引（TableManager 负责）
	if err := e.tm.CreateIndex(stmt.TableName, stmt.IndexName, stmt.Columns, stmt.Unique); err != nil {
		return nil, err
	}

	return &CreateIndexResult{Message: fmt.Sprintf("Index %s created on %s", stmt.IndexName, stmt.TableName)}, nil
}
