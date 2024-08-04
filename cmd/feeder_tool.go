package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/ExocoreNetwork/price-feeder/exoclient"
	"github.com/ExocoreNetwork/price-feeder/fetcher"
	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/cosmos/go-bip39"
)

const (
	statusOk     = 0
	privFile     = "priv_validator_key.json"
	baseCurrency = "USDT"
)

// RunPriceFeeder runs price feeder to fetching price and feed to exocorechain
func RunPriceFeeder(conf feedertypes.Config, mnemonic string, sourcesPath string, standalone bool) {
	runningFeeders := make(map[int64]*feederInfo)
	// start fetcher to get prices from chainlink
	f := fetcher.Init(conf.Sources, conf.Tokens, sourcesPath)
	// start all supported sources and tokens
	_ = f.StartAll()
	time.Sleep(5 * time.Second)

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

	cc := exoclient.CreateGrpcConn(confExocore.Rpc)
	defer cc.Close()
	oracleP, err := exoclient.GetParams(cc)
	for err != nil {
		// retry forever until be interrupted manually
		log.Println("Fail to get oracle params on star, retrying...", err)
		time.Sleep(2 * time.Second)
		oracleP, err = exoclient.GetParams(cc)
	}
	// check all live feeders and set seperate routine to udpate prices
	for feederID, feeder := range oracleP.TokenFeeders {
		if feederID == 0 {
			// feederID=0 is reserved
			continue
		}
		startBlock := feeder.StartBaseBlock
		startRoundID := feeder.StartRoundID
		interval := feeder.Interval
		// check if this feeder is supported by this price-feeder
		for _, token := range conf.Tokens {
			if strings.EqualFold(token, oracleP.Tokens[feeder.TokenID].Name+baseCurrency) {
				trigger := make(chan eventRes, 3)
				decimal := oracleP.Tokens[feeder.TokenID].Decimal
				runningFeeders[int64(feederID)] = &feederInfo{
					params: &feederParams{
						startBlock: feeder.StartBaseBlock,
						endBlock:   feeder.EndBlock,
						interval:   feeder.Interval,
						decimal:    decimal,
						tokenIDStr: strconv.FormatInt(int64(feeder.TokenID), 10),
						feederID:   int64(feederID),
					},
					updateCh: trigger,
				}
				// start a routine to update price for this feeder
				go func(feederID, startBlock, endBlock, interval, startRoundID uint64, decimal int, token string, triggerCh chan eventRes, tokenID uint64) {
					pChan := make(chan *types.PriceInfo)
					prevPrice := ""
					prevDecimal := -1
					prevHeight := uint64(0)
					// TODO: these lines should be active after exocore upgrade
					//					if p, err := exoclient.GetLatestPrice(cc, tokenID); err == nil {
					//						prevPrice = p.Price
					//						prevDecimal = int(p.Decimal)
					//					}

					for t := range triggerCh {
						// update Params if changed, paramsUpdate will be notified to corresponding feeder, not all
						if params := t.params; params != nil {
							startBlock = params.startBlock
							endBlock = params.endBlock
							interval = params.interval
							decimal = int(params.decimal)
						}

						// update latest price if changed
						// TODO: for restart price-feeder, this will cause lots of unacceptable messages to be sent, do initialization for these prev values
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
						log.Printf("debug-feeder. triggered, height:%d, feeder-parames:{feederID:%d, startBlock:%d, interval:%d, roundID:%d}:", t.height, feederID, startBlock, interval, startRoundID)
						if t.height < startBlock {
							// tx event will have zero height, just don't submit price
							continue
						}
						if endBlock > 0 && t.height >= endBlock {
							// TODO: notify corresponding token fetcher
							return
						}
						delta := (t.height - startBlock) % interval
						roundID := (t.height-startBlock)/interval + startRoundID
						if delta < 3 {
							// TODO: use source based on oracle-params
							f.GetLatestPriceFromSourceToken(conf.Sources[0], token, pChan)
							p := <-pChan
							// TODO: this price should be compared with the current price from oracle, not from source
							if prevDecimal > -1 && prevPrice == p.Price && prevDecimal == p.Decimal {
								// if prevPrice not changed between different rounds, we don't submit any messages and the oracle module will use the price from former round to update next round.
								log.Println("price not changed, skip submitting price for roundID:", roundID)
								continue
							}
							basedBlock := t.height - delta

							if p.Decimal > decimal {
								p.Price = p.Price[:len(p.Price)-int(p.Decimal-decimal)]
								p.Decimal = decimal
							} else if p.Decimal < decimal {
								p.Price = p.Price + strings.Repeat("0", decimal-p.Decimal)
								p.Decimal = decimal
							}
							log.Printf("submit price=%s decimal=%d of token=%s on height=%d for roundID:%d", p.Price, p.Decimal, token, t.height, roundID)
							res := exoclient.SendTx(cc, feederID, basedBlock, p.Price, p.RoundID, p.Decimal, int32(delta)+1, t.gas)
							txResponse := res.GetTxResponse()
							if txResponse.Code == statusOk {
								log.Println("sendTx successed")
							} else {
								log.Printf("sendTx failed, response:%v", txResponse)
							}
						}
					}
				}(uint64(feederID), startBlock, 0, interval, startRoundID, int(decimal), token, trigger, feeder.TokenID)
				break
			}
		}
	}
	// subscribe newBlock to to trigger tx
	res, _ := exoclient.Subscriber(confExocore.Ws.Addr, confExocore.Ws.Endpoint)
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
		}
		if len(r.FeederIDs) > 0 {
			feederIDs = strings.Split(r.FeederIDs, "_")
		}
		for _, fInfo := range runningFeeders {
			if r.ParamsUpdate {
				// check if this tokenFeeder's params has been changed
				//tokenFeeder := oracleP.TokenFeeders[feederID]
				if update := fInfo.params.update(oracleP); update {
					paramsCopy := *fInfo.params
					event.params = &paramsCopy
				}
			}
			for _, p := range r.Price {
				parsedPrice := strings.Split(p, "_")
				if fInfo.params.tokenIDStr == parsedPrice[0] {
					if fInfo.latestPrice != parsedPrice[2]+"_"+parsedPrice[3] {
						event.price = parsedPrice[2]
						decimal, _ := strconv.ParseInt(parsedPrice[3], 10, 32)
						event.decimal = int(decimal)
						event.txHeight, _ = strconv.ParseUint(r.TxHeight, 10, 64)
						fInfo.latestPrice = parsedPrice[2] + "_" + parsedPrice[3]
					}
					break
				}
			}

			for _, feedderID := range feederIDs {
				if feedderID == strconv.FormatInt(fInfo.params.feederID, 10) {
					event.priceUpdated = true
				}
			}

			// notify corresponding feeder to update price
			fInfo.updateCh <- event
		}

	}
}
