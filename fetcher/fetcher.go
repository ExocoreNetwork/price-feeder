package fetcher

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"unicode"

	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
)

var (
	sourcesMap sync.Map
	tokensMap  sync.Map
	logger     feedertypes.LoggerInf

	defaultFetcher *Fetcher
)

//	defaultFetcher = &Fetcher{
//		sources:  sourceIDs,
//		interval: defaultInterval,
//		newSource: make(chan struct {
//			name     string
//			endpoint string
//		}),
//		// nativeTokenValidatorsUpdate: make(chan struct {
//		// 	tokenName string
//		// 	info      string
//		// 	success   chan bool
//		// }),
//		configSource: make(chan struct {
//			s string
//			t string
//		}),
//		getLatestPriceWithSourceToken: make(chan struct {
//			p chan *types.PriceInfo
//			s string
//			t string
//		}),
//		newTokenForSource: make(chan struct {
//			sourceName string
//			tokenName  string
//		}),
//	}
type Fetcher struct {
	logger  feedertypes.LoggerInf
	locker  *sync.Mutex
	running bool
	sources map[string]types.SourceInf
	// source->map{token->price}
	priceReadList  map[string]map[string]*types.PriceSync
	addSourceToken chan *addTokenForSourceReq
	//	addSourceToken chan struct {
	//		source string
	//		token  string
	//	}
	getLatestPrice chan *getLatestPriceReq
	stop           chan struct{}
	//	getLatestPrice chan struct {
	//		source string
	//		token  string
	//		price  chan types.PriceInfo
	//	}
}
type addTokenForSourceReq struct {
	source string
	token  string
	result chan bool
}

type getLatestPriceReq struct {
	source string
	token  string
	result chan *getLatestPriceRes
}
type getLatestPriceRes struct {
	price types.PriceInfo
	err   error
}

func newGetLatestPriceReq(source, token string) (*getLatestPriceReq, chan *getLatestPriceRes) {
	res := make(chan *getLatestPriceRes)
	return &getLatestPriceReq{source: source, token: token, result: res}, res
}

func NewFetcher(logger feedertypes.LoggerInf, sources map[string]types.SourceInf) *Fetcher {
	return &Fetcher{
		logger:         logger,
		locker:         new(sync.Mutex),
		sources:        sources,
		priceReadList:  make(map[string]map[string]*types.PriceSync),
		addSourceToken: make(chan *addTokenForSourceReq),
		getLatestPrice: make(chan *getLatestPriceReq),
		stop:           make(chan struct{}),
	}
}

// AddTokenForSource adds token for existing source
// blocked waiting for the result to return
func (f *Fetcher) AddTokenForSource(source, token string) bool {
	res := make(chan bool)
	f.addSourceToken <- &addTokenForSourceReq{
		source: source,
		token:  token,
		result: res,
	}
	return <-res
}

// GetLatestPrice return the queried price for the token from specified source
// blocked waiting for the result to return
func (f Fetcher) GetLatestPrice(source, token string) (types.PriceInfo, error) {
	req, res := newGetLatestPriceReq(source, token)
	f.getLatestPrice <- req
	result := <-res
	return result.price, result.err
}

func (f Fetcher) Start() error {
	f.locker.Lock()
	if f.running {
		f.locker.Unlock()
		return errors.New("failed to start fetcher which is already running")
	}
	if len(f.sources) == 0 {
		f.locker.Unlock()
		return errors.New("failed to start fetcher with no sources set")
	}
	priceList := make(map[string]map[string]*types.PriceSync)
	for sName, source := range f.sources {
		f.logger.Info("start source", "source", sName)
		prices := source.Start()
		priceList[sName] = prices
	}
	f.priceReadList = priceList
	f.running = true
	f.locker.Unlock()

	go func() {
		select {
		case req := <-f.addSourceToken:
			if source, ok := f.sources[req.source]; ok {
				if res := source.AddTokenAndStart(req.token); res.Error() != nil {
					// TODO: clean logs
					f.logger.Error("failed to AddTokenAndStart", "source", source.GetName(), "token", req.token, "error", res.Error())
					req.result <- false
				} else {
					f.priceReadList[req.source][req.token] = res.Price()
					req.result <- true
				}
			} else {
				// we don't support adding source dynamically
				f.logger.Error("failed to add token for a nonexistent soruce", "source", req.source, "token", req.token)
				req.result <- false
			}
		case req := <-f.getLatestPrice:
			if s := f.priceReadList[req.source]; s == nil {
				//				f.logger.Error("failed to get price from a nonexistent source", "source", req.source, "token", req.token)
				req.result <- &getLatestPriceRes{
					price: types.PriceInfo{},
					err:   fmt.Errorf("failed to get price of token:%s from a nonexistent source:%s", req.token, req.source),
				}
			} else if price := s[req.token]; price == nil {
				f.logger.Error("failed to get price of a nonexistent token from an existing source", "source", req.source, "token", req.token)
				req.result <- &getLatestPriceRes{
					price: types.PriceInfo{},
					err:   fmt.Errorf("failed to get price of token %s from a nonexistent token from an existing source", req.token, "source", req.source),
				}
			} else {
				req.result <- &getLatestPriceRes{
					price: price.Get(),
					err:   nil,
				}
			}
		case <-f.stop:
			f.locker.Lock()
			f.running = false
			f.locker.Unlock()
			// TODO: stop all running sources
			return
		}
	}()
	return nil
}

