package cmd

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ExocoreNetwork/price-feeder/exoclient"
	"github.com/ExocoreNetwork/price-feeder/fetcher"
	"github.com/ExocoreNetwork/price-feeder/types"

	oracletypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	"github.com/ExocoreNetwork/price-feeder/fetcher/beaconchain"
	fetchertypes "github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
)

const (
	statusOk        = 0
	privFile        = "priv_validator_key.json"
	baseCurrency    = "USDT"
	defaultMaxRetry = 43200
	retryInterval   = 2 * time.Second

	//feeder_tokenName_feederID
	loggerTagPrefix = "feed_%s_%d"
)

// var updateConfig sync.Mutex

// RunPriceFeeder runs price feeder to fetching price and feed to exocorechain
func RunPriceFeeder(conf feedertypes.Config, logger feedertypes.LoggerInf, mnemonic string, sourcesPath string, standalone bool) {
	// init logger
	if logger = feedertypes.SetLogger(logger); logger == nil {
		panic("logger is not initialized")
	}
	// init logger, fetchers, exocoreclient
	once := new(sync.Once)
	once.Do(func() { initComponents(logger, conf, standalone) })
	// initComponents(logger, conf, standalone)

	f, _ := fetcher.GetFetcher()
	// start fetching on all supported sources and tokens
	logger.Info("start fetching prices from all sources")
	if err := f.Start(); err != nil {
		panic(fmt.Sprintf("failed to start Fetcher, error:%v", err))
	}

	ecClient, _ := exoclient.GetClient()
	defer ecClient.Close()
	// initialize oracle params by querying from exocore
	oracleP, err := ecClient.GetParams()
	for err != nil {
		// retry forever until be interrupted manually
		logger.Error("Failed to get oracle params on start, retrying...", err)
		time.Sleep(2 * time.Second)
		oracleP, err = ecClient.GetParams()
	}
	ecClient.Subscribe()

	fsMap := NewFeederMap()
	// we don't check empty tokenfeeders list
	maxNonce := oracleP.MaxNonce
	for feederID, feeder := range oracleP.TokenFeeders {
		if feederID == 0 {
			continue
		}
		tokenName := strings.ToLower(oracleP.Tokens[feeder.TokenID].Name)
		source := fetchertypes.Chainlink
		if fetchertypes.IsNSTToken(tokenName) {
			nstToken := fetchertypes.NSTToken(tokenName)
			if source = fetchertypes.GetNSTSource(nstToken); len(source) == 0 {
				panic(fmt.Sprintf("source of nst:%s is not set", tokenName))
			}
		}
		fsMap.Add(feeder, feederID, f, ecClient, source, tokenName, maxNonce, feedertypes.GetLogger(fmt.Sprintf(loggerTagPrefix, tokenName, feederID)))
	}
	feeders := fsMap.NewFeeders(logger)
	feeders.Start()

	for event := range ecClient.EventsCh() {
		switch e := event.(type) {
		case *exoclient.EventNewBlock:
			if paramsUpdate := e.ParamsUpdate(); paramsUpdate {
				oracleP, err = getOracleParamsWithMaxRetry(defaultMaxRetry, ecClient, logger)
				if err != nil {
					panic(fmt.Sprintf("Failed to get oracle params with maxRetry when params update detected, error:%v", err))
				}
				feeders.UpdateOracleParams(oracleP)
				// TODO: add newly added tokenfeeders if exists
			}
			feeders.Trigger(e.Height(), e.FeederIDs())
		case *exoclient.EventUpdatePrice:
			finalPrices := make([]*finalPrice, 0, len(e.Prices()))
			for _, price := range e.Prices() {
				feederIDList := oracleP.GetFeederIDsByTokenID(uint64(price.TokenID()))
				l := len(feederIDList)
				if l == 0 {
					logger.Error("Failed to get feederIDs by tokenID when try to updata local price for feeders on event_updatePrice", "tokenID", price.TokenID())
					continue
				}
				feederID := feederIDList[l-1]
				finalPrices = append(finalPrices, &finalPrice{
					feederID: int64(feederID),
					price:    price.Price(),
					decimal:  price.Decimal(),
					roundID:  price.RoundID(),
				})
			}
			feeders.UpdatePrice(e.TxHeight(), finalPrices)
		case *exoclient.EventUpdateNST:
			// int conversion is safe
			if updated := beaconchain.UpdateStakerValidators(int(e.StakerID()), e.ValidatorIndex(), uint64(e.Index()), e.Deposit()); !updated {
				logger.Error("failed to update staker's validator list", "stakerID", e.StakerID(), "validatorIndex", e.ValidatorIndex, "deposit", e.Deposit(), "index", e.Index())
				// try to reset all validatorList
				if err := ResetAllStakerValidators(ecClient, logger); err != nil {
					logger.Error("failed to reset all staker's validators for native-restaking-eth")
					// TODO: should we just clear all info to prevent further nst update
				}
			}
		}
	}
}

