package storage

import (
	"encoding/binary"
	"fmt"
	"hash/crc32"
	"sync"
)

const PageSize = 4096

// PageHeader 占用前 16 字节
// [0:4]  pageID
// [4:8]  pageType
// [8:12] checksum
// [12:16] free space offset
const PageHeaderSize = 16

// Page 是内存中的页对象
type Page struct {
	id     uint32
	data   []byte // 4KB
	dirty  bool
	pins   int    // 引用计数
	mutex  sync.RWMutex
}

func newPage(id uint32) *Page {
	return &Page{
		id:   id,
		data: make([]byte, PageSize),
	}
}

func (p *Page) ID() uint32     { return p.id }
func (p *Page) Data() []byte   { return p.data[PageHeaderSize:] }
func (p *Page) IsDirty() bool  { return p.dirty }
func (p *Page) SetDirty(v bool) { p.dirty = v }

func (p *Page) Lock()                    { p.mutex.Lock() }
func (p *Page) Unlock()                  { p.mutex.Unlock() }
func (p *Page) headerID() uint32         { return binary.LittleEndian.Uint32(p.data[0:4]) }
func (p *Page) headerType() uint32        { return binary.LittleEndian.Uint32(p.data[4:8]) }
func (p *Page) headerChecksum() uint32    { return binary.LittleEndian.Uint32(p.data[8:12]) }
func (p *Page) headerFreeOffset() uint32 { return binary.LittleEndian.Uint32(p.data[12:16]) }

func (p *Page) setHeaderID(v uint32)       { binary.LittleEndian.PutUint32(p.data[0:4], v) }
func (p *Page) setHeaderType(v uint32)    { binary.LittleEndian.PutUint32(p.data[4:8], v) }
func (p *Page) setHeaderChecksum(v uint32) { binary.LittleEndian.PutUint32(p.data[8:12], v) }
func (p *Page) setHeaderFreeOffset(v uint32) { binary.LittleEndian.PutUint32(p.data[12:16], v) }

func (p *Page) calcChecksum() uint32 {
	// 校验和只计算数据区，不含 checksum 字段本身
	dataCopy := make([]byte, PageSize)
	copy(dataCopy, p.data)
	binary.LittleEndian.PutUint32(dataCopy[8:12], 0) // 清零 checksum 字段
	return crc32.ChecksumIEEE(dataCopy)
}

func (p *Page) validateChecksum() bool {
	return p.headerChecksum() == p.calcChecksum()
}

func (p *Page) serializeHeader() {
	p.setHeaderID(p.id)
	p.setHeaderChecksum(p.calcChecksum())
}

func (p *Page) loadFromBytes(raw []byte) error {
	if len(raw) != PageSize {
		return fmt.Errorf("invalid page size: %d", len(raw))
	}
	copy(p.data, raw)
	if !p.validateChecksum() {
		return fmt.Errorf("page %d checksum mismatch", p.id)
	}
	return nil
}

// PageType 常量
const (
	PageTypeMeta    = 1 // 元数据页（存储数据库基本信息）
	PageTypeTable   = 2 // 表数据页
	PageTypeIndex   = 3 // 索引页
	PageTypeFree    = 4 // 空闲页
)
