#!/bin/bash
cd /root/.openclaw/workspace/tinysql

# 启动服务器（后台）
./tinysql-server test_api.db 8080 &
SERVER_PID=$!
sleep 2

echo "=== 帮助信息 ==="
curl -s http://localhost:8080/ | python3 -m json.tool

echo
echo "=== 建表 ==="
curl -s -X POST http://localhost:8080/execute \
  -H "Content-Type: application/json" \
  -d '{"sql":"CREATE TABLE products (id INT, name VARCHAR, price INT);"}' | python3 -m json.tool

echo
echo "=== 插入数据 ==="
curl -s -X POST http://localhost:8080/execute \
  -H "Content-Type: application/json" \
  -d '{"sql":"INSERT INTO products VALUES (1, '\''iPhone'\'', 999);"}' | python3 -m json.tool

curl -s -X POST http://localhost:8080/execute \
  -H "Content-Type: application/json" \
  -d '{"sql":"INSERT INTO products VALUES (2, '\''MacBook'\'', 1999);"}' | python3 -m json.tool

echo
echo "=== 查询 ==="
curl -s -X POST http://localhost:8080/execute \
  -H "Content-Type: application/json" \
  -d '{"sql":"SELECT * FROM products;"}' | python3 -m json.tool

echo
echo "=== 表列表 ==="
curl -s http://localhost:8080/tables | python3 -m json.tool

# 关闭服务器
kill $SERVER_PID 2>/dev/null
echo
echo "=== 测试完成 ==="
