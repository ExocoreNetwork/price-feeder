package cmd

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"

	oracletypes "github.com/imua-xyz/imuachain/x/oracle/types"
	fetchertypes "github.com/imua-xyz/price-feeder/fetcher/types"
	feedertypes "github.com/imua-xyz/price-feeder/types"

	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
)

const (
	loggerTagPrefix = "feed_%s_%d"
	statusOk        = 0
)

type priceFetcher interface {
	GetLatestPrice(source, token string) (fetchertypes.PriceInfo, error)
	AddTokenForSource(source, token string) bool
}

type priceSubmitter interface {
	SendTx(feederID, baseBlock uint64, price fetchertypes.PriceInfo, nonce int32) (*sdktx.BroadcastTxResponse, error)
	SendTx2Phases(feederID, baseBlock uint64, prices []*fetchertypes.PriceInfo, phase oracletypes.AggregationPhase, nonce int32) (*sdktx.BroadcastTxResponse, error)
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

type twoPhasesInfo struct {
	roundID       uint64
	cachedTree    []*oracletypes.MerkleTree
	finalizedTree *oracletypes.MerkleTree
	// this is the local index to record latest index of piece that have sent successfully
	// it starts from -1 to represent no successfully submitted piece
	sentLatestPieceIndex int64
	// this is synced with onchain information from events
	nextPieceIndex uint32
}

type PieceWithProof struct {
	Piece      string
	PieceIndex uint32
	IndexesStr string
	HashesStr  string
}

func (t *twoPhasesInfo) addMT(roundID uint64, mt *oracletypes.MerkleTree) bool {
	// func (t *twoPhasesInfo) addMT(roundID uint64, rd []byte, pieceSize uint32) bool {
	if roundID < t.roundID {
		return false
	}
	if roundID > t.roundID {
		t.roundID = roundID
		//		mt, err := oracletypes.DeriveMT(pieceSize, rd)
		//		if err != nil {
		//			return false
		//		}
		t.cachedTree = []*oracletypes.MerkleTree{
			mt,
		}
		t.finalizedTree = nil
		return true
	}
	if t.finalizedTree != nil {
		return false
	}

	//	mt, err := oracletypes.DeriveMT(pieceSize, rd)
	//	if err != nil {
	//		return false
	//	}
	for _, data := range t.cachedTree {

		// skip duplicated raw data
		if bytes.Equal(data.RootHash(), mt.RootHash()) {
			return false
		}
	}
	t.cachedTree = append(t.cachedTree, mt)
	return true
}

func (t *twoPhasesInfo) finalizeRawData(roundID uint64, rootHash []byte) bool {
	if roundID != t.roundID {
		return false
	}
	for _, mt := range t.cachedTree {
		if bytes.Equal(mt.RootHash(), rootHash) { // && data.roundID == roundID {
			t.finalizedTree = mt
			t.cachedTree = nil
			return true
		}
	}
	// make sure finalizedRawdata is nil if no match found, it's better to be count as miss than 'malicious' for a validator
	t.finalizedTree = nil
	return false
}

// GetRawDataPiece return the raw data piece for 2nd phase price submission
// returns (rootHash, proof, error)
// rootHash: the root hash of the merkle tree as string
// proof: the proof of the raw data piece, it's a string of joined indexes and joined hashes in format of base64, separated by |
// error: error if any
func (t *twoPhasesInfo) getRawDataPieceAndProof(roundID uint64, index uint32) (*PieceWithProof, error) {
	if roundID != t.roundID {
		return nil, errors.New("no finalized raw data found for this round, roundID")
	}
	piece, ok := t.finalizedTree.PieceByIndex(index)
	if !ok {
		return nil, errors.New("failed to get raw data piece by index")
	}
	proof := t.finalizedTree.MinimalProofByIndex(index)
	idxStr, hashStr := proof.FlattenString()
	return &PieceWithProof{Piece: string(piece), PieceIndex: index, IndexesStr: idxStr, HashesStr: hashStr}, nil
}

func (t *twoPhasesInfo) getLatestRootHash() ([]byte, uint32) {
	if len(t.cachedTree) == 0 {
		return nil, 0
	}
	return t.cachedTree[len(t.cachedTree)-1].RootHash(), t.cachedTree[len(t.cachedTree)-1].LeafCount()
}

func (t *twoPhasesInfo) setRoundID(roundID uint64) bool {
	if roundID > t.roundID {
		t.roundID = roundID
		t.cachedTree = nil
		t.finalizedTree = nil
		return true
	}
	return false
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

	twoPhasesInfo      *twoPhasesInfo
	twoPhasesPieceSize uint32
}

type FeederInfo struct {
	Source   string
	Token    string
	TokenID  uint64
	FeederID int
	// TODO: add check for rouleID, v1 can be skipped
	// ruleID
	StartRoundID   int64
	StartBaseBlock int64
	Interval       int64
	EndBlock       int64
	LastPrice      localPrice
	LastSent       signInfo
	// TwoPhases      bool
}

// AddRawData add rawData for 2nd phase price submission, this method will/should not be called concurrently with FinalizeRawData or GetRawDataPiece
// func (f *feeder) AddRawData(roundID uint64, mt *oracletypes.MerkleTree) {
func (f *feeder) AddRawData(roundID uint64, rd []byte, pieceSize uint32) bool {
	if f.twoPhasesInfo == nil {
		return false
	}
	mt, err := oracletypes.DeriveMT(pieceSize, rd)
	if err != nil {
		return false
	}
	return f.twoPhasesInfo.addMT(roundID, mt)
}

// FinalizeRawData finalize the raw data for 2nd phase price submission, this method will/should not be called concurrently with AddRawData or GetRawDataPiece
func (f *feeder) FinalizeRawData(roundID uint64, rootHash []byte) bool {
	if f.twoPhasesInfo == nil {
		return false
	}
	return f.twoPhasesInfo.finalizeRawData(roundID, rootHash)
}

// GetRawDataPiece return the raw data piece for 2nd phase price submission, this method will/should not be called concurrently with AddRawData or FinalizeRawData
func (f *feeder) GetRawDataPieceAndProof(roundID uint64, index uint32) (*PieceWithProof, error) {
	if f.twoPhasesInfo == nil {
		return nil, errors.New("two phases not enabled for this feeder")
	}
	return f.twoPhasesInfo.getRawDataPieceAndProof(roundID, index)
}

func (f *feeder) GetLatestRootHash() ([]byte, uint32) {
	return f.twoPhasesInfo.getLatestRootHash()
}

func (f *feeder) SetRoundID(roundID uint64) bool {
	if f.twoPhasesInfo == nil {
		return false
	}
	return f.twoPhasesInfo.setRoundID(roundID)
}

func (f *feeder) IsTwoPhases() bool {
	return f.twoPhasesInfo != nil
}

func (f *feeder) PriceChanged(p *fetchertypes.PriceInfo) bool {
	if f.twoPhasesInfo != nil {
		root := base64.StdEncoding.EncodeToString([]byte(p.Price))
		return root != f.lastPrice.price.Price
	}

	return f.lastPrice.price.Price != p.Price
}

func (f *feeder) NextSendablePieceWithProofs(roundID uint64) []*PieceWithProof {
	if f.twoPhasesInfo == nil || f.twoPhasesInfo.roundID != roundID || f.twoPhasesInfo.finalizedTree == nil {
		return nil
	}
	ret := make([]*PieceWithProof, 0, 1)
	// send one more for mempool to pre-cache
	if f.twoPhasesInfo.nextPieceIndex == 0 {
		if f.twoPhasesInfo.sentLatestPieceIndex == -1 {
			pwf, err := f.twoPhasesInfo.getRawDataPieceAndProof(roundID, 0)
			if err != nil {
				return nil
			}
			ret = append(ret, pwf)
		}
		if f.twoPhasesInfo.sentLatestPieceIndex <= 0 {
			pwf, err := f.twoPhasesInfo.getRawDataPieceAndProof(roundID, 1)
			if err != nil {
				return nil
			}
			ret = append(ret, pwf)
		}
	}
	if int64(f.twoPhasesInfo.nextPieceIndex) > f.twoPhasesInfo.sentLatestPieceIndex {
		pwf, err := f.twoPhasesInfo.getRawDataPieceAndProof(roundID, f.twoPhasesInfo.nextPieceIndex)
		if err != nil {
			return nil
		}
		ret = append(ret, pwf)
	}
	return ret
}

func (f *feeder) UpdateSentLatestPieceIndex(roundID uint64, index int64) int64 {
	if f.twoPhasesInfo.roundID != roundID {
		return -1
	}
	old := f.twoPhasesInfo.sentLatestPieceIndex
	f.twoPhasesInfo.sentLatestPieceIndex = index
	return old
}

func (f *feeder) Info() FeederInfo {
	var lastPrice localPrice
	var lastSent signInfo
	if f.lastPrice != nil {
		lastPrice = *f.lastPrice
	}
	if f.lastSent != nil {
		lastSent = *f.lastSent
	}
	return FeederInfo{
		Source:         f.source,
		Token:          f.token,
		TokenID:        f.tokenID,
		FeederID:       f.feederID,
		StartRoundID:   f.startRoundID,
		StartBaseBlock: f.startBaseBlock,
		Interval:       f.interval,
		EndBlock:       f.endBlock,
		LastPrice:      lastPrice,
		LastSent:       lastSent,
	}
}

func newFeeder(tf *oracletypes.TokenFeeder, feederID int, fetcher priceFetcher, submitter priceSubmitter, source string, token string, maxNonce int32, logger feedertypes.LoggerInf) *feeder {
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

func (f *feeder) start() {
	go func() {
		for {
			select {
			case h := <-f.heightsCh:
				if h.priceHeight > f.lastPrice.height {
					// the block event arrived early, wait for the price update events to update local price
					break
				}
				baseBlock, roundID, delta, active := f.calculateRound(h.commitHeight)
				if !active {
					break
				}
				// TODO: replace 3 with MaxNonce
				if delta < 3 {
					f.logger.Info("trigger feeder", "height_commit", h.commitHeight, "height_price", h.priceHeight)
					f.SetRoundID(uint64(roundID))

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
							f.logger.Debug("got latsetprice equal to local cache", "feeder", f.Info())
							continue
						}
						if !f.PriceChanged(&price) {
							f.logger.Info("didn't submit price due to price not changed", "roundID", roundID, "delta", delta, "price", price)
							f.logger.Debug("got latsetprice equal to local cache", "feeder", f.Info())
							continue

						}
						// the result of AddRawData does not matter, it's only needed when processing 2nd-phase submission
						f.AddRawData(uint64(roundID), []byte(price.Price), f.twoPhasesPieceSize)
						if nonce := f.lastSent.getNextNonceAndUpdate(roundID); nonce < 0 {
							f.logger.Error("failed to submit due to no available nonce", "roundID", roundID, "delta", delta, "feeder", f.Info())
						} else {
							if f.IsTwoPhases() {
								if root, count := f.GetLatestRootHash(); count > 0 {
									price.Price = string(root)
									price.RoundID = fmt.Sprintf("%d", count)
								} else {
									f.logger.Error("failed to submit 1st-phase price due to no available rootHash for 2-phases aggregation submission", "roundID", roundID, "delta", delta, "feeder", f.Info())
									continue
								}
							}
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
				// handle 2-phase submission
				pwfs := f.NextSendablePieceWithProofs(uint64(roundID))
				if len(pwfs) == 0 {
					f.logger.Info("no piece to submit for 2nd-phase price submission", "roundID", roundID, "delta", delta, "feeder", f.Info(), "height_commit", h.commitHeight, "height_price", h.priceHeight)
					continue
				}
				pInfos := make([]*fetchertypes.PriceInfo, 0, len(pwfs))
				for _, pwf := range pwfs {
					pInfos = append(pInfos, &fetchertypes.PriceInfo{
						Price:   pwf.Piece,
						RoundID: fmt.Sprintf("%d", pwf.PieceIndex),
					})
					if len(pwf.IndexesStr) > 0 && len(pwf.HashesStr) > 0 {
						pInfos = append(pInfos, &fetchertypes.PriceInfo{
							Price:   pwf.HashesStr,
							RoundID: pwf.IndexesStr,
						})
					}
					oldIndex := f.UpdateSentLatestPieceIndex(uint64(roundID), int64(pwf.PieceIndex))
					_, err := f.submitter.SendTx2Phases(uint64(f.feederID), uint64(baseBlock), pInfos, oracletypes.AggregationPhaseTwo, 1)
					if err != nil {
						f.logger.Error("failed to send tx for 2nd-phase price submission", "roundID", roundID, "delta", delta, "feeder", f.Info(), "height_commit", h.commitHeight, "height_price", h.priceHeight, "error", err)
						// revert local index if failed to submit
						f.UpdateSentLatestPieceIndex(uint64(roundID), oldIndex)
					}
				}
			case price := <-f.priceCh:
				f.lastPrice.price = *(price.price)
				// update latest height that price had been updated
				f.lastPrice.height = price.txHeight
				f.logger.Info("updated price", "price", price.price, "txHeight", price.txHeight)
			case req := <-f.paramsCh:
				if err := f.updateFeederParams(req.params); err != nil {
					// This should not happen under this case.
					f.logger.Error("failed to update params", "new params", req.params)
				}
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

func (f *feeder) updateFeederParams(p *oracletypes.Params) error {
	if p == nil || len(p.TokenFeeders) < f.feederID+1 {
		return errors.New("invalid oracle parmas")
	}
	// TODO: update feeder's params
	tokenFeeder := p.TokenFeeders[f.feederID]
	if f.endBlock != int64(tokenFeeder.EndBlock) {
		f.endBlock = int64(tokenFeeder.EndBlock)
	}
	if f.startBaseBlock != int64(tokenFeeder.StartBaseBlock) {
		f.startBaseBlock = int64(tokenFeeder.StartBaseBlock)
	}
	if f.interval != int64(tokenFeeder.Interval) {
		f.interval = int64(tokenFeeder.Interval)
	}
	if p.MaxNonce > 0 {
		f.lastSent.maxNonce = p.MaxNonce
	}
	return nil
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

type Feeders struct {
	locker    *sync.Mutex
	running   bool
	fetcher   priceFetcher
	submitter priceSubmitter
	logger    feedertypes.LoggerInf
	feederMap map[int]*feeder
	// TODO: feeder has sync management, so feeders could remove these channel
	trigger      chan *triggerReq
	updatePrice  chan *updatePricesReq
	updateParams chan *oracletypes.Params
	// updateNST    chan *updateNSTReq
}

func NewFeeders(logger feedertypes.LoggerInf, fetcher priceFetcher, submitter priceSubmitter) *Feeders {
	return &Feeders{
		locker:    new(sync.Mutex),
		logger:    logger,
		fetcher:   fetcher,
		submitter: submitter,
		feederMap: make(map[int]*feeder),
		//		feederMap: fm,
		// don't block on height increasing
		trigger:     make(chan *triggerReq, 1),
		updatePrice: make(chan *updatePricesReq, 1),
		// it's safe to have a buffer to not block running feeders,
		// since for running feeders, only endBlock is possible to be modified
		updateParams: make(chan *oracletypes.Params, 1),
	}

}

func (fs *Feeders) SetupFeeder(tf *oracletypes.TokenFeeder, feederID int, source string, token string, maxNonce int32) {
	fs.locker.Lock()
	defer fs.locker.Unlock()
	if fs.running {
		fs.logger.Error("failed to setup feeder for a running feeders, this should be called before feeders is started")
		return
	}
	fs.feederMap[feederID] = newFeeder(tf, feederID, fs.fetcher, fs.submitter, source, token, maxNonce, fs.logger.With("feeder", fmt.Sprintf(loggerTagPrefix, token, feederID)))
}

// Start will start to listen the trigger(newHeight) and updatePrice events
// usd channels to avoid race condition on map
func (fs *Feeders) Start() {
	fs.locker.Lock()
	if fs.running {
		fs.logger.Error("failed to start feeders since it's already running")
		fs.locker.Unlock()
		return
	}
	fs.running = true
	fs.locker.Unlock()
	for _, f := range fs.feederMap {
		f.start()
	}
	go func() {
		for {
			select {
			case params := <-fs.updateParams:
				results := []chan *updateParamsRes{}
				existingFeederIDs := make(map[int64]struct{})
				for _, f := range fs.feederMap {
					res := f.updateParams(params)
					results = append(results, res)
					existingFeederIDs[int64(f.feederID)] = struct{}{}
				}
				// wait for all feeders to complete updateing params
				for _, res := range results {
					<-res
				}
				for tfID, tf := range params.TokenFeeders {
					if _, ok := existingFeederIDs[int64(tfID)]; !ok {
						// create and start a new feeder
						tokenName := strings.ToLower(params.Tokens[tf.TokenID].Name)
						source := fetchertypes.Chainlink
						if fetchertypes.IsNSTToken(tokenName) {
							nstToken := fetchertypes.NSTToken(tokenName)
							if source = fetchertypes.GetNSTSource(nstToken); len(source) == 0 {
								fs.logger.Error("failed to add new feeder, source of nst token is not set", "token", tokenName)
							}
						} else if !strings.HasSuffix(tokenName, fetchertypes.BaseCurrency) {
							// NOTE: this is for V1 only
							tokenName += fetchertypes.BaseCurrency
						}

						feeder := newFeeder(tf, tfID, fs.fetcher, fs.submitter, source, tokenName, params.MaxNonce, fs.logger)
						fs.feederMap[tfID] = feeder
						feeder.start()
					}
				}
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
						fs.logger.Info("update price for feeder", "feeder", feeder.Info(), "price", price.price, "roundID", price.roundID, "txHeight", req.txHeight)
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
	if p == nil {
		fs.logger.Error("received nil oracle params")
		return
	}
	if len(p.TokenFeeders) == 0 {
		fs.logger.Error("received empty token feeders")
		return
	}
	fs.updateParams <- p
}
