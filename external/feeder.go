package external

import (
	"fmt"
	"os"

	"github.com/ExocoreNetwork/price-feeder/cmd"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/spf13/viper"
)

var conf feedertypes.Config

// LoadConf set conf from invoker instead of rootCmd
func StartPriceFeeder(cfgFile, mnemonic, sourcesPath string) bool {
	if len(cfgFile) == 0 {
		return false
	}
	v := viper.New()
	// Use config file from the flag.
	v.SetConfigFile(cfgFile)

	// Search config in home directory with name ".price-feeder" (without extension).
	v.SetConfigType("yaml")

	// If a config file is found, read it in.
	if err := v.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", v.ConfigFileUsed())
	}

	if err := v.Unmarshal(&conf); err != nil {
		panic(err)
	}

	// Start price feeder
	cmd.RunPriceFeeder(conf, mnemonic, sourcesPath, false)

	return true
}
