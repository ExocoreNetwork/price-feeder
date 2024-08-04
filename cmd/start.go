/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/spf13/cobra"
)

var (
	mnemonic string
	conf     feedertypes.Config
)

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
		RunPriceFeeder(conf, mnemonic, sourcesPath, true)
	},
}
