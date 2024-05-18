/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"log"
	"strconv"
	"time"

	"github.com/ExocoreNetwork/price-feeder/exoclient"
	"github.com/ExocoreNetwork/price-feeder/fetcher"
	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
	"github.com/spf13/cobra"
)

const statusOk = 0

// var oracleP oracletypes.Params

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
		_ = f.StartAll()
		pChan := make(chan *types.PriceInfo)
		time.Sleep(5 * time.Second)

		confExocore := Conf.Exocore
		exoclient.Init(confExocore.Keypath, confExocore.ChainID, confExocore.AppName, confExocore.Sender)

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
		for feederID, feeder := range oracleP.TokenFeeders {
			if feederID == 0 {
				// feederID=0 is reserved
				continue
			}
			startBlock := feeder.StartBaseBlock
			startRoundID := feeder.StartRoundID
			interval := feeder.Interval
			for _, token := range Conf.Tokens {
				if token == oracleP.Tokens[feeder.TokenID].Name+"USDT" {
					go func(feederID, startBlock, interval, roundID uint64) {
						trigger := make(chan struct {
							h uint64
							g int64
						}, 3)
						liveFeeders = append(liveFeeders, trigger)
						prevPrice := ""
						for t := range trigger {
							if t.h < startBlock {
								continue
							}
							delta := (t.h - startBlock) % interval
							roundID := (t.h-startBlock)/interval + startRoundID
							if delta < 3 {
								f.GetLatestPriceFromSourceToken(Conf.Sources[0], Conf.Tokens[0], pChan)
								p := <-pChan
								if prevPrice == p.Price {
									// if prevPrice not changed between different rounds, we don't submit any messages and the oracle module will use the price from former round to update next round.
									log.Println("price not changed, skip submitting price for roundID:", roundID)
									continue
								}
								prevPrice = p.Price
								basedBlock := t.h - delta
								//f.GetLatestPriceFromSourceToken(Conf.Sources[0], Conf.Tokens[0], pChan)
								log.Printf("submit price=%s of token=%s on height=%d for roundID:%d", p.Price, Conf.Tokens[0], t.h, roundID)
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
		res := exoclient.Subscriber(confExocore.Ws.Addr, confExocore.Ws.Endpoint)
		//		skip := false
		for r := range res {
			height, _ := strconv.ParseInt(r.Height, 10, 64)
			gasPrice, _ := strconv.ParseInt(r.Gas, 10, 64)
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
	// Here you will define your flags and configuration settings.

	// Cobra supports Persistent Flags which will work for this command
	// and all subcommands, e.g.:
	// startCmd.PersistentFlags().String("foo", "", "A help for foo")

	// Cobra supports local flags which will only run when this command
	// is called directly, e.g.:
	//startCmd.Flags().StringVarP(&cfgPath, "cfgpath", "c", "", "configpath")
	//startCmd.MarkFlagRequired("cfgpath")
}
