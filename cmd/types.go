package cmd

import (
	oracletypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	fetchertypes "github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	types "github.com/ExocoreNetwork/price-feeder/types"

	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
)

type priceFetcher interface {
	GetLatestPrice(source, token string) (fetchertypes.PriceInfo, error)
}
type priceSubmitter interface {
	SendTx(feederID uint64, baseBlock uint64, price, roundID string, decimal int, nonce int32) (*sdktx.BroadcastTxResponse, error)
}

type signInfo struct {
	maxNonce int32
	roundID  int64
	nonce    int32
}

func (s *signInfo) getNextNonceAndUpdate(roundID int64) int32 {
	if roundID < s.roundID {
		return -1
	} else if roundID > s.roundID {
		s.roundID = roundID
		s.nonce = 1
		return 1
	}
	if s.nonce = s.nonce + 1; s.nonce > s.maxNonce {
		s.nonce = s.maxNonce
		return -1
	}
	return s.nonce
}
func (s *signInfo) revertNonce(roundID int64) {
	if s.roundID == roundID && s.nonce > 0 {
		s.nonce--
	}
}

type Feeder struct {
	logger feedertypes.LoggerInf
	// TODO: currently only 1 source for each token, so we can just set it as a field here
	source   string
	token    string
	tokenID  uint64
	feederID int
	// TODO: add check for rouleID, v1 can be skipped
	// ruleID
	startRoundID   int64
	startBaseBlock int64
	interval       int64
	endBlock       int64

	maxNonce int32

	fetcher    priceFetcher
	submitter  priceSubmitter
	price      fetchertypes.PriceInfo
	latestSent *signInfo

	priceCh  chan *fetchertypes.PriceInfo
	heightCh chan int64
	// TODO: a sending routine
	// chan struct{basedBlock, priceInfo, nonce, gas}
}

// NewFeeder new a feeder struct from oracletypes' tokenfeeder
func NewFeeder(tf *oracletypes.TokenFeeder, feederID int, fetcher priceFetcher, submitter priceSubmitter, source string, token string, maxNonce int32, logger feedertypes.LoggerInf) *Feeder {
	return &Feeder{
		logger:   logger,
		source:   source,
		token:    token,
		tokenID:  tf.TokenID,
		feederID: feederID,
		// these conversion a safe since the block height defined in cosmossdk is int64
		startRoundID:   int64(tf.StartRoundID),
		startBaseBlock: int64(tf.StartBaseBlock),
		interval:       int64(tf.Interval),
		endBlock:       int64(tf.EndBlock),

		maxNonce: maxNonce,

		fetcher:    fetcher,
		submitter:  submitter,
		price:      fetchertypes.PriceInfo{},
		latestSent: &signInfo{},

		priceCh:  make(chan *fetchertypes.PriceInfo, 1),
		heightCh: make(chan int64, 1),
	}
}

// func (f *feeder) Start() (chan int64, chan *fetchertypes.PriceInfo) {
func (f *Feeder) Start() {
	//	trigger := make(chan int64, 1)
	//	update := make(chan *fetchertypes.PriceInfo)
	go func() {
		for {
			select {
			case h := <-f.heightCh:
				baseBlock, roundID, delta := f.calculateRound(h)
				if delta < 3 {
					if price, err := f.fetcher.getLatestPrice(f.source, f.token); err != nil {
						f.logger.Error("failed to get latest price", "source", f.source, "token", f.token, "roundID", roundID, "delta", delta, "error", err)
					} else {
						if price.IsZero() {
							f.logger.Info("got nil latest price, skip submitting price", "source", f.source, "token", f.token, "roundID", roundID, "delta", delta)
							continue
						}
						if price.Equal(f.price) {
							f.logger.Info("didn't submit price due to price not changed", "source", f.source, "token", f.token, "roundID", roundID, "delta", delta)
							f.logger.Debug("got latsetprice equal to local cache", "price", f.price)
						}
						if nonce := f.latestSent.getNextNonceAndUpdate(roundID); nonce < 0 {
							f.logger.Info("didn't submit due to no available nonce", "roundID", roundID, "delta", delta)
						} else {
							f.logger.Info("send tx to submit price", "feederID", f.feederID, "price", price, "nonce", nonce, "baseBlock", baseBlock, "delta", delta)
							res, err := f.submitter.SendTx(uint64(f.feederID), uint64(baseBlock), price.Price, price.RoundID, price.Decimal, nonce)
							if err != nil {
								f.logger.Info("failed to send tx submitting price", "feederID", f.feederID, "price", price, "nonce", nonce, "baseBlock", baseBlock, "delta", delta, "error_feeder", err)
							}
							if txResponse := res.GetTxResponse(); txResponse.Code == statusOk {
								f.logger.Info("sent tx to submit price", "feederID", f.feederID, "price", price, "nonce", nonce, "baseBlock", baseBlock, "delta", delta)
							} else {
								f.logger.Error("failed to send tx submitting price", "feederID", f.feederID, "price", price, "nonce", nonce, "baseBlock", baseBlock, "delta", delta, "response_rawlog", txResponse.RawLog)
							}

						}
					}
				}
			case price := <-f.priceCh:
				f.price = *price
			}
		}
	}()
	// return nil, nil
}

