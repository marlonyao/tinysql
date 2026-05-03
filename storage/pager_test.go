package storage

import (
	"path/filepath"
	"testing"
)

func TestPagerAllocateAndRead(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	// 创建 Pager，数据库文件不存在时自动初始化
	p, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager failed: %v", err)
	}

	// 分配一个新 Page
	pageID, err := p.Allocate()
	if err != nil {
		t.Fatalf("Allocate failed: %v", err)
	}
	if pageID != 1 {
		t.Fatalf("expected first page id=1, got %d", pageID)
	}

	// 写入一些数据到 Page
	page := p.GetPage(pageID)
	copy(page.Data(), []byte("hello tinysql"))
	page.SetDirty(true)

	// 刷盘
	if err := p.Flush(pageID); err != nil {
		t.Fatalf("Flush failed: %v", err)
	}

	// 关闭 Pager
	if err := p.Close(); err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	// 重新打开，验证数据还在
	p2, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	defer p2.Close()

	page2 := p2.GetPage(pageID)
	got := string(page2.Data()[:len("hello tinysql")])
	if got != "hello tinysql" {
		t.Fatalf("expected 'hello tinysql', got '%s'", got)
	}
}

func TestPagerMultiplePages(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	p, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager failed: %v", err)
	}
	defer p.Close()

	// 分配多个 Page，写入不同数据
	for i := 1; i <= 5; i++ {
		pid, err := p.Allocate()
		if err != nil {
			t.Fatalf("Allocate failed: %v", err)
		}
		if pid != uint32(i) {
			t.Fatalf("expected page id=%d, got %d", i, pid)
		}
		page := p.GetPage(pid)
		page.Data()[0] = byte(i)
		page.SetDirty(true)
	}

	// 刷盘所有脏页
	if err := p.FlushAll(); err != nil {
		t.Fatalf("FlushAll failed: %v", err)
	}

	// 验证每个 Page 的数据
	for i := 1; i <= 5; i++ {
		page := p.GetPage(uint32(i))
		if page.Data()[0] != byte(i) {
			t.Fatalf("page %d: expected %d, got %d", i, i, page.Data()[0])
		}
	}
}

func TestPagerPageSize(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	p, err := NewPager(dbPath)
	if err != nil {
		t.Fatalf("NewPager failed: %v", err)
	}
	defer p.Close()

	pid, _ := p.Allocate()
	page := p.GetPage(pid)
	if len(page.Data()) != PageSize - PageHeaderSize {
		t.Fatalf("expected data size %d, got %d", PageSize - PageHeaderSize, len(page.Data()))
	}
}
