package chainlink

import (
	"context"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/joho/godotenv"

	aggregatorv3 "github.com/ExocoreNetwork/price-feeder/fetcher/chainlink/aggregatorv3"
	"github.com/ExocoreNetwork/price-feeder/fetcher/types"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// var rpcURLMainnet, rpcURLSepolia string

// token->contract address
// var feedAddress map[string]string
// var clientMainnet, clientSepolia *ethclient.Client

var (
	networks = []string{"MAINNET", "SEPOLIA"}
	// token-> proxycontract
	chainlinkProxy = make(map[string]*aggregatorv3.AggregatorV3Interface)
	tokenNetwork   = make(map[string]string)
	clients        = make(map[string]*ethclient.Client)
)

// type network string

const (
	envConf           = ".env_chainlink"
	envTokenAddresses = "TOKEN_ADDRESSES"
	envTokenNetwork   = "TOKEN_NET"
	envURLPrefix      = "RPC_URL_"
)

func init() {
	// Read the .env file
	// err := godotenv.Load(".env_" + source)
	err := godotenv.Load(envConf)
	if err != nil {
		log.Println(err)
		panic(err)
	}

	// Fetch the rpc_url.
	for _, net := range networks {
		netStr := os.Getenv(envURLPrefix + net)
		if len(netStr) == 0 {
			log.Fatal("rpcUrl is empty. check the .env file")
		}
		clients[net], err = ethclient.Dial(netStr)
		if err != nil {
			panic(err)
		}
	}

	tokenNetworks := strings.Split(os.Getenv(envTokenNetwork), ",")
	for _, tn := range tokenNetworks {
		tnParsed := strings.Split(strings.TrimSpace(tn), "_")
		tokenNetwork[tnParsed[0]] = tnParsed[1]
	}

	//	feedAddress = make(map[string]string)
	tokens := os.Getenv(envTokenAddresses)
	tokenList := strings.Split(tokens, ",")

	for _, token := range tokenList {
		tokenParsed := strings.Split(strings.TrimSpace(token), "_")
		net := tokenNetwork[tokenParsed[0]]

		if ok := isContractAddress(tokenParsed[1], clients[net]); !ok {
			panic(fmt.Sprintf("address %s is not a contract address\n", tokenParsed[1]))
		}
		if chainlinkProxy[tokenParsed[0]], err = aggregatorv3.NewAggregatorV3Interface(common.HexToAddress(tokenParsed[1]), clients[net]); err != nil {
			panic(err)
		}
	}
}

// func updateConfig() {}

// Start runs the background routine to fetch prices, we use tokenAddr as input instead of token name to avoid access the list every time which might including potential concurrency conflicts
// func FetchWithContractAddress(tokenAddr string) (*types.PriceInfo, error) {
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

	//description, err := chainlinkPriceFeedProxy.Description(&bind.CallOpts{})
	//if err != nil {
	//	log.Println(err)
	//	return nil, err
	//}

	//// Compute a big.int which is 10**decimals.
	//divisor := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)

	//log.Printf("%v Price feed address is  %v\n", description, chainlinkPriceFeedProxyAddress)
	//log.Printf("Round id is %v\n", roundData.RoundId)
	//log.Printf("Answer is %v\n", roundData.Answer)
	//log.Printf("Formatted answer is %v\n", divideBigInt(roundData.Answer, divisor))
	//log.Printf("Started at %v\n", formatTime(roundData.StartedAt))
	//log.Printf("Updated at %v\n", formatTime(roundData.UpdatedAt))
	//log.Printf("Answered in round %v\n", roundData.AnsweredInRound)
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
		fmt.Println("------")
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
