package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"tinysql/sql"
	"tinysql/storage"
	"tinysql/tx"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: tinysql <database_file>")
		os.Exit(1)
	}

	dbPath := os.Args[1]

	pager, err := storage.NewPager(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error opening database: %v\n", err)
		os.Exit(1)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	if err := tm.LoadTables(); err != nil {
		fmt.Fprintf(os.Stderr, "Error loading tables: %v\n", err)
		os.Exit(1)
	}

	txm, err := tx.NewTransactionManager(tm, dbPath+".wal")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error initializing transaction manager: %v\n", err)
		os.Exit(1)
	}
	defer txm.Close()

	if err := txm.Recover(); err != nil {
		fmt.Fprintf(os.Stderr, "Error recovering: %v\n", err)
		os.Exit(1)
	}

	executor := sql.NewExecutorWithTx(tm, txm)

	fmt.Println("TinySQL v0.1")
	fmt.Println("Type .help for usage hints. Type .quit to exit.")
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("tinysql> ")
		if !scanner.Scan() {
			break
		}

		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}

		if strings.HasPrefix(line, ".") {
			handleMetaCommand(line, tm)
			continue
		}

		sqlText := line
		for !strings.HasSuffix(sqlText, ";") {
			fmt.Print("   ...> ")
			if !scanner.Scan() {
				break
			}
			sqlText += " " + strings.TrimSpace(scanner.Text())
		}

		stmt, err := sql.ParseSQL(sqlText)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		result, err := executor.Execute(stmt)
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			continue
		}

		printResult(result)
	}

	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "Error reading input: %v\n", err)
	}
}

func handleMetaCommand(cmd string, tm *storage.TableManager) {
	switch cmd {
	case ".quit", ".exit", ".q":
		fmt.Println("Bye.")
		os.Exit(0)
	case ".tables":
		tables := tm.ListTables()
		if len(tables) == 0 {
			fmt.Println("(no tables)")
		} else {
			fmt.Println(strings.Join(tables, "  "))
		}
	case ".schema":
		fmt.Println("Schema:")
		for _, name := range tm.ListTables() {
			table, _ := tm.GetTable(name)
			var cols []string
			for _, col := range table.Columns {
				cols = append(cols, fmt.Sprintf("%s %s", col.Name, col.Type))
			}
			fmt.Printf("  CREATE TABLE %s (%s);\n", name, strings.Join(cols, ", "))
		}
	case ".help":
		fmt.Println("Available commands:")
		fmt.Println("  .tables     Show all tables")
		fmt.Println("  .schema     Show database schema")
		fmt.Println("  .quit       Exit this program")
		fmt.Println("  .help       Show this help message")
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
	}
}

func printResult(result sql.Result) {
	switch r := result.(type) {
	case *sql.CreateTableResult:
		fmt.Println(r.Message)
	case *sql.InsertResult:
		fmt.Printf("Affected %d row(s)\n", r.RowsAffected)
	case *sql.TxResult:
		fmt.Println(r.Message)
	case *sql.SelectResult:
		if len(r.Rows) == 0 {
			fmt.Println("(no rows)")
			return
		}
		fmt.Println(strings.Join(r.Columns, " | "))
		var sep []string
		for _, col := range r.Columns {
			sep = append(sep, strings.Repeat("-", len(col)))
		}
		fmt.Println(strings.Join(sep, "-+-"))
		for _, row := range r.Rows {
			var strs []string
			for _, val := range row {
				if val == nil {
					strs = append(strs, "NULL")
				} else {
					strs = append(strs, fmt.Sprintf("%v", val))
				}
			}
			fmt.Println(strings.Join(strs, " | "))
		}
		fmt.Printf("(%d row(s))\n", len(r.Rows))
	}
}
