package main

import (
	"fmt"
	"tinysql/sql"
	"tinysql/storage"
	"tinysql/tx"
)

func main() {
	pager, err := storage.NewPager("/tmp/test_cli.db")
	if err != nil {
		panic(err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	tm.LoadTables()

	txm, _ := tx.NewTransactionManager(tm, "/tmp/test_cli.db.wal")
	defer txm.Close()
	txm.Recover()

	executor := sql.NewExecutor(tm)

	// 模拟 CLI 的 SQL 文本
	sqlText := "INSERT INTO t VALUES (1, 'Alice');"
	
	fmt.Println("Parsing:", sqlText)
	stmt, err := sql.ParseSQL(sqlText)
	if err != nil {
		fmt.Println("Parse error:", err)
		return
	}
	
	fmt.Println("Executing...")
	result, err := executor.Execute(stmt)
	if err != nil {
		fmt.Println("Execute error:", err)
		return
	}
	
	fmt.Printf("Result type: %T\n", result)
	if result == nil {
		fmt.Println("Result is nil!")
	} else {
		fmt.Println("Success")
	}
}
