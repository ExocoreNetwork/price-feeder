package cmd

import (
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
)

var debugCmd = &cobra.Command{
	Use:   "debug",
	Short: "",
	Long:  "",
	Run: func(cmd *cobra.Command, args []string) {
		logger := feedertypes.NewLogger(zapcore.DebugLevel)
		DebugPriceFeeder(conf, logger, mnemonic, sourcesPath)
	},
}
