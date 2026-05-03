package storage

import (
	"testing"
)

func TestTableSerializeDeserialize(t *testing.T) {
	table := &Table{
		Name: "users",
		Columns: []Column{
			{Name: "id", Type: TypeInt, Primary: true},
			{Name: "name", Type: TypeVarchar, Length: 50},
			{Name: "active", Type: TypeBool},
		},
	}

	row := &Row{
		Values: []interface{}{1, "alice", true},
	}

	// 序列化
	data, err := table.SerializeRow(row)
	if err != nil {
		t.Fatalf("SerializeRow failed: %v", err)
	}

	// 反序列化
	row2, err := table.DeserializeRow(data)
	if err != nil {
		t.Fatalf("DeserializeRow failed: %v", err)
	}

	// 验证
	if len(row2.Values) != 3 {
		t.Fatalf("expected 3 values, got %d", len(row2.Values))
	}
	if row2.Values[0].(int) != 1 {
		t.Fatalf("expected id=1, got %v", row2.Values[0])
	}
	if row2.Values[1].(string) != "alice" {
		t.Fatalf("expected name=alice, got %v", row2.Values[1])
	}
	if !row2.Values[2].(bool) {
		t.Fatalf("expected active=true, got %v", row2.Values[2])
	}
}

func TestTableWithNulls(t *testing.T) {
	table := &Table{
		Name: "users",
		Columns: []Column{
			{Name: "id", Type: TypeInt},
			{Name: "name", Type: TypeVarchar, Length: 50, Nullable: true},
			{Name: "age", Type: TypeInt, Nullable: true},
		},
	}

	row := &Row{
		Values: []interface{}{42, nil, nil}, // name 和 age 为 null
	}

	data, err := table.SerializeRow(row)
	if err != nil {
		t.Fatalf("SerializeRow failed: %v", err)
	}

	row2, err := table.DeserializeRow(data)
	if err != nil {
		t.Fatalf("DeserializeRow failed: %v", err)
	}

	if row2.Values[0].(int) != 42 {
		t.Fatalf("expected id=42, got %v", row2.Values[0])
	}
	if row2.Values[1] != nil {
		t.Fatalf("expected name=nil, got %v", row2.Values[1])
	}
	if row2.Values[2] != nil {
		t.Fatalf("expected age=nil, got %v", row2.Values[2])
	}
}

func TestTableRowSize(t *testing.T) {
	table := &Table{
		Name: "test",
		Columns: []Column{
			{Name: "a", Type: TypeInt},
			{Name: "b", Type: TypeBool},
			{Name: "c", Type: TypeVarchar, Length: 100},
		},
	}

	// 计算：null bitmap (1) + int(4) + bool(1) + varchar offset(2) = 8
	size := table.rowSize()
	if size < 8 {
		t.Fatalf("expected row size >= 8, got %d", size)
	}
}

func TestColumnTypeString(t *testing.T) {
	tests := []struct {
		typ      ColumnType
		expected string
	}{
		{TypeInt, "INT"},
		{TypeVarchar, "VARCHAR"},
		{TypeBool, "BOOL"},
		{ColumnType(99), "UNKNOWN"},
	}

	for _, tt := range tests {
		if got := tt.typ.String(); got != tt.expected {
			t.Errorf("%v.String() = %s, want %s", tt.typ, got, tt.expected)
		}
	}
}
