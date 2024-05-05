/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	"strconv"
	"time"

	oracletypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	"github.com/ExocoreNetwork/price-feeder/exoclient"
	"github.com/ExocoreNetwork/price-feeder/fetcher"
	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
	"github.com/spf13/cobra"
)

var oracleP oracletypes.Params

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
		cc := exoclient.CreateGrpcConn()
		defer cc.Close()

		// subscribe newBlock to to trigger tx
		res := exoclient.Subscriber("ws://127.0.0.1:26657", "/websocket")
		skip := false
		for r := range res {
			h, _ := strconv.ParseInt(r.Height, 10, 64)
			i := h % 10
			if i < 3 && !skip {
				tmpI := h - i
				f.GetLatestPriceFromSourceToken("chainlink", "ETHUSDT", pChan)
				p := <-pChan
				exoclient.SendTx(cc, 1, uint64(tmpI), p.Price, p.RoundID, p.Decimal)
				skip = true
			} else if i >= 3 {
				skip = false
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
