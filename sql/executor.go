package sql

import (
	"fmt"
	"strconv"
	"strings"

	"tinysql/storage"
)

// Executor SQL 执行器
type Executor struct {
	tm *storage.TableManager
}

func NewExecutor(tm *storage.TableManager) *Executor {
	return &Executor{tm: tm}
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

	if err := e.tm.Insert(stmt.TableName, row); err != nil {
		return nil, err
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

	// 读取所有行
	rows, err := e.tm.SelectAll(stmt.TableName)
	if err != nil {
		return nil, err
	}

	// 应用 WHERE 过滤
	var filtered []*storage.Row
	for _, row := range rows {
		if stmt.Where == nil || evalWhere(stmt.Where, row, table) {
			filtered = append(filtered, row)
		}
	}

	// 确定返回列
	var colNames []string
	if len(stmt.Columns) == 1 && stmt.Columns[0] == "*" {
		for _, col := range table.Columns {
			colNames = append(colNames, col.Name)
		}
	} else {
		colNames = stmt.Columns
	}

	// 构建结果
	var result [][]interface{}
	for _, row := range filtered {
		var rowVals []interface{}
		for _, colName := range colNames {
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
			rowVals = append(rowVals, row.Values[colIdx])
		}
		result = append(result, rowVals)
	}

	return &SelectResult{Columns: colNames, Rows: result}, nil
}

// evalWhere 计算 WHERE 条件
func evalWhere(expr Expr, row *storage.Row, table *storage.Table) bool {
	switch e := expr.(type) {
	case *BinaryExpr:
		left := evalWhereExpr(e.Left, row, table)
		right := evalWhereExpr(e.Right, row, table)

		switch e.Op {
		case "AND":
			return left != nil && right != nil && toBool(left) && toBool(right)
		case "OR":
			return left != nil && toBool(left) || right != nil && toBool(right)
		case "=":
			return compareEqual(left, right)
		case "<>":
			return !compareEqual(left, right)
		case "<":
			return compareLess(left, right)
		case ">":
			return compareLess(right, left)
		case "<=":
			return compareEqual(left, right) || compareLess(left, right)
		case ">=":
			return compareEqual(left, right) || compareLess(right, left)
		}
	}
	return false
}

func evalWhereExpr(expr Expr, row *storage.Row, table *storage.Table) interface{} {
	switch e := expr.(type) {
	case *Identifier:
		for i, col := range table.Columns {
			if strings.EqualFold(col.Name, e.Name) {
				return row.Values[i]
			}
		}
		return nil
	case *Literal:
		return e.Value
	}
	return nil
}

func toBool(v interface{}) bool {
	switch val := v.(type) {
	case bool:
		return val
	case int:
		return val != 0
	case string:
		return val != ""
	default:
		return v != nil
	}
}

func compareEqual(a, b interface{}) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}

	// 统一类型比较
	a = normalize(a)
	b = normalize(b)

	switch va := a.(type) {
	case int:
		vb, ok := b.(int)
		return ok && va == vb
	case string:
		vb, ok := b.(string)
		return ok && va == vb
	case bool:
		vb, ok := b.(bool)
		return ok && va == vb
	default:
		return false
	}
}

func compareLess(a, b interface{}) bool {
	if a == nil || b == nil {
		return false
	}

	a = normalize(a)
	b = normalize(b)

	switch va := a.(type) {
	case int:
		vb, ok := b.(int)
		return ok && va < vb
	case string:
		vb, ok := b.(string)
		return ok && va < vb
	default:
		return false
	}
}

func normalize(v interface{}) interface{} {
	switch val := v.(type) {
	case float64:
		return int(val)
	case int32:
		return int(val)
	case int64:
		return int(val)
	default:
		return v
	}
}
