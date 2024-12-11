package external

import (
	"github.com/ExocoreNetwork/price-feeder/cmd"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
)

var conf feedertypes.Config

// LoadConf set conf from invoker instead of rootCmd
func StartPriceFeeder(cfgFile, mnemonic, sourcesPath string, logger feedertypes.LoggerInf) bool {
	if len(cfgFile) == 0 {
		return false
	}
	feedertypes.ConfigFile = cfgFile
	feedertypes.SourcesConfigPath = sourcesPath
	conf := feedertypes.InitConfig(cfgFile)

	// Start price feeder
	cmd.RunPriceFeeder(conf, logger, mnemonic, sourcesPath, false)

	return true
}
