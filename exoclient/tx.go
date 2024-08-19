package exoclient

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/ExocoreNetwork/exocore/app"
	oracleTypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	"github.com/evmos/evmos/v14/encoding"

	cryptoed25519 "crypto/ed25519"

	"github.com/cosmos/cosmos-sdk/crypto/keys/ed25519"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"

	"github.com/cosmos/cosmos-sdk/client"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	"cosmossdk.io/simapp/params"
	cmdcfg "github.com/ExocoreNetwork/exocore/cmd/config"
	"github.com/cosmos/cosmos-sdk/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"

	"google.golang.org/grpc"
)

const (
	Chainlink      uint64 = 1
	denom                 = "aexo"
	layout                = "2006-01-02 15:04:05"
	defaultChainID        = "exocoretestnet_233-1"
)

var (
	chainID         string
	encCfg          params.EncodingConfig
	txCfg           client.TxConfig
	defaultGasPrice int64
	blockMaxGas     uint64

	privKey cryptotypes.PrivKey
	pubKey  cryptotypes.PubKey
)

// Init intialize the exoclient with configuration including consensuskey info, chainID
func Init(mnemonic, privBase64, cID string, standalone bool) {
	if standalone {
		config := sdk.GetConfig()
		cmdcfg.SetBech32Prefixes(config)
	}
	encCfg = encoding.MakeConfig(app.ModuleBasics)
	txCfg = encCfg.TxConfig

	if len(mnemonic) > 0 {
		privKey = ed25519.GenPrivKeyFromSecret([]byte(mnemonic))
		pubKey = privKey.PubKey()
	} else {
		privBytes, _ := base64.StdEncoding.DecodeString(privBase64)
		privKey = &ed25519.PrivKey{
			Key: cryptoed25519.PrivateKey(privBytes),
		}
		pubKey = privKey.PubKey()
	}
	if len(cID) == 0 {
		chainID = defaultChainID
	}
	chainID = cID
	defaultGasPrice = int64(7)
	// TODO: set from exocore's params

	blockMaxGas = 0
}

// signMsg signs the message with consensusskey
func signMsg(cc *grpc.ClientConn, gasPrice int64, msgs ...sdk.Msg) authsigning.Tx {
	txBuilder := txCfg.NewTxBuilder()
	_ = txBuilder.SetMsgs(msgs...)
	txBuilder.SetGasLimit(blockMaxGas)
	txBuilder.SetFeeAmount(sdk.Coins{types.NewInt64Coin(denom, 0)})

	signMode := txCfg.SignModeHandler().DefaultMode()

	_ = txBuilder.SetSignatures(getSignature(nil, pubKey, signMode))

	bytesToSign := getSignBytes(txCfg, txBuilder.GetTx(), chainID)
	sigBytes, err := privKey.Sign(bytesToSign)
	if err != nil {
		panic(fmt.Sprintf("priv_sign fail %s", err.Error()))
	}
	_ = txBuilder.SetSignatures(getSignature(sigBytes, pubKey, signMode))
	return txBuilder.GetTx()
}

// getSignBytes reteive the bytes from tx for signing
func getSignBytes(txCfg client.TxConfig, tx authsigning.Tx, cID string) []byte {
	b, err := txCfg.SignModeHandler().GetSignBytes(
		txCfg.SignModeHandler().DefaultMode(),
		authsigning.SignerData{
			ChainID: cID,
		},
		tx,
	)
	if err != nil {
		panic(fmt.Sprintf("Get bytesToSign fail, %s", err.Error()))
	}

	return b
}

// getSignature assembles a siging.SignatureV2 structure
func getSignature(s []byte, pub cryptotypes.PubKey, signMode signing.SignMode) signing.SignatureV2 {
	sig := signing.SignatureV2{
		PubKey: pub,
		Data: &signing.SingleSignatureData{
			SignMode:  signMode,
			Signature: s,
		},
	}

	return sig
}

// SendTx build a create-price message and broadcast to the exocoreChain through grpc connection
func SendTx(cc *grpc.ClientConn, feederID uint64, baseBlock uint64, price, roundID string, decimal int, nonce int32, gasPrice int64) *sdktx.BroadcastTxResponse {
	if gasPrice == 0 {
		gasPrice = defaultGasPrice
	}

	// build create-price message
	msg := oracleTypes.NewMsgCreatePrice(
		sdk.AccAddress(pubKey.Address()).String(),
		feederID,
		[]*oracleTypes.PriceSource{
			{
				SourceID: Chainlink,
				Prices: []*oracleTypes.PriceTimeDetID{
					{
						Price:     price,
						Decimal:   int32(decimal),
						Timestamp: time.Now().UTC().Format(layout),
						DetID:     roundID,
					},
				},
				Desc: "",
			},
		},
		baseBlock,
		nonce,
	)

	// sign the message with validator consensus-key configured
	signedTx := signMsg(cc, gasPrice, msg)

	// encode transaction to broadcast
	txBytes, err := txCfg.TxEncoder()(signedTx)
	if err != nil {
		panic(err)
	}

	return broadcastTxBytes(cc, txBytes)
}

// boradcastTxByBytes broadcasts the signed transaction
func broadcastTxBytes(cc *grpc.ClientConn, txBytes []byte) *sdktx.BroadcastTxResponse {
	txClient := sdktx.NewServiceClient(cc)
	ccRes, err := txClient.BroadcastTx(
		context.Background(),
		&sdktx.BroadcastTxRequest{
			Mode:    sdktx.BroadcastMode_BROADCAST_MODE_SYNC,
			TxBytes: txBytes,
		},
	)
	if err != nil {
		panic(err)
	}
	return ccRes
}
