package types

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
)

type SourceInf interface {
	// InitTokens used for initialization, it should only be called before 'Start'
	// when the source is not 'running', this method vill overwrite the source's 'tokens' list
	InitTokens(tokens []string) bool

	// Start starts fetching prices of all tokens configured in the source. token->price
	Start() map[string]*PriceSync

	// AddTokenAndStart adds a new token to fetch price for a running source
	AddTokenAndStart(token string) *addTokenRes

	// GetName returns name of the source
	GetName() string
	// Status returns the status of all tokens configured in the source: running or not
	Status() map[string]*tokenStatus

	// ReloadConfig reload the config file for the source
	//	ReloadConfigForToken(string) error

	// Stop closes all running routine generated from the source
	Stop()

	// TODO: add some interfaces to achieve more fine-grained management
	// StopToken(token string)
	// ReloadConfigAll()
	// RestarAllToken()
}

type SourceInitFunc func(cfgPath string) (SourceInf, error)

type SourceFetchFunc func(token string) (*PriceInfo, error)

// SourceReloadConfigFunc reload source config file
// reload meant to be called during source running, the concurrency should be handled well
type SourceReloadConfigFunc func(config, token string) error

type NativeRestakingInfo struct {
	Chain   string
	TokenID string
}

type PriceInfo struct {
	Price     string
	Decimal   int
	Timestamp string
	RoundID   string
}

type PriceSync struct {
	lock *sync.RWMutex
	info *PriceInfo
}

type tokenInfo struct {
	name   string
	price  *PriceSync
	active *atomic.Bool
}
type tokenStatus struct {
	name   string
	price  PriceInfo
	active bool
}

type addTokenReq struct {
	tokenName string
	result    chan *addTokenRes
}

type addTokenRes struct {
	price *PriceSync
	err   error
}

// IsZero is used to check if a PriceInfo has not been assigned, similar to a nil value in a pointer variable
func (p PriceInfo) IsZero() bool {
	return len(p.Price) == 0
}
func (p PriceInfo) Equal(price PriceInfo) bool {
	if p.Price == price.Price &&
		p.Decimal == price.Decimal &&
		p.RoundID == price.RoundID {
		return true
	}
	return false
}

func NewPriceSync() *PriceSync {
	return &PriceSync{
		lock: new(sync.RWMutex),
		info: &PriceInfo{},
	}
}

func (p *PriceSync) Get() PriceInfo {
	p.lock.RLock()
	price := *p.info
	p.lock.RUnlock()
	return price
}

func (p *PriceSync) Set(price PriceInfo) {
	p.lock.Lock()
	*p.info = price
	p.lock.Unlock()
}

func NewTokenInfo(name string, price *PriceSync) *tokenInfo {
	return &tokenInfo{
		name:   name,
		price:  price,
		active: new(atomic.Bool),
	}
}

// GetPriceSync returns the price structure which has lock to make sure concurrency safe
func (t *tokenInfo) GetPriceSync() *PriceSync {
	return t.price
}

// GetPrice returns the price info
func (t *tokenInfo) GetPrice() PriceInfo {
	return t.price.Get()
}

// GetActive returns the active status
func (t *tokenInfo) GetActive() bool {
	return t.active.Load()
}

// SetActive set the active status
func (t *tokenInfo) SetActive(v bool) {
	t.active.Store(v)
}

func newAddTokenReq(tokenName string) (*addTokenReq, chan *addTokenRes) {
	resCh := make(chan *addTokenRes, 1)
	req := &addTokenReq{
		tokenName: tokenName,
		result:    resCh,
	}
	return req, resCh
}

func (r *addTokenRes) Error() error {
	return r.err
}
func (r *addTokenRes) Price() *PriceSync {
	return r.price
}

var _ SourceInf = &Source{}

// Source is a common implementation of SourceInf
type Source struct {
	logger    feedertypes.LoggerInf
	cfgPath   string
	running   bool
	priceList map[string]*PriceSync
	name      string
	locker    *sync.Mutex
	stop      chan struct{}
	// 'fetch' interacts directly with data source
	fetch            SourceFetchFunc
	reload           SourceReloadConfigFunc
	tokens           map[string]*tokenInfo
	activeTokenCount *atomic.Int32
	interval         time.Duration
	addToken         chan *addTokenReq
	// pendingTokensCount *atomic.Int32
	//	pendingTokensLimit int32
	// used to trigger reloading source config
	tokenNotConfigured chan string
}

