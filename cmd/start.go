/*
Copyright © 2024 NAME HERE <EMAIL ADDRESS>
*/
package cmd

import (
	feedertypes "github.com/imua-xyz/price-feeder/types"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
)

const privFile = "priv_validator_key.json"

var mnemonic string

// startCmd represents the start command
var startCmd = &cobra.Command{
	Use:   "start",
	Short: "A brief description of your command",
	Long: `A longer description that spans multiple lines and likely contains examples
and usage of using your command. For example:

Cobra is a CLI library for Go that empowers applications.
This application is a tool to generate the needed files
to quickly create a Cobra application.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		logger := feedertypes.NewLogger(zapcore.InfoLevel)
		// start fetcher to get prices from chainlink
		RunPriceFeeder(feederConfig, logger, mnemonic, sourcesPath, true)
		return nil
	},
}
