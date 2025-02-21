package external

import (
	"github.com/imua-xyz/price-feeder/cmd"
	feedertypes "github.com/imua-xyz/price-feeder/types"
)

// LoadConf set conf from invoker instead of rootCmd
func StartPriceFeeder(cfgFile, mnemonic, sourcesPath string, logger feedertypes.LoggerInf) bool {
	if len(cfgFile) == 0 {
		return false
	}
	conf, err := feedertypes.InitConfig(cfgFile)
	if err != nil {
		logger.Error("Error loading config file: %s", err)
		return false
	}

	// Start price feeder
	cmd.RunPriceFeeder(conf, logger, mnemonic, sourcesPath, false)

	return true
}
