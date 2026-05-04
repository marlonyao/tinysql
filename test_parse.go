package main

import (
	"fmt"
	"tinysql/sql"
)

func main() {
	// 测试 SQL 解析
	testCases := []string{
		"CREATE TABLE t (id INT, name TEXT);",
		"INSERT INTO t VALUES (1, 'Alice');",
		"INSERT INTO t (id, name) VALUES (1, 'Alice');",
	}
	
	for _, tc := range testCases {
		stmt, err := sql.ParseSQL(tc)
		if err != nil {
			fmt.Printf("Parse error for %q: %v\n", tc, err)
			continue
		}
		switch s := stmt.(type) {
		case *sql.InsertStmt:
			fmt.Printf("INSERT: Table=%s, Columns=%v, Values=%d\n", s.TableName, s.Columns, len(s.Values))
		case *sql.CreateTableStmt:
			fmt.Printf("CREATE TABLE: %s\n", s.TableName)
		}
	}
}
