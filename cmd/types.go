package cmd

import (
	"errors"

	oracletypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	fetchertypes "github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	types "github.com/ExocoreNetwork/price-feeder/types"

	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
)

type priceFetcher interface {
	GetLatestPrice(source, token string) (fetchertypes.PriceInfo, error)
	AddTokenForSource(source, token string) bool
}
type priceSubmitter interface {
	SendTx(feederID uint64, baseBlock uint64, price fetchertypes.PriceInfo, nonce int32) (*sdktx.BroadcastTxResponse, error)
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

type triggerHeights struct {
	commitHeight int64
	priceHeight  int64
}
type updatePrice struct {
	txHeight int64
	price    *fetchertypes.PriceInfo
}
type updateParamsReq struct {
	params *oracletypes.Params
	result chan *updateParamsRes
}
type updateParamsRes struct {
}

type localPrice struct {
	price  fetchertypes.PriceInfo
	height int64
}

// TODO: stop channel to close
type feeder struct {
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

	//	maxNonce int32

	fetcher   priceFetcher
	submitter priceSubmitter
	lastPrice *localPrice
	lastSent  *signInfo

	priceCh   chan *updatePrice
	heightsCh chan *triggerHeights
	paramsCh  chan *updateParamsReq
}

type feederInfo struct {
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
	lastPrice      localPrice
	lastSent       signInfo
}

func (f *feeder) Info() feederInfo {
	return feederInfo{
		source:         f.source,
		token:          f.token,
		tokenID:        f.tokenID,
		feederID:       f.feederID,
		startRoundID:   f.startRoundID,
		startBaseBlock: f.startBaseBlock,
		interval:       f.interval,
		endBlock:       f.endBlock,
		lastPrice:      *f.lastPrice,
		lastSent:       *f.lastSent,
	}
}

// NewFeeder new a feeder struct from oracletypes' tokenfeeder
func NewFeeder(tf *oracletypes.TokenFeeder, feederID int, fetcher priceFetcher, submitter priceSubmitter, source string, token string, maxNonce int32, logger feedertypes.LoggerInf) *feeder {
	return &feeder{
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

		//		maxNonce: maxNonce,

		fetcher:   fetcher,
		submitter: submitter,
		lastSent: &signInfo{
			maxNonce: maxNonce,
		},
		lastPrice: &localPrice{},

		priceCh:   make(chan *updatePrice, 1),
		heightsCh: make(chan *triggerHeights, 1),
		paramsCh:  make(chan *updateParamsReq, 1),
	}
}

func (f *feeder) start() {
	go func() {
		for {
			select {
			case h := <-f.heightsCh:
				if h.priceHeight > f.lastPrice.height {
					// the block event arrived early, wait for the price update evenst to update local price
					break
				}
				baseBlock, roundID, delta, active := f.calculateRound(h.commitHeight)
				if !active {
					break
				}
				if delta < 3 {
					f.logger.Info("trigger feeder", "height_commith", h.commitHeight, "height_price", h.priceHeight)
					if price, err := f.fetcher.GetLatestPrice(f.source, f.token); err != nil {
						f.logger.Error("failed to get latest price", "roundID", roundID, "delta", delta, "feeder", f.Info(), "error", err)
						if errors.Is(err, feedertypes.ErrSourceTokenNotConfigured) {
							f.logger.Error("add token from configure of source", "token", f.token, "source", f.source)
							// blocked this feeder since no available fetcher_source_price working
							if added := f.fetcher.AddTokenForSource(f.source, f.token); !added {
								f.logger.Error("failed to complete adding token from configure, pleas check and update the config file of source if necessary", "token", f.token, "source", f.source)
							}
						}
					} else {
						if price.IsZero() {
							f.logger.Info("got nil latest price, skip submitting price", "roundID", roundID, "delta", delta)
							continue
						}
						if len(price.Price) >= 32 && price.EqualToBase64Price(f.lastPrice.price) {
							f.logger.Info("didn't submit price due to price not changed", "roundID", roundID, "delta", delta, "price", price)
							f.logger.Debug("got latsetprice equal to local cache", "feeder", f.Info())
							continue
						} else if price.EqualPrice(f.lastPrice.price) {
							f.logger.Info("didn't submit price due to price not changed", "roundID", roundID, "delta", delta, "price", price)
							f.logger.Debug("got latsetprice equal to local cache", "feeder", f.Info())
							continue
						}
						if nonce := f.lastSent.getNextNonceAndUpdate(roundID); nonce < 0 {
							f.logger.Error("failed to submit due to no available nonce", "roundID", roundID, "delta", delta, "feeder", f.Info())
						} else {
							//							f.logger.Info("send tx to submit price", "price", price, "nonce", nonce, "baseBlock", baseBlock, "delta", delta)
							res, err := f.submitter.SendTx(uint64(f.feederID), uint64(baseBlock), price, nonce)
							if err != nil {
								f.lastSent.revertNonce(roundID)
								f.logger.Error("failed to send tx submitting price", "price", price, "nonce", nonce, "baseBlock", baseBlock, "delta", delta, "feeder", f.Info(), "error_feeder", err)
							}
							if txResponse := res.GetTxResponse(); txResponse.Code == statusOk {
								f.logger.Info("sent tx to submit price", "price", price, "nonce", nonce, "baseBlock", baseBlock, "delta", delta)
							} else {
								f.lastSent.revertNonce(roundID)
								f.logger.Error("failed to send tx submitting price", "price", price, "nonce", nonce, "baseBlock", baseBlock, "delta", delta, "feeder", f.Info(), "response_rawlog", txResponse.RawLog)
							}

						}
					}
				}
			case price := <-f.priceCh:
				f.lastPrice.price = *(price.price)
				// update latest height that price had been updated
				f.lastPrice.height = price.txHeight
			case req := <-f.paramsCh:
				f.updateFeederParams(req.params)
				req.result <- &updateParamsRes{}
			}
		}
	}()
}

// UpdateParams updates the feeder's params from oracle params, this method will block if the channel is full
// which means the update for params will must be delivered to the feeder's routine when this method is called
// blocked
func (f *feeder) updateParams(params *oracletypes.Params) chan *updateParamsRes {
	// TODO update oracle parms
	res := make(chan *updateParamsRes)
	req := &updateParamsReq{params: params, result: res}
	f.paramsCh <- req
	return res
}

// UpdatePrice will upate local price for feeder
// non-blocked
func (f *feeder) updatePrice(txHeight int64, price *fetchertypes.PriceInfo) {
	// we dont't block this process when the channelis full, if this updating is skipped
	// it will be update at next time when event arrived
	select {
	case f.priceCh <- &updatePrice{price: price, txHeight: txHeight}:
	default:
	}
}

// Trigger notify the feeder that a new block height is committed
// non-blocked
func (f *feeder) trigger(commitHeight, priceHeight int64) {
	// the channel got 1 buffer, so it should always been sent successfully
	// and if not(the channel if full), we just skip this height and don't block
	select {
	case f.heightsCh <- &triggerHeights{commitHeight: commitHeight, priceHeight: priceHeight}:
	default:
	}
}

func (f *feeder) updateFeederParams(p *oracletypes.Params) {
	if p == nil || len(p.TokenFeeders) == 0 {
		return
	}
	// TODO: update feeder's params

}

// TODO: stop feeder routine
// func (f *feeder) Stop()

func (f *feeder) calculateRound(h int64) (baseBlock, roundID, delta int64, active bool) {
	// endBlock itself is considered as active
	if f.startBaseBlock > h || (f.endBlock > 0 && h > f.endBlock) {
		return
	}
	active = true
	delta = (h - f.startBaseBlock) % f.interval
	roundID = (h-f.startBaseBlock)/f.interval + f.startRoundID
	baseBlock = h - delta
	return
}

type triggerReq struct {
	height    int64
	feederIDs map[int64]struct{}
}

type finalPrice struct {
	feederID int64
	price    string
	decimal  int32
	roundID  string
}
type updatePricesReq struct {
	txHeight int64
	prices   []*finalPrice
}

type feederMap map[int]*feeder

// NewFeederMap new a map <feederID->feeder>
func NewFeederMap() feederMap {
	return make(map[int]*feeder)
}
func (fm feederMap) NewFeeders(logger feedertypes.LoggerInf) *Feeders {
	return &Feeders{
		logger:    logger,
		feederMap: fm,
		// don't block on height increasing
		trigger:     make(chan *triggerReq, 1),
		updatePrice: make(chan *updatePricesReq, 1),
		// it's safe to have a buffer to not block running feeders,
		// since for running feeders, only endBlock is possible to be modified
		updateParams: make(chan *oracletypes.Params, 1),
	}
}

// Add adds a new feeder with feederID into the map
func (fm feederMap) Add(tf *oracletypes.TokenFeeder, feederID int, fetcher priceFetcher, submitter priceSubmitter, source string, token string, maxNonce int32, logger feedertypes.LoggerInf) {
	fm[feederID] = &feeder{
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
		fetcher:        fetcher,
		submitter:      submitter,
		lastPrice:      &localPrice{},
		lastSent: &signInfo{
			maxNonce: maxNonce,
		},

		priceCh:   make(chan *updatePrice, 1),
		heightsCh: make(chan *triggerHeights, 1),
		paramsCh:  make(chan *updateParamsReq, 1),
	}
}

type Feeders struct {
	logger    feedertypes.LoggerInf
	feederMap map[int]*feeder
	// TODO: feeder has sync management, so feeders could remove these channel
	trigger      chan *triggerReq
	updatePrice  chan *updatePricesReq
	updateParams chan *oracletypes.Params
	// updateNST    chan *updateNSTReq
}

// Start will start to listen the trigger(newHeight) and updatePrice events
// usd channels to avoid race condition on map
func (fs *Feeders) Start() {
	for _, f := range fs.feederMap {
		f.start()
	}
	go func() {
		for {
			select {
			case params := <-fs.updateParams:
				results := []chan *updateParamsRes{}
				for _, f := range fs.feederMap {
					res := f.updateParams(params)
					results = append(results, res)
				}
				// wait for all feeders to complete updateing params
				for _, res := range results {
					<-res
				}
				// TODO: add newly added tokenfeeders if exists

			case t := <-fs.trigger:
				// the order does not matter
				for _, f := range fs.feederMap {
					priceHeight := int64(0)
					if _, ok := t.feederIDs[int64(f.feederID)]; ok {
						priceHeight = t.height
					}
					f.trigger(t.height, priceHeight)
				}
			case req := <-fs.updatePrice:
				for _, price := range req.prices {
					// int conversion is safe
					if feeder, ok := fs.feederMap[int(price.feederID)]; !ok {
						fs.logger.Error("failed to get feeder by feederID when update price for feeders", "updatePriceReq", req)
						continue
					} else {
						feeder.updatePrice(req.txHeight, &fetchertypes.PriceInfo{
							Price:   price.price,
							Decimal: price.decimal,
							RoundID: price.roundID,
						})
					}
				}
			}
		}
	}()
}

// Trigger notify all feeders that a new block height is committed
// non-blocked
func (fs *Feeders) Trigger(height int64, feederIDs map[int64]struct{}) {
	select {
	case fs.trigger <- &triggerReq{height: height, feederIDs: feederIDs}:
	default:
	}
}

// UpdatePrice will upate local price for all feeders
// non-blocked
func (fs *Feeders) UpdatePrice(txHeight int64, prices []*finalPrice) {
	select {
	case fs.updatePrice <- &updatePricesReq{txHeight: txHeight, prices: prices}:
	default:
	}
}

// UpdateOracleParams updates all feeders' params from oracle params
// if the receiving channel is full, blocking until all updateParams are received by the channel
func (fs *Feeders) UpdateOracleParams(p *oracletypes.Params) {
	fs.updateParams <- p
}

// Define the types for the feeder
// type feederParams struct {
// 	startRoundID uint64
// 	startBlock   uint64
// 	endBlock     uint64
// 	interval     uint64
// 	decimal      int32
// 	tokenIDStr   string
// 	feederID     int64
// 	tokenName    string
// }
//
// func (f *feederParams) update(p oracletypes.Params) (updated bool) {
// 	tokenFeeder := p.TokenFeeders[f.feederID]
// 	if tokenFeeder.StartBaseBlock != f.startBlock {
// 		f.startBlock = tokenFeeder.StartBaseBlock
// 		updated = true
// 	}
// 	if tokenFeeder.EndBlock != f.endBlock {
// 		f.endBlock = tokenFeeder.EndBlock
// 		updated = true
// 	}
// 	if tokenFeeder.Interval != f.interval {
// 		f.interval = tokenFeeder.Interval
// 		updated = true
// 	}
// 	if p.Tokens[tokenFeeder.TokenID].Decimal != f.decimal {
// 		f.decimal = p.Tokens[tokenFeeder.TokenID].Decimal
// 		updated = true
// 	}
// 	return
// }

func getLogger() types.LoggerInf {
	return types.GetLogger("")
}
