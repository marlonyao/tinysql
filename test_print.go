package main

import (
	"fmt"
	"tinysql/sql"
	"tinysql/storage"
	"tinysql/tx"
)

func printResult(result sql.Result) {
	switch r := result.(type) {
	case *sql.CreateTableResult:
		fmt.Println(r.Message)
	case *sql.InsertResult:
		fmt.Printf("Affected %d row(s)\n", r.RowsAffected)
	case *sql.SelectResult:
		if len(r.Rows) == 0 {
			fmt.Println("(no rows)")
			return
		}
		fmt.Println("SELECT result...")
	}
}

func main() {
	pager, err := storage.NewPager("/tmp/test_print.db")
	if err != nil {
		panic(err)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	tm.LoadTables()

	txm, _ := tx.NewTransactionManager(tm, "/tmp/test_print.db.wal")
	defer txm.Close()
	txm.Recover()

	executor := sql.NewExecutor(tm)

	stmt1, _ := sql.ParseSQL("CREATE TABLE t (id INT, name TEXT);")
	result1, _ := executor.Execute(stmt1)
	fmt.Printf("CREATE result type: %T, nil=%v\n", result1, result1 == nil)
	printResult(result1)

	stmt2, _ := sql.ParseSQL("INSERT INTO t VALUES (1, 'Alice');")
	result2, _ := executor.Execute(stmt2)
	fmt.Printf("INSERT result type: %T, nil=%v\n", result2, result2 == nil)
	printResult(result2)
}
