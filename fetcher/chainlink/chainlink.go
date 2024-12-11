package chainlink

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ExocoreNetwork/price-feeder/fetcher/types"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	clients = make(map[string]*ethclient.Client)
	// token-> proxycontract
	chainlinkProxy = newProxy()
)

func Fetch(token string) (*types.PriceInfo, error) {
	chainlinkPriceFeedProxy, ok := chainlinkProxy.get(token)
	if !ok {
		// reload config to add new token
		// TODO: this is not concurrent safe, if multiple tokens are fetching conconrrently, the access to chainlinkProxy sould be synced
		logger.Error("chainlinkPriceFeedProxy not found, try to reload form chainlink config file and skip this round of fetching price", "token", token)
		go func() {
			// TODO: limit maximum reloading simultaneously
			err := errors.New("start reload")
			success := false
			var cfg Config
			for err != nil {
				if cfg, err = parseConfig(configPath); err != nil {
					logger.Error("failed to parse config file of source chalink", "error", err, "config_path", configPath)
					time.Sleep(10 * time.Second)
					continue
				}
				for tName, address := range cfg.Tokens {
					if token == strings.ToLower(tName) {
						if err = chainlinkProxy.add(map[string]string{token: address}); err != nil {
							logger.Error("failed to add chainlinkPriceFeedProxy, wait for 10 seconds and try again to reload chalink config file", "token", token, "error", err, "config_path", configPath)
							time.Sleep(10 * time.Second)
						} else {
							success = true
							logger.Info("scuccessed to add new chainlinkPriceFeedProxy, it will be active for next round fetching price", "token", token)
						}
						break
					}
				}
			}
			if !success {
				logger.Error("failed to find info for chainlinkPriceFeedProxy in chainlink config file, please update that file and it will be reloaded on next round", "token", token, "config_path", configPath)
			}
		}()
		return nil, fmt.Errorf("there's no active chainlinkPriceFeedProxy for token:%s", token)
	}

	roundData, err := chainlinkPriceFeedProxy.LatestRoundData(&bind.CallOpts{})
	if err != nil {
		logger.Error("failed to get LatestRoundData from chainlink", "token", token, "error", err)
		return nil, err
	}

	decimals, err := chainlinkPriceFeedProxy.Decimals(&bind.CallOpts{})
	if err != nil {
		logger.Error("failed to get decimal from chainlinkPriceFeedProxy", "token", token, "error", err)
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
		logger.Error("contract address is empty")
		return false
	}

	// Ensure it is an Ethereum address: 0x followed by 40 hexadecimal characters.
	re := regexp.MustCompile("^0x[0-9a-fA-F]{40}$")
	if !re.MatchString(addr) {
		logger.Error(" contract address is not valid", "address", addr)
		return false
	}

	// Ensure it is a contract address.
	address := common.HexToAddress(addr)
	bytecode, err := client.CodeAt(context.Background(), address, nil)
	if err != nil {
		logger.Error("failed to get code at contract address", "address", address, "error", err)
		return false
	}
	return len(bytecode) > 0
}

// func formatTime(timestamp *big.Int) time.Time {
// 	timestampInt64 := timestamp.Int64()
// 	if timestampInt64 == 0 {
// 		log.Fatalf("timestamp %v cannot be represented as int64", timestamp)
// 	}
// 	return time.Unix(timestampInt64, 0)
// }
//
// func divideBigInt(num1 *big.Int, num2 *big.Int) *big.Float {
// 	if num2.BitLen() == 0 {
// 		log.Fatal("cannot divide by zero.")
// 	}
// 	num1BigFloat := new(big.Float).SetInt(num1)
// 	num2BigFloat := new(big.Float).SetInt(num2)
// 	result := new(big.Float).Quo(num1BigFloat, num2BigFloat)
// 	return result
// }
