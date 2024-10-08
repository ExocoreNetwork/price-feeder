package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	oracleTypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	"github.com/ExocoreNetwork/price-feeder/exoclient"
	"github.com/ExocoreNetwork/price-feeder/fetcher"
	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/cosmos/go-bip39"
	"google.golang.org/grpc"
)

const (
	statusOk     = 0
	privFile     = "priv_validator_key.json"
	baseCurrency = "USDT"
)

var updateConfig sync.Mutex

// RunPriceFeeder runs price feeder to fetching price and feed to exocorechain
func RunPriceFeeder(conf feedertypes.Config, mnemonic string, sourcesPath string, standalone bool) {
	runningFeeders := make(map[int64]*feederInfo)
	// start fetcher to get prices from chainlink
	f := fetcher.Init(conf.Sources, conf.Tokens, sourcesPath)
	// start all supported sources and tokens
	_ = f.StartAll()
	// TODO: wait to continue until price fetched
	//	time.Sleep(5 * time.Second)

	cc := initExocoreClient(conf, standalone)
	defer cc.Close()

	// query oracle params
	oracleP, err := exoclient.GetParams(cc)
	for err != nil {
		// retry forever until be interrupted manually
		log.Println("Fail to get oracle params on star, retrying...", err)
		time.Sleep(2 * time.Second)
		oracleP, err = exoclient.GetParams(cc)
	}

	oracleParamsFeedingTokens := make(map[string]struct{})
	// remainingFeeders used to track token feeders that have been set in oracle params but with no configuration from price-feeder for price fetching, and if the lenght of map is bigger than 0, price-feeder will try continuously to reload the configure file until those feeders are able to work
	remainningFeeders := make(map[string]*feederInfo)
	// check all live feeders and set seperate routine to udpate prices
	for feederID, feeder := range oracleP.TokenFeeders {
		if feederID == 0 {
			// feederID=0 is reserved
			continue
		}
		var tokenName string
		if strings.EqualFold(oracleP.Tokens[feeder.TokenID].Name, types.NativeTokenETH) {
			tokenName = strings.ToLower(oracleP.Tokens[feeder.TokenID].Name)
		} else {
			tokenName = strings.ToLower(oracleP.Tokens[feeder.TokenID].Name + baseCurrency)
		}
		oracleParamsFeedingTokens[tokenName] = struct{}{}
		decimal := oracleP.Tokens[feeder.TokenID].Decimal
		fInfo := &feederInfo{
			params: &feederParams{
				startRoundID: feeder.StartRoundID,
				startBlock:   feeder.StartBaseBlock,
				endBlock:     feeder.EndBlock,
				interval:     feeder.Interval,
				decimal:      decimal,
				tokenIDStr:   strconv.FormatInt(int64(feeder.TokenID), 10),
				feederID:     int64(feederID),
				tokenName:    tokenName,
			},
		}
		remainningFeeders[tokenName] = fInfo
		// TODO: refactor
		if strings.EqualFold(tokenName, types.NativeTokenETH) {
			// actually not usdt, we so need to do refactor for the mess
			delete(remainningFeeders, tokenName)
			trigger := make(chan eventRes, 3)
			fInfo.updateCh = trigger
			runningFeeders[int64(feederID)] = fInfo
			// start a routine to update price for this feeder
			go feedToken(fInfo, cc, f, conf)
			continue
		}

		// check if this feeder is supported by this price-feeder
		for _, token := range conf.Tokens {
			if strings.EqualFold(token, tokenName) {
				delete(remainningFeeders, tokenName)
				trigger := make(chan eventRes, 3)
				fInfo.updateCh = trigger
				runningFeeders[int64(feederID)] = fInfo
				// start a routine to update price for this feeder
				go feedToken(fInfo, cc, f, conf)
				break
			}
		}
	}

	newFeeder := make(chan *feederInfo)
	// check config update for remaining tokenFeeders set in oracle params
	if len(remainningFeeders) > 0 {
		// we set a background routine to update config file until we got all remainning tokens configured in oracleParams
		go reloadConfigToFetchNewTokens(remainningFeeders, newFeeder, cc, f)

	}

	// initialize staker's validator list for eth-native-restaking
	nativeStakers := initializeNativeRestakingStakers(cc)
	for nativeToken, stakerInfos := range nativeStakers {
		f.ResetStakerValidatorsForAll(nativeToken, stakerInfos)
	}

	// subscribe newBlock to to trigger tx
	res, _ := exoclient.Subscriber(conf.Exocore.Ws.Addr, conf.Exocore.Ws.Endpoint)

	for r := range res {
		event := eventRes{}
		var feederIDs []string
		if len(r.Height) > 0 {
			event.height, _ = strconv.ParseUint(r.Height, 10, 64)
		}
		if len(r.Gas) > 0 {
			event.gas, _ = strconv.ParseInt(r.Gas, 10, 64)
		}
		if r.ParamsUpdate {
			oracleP, err = exoclient.GetParams(cc)
			for err != nil {
				// retry forever until be interrupted manually
				log.Println("Fail to get oracle params when params update detected, retrying...", err)
				oracleP, err = exoclient.GetParams(cc)
				time.Sleep(2 * time.Second)
			}
			// set newly added tokenfeeders in the running queue and reload the configure for them to run porperly
			if remainningFeeders = updateCurrentFeedingTokens(oracleP, oracleParamsFeedingTokens); len(remainningFeeders) > 0 {
				// reoload config for newly added token feeders
				go reloadConfigToFetchNewTokens(remainningFeeders, newFeeder, cc, f)
			}
		}

		if len(r.FeederIDs) > 0 {
			feederIDs = strings.Split(r.FeederIDs, "_")
		}

		// this is an event that tells native token stakers update, we use 'ETH' temporary since we currently support eth-native-restaking only
		if len(r.NativeETH) > 0 {
			// TODO: we only support eth-native-restaking for now
			if success := f.UpdateNativeTokenValidators(types.NativeTokenETH, r.NativeETH); !success {
				stakerInfos, err := exoclient.GetStakerInfos(cc, types.NativeTokenETHAssetID)
				for err != nil {
					log.Println("Fail to get stakerInfos, retrying...")
					stakerInfos, err = exoclient.GetStakerInfos(cc, types.NativeTokenETHAssetID)
					time.Sleep(2 * time.Second)
				}
				f.ResetStakerValidatorsForAll(types.NativeTokenETH, stakerInfos)
			}
		}

		select {
		case feeder := <-newFeeder:
			runningFeeders[feeder.params.feederID] = feeder
		default:
			for _, fInfo := range runningFeeders {
				triggerFeeders(r, fInfo, event, oracleP, feederIDs)
			}
		}
	}
}

