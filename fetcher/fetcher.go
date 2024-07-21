package fetcher

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"go.uber.org/atomic"

	"github.com/ExocoreNetwork/price-feeder/fetcher/chainlink"
	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
)

var sourcesMap sync.Map
var tokensMap sync.Map

// type tokenSet []*token
// type sourceSet []*source

// var sources sourceSet
// var tokens tokenSet

func Init(sourcesIn, tokensIn []string) *Fetcher {
	sourceIDs := make([]string, 0)
	for _, tName := range tokensIn {
		// tokens = append(tokens, &token{name: s, active: true})
		tokensMap.Store(tName, &token{name: tName, active: true})
	}
	for _, sName := range sourcesIn {
		s := &source{name: sName, tokens: &sync.Map{}, running: atomic.NewInt32(-1), stopCh: make(chan struct{}), stopResCh: make(chan struct{})}
		for _, tName := range tokensIn {
			// s.tokens.Store(tName, &types.PriceSync{})
			s.tokens.Store(tName, types.NewPriceSyc())
		}
		sourcesMap.Store(sName, s)
		sourceIDs = append(sourceIDs, sName)
	}

	return &Fetcher{
		sources:  sourceIDs,
		interval: time.Second,
		newSource: make(chan struct {
			name     string
			endpoint string
		}),
		configSource: make(chan struct {
			s string
			t string
		}),
		getLatestPriceWithSourceToken: make(chan struct {
			p chan *types.PriceInfo
			s string
			t string
		}),
	}
}

// func (ss *sourceSet) Add(name, endpoint string) bool {
// 	s, _ := ss.GetByName(name)
// 	if s != nil {
// 		return false
// 	}
// 	*ss = append(*ss, &source{name: name, tokens: make(map[int]*types.PriceInfo)})
// 	return true
// }
//
// func (ss sourceSet) GetByName(name string) (*source, int) {
// 	for id, s := range ss {
// 		if s.name == name {
// 			return s, id
// 		}
// 	}
// 	return nil, -1
// }
//
// func (ts tokenSet) GetByName(name string) (*token, int) {
// 	for id, t := range ts {
// 		if t.name == name {
// 			return t, id
// 		}
// 	}
// 	return nil, -1
// }
//
// func (ts *tokenSet) Add(name, endpoint string) bool {
// 	t, _ := ts.GetByName(name)
// 	if t != nil {
// 		return false
// 	}
// 	//*ts = append(*ts, &token{name: name, endpoint: endpoint, active: true})
// 	*ts = append(*ts, &token{name: name, active: true})
// 	return true
// }

type source struct {
	lock      sync.Mutex
	running   *atomic.Int32
	stopCh    chan struct{}
	stopResCh chan struct{}
	// running []chan struct{}
	//lock sync.Mutex
	// source name, should be unique
	name string
	// token ids from the token slice
	//tokens map[int]struct{}
	//	tokens  map[int]*types.PriceInfo
	//	tokens map[string]*types.PriceSync
	tokens *sync.Map
	// tokensRead map[string]*types.PriceSync
	// endpoint of the source to retreive the price data; eg. https://rpc.ankr.com/eth  is used for chainlink's ethereum endpoint
	// might vary for different sources
	//endpoint string
	// indicates the work status
	// working *atomic.Bool
}

// set source's status as working
// func (s *source) start(interval time.Duration) (bool, chan struct{}) {
func (s *source) start(interval time.Duration) bool { // (bool, chan struct{}) {
	s.lock.Lock()
	if s.running.Load() == -1 {
		s.stopCh = make(chan struct{})
		s.stopResCh = make(chan struct{})
		s.running.Inc()
		s.lock.Unlock()
		s.Fetch(interval)
		return true
	}
	return false
}

// set source 's status as not working
func (s *source) stop() bool {
	s.lock.Lock()
	defer s.lock.Unlock()
	select {
	case <-s.stopCh:
		fmt.Println("closed already")
		return false
	default:
		close(s.stopCh)
		<-s.stopResCh
		if !s.running.CompareAndSwap(0, -1) {
			panic("running count should be zero when all token fetchers stopped")
		}
		return true
	}
}

// AddToken not concurrency safe: stop->AddToken->start(all)(/startOne need lock/select to ensure concurrent safe
func (s *source) AddToken(name string) bool {
	// _, loaded := s.tokens.LoadOrStore(name, &types.PriceSync{})
	_, loaded := s.tokens.LoadOrStore(name, types.NewPriceSyc())
	return !loaded
}