func (f Fetcher) Stop() {
	f.locker.Lock()
	select {
	case _, ok := <-f.stop:
		if ok {
			close(f.stop)
		}
	default:
		close(f.stop)
	}
	//TODO: check and make sure all sources closed
	f.running = false
	f.locker.Unlock()
}

// Init initializes the fetcher with sources and tokens
func Init(tokenSources []feedertypes.TokenSources, sourcesPath string) error {
	if logger = feedertypes.GetLogger("fetcher"); logger == nil {
		panic("logger is not initialized")
	}

	sources := make(map[string]types.SourceInf)
	for _, ts := range tokenSources {
		sNames := strings.Split(strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, ts.Sources), ",")

		var err error
		sourceTokens := make(map[string][]string)
		// add sources with names
		for _, sName := range sNames {
			source := sources[sName]
			// new a source if not exists
			if source == nil {
				source, err = types.SourceInitializers[sName](sourcesPath)
				if err != nil {
					return fmt.Errorf("failed to init source:%s, soources_config_path:%s, error:%w", sName, sourcesPath, err)
				}
				sources[sName] = source
			}
			sourceTokens[sName] = append(sourceTokens[sName], ts.Token)
		}
		// setup tokens for sources
		for sName, tokens := range sourceTokens {
			sources[sName].InitTokens(tokens)
		}
	}

	defaultFetcher = NewFetcher(logger, sources)
	return nil
}

func GetFetcher() (*Fetcher, bool) {
	if defaultFetcher == nil {
		return nil, false
	}
	return defaultFetcher, true
}

// source is the data source to fetch token prices
// type source struct {
// 	lock      sync.Mutex
// 	running   *atomic.Int32
// 	stopCh    chan struct{}
// 	stopResCh chan struct{}
// 	// source name, should be unique
// 	name   string
// 	tokens *sync.Map
// 	// endpoint of the source to retreive the price data; eg. https://rpc.ankr.com/eth  is used for chainlink's ethereum endpoint
// 	// might vary for different sources
// 	fetch types.FType
// }

// func NewSource() *source {
// 	return &source{
// 		running: atomic.NewInt32(-1),
// 	}
// }
//
// // set source's status as working
// func (s *source) start(interval time.Duration) bool {
// 	s.lock.Lock()
// 	if s.running.Load() == -1 {
// 		s.stopCh = make(chan struct{})
// 		s.stopResCh = make(chan struct{})
// 		s.running.Inc()
// 		s.lock.Unlock()
// 		s.Fetch(interval)
// 		return true
// 	}
// 	return false
// }
//
// // set source 's status as not working
// func (s *source) stop() bool {
// 	s.lock.Lock()
// 	defer s.lock.Unlock()
// 	select {
// 	case _, ok := <-s.stopCh:
// 		if ok {
// 			close(s.stopCh)
// 		}
// 		return false
// 	default:
// 		close(s.stopCh)
// 		<-s.stopResCh
// 		if !s.running.CompareAndSwap(0, -1) {
// 			panic("running count should be zero when all token fetchers stopped")
// 		}
// 		return true
// 	}
// }
//
// // AddToken not concurrency safe: stop->AddToken->start(all)(/startOne need lock/select to ensure concurrent safe
// func (s *source) AddToken(name string) bool {
// 	priceInfo := types.NewPriceSyc()
// 	_, loaded := s.tokens.LoadOrStore(name, priceInfo)
// 	if loaded {
// 		return false
// 	}
// 	// fetching the new token
// 	if tokenAny, found := tokensMap.Load(name); found && tokenAny.(*token).active {
// 		s.lock.Lock()
// 		s.running.Inc()
// 		s.lock.Unlock()
// 		go func(tName string) {
// 			// TODO: set interval for different sources
// 			tic := time.NewTicker(defaultInterval)
// 			for {
// 				select {
// 				case <-tic.C:
// 					price, err := s.fetch(tName)
// 					prevPrice := priceInfo.GetInfo()
// 					if err == nil && (prevPrice.Price != price.Price || prevPrice.Decimal != price.Decimal) {
// 						priceInfo.UpdateInfo(price)
// 						logger.Info("update token price", "token", tName, "price", price)
// 					}
// 				case <-s.stopCh:
// 					if zero := s.running.Dec(); zero == 0 {
// 						close(s.stopResCh)
// 					}
// 					return
// 				}
// 			}
// 		}(name)
// 	}
// 	return true
// }

