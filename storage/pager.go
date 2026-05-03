package storage

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
)

// Pager 管理数据库文件的页分配和读写
type Pager struct {
	path       string
	file       *os.File
	pages      map[uint32]*Page  // 内存缓存
	mutex      sync.RWMutex
	nextPageID uint32            // 下一个待分配的 page id
}

// NewPager 打开或创建数据库文件
func NewPager(path string) (*Pager, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, fmt.Errorf("open db file: %w", err)
	}

	pager := &Pager{
		path:       path,
		file:       f,
		pages:      make(map[uint32]*Page),
		nextPageID: 1, // page 0 是 meta page，数据从 1 开始
	}

	// 获取文件大小
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, fmt.Errorf("stat db file: %w", err)
	}

	if info.Size() == 0 {
		// 新文件：初始化 meta page
		if err := pager.initMetaPage(); err != nil {
			f.Close()
			return nil, fmt.Errorf("init meta page: %w", err)
		}
	} else {
		// 已有文件：读取 meta page 恢复 nextPageID
		if err := pager.loadMetaPage(); err != nil {
			f.Close()
			return nil, fmt.Errorf("load meta page: %w", err)
		}
	}

	return pager, nil
}

// initMetaPage 初始化数据库文件的 meta page
func (p *Pager) initMetaPage() error {
	meta := newPage(0)
	meta.setHeaderType(PageTypeMeta)
	// page 0 data area 布局:
	// [16:20] tableCount (uint32)
	// [20:24] nextPageID (uint32)
	p.writePageIDAt(meta.data, 16, 0) // tableCount = 0
	p.writePageIDAt(meta.data, 20, 1) // nextPageID = 1
	meta.serializeHeader()
	meta.SetDirty(true)
	p.pages[0] = meta
	return p.Flush(0)
}

// loadMetaPage 从已有文件恢复状态
func (p *Pager) loadMetaPage() error {
	meta := newPage(0)
	raw := make([]byte, PageSize)
	if _, err := p.file.ReadAt(raw, 0); err != nil {
		return fmt.Errorf("read meta page: %w", err)
	}
	if err := meta.loadFromBytes(raw); err != nil {
		return err
	}
	p.pages[0] = meta
	p.nextPageID = p.readPageIDAt(meta.data, 20)
	return nil
}

// Allocate 分配一个新 Page，返回 pageID
func (p *Pager) Allocate() (uint32, error) {
	p.mutex.Lock()
	defer p.mutex.Unlock()

	pid := p.nextPageID
	p.nextPageID++

	page := newPage(pid)
	page.setHeaderType(PageTypeTable) // 默认作为数据页
	page.SetDirty(true)
	p.pages[pid] = page

	// 更新 meta page 的 nextPageID
	p.updateNextPageID()

	return pid, nil
}

func (p *Pager) updateNextPageID() {
	meta, ok := p.pages[0]
	if !ok {
		meta = newPage(0)
		p.pages[0] = meta
	}
	p.writePageIDAt(meta.data, 20, p.nextPageID)
	meta.SetDirty(true)
}

// GetPage 获取指定 pageID 的 Page（从缓存或磁盘加载）
func (p *Pager) GetPage(id uint32) *Page {
	p.mutex.RLock()
	page, ok := p.pages[id]
	p.mutex.RUnlock()
	if ok {
		return page
	}

	p.mutex.Lock()
	defer p.mutex.Unlock()

	// double check
	page, ok = p.pages[id]
	if ok {
		return page
	}

	// 从磁盘加载
	page = newPage(id)
	raw := make([]byte, PageSize)
	offset := int64(id) * PageSize
	if _, err := p.file.ReadAt(raw, offset); err != nil {
		// 读取失败也返回空 page（允许新分配的 page 还没刷盘）
		page.SetDirty(true)
	} else {
		if err := page.loadFromBytes(raw); err != nil {
			// 校验失败，但返回 page 让上层处理
			fmt.Printf("warning: page %d checksum error\n", id)
		}
	}
	p.pages[id] = page
	return page
}

// Flush 将指定 page 刷到磁盘
func (p *Pager) Flush(id uint32) error {
	p.mutex.Lock()
	page, ok := p.pages[id]
	p.mutex.Unlock()
	if !ok {
		return fmt.Errorf("page %d not found", id)
	}

	if !page.IsDirty() {
		return nil
	}

	page.Lock()
	defer page.Unlock()

	page.serializeHeader()
	offset := int64(id) * PageSize
	if _, err := p.file.WriteAt(page.data, offset); err != nil {
		return fmt.Errorf("write page %d: %w", id, err)
	}
	page.SetDirty(false)
	return nil
}

// FlushAll 刷所有脏页
func (p *Pager) FlushAll() error {
	p.mutex.RLock()
	ids := make([]uint32, 0, len(p.pages))
	for id, page := range p.pages {
		if page.IsDirty() {
			ids = append(ids, id)
		}
	}
	p.mutex.RUnlock()

	for _, id := range ids {
		if err := p.Flush(id); err != nil {
			return err
		}
	}
	return nil
}

// Close 关闭数据库文件
func (p *Pager) Close() error {
	if err := p.FlushAll(); err != nil {
		return err
	}
	return p.file.Close()
}

// 辅助函数：在 page data 的指定 offset 读写 uint32
func (p *Pager) readPageIDAt(data []byte, offset int) uint32 {
	return binary.LittleEndian.Uint32(data[offset : offset+4])
}

func (p *Pager) writePageIDAt(data []byte, offset int, val uint32) {
	binary.LittleEndian.PutUint32(data[offset:offset+4], val)
}
