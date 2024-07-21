package types

import "sync"

type PriceInfo struct {
	Price     string
	Decimal   int
	Timestamp string
	RoundID   string
}

type PriceSync struct {
	Lock sync.RWMutex
	Info *PriceInfo
}

func (ps *PriceSync) UpdateInfo(info *PriceInfo) {
	ps.Lock.Lock()
	*ps.Info = *info
	ps.Lock.Unlock()
}

func (ps *PriceSync) GetInfo() PriceInfo {
	ps.Lock.RLock()
	defer ps.Lock.RUnlock()
	return *ps.Info
}

func NewPriceSyc() *PriceSync {
	return &PriceSync{
		Info: &PriceInfo{},
	}
}
