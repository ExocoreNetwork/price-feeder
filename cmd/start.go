/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
)

var (
	mnemonic string
	conf     feedertypes.Config
)

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	//	PreRun: func(cmd *cobra.Command, args []string) {
	//
	//	},
	Run: func(cmd *cobra.Command, args []string) {
		logger := feedertypes.NewLogger(zapcore.InfoLevel)
		// start fetcher to get prices from chainlink
		RunPriceFeeder(conf, logger, mnemonic, sourcesPath, true)
	},
}
