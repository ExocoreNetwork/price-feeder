/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/ExocoreNetwork/price-feeder/exoclient"
	"github.com/ExocoreNetwork/price-feeder/fetcher"
	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
	"github.com/cosmos/go-bip39"
	"github.com/spf13/cobra"
)

const (
	statusOk     = 0
	privFile     = "priv_validator_key.json"
	baseCurrency = "USDT"
)

// var mnemonic = ""
var mnemonic string

type PrivValidatorKey struct {
	Address string `json:"address"`
	PrivKey struct {
		Value string `json:"value"`
	} `json:"priv_key"`
}

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	Run: func(cmd *cobra.Command, args []string) {
		// start fetcher to get prices from chainlink
		f := fetcher.Init(Conf.Sources, Conf.Tokens)
		// start all supported sources and tokens
		_ = f.StartAll()
		pChan := make(chan *types.PriceInfo)
		time.Sleep(5 * time.Second)

		confExocore := Conf.Exocore
		confSender := Conf.Sender
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
		exoclient.Init(mnemonic, privBase64, confExocore.ChainID)

		cc := exoclient.CreateGrpcConn(confExocore.Rpc)
		defer cc.Close()
		oracleP, err := exoclient.GetParams(cc)
		if err != nil {
			log.Fatalf("Fail to get oracle params:%s", err)
		}

		liveFeeders := []chan struct {
			h uint64
			g int64
		}{}

		// check all live feeders and set seperate routine to udpate prices
		for feederID, feeder := range oracleP.TokenFeeders {
			if feederID == 0 {
				// feederID=0 is reserved
				continue
			}
			startBlock := feeder.StartBaseBlock
			startRoundID := feeder.StartRoundID
			interval := feeder.Interval
			for _, token := range Conf.Tokens {
				if token == oracleP.Tokens[feeder.TokenID].Name+baseCurrency {
					trigger := make(chan struct {
						h uint64
						g int64
					}, 3)
					liveFeeders = append(liveFeeders, trigger)
					go func(feederID, startBlock, interval, roundID uint64) {
						prevPrice := ""
						for t := range trigger {
							log.Printf("debug-feeder. triggered, feeder-parames:{feederID:%d, startBlock:%d, interval:%d, roundID:%d}", feederID, startBlock, interval, roundID)
							if t.h < startBlock {
								continue
							}
							delta := (t.h - startBlock) % interval
							roundID := (t.h-startBlock)/interval + startRoundID
							if delta < 3 {
								// TODO: use source based on oracle-params
								f.GetLatestPriceFromSourceToken(Conf.Sources[0], token, pChan)
								p := <-pChan
								// TODO: this price should be compared with the current price from oracle, not from source
								if prevPrice == p.Price {
									// if prevPrice not changed between different rounds, we don't submit any messages and the oracle module will use the price from former round to update next round.
									log.Println("price not changed, skip submitting price for roundID:", roundID)
									continue
								}
								prevPrice = p.Price
								basedBlock := t.h - delta
								log.Printf("submit price=%s of token=%s on height=%d for roundID:%d", p.Price, token, t.h, roundID)
								res := exoclient.SendTx(cc, feederID, basedBlock, p.Price, p.RoundID, p.Decimal, int32(delta)+1, t.g)
								txResponse := res.GetTxResponse()
								if txResponse.Code == statusOk {
									log.Println("sendTx successed")
								} else {
									prevPrice = ""
									log.Printf("sendTx failed, response:%v", txResponse)
								}
							}
						}
					}(uint64(feederID), startBlock, interval, startRoundID)
					break
				}
			}
		}

		// subscribe newBlock to to trigger tx
		res, _ := exoclient.Subscriber(confExocore.Ws.Addr, confExocore.Ws.Endpoint)
		for r := range res {
			height, _ := strconv.ParseInt(r.Height, 10, 64)
			gasPrice, _ := strconv.ParseInt(r.Gas, 10, 64)
			log.Println("debug-newblock-height:", height)
			for _, t := range liveFeeders {
				t <- struct {
					h uint64
					g int64
				}{h: uint64(height), g: gasPrice}
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(startCmd)
	startCmd.Flags().StringVarP(&mnemonic, "mnemonic", "m", "", "mnemonic of consensus key")
}
