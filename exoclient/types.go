package exoclient

import (
	cryptoed25519 "crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path"

	"cosmossdk.io/simapp/params"
	"github.com/ExocoreNetwork/exocore/app"
	cmdcfg "github.com/ExocoreNetwork/exocore/cmd/config"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	"github.com/cosmos/go-bip39"
	"github.com/evmos/evmos/v16/encoding"
	"google.golang.org/grpc"
)

const (
	Chainlink uint64 = 1
	denom            = "hua"
	layout           = "2006-01-02 15:04:05"
)

var (
	defaultGasPrice = int64(7)

	logger feedertypes.LoggerInf

	blockMaxGas uint64
	chainID     string
	encCfg      params.EncodingConfig
	txCfg       client.TxConfig

	privKey cryptotypes.PrivKey
	pubKey  cryptotypes.PubKey
)

// Init intialize the exoclient with configuration including consensuskey info, chainID
func Init(conf feedertypes.Config, mnemonic, privFile string, standalone bool) (*grpc.ClientConn, error) {
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
		mnemonic = confSender.Mnemonic
	}

	if len(mnemonic) == 0 {
		// load privatekey from local path
		file, err := os.Open(path.Join(confSender.Path, privFile))
		if err != nil {
			logger.Error("failed to load privatekey from local path", "path", privFile, "error", err)
			return nil, feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to open consensuskey file, %v", err))
		}
		defer file.Close()
		var privKey feedertypes.PrivValidatorKey
		if err := json.NewDecoder(file).Decode(&privKey); err != nil {
			logger.Error("failed to parse consensuskey from json file", "error", err)
			return nil, feedertypes.ErrInitFail.Wrap(fmt.Sprintf("failed to parse consensuskey from json file %v", err))
		}
		privBase64 = privKey.PrivKey.Value
	} else if !bip39.IsMnemonicValid(mnemonic) {
		logger.Error("invalid mnemonic from config")
		return nil, feedertypes.ErrInitFail.Wrap("invalid mnemonic from config")
	}

	if len(mnemonic) > 0 {
		privKey = ed25519.GenPrivKeyFromSecret([]byte(mnemonic))
	} else {
		privBytes, err := base64.StdEncoding.DecodeString(privBase64)
		if err != nil {
			logger.Error("failed to parse privateKey form base64", "base64_string", privBase64)
			return nil, feedertypes.ErrInitFail.Wrap(err.Error())
		}
		//nolint:all
		privKey = &ed25519.PrivKey{
			Key: cryptoed25519.PrivateKey(privBytes),
		}
	}
	pubKey = privKey.PubKey()

	encCfg = encoding.MakeConfig(app.ModuleBasics)
	txCfg = encCfg.TxConfig

	if len(confExocore.ChainID) == 0 {
		logger.Error("chainID must be sepecified in config")
		return nil, feedertypes.ErrInitFail.Wrap("ChainID must be specified in config")
	}
	chainID = confExocore.ChainID

	conn, err := CreateGrpcConn(confExocore.Rpc)
	if err != nil {
		logger.Error("failed to crete grpc connect when initialize exoclient")
		return conn, feedertypes.ErrInitFail.Wrap(err.Error())
	}
	return conn, nil
}
