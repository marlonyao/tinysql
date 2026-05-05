package tx

import "sync"

// ReadView MVCC 快照读视图
type ReadView struct {
	CreatorTrxID uint64          // 创建该 ReadView 的事务 ID
	MinTrxID     uint64          // 创建时刻最小的活跃事务 ID
	MaxTrxID     uint64          // 创建时刻最大的活跃事务 ID + 1（下一个要分配的 ID）
	ActiveIDs    map[uint64]bool // 创建时刻所有活跃事务 ID
}

// IsVisible 判断给定 trx_id 的行对当前 ReadView 是否可见
func (rv *ReadView) IsVisible(trxID uint64) bool {
	if trxID == 0 {
		return true // 旧数据（未分配 trx_id）
	}
	if trxID == rv.CreatorTrxID {
		return true // 自己创建的，总是可见
	}
	if trxID < rv.MinTrxID {
		return true // 在 ReadView 创建前已提交
	}
	if trxID >= rv.MaxTrxID {
		return false // 在 ReadView 创建后才开始，未提交
	}
	// trxID 在 [Min, Max) 之间：检查是否在活跃列表中
	return !rv.ActiveIDs[trxID]
}

// TransactionManager 扩展：活跃事务管理
type activeTxSet struct {
	mu     sync.RWMutex
	active map[uint64]bool
}

func newActiveTxSet() *activeTxSet {
	return &activeTxSet{active: make(map[uint64]bool)}
}

func (s *activeTxSet) Add(txID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[txID] = true
}

func (s *activeTxSet) Remove(txID uint64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, txID)
}

func (s *activeTxSet) Snapshot() (min, max uint64, ids map[uint64]bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ids = make(map[uint64]bool, len(s.active))
	min = ^uint64(0)
	max = uint64(0)

	for id := range s.active {
		ids[id] = true
		if id < min {
			min = id
		}
		if id > max {
			max = id
		}
	}

	// 没有活跃事务时的边界处理
	if len(ids) == 0 {
		min = 0
		max = 0
	}

	return min, max + 1, ids // max = 最大活跃 ID + 1（下一个要分配的 ID）
}
