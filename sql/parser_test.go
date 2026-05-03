package sql

import (
	"testing"
)

func TestParseCreateTable(t *testing.T) {
	input := `CREATE TABLE users (
		id INT,
		name VARCHAR(255),
		age INT,
		active BOOL
	);`

	stmt, err := ParseSQL(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	ct, ok := stmt.(*CreateTableStmt)
	if !ok {
		t.Fatalf("expected CreateTableStmt, got %T", stmt)
	}

	if ct.TableName != "users" {
		t.Fatalf("expected table name 'users', got '%s'", ct.TableName)
	}

	if len(ct.Columns) != 4 {
		t.Fatalf("expected 4 columns, got %d", len(ct.Columns))
	}

	cols := ct.Columns
	if cols[0].Name != "id" || cols[0].Type != "INT" {
		t.Fatalf("column 0: expected {id, INT}, got {%s, %s}", cols[0].Name, cols[0].Type)
	}
	if cols[1].Name != "name" || cols[1].Type != "VARCHAR" {
		t.Fatalf("column 1: expected {name, VARCHAR}, got {%s, %s}", cols[1].Name, cols[1].Type)
	}
	if cols[3].Name != "active" || cols[3].Type != "BOOL" {
		t.Fatalf("column 3: expected {active, BOOL}, got {%s, %s}", cols[3].Name, cols[3].Type)
	}
}

func TestParseInsert(t *testing.T) {
	input := `INSERT INTO users (id, name, age) VALUES (1, 'Alice', 25);`

	stmt, err := ParseSQL(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	ins, ok := stmt.(*InsertStmt)
	if !ok {
		t.Fatalf("expected InsertStmt, got %T", stmt)
	}

	if ins.TableName != "users" {
		t.Fatalf("expected table 'users', got '%s'", ins.TableName)
	}

	if len(ins.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(ins.Columns))
	}
	if ins.Columns[0] != "id" || ins.Columns[1] != "name" || ins.Columns[2] != "age" {
		t.Fatalf("unexpected columns: %v", ins.Columns)
	}

	if len(ins.Values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(ins.Values))
	}

	// 检查值
	v0 := ins.Values[0].(*Literal)
	if v0.Value != 1 {
		t.Fatalf("value 0: expected 1, got %v", v0.Value)
	}
	v1 := ins.Values[1].(*Literal)
	if v1.Value != "Alice" {
		t.Fatalf("value 1: expected 'Alice', got %v", v1.Value)
	}
	v2 := ins.Values[2].(*Literal)
	if v2.Value != 25 {
		t.Fatalf("value 2: expected 25, got %v", v2.Value)
	}
}

func TestParseSelect(t *testing.T) {
	input := `SELECT id, name FROM users WHERE id = 1;`

	stmt, err := ParseSQL(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	sel, ok := stmt.(*SelectStmt)
	if !ok {
		t.Fatalf("expected SelectStmt, got %T", stmt)
	}

	if len(sel.Columns) != 2 || sel.Columns[0] != "id" || sel.Columns[1] != "name" {
		t.Fatalf("unexpected columns: %v", sel.Columns)
	}
	if sel.TableName != "users" {
		t.Fatalf("expected table 'users', got '%s'", sel.TableName)
	}
	if sel.Where == nil {
		t.Fatalf("expected WHERE clause")
	}

	// 检查 WHERE
	bin := sel.Where.(*BinaryExpr)
	if bin.Op != "=" {
		t.Fatalf("expected op '=', got '%s'", bin.Op)
	}
	left := bin.Left.(*Identifier)
	if left.Name != "id" {
		t.Fatalf("expected left 'id', got '%s'", left.Name)
	}
	right := bin.Right.(*Literal)
	if right.Value != 1 {
		t.Fatalf("expected right 1, got %v", right.Value)
	}
}

func TestParseSelectStar(t *testing.T) {
	input := `SELECT * FROM users;`

	stmt, err := ParseSQL(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	sel, ok := stmt.(*SelectStmt)
	if !ok {
		t.Fatalf("expected SelectStmt, got %T", stmt)
	}

	if len(sel.Columns) != 1 || sel.Columns[0] != "*" {
		t.Fatalf("expected '*', got %v", sel.Columns)
	}
	if sel.Where != nil {
		t.Fatalf("expected no WHERE clause")
	}
}

func TestParseSelectWhereAnd(t *testing.T) {
	input := `SELECT * FROM users WHERE age >= 18 AND name = 'Bob';`

	stmt, err := ParseSQL(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	sel := stmt.(*SelectStmt)
	bin := sel.Where.(*BinaryExpr)
	if bin.Op != "AND" {
		t.Fatalf("expected AND, got %s", bin.Op)
	}

	// left: age >= 18
	left := bin.Left.(*BinaryExpr)
	if left.Op != ">=" {
		t.Fatalf("expected >=, got %s", left.Op)
	}
	// right: name = 'Bob'
	right := bin.Right.(*BinaryExpr)
	if right.Op != "=" {
		t.Fatalf("expected =, got %s", right.Op)
	}
}

func TestParseInsertNull(t *testing.T) {
	input := `INSERT INTO users (id, name) VALUES (1, NULL);`

	stmt, err := ParseSQL(input)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	ins := stmt.(*InsertStmt)
	v1 := ins.Values[1].(*Literal)
	if v1.Value != nil {
		t.Fatalf("expected NULL, got %v", v1.Value)
	}
}
