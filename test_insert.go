package main

import (
	"fmt"
	"tinysql/sql"
	"tinysql/storage"
	"tinysql/tx"
)

func main() {
	pager, err := storage.NewPager("/tmp/test_insert2.db")
	if err != nil {
		panic(err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	tm.LoadTables()

	txm, _ := tx.NewTransactionManager(tm, "/tmp/test_insert2.db.wal")
	defer txm.Close()
	txm.Recover()

	executor := sql.NewExecutor(tm)

	// 1. CREATE TABLE
	stmt1, _ := sql.ParseSQL("CREATE TABLE users (id INT, name TEXT);")
	_, err = executor.Execute(stmt1)
	fmt.Println("CREATE:", err)

	// 2. INSERT without column names
	stmt2, _ := sql.ParseSQL("INSERT INTO users VALUES (1, 'Alice');")
	fmt.Printf("INSERT stmt: Columns=%v, Values=%v\n", stmt2.(*sql.InsertStmt).Columns, stmt2.(*sql.InsertStmt).Values)
	
	_, err = executor.Execute(stmt2)
	fmt.Println("INSERT:", err)

	// 3. SELECT
	stmt3, _ := sql.ParseSQL("SELECT * FROM users;")
	result, err := executor.Execute(stmt3)
	fmt.Println("SELECT:", err)
	fmt.Printf("Result: %+v\n", result)
}