func (s *source) Fetch(interval time.Duration) {
	switch s.name {
	case "chainlink":
		// Fetch start all tokens and this will happen before any modify with the tokens maps, so there will actually have no conflicts at all
		s.tokens.Range(func(key, value any) bool {
			tName := key.(string)
			priceInfo := value.(*types.PriceSync)
			if tokenAny, found := tokensMap.Load(tName); found && tokenAny.(*token).active {
				tokenAddr := chainlink.GetTokenAddress(tName)
				s.lock.Lock()
				s.running.Inc()
				s.lock.Unlock()
				go func(tokenAddr string) {
					tic := time.NewTimer(interval)
					for {
						select {
						case <-tic.C:
							price, err := chainlink.FetchWithContractAddress(tokenAddr)
							prevPrice := priceInfo.GetInfo()
							if err == nil && (prevPrice.Price != price.Price || prevPrice.Decimal != price.Decimal) {
								priceInfo.UpdateInfo(price)
								log.Printf("update token:%s, price:%s, decimal:%d", tName, price.Price, price.Decimal)
							}
						case <-s.stopCh:
							if zero := s.running.Dec(); zero == 0 {
								close(s.stopResCh)
							}
							return
						}
					}
				}(tokenAddr)
			}
			return true
		})
	}
}

type token struct {
	name string // chain_token_address, _address is optional
	// indicates if this token is still alive for price reporting
	active bool
	// endpoint of the token; eg. 0x5f4eC3Df9cbd43714FE2740f5E3616155c5b8419  is used for chainlink to identify specific token to fetch its price
	// format might vary for different source to use; eg. for chainlink, this is used to tell the token's address on ethereum(when we use ethereume's contract)
	//endpoint string
}

// Fetcher serves as the unique entry point to fetch token prices as background routine continuously
type Fetcher struct {
	running atomic.Bool
	// source ids frmo the sourceSet
	sources []string
	// set the interval of fetching price, this value is the same for all source/tokens
	interval time.Duration
	// add new source
	newSource chan struct {
		name     string
		endpoint string
	}
	// config source's token
	configSource chan struct {
		s string
		t string
	}
	getLatestPriceWithSourceToken chan struct {
		p chan *types.PriceInfo
		s string
		t string
	}
}

// Start runs the background routine to fetch prices
func (f *Fetcher) StartAll() context.CancelFunc {
	if !f.running.CompareAndSwap(false, true) {
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	for _, sName := range f.sources {
		if sourceAny, ok := sourcesMap.Load(sName); ok {
			// start fetcheing data from 'source', setup as a background routine
			sourceAny.(*source).start(f.interval)
		}
	}

	// monitor routine for: 1. add new sources, 2. config tokens for existing source, 3. stop all running fetchers
	go func() {
		for {
			select {
			case <-ctx.Done():
				// close all running sources
				for _, sName := range f.sources {
					if sourceAny, ok := sourcesMap.Load(sName); ok {
						// safe the do multiple times/ on stopped sources, this process is synced(blocked until one source is stopped completely)
						sourceAny.(*source).stop()
					}
				}
				return

			// add a new source and start fetching its data
			case _ = <-f.newSource:

			// add tokens for a existing source
			case _ = <-f.configSource:
				// TODO: we currently don't handle the request like 'remove token', if we do that support, we should take care of the process in reading routine
			}
		}
	}()

	// read loop to serve for price quering
	go func() {
		// read cache, in this way, we don't need to lock every time for potential conflict with tokens update in source(like add one new token), only when we fail to found corresopnding token in this readList
		// TODO: we currently don't have process for 'remove-token' from source, so the price will just not be updated, and we don't clear the priceInfo(it's no big deal since only the latest values are kept, and for reader, they will either ont quering this value any more or find out the timestamp not updated like forever)
		readList := make(map[string]map[string]*types.PriceSync)
		for ps := range f.getLatestPriceWithSourceToken {
			s := readList[ps.s]
			if s == nil {
				if _, found := sourcesMap.Load(ps.s); found {
					readList[ps.s] = make(map[string]*types.PriceSync)
					s = readList[ps.s]
				} else {
					fmt.Println("source not exists")
					ps.p <- nil
					continue
				}
			}

			tPrice := s[ps.t]
			if tPrice == nil {
				if sourceAny, found := sourcesMap.Load(ps.s); found {
					if p, found := sourceAny.(*source).tokens.Load(ps.t); found {
						tPrice = p.(*types.PriceSync)
						s[ps.t] = tPrice
					} else {
						if len(s) == 0 {
							fmt.Println("source has no valid token being read, remove this source for reading")
							delete(readList, ps.s)
						}
						continue
					}
				} else {
					fmt.Println("source not exists any more, remove this source for reading")
					delete(readList, ps.s)
					continue
				}
			}
			pRes := tPrice.GetInfo()
			ps.p <- &pRes
		}
	}()
	return cancel
}

func (f *Fetcher) GetLatestPriceFromSourceToken(source, token string, c chan *types.PriceInfo) {
	f.getLatestPriceWithSourceToken <- struct {
		p chan *types.PriceInfo
		s string
		t string
	}{c, source, token}
}