// UpdatePrice will upate local price for feeder
// non-blocked
func (f *Feeder) UpdatePrice(price *fetchertypes.PriceInfo) {
	// we dont't block this process when the channelis full, if this updating is skipped
	// it will be update at next time when event arrived
	select {
	case f.priceCh <- price:
	default:
	}
}
func (f *Feeder) Trigger(height int64) {
	// the channel got 1 buffer, so it should always been sent successfully
	// and if not(the channel if full), we just skip this height and don't block
	select {
	case f.heightCh <- height:
	default:
	}
}

// TODO: stop feeder routine
// func (f *feeder) Stop()

func (f *Feeder) calculateRound(h int64) (baseBlock, roundID, delta int64) {
	delta = (h - f.startBaseBlock) % f.interval
	roundID = (h-f.startBaseBlock)/f.interval + f.startRoundID
	baseBlock = h - delta
	return
}

type updatePriceReq struct {
	price    *fetchertypes.PriceInfo
	feederID int
}

type updateNSTReq struct {
	feederID int
}

type Feeders struct {
	feederMap map[int]*Feeder
	// TODO: feeder has sync management, so feeders could remove these channel
	trigger     chan int64
	updatePrice chan *updatePriceReq
	updateNST   chan *updateNSTReq
}

// type updatePriceReq struct {
// 	price    *fetchertypes.PriceInfo
// 	feederID int64
// }
//
// type updateNSTReq struct {
// 	feederID int64
// }

func NewFeeders(feederMap map[int]*Feeder) *Feeders {
	return &Feeders{
		feederMap: feederMap,
		// don't block on height increasing
		trigger:     make(chan int64, 1),
		updatePrice: make(chan *updatePriceReq),
		updateNST:   make(chan *updateNSTReq),
	}
}

// TODO: remove channels
func (fs *Feeders) Start() {
	// TODO: buffer_1 ?
	//	trigger := make(chan int64)
	//	updatePrice := make(chan *updatePriceReq)
	//	updateNST := make(chan *updateNSTReq)
	for _, f := range fs.feederMap {
		f.Start()
	}
	go func() {
		for {
			select {
			case height := <-fs.trigger:
				// the order does not matter
				for _, f := range fs.feederMap {
					f.Trigger(height)
				}
			case req := <-fs.updatePrice:
				fs.feederMap[req.feederID].UpdatePrice(req.price)
			case nstInfo := <-fs.updateNST:
				// TODO: update staker's validatorList
				_ = nstInfo
			}
		}
	}()
}

func (fs *Feeders) Trigger() {

}

func (fs *Feeders) UpdatePrice() {

}

func (fs *Feeders) UpdateNST() {

}

// Define the types for the feeder
type feederParams struct {
	startRoundID uint64
	startBlock   uint64
	endBlock     uint64
	interval     uint64
	decimal      int32
	tokenIDStr   string
	feederID     int64
	tokenName    string
}

// Define the types for the event
type eventRes struct {
	height       uint64
	txHeight     uint64
	gas          int64
	price        string
	decimal      int
	roundID      uint64
	params       *feederParams
	priceUpdated bool
	nativeToken  string
}

// Define the types for the feederInfo
type feederInfo struct {
	params      *feederParams
	latestPrice string
	updateCh    chan eventRes
}

func (f *feederParams) update(p oracletypes.Params) (updated bool) {
	tokenFeeder := p.TokenFeeders[f.feederID]
	if tokenFeeder.StartBaseBlock != f.startBlock {
		f.startBlock = tokenFeeder.StartBaseBlock
		updated = true
	}
	if tokenFeeder.EndBlock != f.endBlock {
		f.endBlock = tokenFeeder.EndBlock
		updated = true
	}
	if tokenFeeder.Interval != f.interval {
		f.interval = tokenFeeder.Interval
		updated = true
	}
	if p.Tokens[tokenFeeder.TokenID].Decimal != f.decimal {
		f.decimal = p.Tokens[tokenFeeder.TokenID].Decimal
		updated = true
	}
	return
}

func getLogger() types.LoggerInf {
	return types.GetLogger("")
}
