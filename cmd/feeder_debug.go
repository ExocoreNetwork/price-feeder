package cmd

import (
	"errors"
	"fmt"
	"time"

	"github.com/ExocoreNetwork/price-feeder/exoclient"
	fetchertypes "github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
)

type PriceJSON struct {
	Price     string `json:"price"`
	DetID     string `json:"det_id"`
	Decimal   int32  `json:"decimal"`
	Timestamp string `json:"timestamp"`
}

func (p PriceJSON) getPriceInfo() fetchertypes.PriceInfo {
	return fetchertypes.PriceInfo{
		Price:     p.Price,
		Decimal:   p.Decimal,
		RoundID:   p.DetID,
		Timestamp: p.Timestamp,
	}
}

type sendRes struct {
	status  statusCode
	txHash  string
	details string
}

type sendReq struct {
	// on which block committed send this transaction, required
	height    int64
	baseBlock uint64
	// optional
	roundID int64

	feederID uint64
	price    PriceJSON
	//	decimal   int32
	//	detID     string
	//	timestamp string
	nonce int32

	result chan *sendRes
}

type statusCode string

const (
	overwrite statusCode = "overwrite the pending transaction"
	success   statusCode = "successfully sent the transaction"
	fail      statusCode = "failed to send transaction"
)

var (
	sendCh = make(chan *sendReq)
)

func DebugPriceFeeder(conf feedertypes.Config, logger feedertypes.LoggerInf, mnemonic string, sourcesPath string) {
	// init logger
	if logger = feedertypes.SetLogger(logger); logger == nil {
		panic("logger is not initialized")
	}
	// init exoclient
	// 1. subscribing for heights
	// 2. sending create-price tx
	count := 0
	for count < DebugRetryConfig.MaxAttempts {
		if err := exoclient.Init(conf, mnemonic, privFile, true); err != nil {
			if errors.Is(err, feedertypes.ErrInitConnectionFail) {
				logger.Info("retry initComponents due to connectionfailed", "count", count, "maxRetry", DefaultRetryConfig.MaxAttempts, "error", err)
				time.Sleep(DebugRetryConfig.Interval)
				continue
			}
			logger.Error("failed to init exoclient", "error", err)
			return
		}
		break
	}
	ec, _ := exoclient.GetClient()

	_, err := getOracleParamsWithMaxRetry(1, ec, logger)
	if err != nil {
		logger.Error("failed to get oracle params", "error", err)
		return
	}
	logger.Info("subscribe block heights...")
	ecClient, _ := exoclient.GetClient()
	defer ecClient.Close()
	ecClient.Subscribe()
	var pendingReq *sendReq
	for {
		select {
		case event := <-ecClient.EventsCh():
			if e, ok := event.(*exoclient.EventNewBlock); ok {
				logger.Info("new block commited", "height", e.Height())
				if pendingReq != nil {
					if pendingReq.height <= e.Height() {
						// send this transaction
						res, err := ecClient.SendTxDebug(pendingReq.feederID, pendingReq.baseBlock, pendingReq.price.getPriceInfo(), pendingReq.nonce)
						fmt.Println("debug--> result")
						fmt.Println(res)
						fmt.Println(err)
						pendingReq = nil
					}
				}
			}
		case req := <-sendCh:
			pendingReq = req
		}
	}
}
