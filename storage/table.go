package storage

import (
	"bytes"
	"encoding/binary"
	"fmt"
)

// ColumnType 字段类型
type ColumnType byte

const (
	TypeInt     ColumnType = 1
	TypeVarchar ColumnType = 2
	TypeBool    ColumnType = 3
)

func (t ColumnType) String() string {
	switch t {
	case TypeInt:
		return "INT"
	case TypeVarchar:
		return "VARCHAR"
	case TypeBool:
		return "BOOL"
	default:
		return "UNKNOWN"
	}
}

// Column 列定义
type Column struct {
	Name     string
	Type     ColumnType
	Length   uint16 // VARCHAR 长度
	Nullable bool
	Primary  bool
}

// Index 二级索引定义
type Index struct {
	Name       string
	Columns    []string // 索引列名（顺序重要，支持最左前缀）
	RootPageID uint32   // BTree 根页
	Unique     bool
}

// Table 表元数据
type Table struct {
	Name       string
	Columns    []Column
	RootPageID uint32  // B+Tree 聚簇索引根节点页ID
	NextRowID  int     // 自增 _rowid 计数器
	Indexes    []Index // 二级索引列表
}

// Row 一行数据
type Row struct {
	Values []interface{} // 按 Columns 顺序
}

// 行格式（简化版）：
// [0:n]         null bitmap (1 bit per column, n = ceil(n_cols/8))
// [n:n+fixed]   定长字段区（INT/BOOL，按列顺序）
// [n+fixed:]    变长字段区（每个: 2字节长度 + 数据，按列顺序）
//
// 设计原则：简单可预测，反序列化时按列类型顺序解析

func (t *Table) rowSize() int {
	size := (len(t.Columns) + 7) / 8 // null bitmap
	for _, col := range t.Columns {
		switch col.Type {
		case TypeInt:
			size += 4
		case TypeBool:
			size += 1
		case TypeVarchar:
			size += 2 // max data len prefix
		}
	}
	return size
}

// SerializeRow 将 Row 序列化为字节
func (t *Table) SerializeRow(row *Row) ([]byte, error) {
	if len(row.Values) != len(t.Columns) {
		return nil, fmt.Errorf("column count mismatch: expected %d, got %d", len(t.Columns), len(row.Values))
	}

	nullBitmapSize := (len(t.Columns) + 7) / 8
	
	// 第一阶段：计算 null bitmap + 定长区大小
	nullBitmap := make([]byte, nullBitmapSize)
	fixedSize := 0
	for i, val := range row.Values {
		if val == nil {
			nullBitmap[i/8] |= 1 << (i % 8)
		}
		// 所有字段都在固定区占位置（null 也占位，保证偏移可预测）
		switch t.Columns[i].Type {
		case TypeInt:
			fixedSize += 4
		case TypeBool:
			fixedSize += 1
		case TypeVarchar:
			fixedSize += 2 // offset/length 占位
		}
	}

	// 第二阶段：写入
	buf := new(bytes.Buffer)
	buf.Write(nullBitmap)

	varcharBuf := new(bytes.Buffer)
	varcharDataStart := make([]uint16, len(t.Columns)) // 记录每个 varchar 在变长区的起始位置

	for i, col := range t.Columns {
		if row.Values[i] == nil {
			// null 字段：定长区写 0 占位
			switch col.Type {
			case TypeInt:
				buf.Write(make([]byte, 4))
			case TypeBool:
				buf.WriteByte(0)
			case TypeVarchar:
				binary.Write(buf, binary.LittleEndian, uint16(0xFFFF)) // null marker
			}
			continue
		}

		switch col.Type {
		case TypeInt:
			v, ok := row.Values[i].(int)
			if !ok {
				return nil, fmt.Errorf("column %s: expected int", col.Name)
			}
			binary.Write(buf, binary.LittleEndian, int32(v))
		case TypeBool:
			v, ok := row.Values[i].(bool)
			if !ok {
				return nil, fmt.Errorf("column %s: expected bool", col.Name)
			}
			if v {
				buf.WriteByte(1)
			} else {
				buf.WriteByte(0)
			}
		case TypeVarchar:
			v, ok := row.Values[i].(string)
			if !ok {
				return nil, fmt.Errorf("column %s: expected string", col.Name)
			}
			// 记录该 varchar 在变长区的起始位置
			varcharDataStart[i] = uint16(varcharBuf.Len())
			binary.Write(varcharBuf, binary.LittleEndian, uint16(len(v)))
			varcharBuf.WriteString(v)
			// 定长区写偏移
			binary.Write(buf, binary.LittleEndian, uint16(varcharBuf.Len()-len(v)-2)) // 变长区中的起始位置
		}
	}

	// 写入变长区
	buf.Write(varcharBuf.Bytes())

	return buf.Bytes(), nil
}

// DeserializeRow 将字节反序列化为 Row
func (t *Table) DeserializeRow(data []byte) (*Row, error) {
	nullBitmapSize := (len(t.Columns) + 7) / 8
	if len(data) < nullBitmapSize {
		return nil, fmt.Errorf("data too short for null bitmap: got %d bytes, need %d", len(data), nullBitmapSize)
	}

	row := &Row{Values: make([]interface{}, len(t.Columns))}
	nullBitmap := data[0:nullBitmapSize]
	
	// 计算定长区大小（所有字段都占位置）
	fixedSize := 0
	for _, col := range t.Columns {
		switch col.Type {
		case TypeInt:
			fixedSize += 4
		case TypeBool:
			fixedSize += 1
		case TypeVarchar:
			fixedSize += 2
		}
	}

	fixedOffset := nullBitmapSize // 当前在定长区的偏移
	varOffset := nullBitmapSize + fixedSize // 变长区起始

	for i, col := range t.Columns {
		isNull := nullBitmap[i/8]&(1<<(i%8)) != 0
		if isNull {
			row.Values[i] = nil
			// 推进 fixedOffset
			switch col.Type {
			case TypeInt:
				fixedOffset += 4
			case TypeBool:
				fixedOffset += 1
			case TypeVarchar:
				fixedOffset += 2
			}
			continue
		}

		switch col.Type {
		case TypeInt:
			if fixedOffset+4 > len(data) {
				return nil, fmt.Errorf("int data out of bounds")
			}
			row.Values[i] = int(binary.LittleEndian.Uint32(data[fixedOffset:fixedOffset+4]))
			fixedOffset += 4
		case TypeBool:
			if fixedOffset >= len(data) {
				return nil, fmt.Errorf("bool data out of bounds")
			}
			row.Values[i] = data[fixedOffset] != 0
			fixedOffset += 1
		case TypeVarchar:
			if fixedOffset+2 > len(data) {
				return nil, fmt.Errorf("varchar offset out of bounds")
			}
			// 读取该 varchar 在变长区的位置
			vStartInVar := int(binary.LittleEndian.Uint16(data[fixedOffset:fixedOffset+2]))
			fixedOffset += 2
			
			if vStartInVar == 0xFFFF {
				// null marker (不应该走到这里，因为前面已经判断了 isNull)
				row.Values[i] = nil
				continue
			}
			
			dataOff := varOffset + vStartInVar
			if dataOff+2 > len(data) {
				return nil, fmt.Errorf("varchar len out of bounds")
			}
			strLen := int(binary.LittleEndian.Uint16(data[dataOff:]))
			dataOff += 2
			if dataOff+strLen > len(data) {
				return nil, fmt.Errorf("varchar data out of bounds")
			}
			row.Values[i] = string(data[dataOff:dataOff+strLen])
		}
	}

	return row, nil
}
