package fetcher

import (
	"context"
	"log"
	"time"

	"go.uber.org/atomic"

	"github.com/ExocoreNetwork/price-feeder/fetcher/chainlink"
	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
)

type tokenSet []*token
type sourceSet []*source

var sources sourceSet
var tokens tokenSet

func Init(sourcesIn, tokensIn []string) *Fetcher {
	sourceIDs := make([]int, 0)
	for _, s := range tokensIn {
		tokens = append(tokens, &token{name: s, active: true})
	}
	for i, sName := range sourcesIn {
		s := &source{name: sName, tokens: make(map[int]*types.PriceInfo), working: atomic.NewBool(true)}
		for tID := range tokensIn {
			s.tokens[tID] = &types.PriceInfo{}
		}
		sources = append(sources, s)
		sourceIDs = append(sourceIDs, i)
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

func (ss *sourceSet) Add(name, endpoint string) bool {
	s, _ := ss.GetByName(name)
	if s != nil {
		return false
	}
	*ss = append(*ss, &source{name: name, tokens: make(map[int]*types.PriceInfo)})
	return true
}

func (ss sourceSet) GetByName(name string) (*source, int) {
	for id, s := range ss {
		if s.name == name {
			return s, id
		}
	}
	return nil, -1
}

func (ts tokenSet) GetByName(name string) (*token, int) {
	for id, t := range ts {
		if t.name == name {
			return t, id
		}
	}
	return nil, -1
}

func (ts *tokenSet) Add(name, endpoint string) bool {
	t, _ := ts.GetByName(name)
	if t != nil {
		return false
	}
	//*ts = append(*ts, &token{name: name, endpoint: endpoint, active: true})
	*ts = append(*ts, &token{name: name, active: true})
	return true
}

type source struct {
	//lock sync.Mutex
	// source name, should be unique
	name string
	// token ids from the token slice
	//tokens map[int]struct{}
	tokens map[int]*types.PriceInfo
	// endpoint of the source to retreive the price data; eg. https://rpc.ankr.com/eth  is used for chainlink's ethereum endpoint
	// might vary for different sources
	//endpoint string
	// indicates the work status
	working *atomic.Bool
}

// set source's status as working
func (s *source) start() bool {
	return s.working.CAS(false, true)
}

// set source 's status as not working
func (s *source) stop() bool {
	return s.working.CAS(true, false)
}

// not concurrency safe
func (s *source) AddToken(i int) bool {
	if _, ok := s.tokens[i]; ok {
		return false
	}
	s.tokens[i] = &types.PriceInfo{}
	return true
}

func (s *source) Fetch() {
	switch s.name {
	case "chainlink":
		for tID, prePrice := range s.tokens {
			if tID < len(tokens) {
				t := tokens[tID]
				if t.active {
					price, err := chainlink.Fetch(s.name, tokens[tID].name)
					if err == nil && prePrice.Price != price.Price {
						s.tokens[tID] = price
						log.Printf("update price:%s, decimal:%d", price.Price, price.Decimal)
					}
				}
			}
		}
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
	// source ids frmo the sourceSet
	sources []int
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
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		tic := time.NewTicker(f.interval)
		for {
			select {
			case <-ctx.Done():
				return
			case <-tic.C:
				for _, sID := range f.sources {
					if sID < len(sources) {
						s := sources[sID]
						if s.working.Load() {
							s.Fetch()
						}
					}
				}
			case s := <-f.newSource:
				sources.Add(s.name, s.endpoint)
			case cfg := <-f.configSource:
				s, _ := sources.GetByName(cfg.s)
				if s != nil {
					_, tID := tokens.GetByName(cfg.t)
					if tID >= 0 {
						s.AddToken(tID)
					}
				}
			case ps := <-f.getLatestPriceWithSourceToken:
				s, _ := sources.GetByName(ps.s)
				if s != nil {
					_, tID := tokens.GetByName(ps.t)
					if tID < len(s.tokens) {
						ps.p <- s.tokens[tID]
						break
					}
				}
				ps.p <- nil
			}
		}
	}()
	return cancel
}

func (f *Fetcher) GetLatestPriceFromSourceToken(source, token string, c chan *types.PriceInfo) {
	//f.getLatestPriceWithSourceToken<-struct{s:source, t:token, p: c}
	f.getLatestPriceWithSourceToken <- struct {
		p chan *types.PriceInfo
		s string
		t string
	}{c, source, token}
}
