package types

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