// Fetch token price from source
// func (s *source) Fetch(interval time.Duration) {
// 	s.tokens.Range(func(key, value any) bool {
// 		tName := key.(string)
// 		priceInfo := value.(*types.PriceSync)
// 		if tokenAny, found := tokensMap.Load(tName); found && tokenAny.(*token).active {
// 			s.lock.Lock()
// 			s.running.Inc()
// 			s.lock.Unlock()
// 			go func(tName string) {
// 				tic := time.NewTicker(interval)
// 				for {
// 					select {
// 					case <-tic.C:
// 						price, err := s.fetch(tName)
// 						prevPrice := priceInfo.GetInfo()
// 						if err == nil && (prevPrice.Price != price.Price || prevPrice.Decimal != price.Decimal) {
// 							priceInfo.UpdateInfo(price)
// 							logger.Info("update token price", "token", tName, "price", price)
// 						}
// 					case <-s.stopCh:
// 						if zero := s.running.Dec(); zero == 0 {
// 							close(s.stopResCh)
// 						}
// 						return
// 					}
// 				}
// 			}(tName)
// 		}
// 		return true
// 	})
// }

// type token struct {
// 	name string // chain_token_address, _address is optional
// 	// indicates if this token is still alive for price reporting
// 	active bool
// 	// endpoint of the token; eg. 0x5f4eC3Df9cbd43714FE2740f5E3616155c5b8419  is used for chainlink to identify specific token to fetch its price
// 	// format might vary for different source to use; eg. for chainlink, this is used to tell the token's address on ethereum(when we use ethereume's contract)
// 	//endpoint string
// }

// Fetcher serves as the unique entry point to fetch token prices as background routine continuously
// type Fetcher struct {
// 	running atomic.Bool
// 	// source ids frmo the sourceSet
// 	sources []string
// 	// set the interval of fetching price, this value is the same for all source/tokens
// 	interval time.Duration
// 	// add new source
// 	newSource chan struct {
// 		name     string
// 		endpoint string
// 	}
// 	newTokenForSource chan struct {
// 		sourceName string
// 		tokenName  string
// 	}
// 	// withdraw/deposit_stakerIdx_validatorIndex
// 	//	nativeTokenValidatorsUpdate chan struct {
// 	//		tokenName string
// 	//		info      string
// 	//		success   chan bool
// 	//	}
// 	// config source's token
// 	configSource chan struct {
// 		s string
// 		t string
// 	}
// 	getLatestPriceWithSourceToken chan struct {
// 		p chan *types.PriceInfo
// 		s string
// 		t string
// 	}
// }

// func (f *Fetcher) AddTokenForSource(sourceName string, tokenName string) {
// 	f.newTokenForSource <- struct {
// 		sourceName string
// 		tokenName  string
// 	}{sourceName, tokenName}
// }