// NewSource returns a implementaion of sourceInf
// for sources they could utilitize this function to provide that 'SourceInf' by taking care of only the 'fetch' function
func NewSource(logger feedertypes.LoggerInf, name string, fetch SourceFetchFunc, cfgPath string, reload SourceReloadConfigFunc) *Source {
	return &Source{
		logger:           logger,
		cfgPath:          cfgPath,
		name:             name,
		locker:           new(sync.Mutex),
		stop:             make(chan struct{}),
		tokens:           make(map[string]*tokenInfo),
		activeTokenCount: new(atomic.Int32),
		priceList:        make(map[string]*PriceSync),
		interval:         defaultInterval,
		addToken:         make(chan *addTokenReq, defaultPendingTokensLimit),
		// pendingTokensCount: new(atomic.Int32),
		// pendingTokensLimit: defaultPendingTokensLimit,
		fetch:  fetch,
		reload: reload,
	}
}

// InitTokenNames adds the token names in the source's token list
// NOTE: call before start
func (s *Source) InitTokens(tokens []string) bool {
	s.locker.Lock()
	defer s.locker.Unlock()
	if s.running {
		s.logger.Info("failed to add a token to the running source")
		return false
	}
	// reset all tokens
	s.tokens = make(map[string]*tokenInfo)
	for _, token := range tokens {
		// we standardize all token names to lowercase
		token = strings.ToLower(token)
		s.tokens[token] = NewTokenInfo(token, NewPriceSync())
	}
	return true
}

// Start starts background routines to fetch all registered token for the source frequently
// and watch for 1. add token, 2.stop events
// TODO(leon): return error and existing map when running already
func (s *Source) Start() map[string]*PriceSync {
	s.locker.Lock()
	if s.running {
		s.logger.Error("failed to start the source which is already running", "source", s.name)
		s.locker.Unlock()
		return nil
	}
	if len(s.tokens) == 0 {
		s.logger.Error("failed to start the source which has no tokens set", "source", s.name)
		s.locker.Unlock()
		return nil
	}
	s.running = true
	s.locker.Unlock()
	ret := make(map[string]*PriceSync)
	for tName, token := range s.tokens {
		ret[tName] = token.GetPriceSync()
		s.logger.Info("start fetching prices", "source", s.name, "token", token)
		s.startFetchToken(token)
	}
	// main routine of source, listen to:
	// addToken to add a new token for the source and start fetching that token's price
	// tokenNotConfitured to reload the source's config file for required token
	// stop closes the source routines and set runnign status to false
	go func() {
		for {
			select {
			case req := <-s.addToken:
				price := NewPriceSync()
				// check token existence and then add to token list & start if not exists
				if token, ok := s.tokens[req.tokenName]; !ok {
					s.tokens[req.tokenName] = NewTokenInfo(req.tokenName, price)
					s.logger.Info("add a new token and start fetching price", "source", s.name, "token", req.tokenName)
					s.startFetchToken(token)
				} else {
					s.logger.Info("didn't add duplicated token, return existing priceSync", "source", s.name, "token", req.tokenName)
					price = token.GetPriceSync()
				}
				req.result <- &addTokenRes{
					price: price,
					err:   nil,
				}
			case tName := <-s.tokenNotConfigured:
				if err := s.reloadConfigForToken(tName); err != nil {
					s.logger.Error("failed to reload config for adding token", "source", s.name, "token", tName)
				}
			case <-s.stop:
				s.logger.Info("exit listening rountine for addToken", "source", s.name)
				// waiting for all token routines to exist
				for s.activeTokenCount.Load() > 0 {
					time.Sleep(1 * time.Second)
				}
				s.locker.Lock()
				s.running = false
				s.locker.Unlock()
				return
			}
		}
	}()
	return ret
}

