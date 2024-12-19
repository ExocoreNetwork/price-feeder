package chainlink

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func (s *source) fetch(token string) (*types.PriceInfo, error) {
	chainlinkPriceFeedProxy, ok := s.chainlinkProxy.get(token)
	if !ok {
		return nil, feedertypes.ErrSrouceTokenNotConfigured.Wrap(fmt.Sprintf("failed to get chainlinkProxy for token:%s for not set", token))
	}

	roundData, err := chainlinkPriceFeedProxy.LatestRoundData(&bind.CallOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to get LatestRoundData of token:%s from chainlink, error:%w", token, err)
	}

	decimals, err := chainlinkPriceFeedProxy.Decimals(&bind.CallOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to get decimals, error:%w", err)
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

func (s *source) reload(cfgPath string, token string) error {
	cfg, err := parseConfig(cfgPath)
	if err != nil {
		return errors.New("failed to path config file")
	}
	// add new network from config file
	for network, url := range cfg.URLs {
		network = strings.ToLower(network)
		if err := s.chainlinkProxy.addClient(network, url); err != nil {
			return fmt.Errorf("failed to add ethClient for network:%s with url:%s, error:%w", network, url, err)
		}
	}
	// add proxy for new token matches the required token if found
	for tName, tContract := range cfg.Tokens {
		tName = strings.ToLower(tName)
		if strings.EqualFold(tName, token) {
			s.chainlinkProxy.addToken(map[string]string{tName: tContract})
			return nil
		}
	}
	return errors.New("token not found in reloaded config filed")
}
