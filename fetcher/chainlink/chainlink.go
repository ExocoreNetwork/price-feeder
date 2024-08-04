package chainlink

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"path"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v2"

	aggregatorv3 "github.com/ExocoreNetwork/price-feeder/fetcher/chainlink/aggregatorv3"
	"github.com/ExocoreNetwork/price-feeder/fetcher/types"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// token->contract address
type config struct {
	URLs     map[string]string `yaml:"urls"`
	Tokens   map[string]string `yaml:"tokens"`
	TokenNet []string          `yaml:"tokenNet"`
}

var (
	clients = make(map[string]*ethclient.Client)
	// token-> proxycontract
	chainlinkProxy = make(map[string]*aggregatorv3.AggregatorV3Interface)
)

const (
	// envConf = ".env_chainlink.yaml"
	envConf = "oracle_env_chainlink.yaml"
)

func init() {
	types.InitFetchers[types.Chainlink] = Init
}

func Init(confPath string) error {
	yamlFile, err := os.Open(path.Join(confPath, envConf))
	if err != nil {
		return err
	}
	cfg := config{}
	if err = yaml.NewDecoder(yamlFile).Decode(&cfg); err != nil {
		return err
	}
	for network, url := range cfg.URLs {
		if len(url) == 0 {
			log.Fatal("rpcUrl is empty. check the .env file")
		}
		clients[network], err = ethclient.Dial(url)
		if err != nil {
			panic(err)
		}
	}
	for token, address := range cfg.Tokens {
		addrParsed := strings.Split(strings.TrimSpace(address), "_")
		if ok := isContractAddress(addrParsed[0], clients[addrParsed[1]]); !ok {
			panic(fmt.Sprintf("address %s is not a contract address\n", addrParsed[0]))
		}
		if chainlinkProxy[token], err = aggregatorv3.NewAggregatorV3Interface(common.HexToAddress(addrParsed[0]), clients[addrParsed[1]]); err != nil {
			panic(err)
		}
	}
	types.Fetchers[types.Chainlink] = Fetch
	return nil
}

// Start runs the background routine to fetch prices, we use tokenAddr as input instead of token name to avoid access the list every time which might including potential concurrency conflicts
func Fetch(token string) (*types.PriceInfo, error) {
	chainlinkPriceFeedProxy, ok := chainlinkProxy[token]
	if !ok {
		log.Printf("token %s not found\n", token)
		return nil, errors.New("token not found")
	}

	roundData, err := chainlinkPriceFeedProxy.LatestRoundData(&bind.CallOpts{})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	decimals, err := chainlinkPriceFeedProxy.Decimals(&bind.CallOpts{})
	if err != nil {
		log.Println(err)
		return nil, err
	}

	return &types.PriceInfo{
		Price:     roundData.Answer.String(),
		Decimal:   int(decimals),
		Timestamp: time.Now().String(),
		RoundID:   roundData.RoundId.String(),
	}, nil
}

func isContractAddress(addr string, client *ethclient.Client) bool {
	if len(addr) == 0 {
		log.Fatal("feedAddress is empty.")
	}

	// Ensure it is an Ethereum address: 0x followed by 40 hexadecimal characters.
	re := regexp.MustCompile("^0x[0-9a-fA-F]{40}$")
	if !re.MatchString(addr) {
		log.Fatalf("address %s non valid\n", addr)
	}

	// Ensure it is a contract address.
	address := common.HexToAddress(addr)
	bytecode, err := client.CodeAt(context.Background(), address, nil) // nil is latest block
	if err != nil {
		log.Fatal(err)
	}
	isContract := len(bytecode) > 0
	return isContract
}

func formatTime(timestamp *big.Int) time.Time {
	timestampInt64 := timestamp.Int64()
	if timestampInt64 == 0 {
		log.Fatalf("timestamp %v cannot be represented as int64", timestamp)
	}
	return time.Unix(timestampInt64, 0)
}

func divideBigInt(num1 *big.Int, num2 *big.Int) *big.Float {
	if num2.BitLen() == 0 {
		log.Fatal("cannot divide by zero.")
	}
	num1BigFloat := new(big.Float).SetInt(num1)
	num2BigFloat := new(big.Float).SetInt(num2)
	result := new(big.Float).Quo(num1BigFloat, num2BigFloat)
	return result
}
