package main

import (
	"fmt"
	"time"
	"tinysql/sql"
	"tinysql/storage"
	"tinysql/tx"
)

func main() {
	pager, err := storage.NewPager("/tmp/test_insert3.db")
	if err != nil {
		panic(err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	tm.LoadTables()

	txm, _ := tx.NewTransactionManager(tm, "/tmp/test_insert3.db.wal")
	defer txm.Close()
	txm.Recover()

	executor := sql.NewExecutor(tm)

	fmt.Println("Step 1: CREATE TABLE")
	stmt1, _ := sql.ParseSQL("CREATE TABLE users (id INT, name TEXT);")
	_, err = executor.Execute(stmt1)
	fmt.Println("CREATE result:", err)

	fmt.Println("Step 2: INSERT")
	stmt2, err2 := sql.ParseSQL("INSERT INTO users VALUES (1, 'Alice');")
	fmt.Println("Parse result:", err2)
	fmt.Printf("Columns: %v, Values count: %d\n", stmt2.(*sql.InsertStmt).Columns, len(stmt2.(*sql.InsertStmt).Values))
	
	done := make(chan struct{})
	var insertErr error
	go func() {
		_, insertErr = executor.Execute(stmt2)
		close(done)
	}()
	
	select {
	case <-done:
		fmt.Println("INSERT result:", insertErr)
	case <-time.After(5 * time.Second):
		fmt.Println("INSERT TIMEOUT - likely deadlock or infinite loop!")
	}
}
