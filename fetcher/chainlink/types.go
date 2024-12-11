package chainlink

import (
	"fmt"
	"os"
	"path"
	"strings"
	"sync"

	aggregatorv3 "github.com/ExocoreNetwork/price-feeder/fetcher/chainlink/aggregatorv3"
	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"gopkg.in/yaml.v2"
)

type Config struct {
	URLs     map[string]string `yaml:"urls"`
	Tokens   map[string]string `yaml:"tokens"`
	TokenNet []string          `yaml:"tokenNet"`
}

type proxy struct {
	lock        sync.RWMutex
	aggregators map[string]*aggregatorv3.AggregatorV3Interface
}

const envConf = "oracle_env_chainlink.yaml"

func newProxy() *proxy {
	return &proxy{
		aggregators: make(map[string]*aggregatorv3.AggregatorV3Interface),
	}
}

func (p *proxy) add(tokens map[string]string) error {
	p.lock.Lock()
	defer p.lock.Unlock()
	for token, address := range tokens {
		addrParsed := strings.Split(strings.TrimSpace(address), "_")
		if ok := isContractAddress(addrParsed[0], clients[addrParsed[1]]); !ok {
			logger.Error("address tried to be added as chainlink proxy is not a contract address", "address", addrParsed[0], "chain", addrParsed[1])
			return fmt.Errorf("address %s is not a contract address on chain:%s\n", addrParsed[0], addrParsed[1])
		}
		var err error
		if p.aggregators[strings.ToLower(token)], err = aggregatorv3.NewAggregatorV3Interface(common.HexToAddress(addrParsed[0]), clients[addrParsed[1]]); err != nil {
			logger.Error("failed to newAggregator of chainlink", "address", common.HexToAddress(addrParsed[0]), "chain", addrParsed[1], "error", err)
			return err
		}
	}
	return nil
}

func (p *proxy) get(token string) (*aggregatorv3.AggregatorV3Interface, bool) {
	p.lock.RLock()
	aggregator, ok := p.aggregators[token]
	p.lock.RUnlock()
	return aggregator, ok
}

var (
	logger     feedertypes.LoggerInf
	configPath string
)

func init() {
	types.InitFetchers[types.Chainlink] = initChainlink
}

func initChainlink(confPath string) error {
	if logger = feedertypes.GetLogger("fetcher_chainlink"); logger == nil {
		return feedertypes.ErrInitFail.Wrap("logger is not initialized")
	}
	configPath = confPath
	cfg, err := parseConfig(configPath)
	if err != nil {
		logger.Error("failed to parse config file", "path", configPath, "error", err)
		return feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to parse config file, path;%s, error:%s", configPath, err))
	}
	for network, url := range cfg.URLs {
		if len(url) == 0 {
			logger.Error("rpcURL is empty. check the oracle_env_chainlink.yaml")
			return feedertypes.ErrInitFail.Wrap("rpcURL from config is empty")
		}
		clients[network], err = ethclient.Dial(url)
		if err != nil {
			logger.Error("failed to initialize ethClient", "url", url, "error", err)
			return feedertypes.ErrInitFail.Wrap(fmt.Sprintf("fail to initialize ethClient, url:%s, erro:%s", url, err))
		}
	}

	if err = chainlinkProxy.add(cfg.Tokens); err != nil {
		logger.Error("failed to add chainlinkPriceFeedProxy", "error", err, "tokens", cfg.Tokens)
		return feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to add chainlinkPriceFeedProxy, error:%v", err))
	}

	types.Fetchers[types.Chainlink] = Fetch
	return nil
}

func parseConfig(confPath string) (Config, error) {
	yamlFile, err := os.Open(path.Join(confPath, envConf))
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err = yaml.NewDecoder(yamlFile).Decode(&cfg); err != nil {
		return Config{}, err
	}
	return cfg, nil
}
