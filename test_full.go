package main

import (
	"fmt"
	"tinysql/sql"
	"tinysql/storage"
	"tinysql/tx"
)

func main() {
	pager, err := storage.NewPager("/tmp/test_full.db")
	if err != nil {
		panic(err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	tm.LoadTables()

	txm, _ := tx.NewTransactionManager(tm, "/tmp/test_full.db.wal")
	defer txm.Close()
	txm.Recover()

	executor := sql.NewExecutor(tm)

	// 1. CREATE TABLE
	stmt1, _ := sql.ParseSQL("CREATE TABLE t (id INT, name TEXT);")
	result1, err1 := executor.Execute(stmt1)
	fmt.Println("CREATE:", err1)
	if result1 != nil {
		fmt.Printf("  Result: %T\n", result1)
	}

	// 检查内存中的表
	if _, ok := tm.GetTable("t"); ok {
		fmt.Println("  Table 't' in memory: YES")
	} else {
		fmt.Println("  Table 't' in memory: NO")
	}

	// 2. INSERT
	stmt2, _ := sql.ParseSQL("INSERT INTO t VALUES (1, 'Alice');")
	fmt.Println("INSERT: parsing OK")
	
	result2, err2 := executor.Execute(stmt2)
	fmt.Println("INSERT:", err2)
	if result2 != nil {
		fmt.Printf("  Result: %T\n", result2)
	}
}
