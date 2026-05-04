package main

import (
	"bufio"
	"fmt"
	"strings"
	"time"
)

func main() {
	input := "CREATE TABLE t (id INT, name TEXT);\nINSERT INTO t VALUES (1, 'Alice');\n.quit\n"
	scanner := bufio.NewScanner(strings.NewReader(input))
	
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		fmt.Printf("Line %d: %q\n", lineNum, line)
		if lineNum >= 3 {
			break
		}
	}
	
	if err := scanner.Err(); err != nil {
		fmt.Println("Scanner error:", err)
	}
	
	fmt.Println("Done")
	time.Sleep(100 * time.Millisecond)
}
