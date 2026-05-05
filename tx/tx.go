package tx

import (
	"fmt"
	"sync"

	"tinysql/storage"
)

// TransactionManager 事务管理器
type TransactionManager struct {
	tm        *storage.TableManager
	wal       *WAL
	mu        sync.Mutex
	nextTxID  uint64
	activeTxs *activeTxSet
}

// NewTransactionManager 创建事务管理器
func NewTransactionManager(tm *storage.TableManager, walPath string) (*TransactionManager, error) {
	wal, err := OpenWAL(walPath)
	if err != nil {
		return nil, fmt.Errorf("open wal: %w", err)
	}

	return &TransactionManager{
		tm:        tm,
		wal:       wal,
		nextTxID:  1,
		activeTxs: newActiveTxSet(),
	}, nil
}

// Begin 开始一个新事务
func (txm *TransactionManager) Begin() (*Transaction, error) {
	txm.mu.Lock()
	defer txm.mu.Unlock()

	txID := txm.nextTxID
	txm.nextTxID++

	// 写 BEGIN 记录到 WAL
	if err := txm.wal.Write(&WALRecord{
		TXID: txID,
		Type: WALBegin,
	}); err != nil {
		return nil, fmt.Errorf("write begin: %w", err)
	}
	if err := txm.wal.Flush(); err != nil {
		return nil, fmt.Errorf("flush begin: %w", err)
	}

	txm.activeTxs.Add(txID)

	return &Transaction{
		ID:         txID,
		tm:         txm.tm,
		wal:        txm.wal,
		pager:      txm.tm.GetPager(),
		dirtyPages: make(map[uint32]bool),
		active:     true,
	}, nil
}

// Commit 提交事务
func (txm *TransactionManager) Commit(tx *Transaction) error {
	if !tx.active {
		return fmt.Errorf("transaction already finished")
	}

	// 1. 写 COMMIT 记录
	if err := txm.wal.Write(&WALRecord{
		TXID: tx.ID,
		Type: WALCommit,
	}); err != nil {
		return fmt.Errorf("write commit: %w", err)
	}

	// 2. Flush WAL（保证 COMMIT 记录在磁盘上）
	if err := txm.wal.Flush(); err != nil {
		return fmt.Errorf("flush wal: %w", err)
	}

	// 3. Flush 所有脏页（保证数据在磁盘上）
	for pageID := range tx.dirtyPages {
		if err := tx.pager.Flush(pageID); err != nil {
			return fmt.Errorf("flush page %d: %w", pageID, err)
		}
	}

	// 4. 截断 WAL（已提交事务不需要保留）
	if err := txm.wal.Truncate(); err != nil {
		return fmt.Errorf("truncate wal: %w", err)
	}

	txm.activeTxs.Remove(tx.ID)
	tx.active = false
	return nil
}

// Rollback 回滚事务
func (txm *TransactionManager) Rollback(tx *Transaction) error {
	if !tx.active {
		return fmt.Errorf("transaction already finished")
	}

	// 1. 从 WAL 中读取当前事务的所有记录，执行 UNDO
	records, err := txm.wal.ReadAll()
	if err != nil {
		return fmt.Errorf("read wal: %w", err)
	}

	// 按 LSN 倒序 UNDO（从后往前回滚）
	for i := len(records) - 1; i >= 0; i-- {
		r := records[i]
		if r.TXID != tx.ID {
			continue
		}
		if r.Type == WALBegin {
			break // 到 BEGIN 为止
		}
		if r.Type == WALInsert {
			// UNDO INSERT = DELETE
			if err := tx.tm.DeleteByRowID(r.TableName, r.RowID); err != nil {
				return fmt.Errorf("undo insert: %w", err)
			}
		}
	}

	// 2. 写 ROLLBACK 记录
	if err := txm.wal.Write(&WALRecord{
		TXID: tx.ID,
		Type: WALRollback,
	}); err != nil {
		return fmt.Errorf("write rollback: %w", err)
	}
	if err := txm.wal.Flush(); err != nil {
		return fmt.Errorf("flush wal: %w", err)
	}

	// 3. 截断 WAL
	if err := txm.wal.Truncate(); err != nil {
		return fmt.Errorf("truncate wal: %w", err)
	}

	txm.activeTxs.Remove(tx.ID)
	tx.active = false
	return nil
}

// Recover 崩溃恢复：扫描 WAL，回滚未提交的事务
func (txm *TransactionManager) Recover() error {
	records, err := txm.wal.ReadAll()
	if err != nil {
		return fmt.Errorf("read wal: %w", err)
	}

	if len(records) == 0 {
		return nil // WAL 为空，无需恢复
	}

	// 按 TXID 分组，检查哪些事务已提交
	txRecords := make(map[uint64][]*WALRecord)
	txCommitted := make(map[uint64]bool)

	for _, r := range records {
		txRecords[r.TXID] = append(txRecords[r.TXID], r)
		if r.Type == WALCommit {
			txCommitted[r.TXID] = true
		}
	}

	// 对未提交的事务执行 UNDO
	for txID, recs := range txRecords {
		if txCommitted[txID] {
			continue // 已提交的事务不需要处理（数据已在磁盘）
		}

		// 倒序 UNDO
		for i := len(recs) - 1; i >= 0; i-- {
			r := recs[i]
			if r.Type == WALInsert {
				if err := txm.tm.DeleteByRowID(r.TableName, r.RowID); err != nil {
					return fmt.Errorf("recover undo insert tx=%d: %w", txID, err)
				}
			}
		}
	}

	// 恢复完成后截断 WAL
	return txm.wal.Truncate()
}

// Transaction 单个事务
type Transaction struct {
	ID         uint64
	tm         *storage.TableManager
	wal        *WAL
	pager      *storage.Pager
	dirtyPages map[uint32]bool
	active     bool
	readView   *ReadView // MVCC 快照（事务开始时创建）
}

// NewReadView 为当前事务创建 ReadView（Repeatable Read 语义）
func (txm *TransactionManager) NewReadView(trxID uint64) *ReadView {
	min, _, active := txm.activeTxs.Snapshot()
	return &ReadView{
		CreatorTrxID: trxID,
		MinTrxID:     min,
		MaxTrxID:     txm.nextTxID, // 全局下一个要分配的 ID = 已分配最大 ID + 1
		ActiveIDs:    active,
	}
}

// GetReadView 获取事务的 ReadView（懒创建）
func (tx *Transaction) GetReadView(txm *TransactionManager) *ReadView {
	if tx.readView == nil {
		tx.readView = txm.NewReadView(tx.ID)
	}
	return tx.readView
}

// Insert 事务内插入一行
func (tx *Transaction) Insert(tableName string, row *storage.Row) error {
	if !tx.active {
		return fmt.Errorf("transaction not active")
	}

	rowID, err := tx.tm.InsertTx(tableName, row)
	if err != nil {
		return err
	}

	// 序列化 rowData 用于 WAL
	table, _ := tx.tm.GetTable(tableName)
	rowData, _ := table.SerializeRow(row)

	// 写 WAL
	if err := tx.wal.Write(&WALRecord{
		TXID:      tx.ID,
		Type:      WALInsert,
		TableName: tableName,
		RowID:     rowID,
		Before:    nil,      // INSERT 没有 BeforeImage
		After:     rowData,  // AfterImage 用于 REDO
	}); err != nil {
		return fmt.Errorf("write wal: %w", err)
	}

	return nil
}

// Close 关闭事务管理器
func (txm *TransactionManager) Close() error {
	return txm.wal.Close()
}