// reloadConfigToFetchNewTokens reload config file for the remainning token feeders that are set in oracle params but not running properly for config missing
func reloadConfigToFetchNewTokens(remainningFeeders map[string]*feederInfo, newFeeder chan *feederInfo, cc *grpc.ClientConn, f *fetcher.Fetcher) {
	updateConfig.Lock()
	length := len(remainningFeeders)
	for length > 0 {
		conf := feedertypes.ReloadConfig()
		for tokenRemainning, fInfo := range remainningFeeders {
			fmt.Printf("loading config for for token %s ...\r\n", fInfo.params.tokenName)
			for _, token := range conf.Tokens {
				if strings.EqualFold(token, tokenRemainning) {
					delete(remainningFeeders, tokenRemainning)
					length--
					trigger := make(chan eventRes, 3)
					fInfo.updateCh = trigger
					// TODO: currently support chainlink only (index=0)
					f.AddTokenForSource(conf.Sources[0], tokenRemainning)
					// start a routine to update price for this feeder
					newFeeder <- fInfo
					go feedToken(fInfo, cc, f, conf)
					break
				}
			}
		}
		time.Sleep(10 * time.Second)
	}
	updateConfig.Unlock()
}

// updateCurrentFeedingTokens will update current running tokenFeeders based on the params change from upstream, and it will add the newly added tokenFeeders into the running queue and return that list which will be handled by invoker to set them properly as running tokenFeeders
func updateCurrentFeedingTokens(oracleP oracleTypes.Params, currentFeedingTokens map[string]struct{}) map[string]*feederInfo {
	remain := make(map[string]*feederInfo)
	for feederID, feeder := range oracleP.TokenFeeders {
		if feederID == 0 {
			// feederID=0 is reserved
			continue
		}
		tokenName := strings.ToLower(oracleP.Tokens[feeder.TokenID].Name + baseCurrency)
		if _, ok := currentFeedingTokens[tokenName]; ok {
			continue
		}
		decimal := oracleP.Tokens[feeder.TokenID].Decimal
		fInfo := &feederInfo{
			params: &feederParams{
				startRoundID: feeder.StartRoundID,
				startBlock:   feeder.StartBaseBlock,
				endBlock:     feeder.EndBlock,
				interval:     feeder.Interval,
				decimal:      decimal,
				tokenIDStr:   strconv.FormatInt(int64(feeder.TokenID), 10),
				feederID:     int64(feederID),
				tokenName:    tokenName,
			},
		}

		remain[tokenName] = fInfo
		currentFeedingTokens[tokenName] = struct{}{}
	}
	return remain
}

