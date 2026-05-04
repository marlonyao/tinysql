package tx

import (
	"encoding/binary"
	"fmt"
	"os"
	"sync"
)

const walMagic = "TWAL"
const walVersion = 1

// WALType 日志类型
type WALType byte

const (
	WALBegin  WALType = 1
	WALInsert WALType = 2
	WALCommit WALType = 3
	WALRollback WALType = 4
)

// WALRecord 单条日志记录
type WALRecord struct {
	TXID      uint64
	Type      WALType
	TableName string
	RowID     int    // B+Tree 逻辑 rowid（替代 PageID+SlotIdx）
	Before    []byte // UNDO 用
	After     []byte // REDO 用
}

// WAL 预写日志
type WAL struct {
	file *os.File
	mu   sync.Mutex
	lsn  uint64 // 当前日志序列号
}

// OpenWAL 打开 WAL 文件（如果不存在则创建）
func OpenWAL(path string) (*WAL, error) {
	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}

	wal := &WAL{file: file, lsn: 0}

	// 检查文件是否为空，如果是则写头部
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if stat.Size() == 0 {
		if err := wal.writeHeader(); err != nil {
			return nil, err
		}
	} else {
		// 读取已有日志，计算最大 LSN
		records, err := wal.ReadAll()
		if err != nil {
			return nil, err
		}
		for _, r := range records {
			if r.TXID > wal.lsn {
				wal.lsn = r.TXID
			}
		}
		wal.lsn++ // 下一条从最大值+1 开始
	}

	return wal, nil
}

func (w *WAL) writeHeader() error {
	_, err := w.file.Write([]byte(walMagic))
	if err != nil {
		return err
	}
	buf := make([]byte, 4)
	binary.LittleEndian.PutUint32(buf, walVersion)
	_, err = w.file.Write(buf)
	return err
}

// Write 写入一条记录
func (w *WAL) Write(r *WALRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.lsn++

	// 计算长度
	tableNameBytes := []byte(r.TableName)
	length := 8 + 1 + 2 + len(tableNameBytes) + 8 + 4 + len(r.Before) + 4 + len(r.After)

	buf := make([]byte, 4+length)
	offset := 0

	// Length
	binary.LittleEndian.PutUint32(buf[offset:offset+4], uint32(length))
	offset += 4
	// TXID
	binary.LittleEndian.PutUint64(buf[offset:offset+8], r.TXID)
	offset += 8
	// Type
	buf[offset] = byte(r.Type)
	offset += 1
	// TableName
	binary.LittleEndian.PutUint16(buf[offset:offset+2], uint16(len(tableNameBytes)))
	offset += 2
	copy(buf[offset:], tableNameBytes)
	offset += len(tableNameBytes)
	// RowID (int64)
	binary.LittleEndian.PutUint64(buf[offset:offset+8], uint64(r.RowID))
	offset += 8
	// Before
	binary.LittleEndian.PutUint32(buf[offset:offset+4], uint32(len(r.Before)))
	offset += 4
	copy(buf[offset:], r.Before)
	offset += len(r.Before)
	// After
	binary.LittleEndian.PutUint32(buf[offset:offset+4], uint32(len(r.After)))
	offset += 4
	copy(buf[offset:], r.After)
	offset += len(r.After)

	_, err := w.file.Write(buf)
	return err
}

// Flush 刷盘
func (w *WAL) Flush() error {
	return w.file.Sync()
}

// ReadAll 读取所有记录
func (w *WAL) ReadAll() ([]*WALRecord, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	// 回到文件开头
	if _, err := w.file.Seek(0, os.SEEK_SET); err != nil {
		return nil, err
	}

	// 读 Magic
	magic := make([]byte, 4)
	if _, err := w.file.Read(magic); err != nil {
		return nil, err
	}
	if string(magic) != walMagic {
		return nil, fmt.Errorf("invalid WAL magic: %s", string(magic))
	}

	// 读 Version
	verBuf := make([]byte, 4)
	if _, err := w.file.Read(verBuf); err != nil {
		return nil, err
	}

	var records []*WALRecord
	for {
		// 读 Length
		lenBuf := make([]byte, 4)
		_, err := w.file.Read(lenBuf)
		if err != nil {
			break // EOF
		}
		length := binary.LittleEndian.Uint32(lenBuf)

		data := make([]byte, length)
		if _, err := w.file.Read(data); err != nil {
			break
		}

		r := parseRecord(data)
		records = append(records, r)
	}

	return records, nil
}

func parseRecord(data []byte) *WALRecord {
	offset := 0
	r := &WALRecord{}

	r.TXID = binary.LittleEndian.Uint64(data[offset : offset+8])
	offset += 8
	r.Type = WALType(data[offset])
	offset += 1

	tableLen := binary.LittleEndian.Uint16(data[offset : offset+2])
	offset += 2
	r.TableName = string(data[offset : offset+int(tableLen)])
	offset += int(tableLen)

	r.RowID = int(binary.LittleEndian.Uint64(data[offset : offset+8]))
	offset += 8

	beforeLen := binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4
	r.Before = data[offset : offset+int(beforeLen)]
	offset += int(beforeLen)

	afterLen := binary.LittleEndian.Uint32(data[offset : offset+4])
	offset += 4
	r.After = data[offset : offset+int(afterLen)]

	return r
}

// Truncate 清空 WAL 文件（已提交事务可以截断）
func (w *WAL) Truncate() error {
	w.mu.Lock()
	defer w.mu.Unlock()

	if err := w.file.Close(); err != nil {
		return err
	}

	// 重新创建空文件
	file, err := os.Create(w.file.Name())
	if err != nil {
		return err
	}
	w.file = file
	w.lsn = 0
	return w.writeHeader()
}

// Close 关闭 WAL
func (w *WAL) Close() error {
	return w.file.Close()
}