// StartAll runs the background routine to fetch prices
// func (f *Fetcher) StartAll() context.CancelFunc {
// 	if !f.running.CompareAndSwap(false, true) {
// 		return nil
// 	}
// 	ctx, cancel := context.WithCancel(context.Background())
// 	for _, sName := range f.sources {
// 		if sourceAny, ok := sourcesMap.Load(sName); ok {
// 			// start fetcheing data from 'source', setup as a background routine
// 			logger.Info("start fetching prices", "source", sName, "interaval", f.interval)
// 			sourceAny.(*source).start(f.interval)
// 		}
// 	}
//
// 	// monitor routine for: 1. add new sources, 2. config tokens for existing source, 3. stop all running fetchers
// 	go func() {
// 		for {
// 			select {
// 			case <-ctx.Done():
// 				// close all running sources
// 				for _, sName := range f.sources {
// 					if sourceAny, ok := sourcesMap.Load(sName); ok {
// 						// safe the do multiple times/ on stopped sources, this process is synced(blocked until one source is stopped completely)
// 						sourceAny.(*source).stop()
// 					}
// 				}
// 				return
//
// 				// TODO: add a new source and start fetching its data
// 			case <-f.newSource:
// 			case t := <-f.newTokenForSource:
// 				for _, sName := range f.sources {
// 					if sName == t.sourceName {
// 						// TODO: it's ok to add multiple times for the same token
// 						tokensMap.Store(t.tokenName, &token{name: t.tokenName, active: true})
// 						if s, ok := sourcesMap.Load(t.sourceName); ok {
// 							// add token for source, so this source is running already
// 							s.(*source).AddToken(t.tokenName)
// 						}
// 						break
// 					}
// 				}
// 				// add tokens for a existing source
// 			case <-f.configSource:
// 				// TODO: we currently don't handle the request like 'remove token', if we do that support, we should take care of the process in reading routine
// 			}
// 		}
// 	}()
//
// 	// read routine, loop to serve for price quering
// 	go func() {
// 		// read cache, in this way, we don't need to lock every time for potential conflict with tokens update in source(like add one new token), only when we fail to found corresopnding token in this readList
// 		// TODO: we currently don't have process for 'remove-token' from source, so the price will just not be updated, and we don't clear the priceInfo(it's no big deal since only the latest values are kept, and for reader, they will either ont quering this value any more or find out the timestamp not updated like forever)
// 		readList := make(map[string]map[string]*types.PriceSync)
// 		for ps := range f.getLatestPriceWithSourceToken {
// 			s := readList[ps.s]
// 			if s == nil {
// 				if _, found := sourcesMap.Load(ps.s); found {
// 					readList[ps.s] = make(map[string]*types.PriceSync)
// 					s = readList[ps.s]
// 				} else {
// 					fmt.Println("source not exists")
// 					ps.p <- nil
// 					continue
// 				}
// 			}
//
// 			tPrice := s[ps.t]
// 			if tPrice == nil {
// 				if sourceAny, found := sourcesMap.Load(ps.s); found {
// 					if p, found := sourceAny.(*source).tokens.Load(ps.t); found {
// 						tPrice = p.(*types.PriceSync)
// 						s[ps.t] = tPrice
// 					} else {
// 						if len(s) == 0 {
// 							fmt.Println("source has no valid token being read, remove this source for reading", ps.s, ps.t)
// 							delete(readList, ps.s)
// 						}
// 						continue
// 					}
// 				} else {
// 					fmt.Println("source not exists any more, remove this source for reading")
// 					delete(readList, ps.s)
// 					continue
// 				}
// 			}
// 			pRes := tPrice.GetInfo()
// 			ps.p <- &pRes
// 		}
// 	}()
// 	return cancel
// }
//
// // GetLatestPriceFromSourceToken gets the latest price of a token from a source
// func (f *Fetcher) GetLatestPriceFromSourceToken(source, token string, c chan *types.PriceInfo) {
// 	f.getLatestPriceWithSourceToken <- struct {
// 		p chan *types.PriceInfo
// 		s string
// 		t string
// 	}{c, source, token}
// }
//
// // UpdateNativeTokenValidators updates validator list for stakers of native-restaking-token(client-chain)
// func (f *Fetcher) UpdateNativeTokenValidators(tokenName, updateInfo string) bool {
// 	parsedInfo := strings.Split(updateInfo, "_")
// 	if len(parsedInfo) != 4 {
// 		return false
// 	}
// 	stakerIdx, _ := strconv.ParseInt(parsedInfo[1], 10, 64)
// 	validatorPubkey := parsedInfo[2]
// 	validatorsSize, _ := strconv.ParseUint(parsedInfo[3], 10, 64)
// 	return beaconchain.UpdateStakerValidators(int(stakerIdx), validatorPubkey, parsedInfo[0] == "deposit", validatorsSize)
// }
//
// func (f *Fetcher) ResetStakerValidatorsForAll(tokenName string, stakerInfos []*oracletypes.StakerInfo) {
// 	beaconchain.ResetStakerValidatorsForAll(stakerInfos)
// }