// feedToken will try to send create-price tx to update prices onto exocore chain when conditions reached including: tokenFeeder-running, inside-a-pricing-window, price-updated-since-previous-round
func feedToken(fInfo *feederInfo, cc *grpc.ClientConn, f *fetcher.Fetcher, conf feedertypes.Config) {
	pChan := make(chan *types.PriceInfo)
	prevPrice := ""
	prevDecimal := -1
	prevHeight := uint64(0)
	tokenID, _ := strconv.ParseUint(fInfo.params.tokenIDStr, 10, 64)

	startBlock := fInfo.params.startBlock
	endBlock := fInfo.params.endBlock
	interval := fInfo.params.interval
	decimal := int(fInfo.params.decimal)
	feederID := uint64(fInfo.params.feederID)

	if p, err := exoclient.GetLatestPrice(cc, tokenID); err == nil {
		prevPrice = p.Price
		prevDecimal = int(p.Decimal)
	}

	for t := range fInfo.updateCh {
		// update Params if changed, paramsUpdate will be notified to corresponding feeder, not all
		if params := t.params; params != nil {
			startBlock = params.startBlock
			endBlock = params.endBlock
			interval = params.interval
			decimal = int(params.decimal)
		}

		// update latest price if changed
		if len(t.price) > 0 {
			prevPrice = t.price
			prevDecimal = t.decimal
			prevHeight = t.txHeight
			// this is an tx event with height==0, so just don't submit any messages, tx will be triggered by newBlock event
			continue
		} else if t.priceUpdated && prevHeight < t.height {
			// this is a newblock event and this case is: newBlock event arrived before tx event, (interval>=2*maxNonce, so interval must > 1, so we skip one block is safe)
			// wait txEvent to update the price
			continue
		}
		// check feeder status to feed price
		log.Printf("Triggered, height:%d, feeder-parames:{feederID:%d, startBlock:%d, interval:%d, roundID:%d}:", t.height, feederID, fInfo.params.startBlock, fInfo.params.interval, fInfo.params.startRoundID)
		if t.height < startBlock {
			// tx event will have zero height, just don't submit price
			continue
		}
		if endBlock > 0 && t.height >= endBlock {
			// TODO: notify corresponding token fetcher
			return
		}
		delta := (t.height - startBlock) % interval
		roundID := (t.height-startBlock)/interval + fInfo.params.startRoundID
		if delta < 3 {
			//TODO: for v1 exocored, we do no restrictions on sources, so here we hardcode source information for nativetoken and normal token
			source := conf.Sources[0]
			if strings.EqualFold(fInfo.params.tokenName, types.NativeTokenETH) {
				fmt.Println("nstETH, use beaconchain instead of chainlink as source", t.height, feederID, fInfo.params.startRoundID)
				source = types.BeaconChain
			}
			// TODO: use source based on oracle-params
			// f.GetLatestPriceFromSourceToken(conf.Sources[0], fInfo.params.tokenName, pChan)
			f.GetLatestPriceFromSourceToken(source, fInfo.params.tokenName, pChan)
			p := <-pChan
			if p == nil {
				continue
			}
			// TODO: this price should be compared with the current price from oracle, not from source
			if prevDecimal > -1 && prevPrice == p.Price && prevDecimal == p.Decimal {
				// if prevPrice not changed between different rounds, we don't submit any messages and the oracle module will use the price from former round to update next round.
				log.Printf("price not changed, skip submitting price for roundID:%d, feederID:%d", roundID, feederID)
				continue
			}
			basedBlock := t.height - delta

			if !(fInfo.params.tokenName == types.NativeTokenETH) {
				if p.Decimal > decimal {
					p.Price = p.Price[:len(p.Price)-int(p.Decimal-decimal)]
					p.Decimal = decimal
				} else if p.Decimal < decimal {
					p.Price = p.Price + strings.Repeat("0", decimal-p.Decimal)
					p.Decimal = decimal
				}
			}
			log.Printf("submit price=%s decimal=%d of token=%s on height=%d for roundID:%d, feederID:%d", p.Price, p.Decimal, fInfo.params.tokenName, t.height, roundID, feederID)
			res := exoclient.SendTx(cc, feederID, basedBlock, p.Price, p.RoundID, p.Decimal, int32(delta)+1, t.gas)
			txResponse := res.GetTxResponse()
			if txResponse.Code == statusOk {
				log.Printf("sendTx successed, feederID:%d", feederID)
			} else {
				log.Printf("sendTx failed, feederID:%d, response:%v", feederID, txResponse)
			}
		}
	}
}

