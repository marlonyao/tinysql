package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCLI_BasicWorkflow 测试完整的 CLI 工作流
func TestCLI_BasicWorkflow(t *testing.T) {
	// 创建临时目录
	tmpDir, err := os.MkdirTemp("", "tinysql-cli-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// 编译 CLI
	binPath := filepath.Join(tmpDir, "tinysql")
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/tinysql")
	buildCmd.Dir = filepath.Join("..", "..")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// 启动 CLI
	cmd := exec.Command(binPath, dbPath)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}

	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer cmd.Process.Kill()

	// 读取欢迎信息
	buf := make([]byte, 1024)
	n, _ := stdout.Read(buf)
	welcome := string(buf[:n])
	if !strings.Contains(welcome, "TinySQL") {
		t.Fatalf("expected welcome message, got: %s", welcome)
	}

	// 测试 CREATE TABLE
	fmt.Fprintln(stdin, "CREATE TABLE users (id INT, name VARCHAR, active BOOL);")
	time.Sleep(100 * time.Millisecond)
	n, _ = stdout.Read(buf)
	output := string(buf[:n])
	if !strings.Contains(output, "created") {
		t.Fatalf("expected 'created', got: %s", output)
	}

	// 测试 INSERT
	fmt.Fprintln(stdin, "INSERT INTO users (id, name, active) VALUES (1, 'Alice', true);")
	time.Sleep(100 * time.Millisecond)
	n, _ = stdout.Read(buf)
	output = string(buf[:n])
	if !strings.Contains(output, "Affected 1 row(s)") {
		t.Fatalf("expected 'Affected 1 row(s)', got: %s", output)
	}

	// 测试 SELECT
	fmt.Fprintln(stdin, "SELECT * FROM users;")
	time.Sleep(100 * time.Millisecond)
	n, _ = stdout.Read(buf)
	output = string(buf[:n])
	if !strings.Contains(output, "Alice") {
		t.Fatalf("expected 'Alice' in output, got: %s", output)
	}

	// 测试 .tables
	fmt.Fprintln(stdin, ".tables")
	time.Sleep(100 * time.Millisecond)
	n, _ = stdout.Read(buf)
	output = string(buf[:n])
	if !strings.Contains(output, "users") {
		t.Fatalf("expected 'users' in tables, got: %s", output)
	}

	// 测试 .schema
	fmt.Fprintln(stdin, ".schema")
	time.Sleep(100 * time.Millisecond)
	n, _ = stdout.Read(buf)
	output = string(buf[:n])
	if !strings.Contains(output, "CREATE TABLE users") {
		t.Fatalf("expected schema output, got: %s", output)
	}

	// 测试 .quit
	fmt.Fprintln(stdin, ".quit")
	time.Sleep(100 * time.Millisecond)
	n, _ = stdout.Read(buf)
	output = string(buf[:n])
	if !strings.Contains(output, "Bye") {
		t.Fatalf("expected 'Bye', got: %s", output)
	}

	// 检查 stderr
	stderrBuf := make([]byte, 1024)
	n, _ = stderr.Read(stderrBuf)
	if n > 0 {
		t.Fatalf("unexpected stderr: %s", string(stderrBuf[:n]))
	}
}

// TestCLI_MultipleInserts 测试多条插入和查询
func TestCLI_MultipleInserts(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinysql-cli-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "multi.db")

	binPath := filepath.Join(tmpDir, "tinysql")
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/tinysql")
	buildCmd.Dir = filepath.Join("..", "..")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	cmd := exec.Command(binPath, dbPath)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()
	defer cmd.Process.Kill()

	// 跳过欢迎信息
	buf := make([]byte, 1024)
	stdout.Read(buf)

	// 创建表
	fmt.Fprintln(stdin, "CREATE TABLE nums (id INT, val INT);")
	time.Sleep(100 * time.Millisecond)
	stdout.Read(buf)

	// 插入 3 行
	fmt.Fprintln(stdin, "INSERT INTO nums (id, val) VALUES (1, 100);")
	time.Sleep(50 * time.Millisecond)
	stdout.Read(buf)
	fmt.Fprintln(stdin, "INSERT INTO nums (id, val) VALUES (2, 200);")
	time.Sleep(50 * time.Millisecond)
	stdout.Read(buf)
	fmt.Fprintln(stdin, "INSERT INTO nums (id, val) VALUES (3, 300);")
	time.Sleep(50 * time.Millisecond)
	stdout.Read(buf)

	// 查询所有
	fmt.Fprintln(stdin, "SELECT * FROM nums;")
	time.Sleep(100 * time.Millisecond)
	n, _ := stdout.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "100") || !strings.Contains(output, "200") || !strings.Contains(output, "300") {
		t.Fatalf("expected all values in output, got: %s", output)
	}
	if !strings.Contains(output, "(3 row(s))") {
		t.Fatalf("expected 3 rows, got: %s", output)
	}

	fmt.Fprintln(stdin, ".quit")
}

// TestCLI_WHEREClause 测试 WHERE 条件
func TestCLI_WHEREClause(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "tinysql-cli-test-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "where.db")

	binPath := filepath.Join(tmpDir, "tinysql")
	buildCmd := exec.Command("go", "build", "-o", binPath, "./cmd/tinysql")
	buildCmd.Dir = filepath.Join("..", "..")
	if out, err := buildCmd.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	cmd := exec.Command(binPath, dbPath)
	stdin, _ := cmd.StdinPipe()
	stdout, _ := cmd.StdoutPipe()
	cmd.Start()
	defer cmd.Process.Kill()

	buf := make([]byte, 1024)
	stdout.Read(buf) // 跳过欢迎信息

	fmt.Fprintln(stdin, "CREATE TABLE items (id INT, name VARCHAR, price INT);")
	time.Sleep(100 * time.Millisecond)
	stdout.Read(buf)

	fmt.Fprintln(stdin, "INSERT INTO items (id, name, price) VALUES (1, 'apple', 5);")
	time.Sleep(50 * time.Millisecond)
	stdout.Read(buf)
	fmt.Fprintln(stdin, "INSERT INTO items (id, name, price) VALUES (2, 'banana', 3);")
	time.Sleep(50 * time.Millisecond)
	stdout.Read(buf)
	fmt.Fprintln(stdin, "INSERT INTO items (id, name, price) VALUES (3, 'cherry', 10);")
	time.Sleep(50 * time.Millisecond)
	stdout.Read(buf)

	// WHERE price > 3
	fmt.Fprintln(stdin, "SELECT name, price FROM items WHERE price > 3;")
	time.Sleep(100 * time.Millisecond)
	n, _ := stdout.Read(buf)
	output := string(buf[:n])

	if !strings.Contains(output, "apple") || !strings.Contains(output, "cherry") {
		t.Fatalf("expected apple and cherry, got: %s", output)
	}
	if strings.Contains(output, "banana") {
		t.Fatalf("banana should be filtered out, got: %s", output)
	}

	fmt.Fprintln(stdin, ".quit")
}

// TestCLI_NoDatabaseArg 测试不带参数启动
func TestCLI_NoDatabaseArg(t *testing.T) {
	cmd := exec.Command("go", "run", ".")
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("expected error when no database arg provided")
	}
	if !strings.Contains(string(output), "Usage") {
		t.Fatalf("expected usage message, got: %s", string(output))
	}
}