// getOracleParamsWithMaxRetry, get oracle params with max retry
// blocked
func getOracleParamsWithMaxRetry(maxRetry int, ecClient exoclient.ExoClientInf, logger feedertypes.LoggerInf) (oracleP *oracletypes.Params, err error) {
	if maxRetry <= 0 {
		maxRetry = defaultMaxRetry
	}
	for i := 0; i < maxRetry; i++ {
		oracleP, err = ecClient.GetParams()
		if err == nil {
			return
		}
		logger.Error("Failed to get oracle params, retrying...", "count", i, "max", maxRetry, "error", err)
		time.Sleep(retryInterval)
	}
	return
}

func ResetAllStakerValidators(ec exoclient.ExoClientInf, logger feedertypes.LoggerInf) error {
	stakerInfos, err := ec.GetStakerInfos(fetchertypes.GetNSTAssetID(fetchertypes.NativeTokenETH))
	if err != nil {
		return fmt.Errorf("failed to get stakerInfos for native-restaking-eth, error:%w", err)
	}
	if len(stakerInfos) > 0 {
		f, _ := fetcher.GetFetcher()
		if err := f.InitNSTStakerValidators(stakerInfos); err != nil {
			return fmt.Errorf("failed to set stakerInfs for native-restaking-eth, error:%w", err)
		}
	}
	return nil
}

// // initComponents, initialize fetcher, exoclient, it will panic if any initialization fialed
func initComponents(logger types.LoggerInf, conf types.Config, standalone bool) {
	count := 0
	for count < defaultMaxRetry {
		count++
		// init fetcher, start fetchers to get prices from sources
		err := fetcher.Init(conf.Tokens, sourcesPath)
		if err != nil {
			logger.Error("failed to init fetcher", "error", err)
			panic(err)
		}

		// init exoclient
		err = exoclient.Init(conf, mnemonic, privFile, standalone)
		if err != nil {
			if errors.Is(err, feedertypes.ErrInitConnectionFail) {
				logger.Info("retry initComponents due to connectionfailed", "count", count, "maxRetry", defaultMaxRetry, "error", err)
				time.Sleep(retryInterval)
				continue
			}
			logger.Error("failed to init exoclient", "error", err)
			panic(err)
		}

		ec, _ := exoclient.GetClient()
		_, err = ec.GetParams()
		for err != nil {
			// retry forever until be interrupted manually
			logger.Info("failed to get oracle params on start, retrying...", "error", err)
			time.Sleep(2 * time.Second)
			_, err = ec.GetParams()
		}

		// init native stakerlist for nstETH(beaconchain)
		if err := ResetAllStakerValidators(ec, logger); err != nil {
			panic(fmt.Sprintf("failed in initialize nst:%v", err))
		}

		logger.Info("Initialization for price-feeder done")
		break
	}
}
