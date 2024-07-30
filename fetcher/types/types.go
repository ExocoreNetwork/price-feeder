package types

import "sync"

const (
	Chainlink = "chainlink"
)

type FType func(string) (*PriceInfo, error)

// var Fetchers = make(map[string]func(string) (*PriceInfo, error))
var Fetchers = make(map[string]FType)

// TODO Init fetchers
var InitFetchers = make(map[string]func(string) error)

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