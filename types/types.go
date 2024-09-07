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
	v                 *viper.Viper
)

// InitConfig will only read path cfgFile once, and for reload after InitConfig, should use ReloadConfig
func InitConfig(cfgFile string) Config {
	if v == nil {
		v = viper.New()
		v.SetConfigFile(cfgFile)
		v.SetConfigType("yaml")
	}

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

// ReloadConfig will reload config file with path set by InitConfig
func ReloadConfig() Config {

	// If a config file is found, read it in.
	if err := v.ReadInConfig(); err == nil {
		fmt.Fprintln(os.Stderr, "Using config file:", v.ConfigFileUsed())
	}

	conf := &Config{}
	if err := v.Unmarshal(conf); err != nil {
		fmt.Fprintln(os.Stderr, "parse config file failed:", v.ConfigFileUsed())
	}
	return *conf
}
