package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"

	"tinysql/sql"
	"tinysql/storage"
	"tinysql/tx"
)

type Server struct {
	tm       *storage.TableManager
	txm      *tx.TransactionManager
	executor *sql.Executor
}

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: tinysql-server <database_file> [port]")
		os.Exit(1)
	}

	dbPath := os.Args[1]
	port := "8080"
	if len(os.Args) >= 3 {
		port = os.Args[2]
	}

	// 打开数据库
	pager, err := storage.NewPager(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer pager.Close()

	tm := storage.NewTableManager(pager)
	txm, err := tx.NewTransactionManager(tm, dbPath+".wal")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer txm.Close()
	_ = txm.Recover()

	server := &Server{
		tm:       tm,
		txm:      txm,
		executor: sql.NewExecutor(tm),
	}

	http.HandleFunc("/execute", server.handleExecute)
	http.HandleFunc("/tables", server.handleTables)

	fmt.Printf("TinySQL Server starting on :%s\n", port)
	fmt.Printf("Database: %s\n", dbPath)
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Printf("  curl -X POST http://localhost:%s/execute -H 'Content-Type: application/json' -d '{\"sql\":\"SELECT * FROM users\"}'\n", port)
	fmt.Printf("  curl -X POST http://localhost:%s/execute -H 'Content-Type: application/json' -d '{\"sql\":\"CREATE TABLE t (id INT, name VARCHAR);\"}'\n", port)
	fmt.Printf("  curl -X POST http://localhost:%s/execute -H 'Content-Type: application/json' -d '{\"sql\":\"INSERT INTO t VALUES (1, 'Alice');\"}'\n", port)
	fmt.Println()
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
		os.Exit(1)
	}
}

func (s *Server) handleExecute(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SQL string `json:"sql"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, err)
		return
	}

	stmt, err := sql.ParseSQL(req.SQL)
	if err != nil {
		jsonError(w, err)
		return
	}

	result, err := s.executor.Execute(stmt)
	if err != nil {
		jsonError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	
	switch res := result.(type) {
	case *sql.SelectResult:
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"type":    "select",
			"columns": res.Columns,
			"rows":    res.Rows,
			"count":   len(res.Rows),
		})
	case *sql.InsertResult:
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success":    true,
			"type":       "insert",
			"affected":   res.RowsAffected,
		})
	case *sql.CreateTableResult:
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"type":    "create",
			"message": res.Message,
		})
	default:
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"type":    "other",
			"result":  fmt.Sprintf("%v", result),
		})
	}
}

func (s *Server) handleTables(w http.ResponseWriter, r *http.Request) {
	tables := s.tm.ListTables()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"tables": tables,
		"count":  len(tables),
	})
}

func jsonError(w http.ResponseWriter, err error) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusBadRequest)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": false,
		"error":   err.Error(),
	})
}
