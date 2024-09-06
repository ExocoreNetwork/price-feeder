package external

import (
	"github.com/ExocoreNetwork/price-feeder/cmd"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
)

var conf feedertypes.Config

// LoadConf set conf from invoker instead of rootCmd
func StartPriceFeeder(cfgFile, mnemonic, sourcesPath string) bool {
	if len(cfgFile) == 0 {
		return false
	}
	feedertypes.ConfigFile = cfgFile
	feedertypes.SourcesConfigPath = sourcesPath
	conf := feedertypes.ReadConfig(cfgFile)

	// Start price feeder
	cmd.RunPriceFeeder(conf, mnemonic, sourcesPath, false)

	return true
}