// AddTokenAndStart adds token into a running source and start fetching that token
// return (nil, false) and skip adding this token when previously adding request is not handled
func (s *Source) AddTokenAndStart(token string) *addTokenRes {
	s.locker.Lock()
	defer s.locker.Unlock()
	if !s.running {
		return &addTokenRes{
			price: nil,
			err:   fmt.Errorf("didn't add token due to source:%s not running", s.name),
		}
	}
	// we don't block the process when then channel is not available
	// caller should handle the returned bool value properly
	addReq, addResCh := newAddTokenReq(token)
	select {
	case s.addToken <- addReq:
		return <-addResCh
	default:
	}
	// TODO(leon): define an res-skipErr variable
	return &addTokenRes{
		price: nil,
		err:   fmt.Errorf("didn't add token, too many pendings, limit:%d", defaultPendingTokensLimit),
	}
}

func (s *Source) Stop() {
	s.logger.Info("stop source and close all running routines", "source", s.name)
	s.locker.Lock()
	// make it safe when closed more than one time
	select {
	case _, ok := <-s.stop:
		if ok {
			close(s.stop)
		}
	default:
		close(s.stop)
	}
	s.running = false
	s.locker.Unlock()
}

func (s *Source) startFetchToken(token *tokenInfo) {
	s.activeTokenCount.Add(1)
	token.SetActive(true)
	go func() {
		defer func() {
			token.SetActive(false)
			s.activeTokenCount.Add(-1)
		}()
		tic := time.NewTicker(s.interval)
		for {
			select {
			case <-s.stop:
				s.logger.Info("exist fetching routine", "source", s.name, "token", token)
				return
			case <-tic.C:
				if price, err := s.fetch(token.name); err != nil {
					if errors.Is(err, feedertypes.ErrSrouceTokenNotConfigured) {
						s.logger.Info("token not config for source", "token", token.name)
						s.tokenNotConfigured <- token.name
					} else {
						s.logger.Error("failed to fetch price", "source", s.name, "token", token.name, "error", err)
						// TODO(leon): exist this routine after maximum fails ?
						// s.tokens[token.name].active = false
						// return
					}
				} else {
					// update price
					token.price.Set(*price)
				}
			}
		}
	}()
}

func (s *Source) reloadConfigForToken(token string) error {
	if err := s.reload(s.cfgPath, token); err != nil {
		return fmt.Errorf("failed to reload config file to from path:%s when adding token", s.cfgPath)
	}
	return nil
}

func (s *Source) GetName() string {
	return s.name
}

func (s *Source) Status() map[string]*tokenStatus {
	s.locker.Lock()
	ret := make(map[string]*tokenStatus)
	for tName, token := range s.tokens {
		ret[tName] = &tokenStatus{
			name:   tName,
			price:  token.price.Get(),
			active: token.GetActive(),
		}
	}
	s.locker.Unlock()
	return ret
}

const (
	defaultPendingTokensLimit = 5
	defaultInterval           = 30 * time.Second
	Chainlink                 = "chainlink"
	BeaconChain               = "beaconchain"

	NativeTokenETH = "nsteth"

	DefaultSlotsPerEpoch = uint64(32)
)

var (
	// source -> fetch method
	//	Fetchers = make(map[string]FetchFunc)
	// source -> initializers of source
	SourceInitializers   = make(map[string]SourceInitFunc)
	ChainToSlotsPerEpoch = map[uint64]uint64{
		101:   DefaultSlotsPerEpoch,
		40161: DefaultSlotsPerEpoch,
		40217: DefaultSlotsPerEpoch,
	}

	NativeTokenETHAssetID = "0xeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee_0x65"
	//	NativeRestakings      = map[string][]string{
	//		"eth": {BeaconChain, NativeTokenETH},
	//	}
	//	NativeRestakings = map[string]NativeRestakingInfo{
	//		"eth": NativeRestakingInfo{
	//			Chain:   BeaconChain,
	//			TokenID: NativeTokenETH,
	//		},
	//	}

	AssetIDMap = map[string]string{
		NativeTokenETH: NativeTokenETHAssetID,
	}

	Logger feedertypes.LoggerInf
)

func UpdateNativeAssetID(nstID string) {
	NativeTokenETHAssetID = nstID
	AssetIDMap[NativeTokenETH] = NativeTokenETHAssetID
}
