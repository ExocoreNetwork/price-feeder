package types

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
)

type Config struct {
	Sources []string `mapstructure:"sources"`
	Tokens  []string `mapstructure:"tokens"`
	Sender  struct {
		Mnemonic string `mapstructure:"mnemonic"`
		Path     string `mapstructure:"path"`
	} `mapstructure:"sender"`
	Exocore struct {
		ChainID string `mapstructure:"chainid"`
		AppName string `mapstructure:"appname"`
		Rpc     string `mapstructure:"rpc"`
		Ws      struct {
			Addr     string `mapstructure:"addr"`
			Endpoint string `mapstructure:"endpoint"`
		} `mapstructure:"ws"`
	} `mapstructure:"exocore"`
}

var (
	ConfigFile        string
	SourcesConfigPath string
)

func ReadConfig(cfgFile string) Config {
	v := viper.New()
	// Use config file from the flag.
	v.SetConfigFile(cfgFile)

	// Search config in home directory with name ".price-feeder" (without extension).
	v.SetConfigType("yaml")

	// If a config file is found, read it in.
	if err := v.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", v.ConfigFileUsed())
	}

	conf := &Config{}
	if err := v.Unmarshal(conf); err != nil {
		panic(err)
	}
	return *conf
}
