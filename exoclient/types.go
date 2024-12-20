package exoclient

import (
	cryptoed25519 "crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path"

	"github.com/ExocoreNetwork/exocore/app"
	cmdcfg "github.com/ExocoreNetwork/exocore/cmd/config"
	oracleTypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	oracletypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/go-bip39"
	"github.com/evmos/evmos/v16/encoding"
)

type exoClientInf interface {
	// Query
	GetParams() (oracletypes.Params, error)
	GetLatestPrice(tokenID uint64) (oracletypes.PriceTimeRound, error)
	GetStakerInfos(assetID string) ([]*oracleTypes.StakerInfo, error)
	GetStakerInfo(assetID, stakerAddr string) ([]*oracleTypes.StakerInfo, error)

	// Tx
	SendTx(feederID uint64, baseBlock uint64, price, roundID string, decimal int, nonce int32) (*sdktx.BroadcastTxResponse, error)

	// Ws subscriber
	Subscribe()
}

type EventType int

type EventRes struct {
	Height       string
	Gas          string
	ParamsUpdate bool
	Price        []string
	FeederIDs    string
	TxHeight     string
	NativeETH    string
	Type         EventType
}

type SubscribeResult struct {
	Result struct {
		Query string `json:"query"`
		Data  struct {
			Value struct {
				TxResult struct {
					Height string `json:"height"`
				} `json:"TxResult"`
				Block struct {
					Header struct {
						Height string `json:"height"`
					} `json:"header"`
				} `json:"block"`
			} `json:"value"`
		} `json:"data"`
		Events struct {
			Fee               []string `json:"fee_market.base_fee"`
			ParamsUpdate      []string `json:"create_price.params_update"`
			FinalPrice        []string `json:"create_price.final_price"`
			PriceUpdate       []string `json:"create_price.price_update"`
			FeederID          []string `json:"create_price.feeder_id"`
			FeederIDs         []string `json:"create_price.feeder_ids"`
			NativeTokenUpdate []string `json:"create_price.native_token_update"`
			NativeTokenChange []string `json:"create_price.native_token_change"`
		} `json:"events"`
	} `json:"result"`
}

const (
	// current version of 'Oracle' only support id=1(chainlink) as valid source
	Chainlink uint64 = 1
	denom            = "hua"
	layout           = "2006-01-02 15:04:05"
)

const (
	NewBlock EventType = iota + 1
	UpdatePrice
	UpdateNST
)

var (
	logger feedertypes.LoggerInf

	blockMaxGas uint64

	defaultExoClient *exoClient
)

// Init intialize the exoclient with configuration including consensuskey info, chainID
// func Init(conf feedertypes.Config, mnemonic, privFile string, standalone bool) (*grpc.ClientConn, func(), error) {
func Init(conf feedertypes.Config, mnemonic, privFile string, standalone bool) error {
	if logger = feedertypes.GetLogger("exoclient"); logger == nil {
		panic("logger is not initialized")
	}

	// set prefixs to exocore when start as standlone mode
	if standalone {
		config := sdk.GetConfig()
		cmdcfg.SetBech32Prefixes(config)
	}

	confExocore := conf.Exocore
	confSender := conf.Sender
	privBase64 := ""

	// if mnemonic is not set from flag, then check config file to find if there is mnemonic configured
	if len(mnemonic) == 0 && len(confSender.Mnemonic) > 0 {
		logger.Info("set mnemonic from config", "mnemonic", confSender.Mnemonic)
		mnemonic = confSender.Mnemonic
	}

	if len(mnemonic) == 0 {
		// load privatekey from local path
		file, err := os.Open(path.Join(confSender.Path, privFile))
		if err != nil {
			// return feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to open consensuskey file, %v", err))
			return feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to open consensuskey file, path:%s, error:%v", privFile, err))
		}
		defer file.Close()
		var privKey feedertypes.PrivValidatorKey
		if err := json.NewDecoder(file).Decode(&privKey); err != nil {
			return feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to parse consensuskey from json file, file path:%s,  error:%v", privFile, err))
		}
		logger.Info("load privatekey from local file", "path", privFile)
		privBase64 = privKey.PrivKey.Value
	} else if !bip39.IsMnemonicValid(mnemonic) {
		return feedertypes.ErrInitFail.Wrap(fmt.Sprintf("invalid mnemonic:%s", mnemonic))
	}
	var privKey cryptotypes.PrivKey
	if len(mnemonic) > 0 {
		privKey = ed25519.GenPrivKeyFromSecret([]byte(mnemonic))
	} else {
		privBytes, err := base64.StdEncoding.DecodeString(privBase64)
		if err != nil {
			return feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to parse privatekey from base64_string:%s, error:%v", privBase64, err))
		}
		//nolint:all
		privKey = &ed25519.PrivKey{
			Key: cryptoed25519.PrivateKey(privBytes),
		}
	}

	encCfg := encoding.MakeConfig(app.ModuleBasics)

	if len(confExocore.ChainID) == 0 {
		return feedertypes.ErrInitFail.Wrap(fmt.Sprintf("ChainID must be specified in config"))
	}

	var err error
	if defaultExoClient, err = NewExoClient(logger, confExocore.Rpc, confExocore.Ws, privKey, encCfg, confExocore.ChainID); err != nil {
		return feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to NewExoClient, privKey:%v, chainID:%s, error:%v", privKey, confExocore.ChainID, err))
	}

	return nil
}
