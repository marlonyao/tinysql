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

// Execute 执行 SQL 语句
func (e *Executor) Execute(stmt Statement) (Result, error) {
	switch s := stmt.(type) {
	case *CreateTableStmt:
		return e.executeCreateTable(s)
	case *InsertStmt:
		return e.executeInsert(s)
	case *SelectStmt:
		return e.executeSelect(s)
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

	// 全表扫描
	rows, err := e.tm.SelectAll(stmt.TableName)
	if err != nil {
		return nil, err
	}

	// WHERE 过滤
	if stmt.Where != nil {
		var filtered []*storage.Row
		for _, row := range rows {
			match, err := evalWhere(stmt.Where, row, table.Columns)
			if err != nil {
				return nil, err
			}
			if match {
				filtered = append(filtered, row)
			}
		}
		rows = filtered
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