// initExocoreClient initializes exocore client which is responsible for interact with exocored including: query, tx, ws-subscribe
func initExocoreClient(conf feedertypes.Config, standalone bool) *grpc.ClientConn {
	confExocore := conf.Exocore
	confSender := conf.Sender
	privBase64 := ""

	// if mnemonic is not set from flag, then check config file to find if there is mnemonic configured
	if len(mnemonic) == 0 && len(confSender.Mnemonic) > 0 {
		mnemonic = confSender.Mnemonic
	}

	if len(mnemonic) == 0 {
		// load privatekey from local path
		file, err := os.Open(path.Join(confSender.Path, privFile))
		if err != nil {
			panic(fmt.Sprintf("fail to open consensuskey file, %s", err.Error()))
		}
		defer file.Close()
		var pvKey PrivValidatorKey
		if err := json.NewDecoder(file).Decode(&pvKey); err != nil {
			panic(fmt.Sprintf("fail to parse consensuskey file from json, %s", err.Error()))
		}
		privBase64 = pvKey.PrivKey.Value
	} else if !bip39.IsMnemonicValid(mnemonic) {
		panic("invalid mnemonic")
	}
	// Init consensus keys and related tx infos
	exoclient.Init(mnemonic, privBase64, confExocore.ChainID, standalone)
	return exoclient.CreateGrpcConn(confExocore.Rpc)
}

// triggerFeeders will trigger tokenFeeder based on arrival events to check and send create-price tx to update prices onto exocore chain
func triggerFeeders(r exoclient.ReCh, fInfo *feederInfo, event eventRes, oracleP oracleTypes.Params, feederIDsPriceUpdated []string) {
	eventCpy := event
	if r.ParamsUpdate {
		// check if this tokenFeeder's params has been changed
		if update := fInfo.params.update(oracleP); update {
			paramsCopy := *fInfo.params
			eventCpy.params = &paramsCopy
		}
	}
	for _, p := range r.Price {
		parsedPrice := strings.Split(p, "_")
		if fInfo.params.tokenIDStr == parsedPrice[0] {
			if fInfo.latestPrice != strings.Join(parsedPrice[1:], "_") {
				eventCpy.price = parsedPrice[2]
				decimal, _ := strconv.ParseInt(parsedPrice[3], 10, 32)
				eventCpy.decimal = int(decimal)
				eventCpy.txHeight, _ = strconv.ParseUint(r.TxHeight, 10, 64)
				eventCpy.roundID, _ = strconv.ParseUint(parsedPrice[1], 10, 64)
				fInfo.latestPrice = strings.Join(parsedPrice[1:], "_")
			}
			break
		}
	}

	for _, feederID := range feederIDsPriceUpdated {
		if feederID == strconv.FormatInt(fInfo.params.feederID, 10) {
			eventCpy.priceUpdated = true
		}
	}

	// notify corresponding feeder to update price
	fInfo.updateCh <- eventCpy
}

// initializeNativeRestkingStakers initialize stakers' validator list since we wrap validator set into one single staker for each native-restaking-asset
func initializeNativeRestakingStakers(cc *grpc.ClientConn) map[string][]*oracleTypes.StakerInfo {
	ret := make(map[string][]*oracleTypes.StakerInfo)
	for _, v := range types.NativeRestakings {
		stakerInfos, err := exoclient.GetStakerInfos(cc, types.AssetIDMap[v[1]])
		for err != nil {
			log.Println("Fail to get stakerInfos, retrying...")
			stakerInfos, err = exoclient.GetStakerInfos(cc, types.NativeTokenETHAssetID)
			time.Sleep(2 * time.Second)
		}
		ret[v[1]] = stakerInfos
	}
	return ret
}
