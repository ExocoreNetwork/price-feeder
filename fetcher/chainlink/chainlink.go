package chainlink

import (
	"context"
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

var rpcURL string
var feedAddress map[string]string
var client *ethclient.Client

const source = "chainlink"

func init() {
	// Read the .env file
	err := godotenv.Load(".env_" + source)
	if err != nil {
		log.Println(err)
		panic(err)
	}

	// Fetch the rpc_url.
	rpcURL = os.Getenv("RPC_URL")
	if len(rpcURL) == 0 {
		log.Fatal("rpcUrl is empty. check the .env file")
	}

	// Initialize client instance using the rpcUrl.
	client, err = ethclient.Dial(rpcURL)
	if err != nil {
		log.Println(err)
		panic(err)
	}

	tokens := os.Getenv("TOKEN_ADDRESSES")
	tokenList := strings.Split(tokens, ",")
	feedAddress = make(map[string]string)
	for _, token := range tokenList {
		tokenParsed := strings.Split(strings.TrimSpace(token), "_")
		feedAddress[tokenParsed[0]] = tokenParsed[1]
		if ok := isContractAddress(tokenParsed[1], client); !ok {
			panic(fmt.Sprintf("address %s is not a contract address\n", feedAddress))
		}
		feedAddress[tokenParsed[0]] = tokenParsed[1]
	}
}

func updateConfig() {}

// not concurrent safe
func GetTokenAddress(token string) string {
	return feedAddress[token]
}

// Start runs the background routine to fetch prices, we use tokenAddr as input instead of token name to avoid access the list every time which might including potential concurrency conflicts
func FetchWithContractAddress(tokenAddr string) (*types.PriceInfo, error) {
	chainlinkPriceFeedProxyAddress := common.HexToAddress(tokenAddr)
	chainlinkPriceFeedProxy, err := aggregatorv3.NewAggregatorV3Interface(chainlinkPriceFeedProxyAddress, client)
	if err != nil {
		log.Println(err)
		return nil, err
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
