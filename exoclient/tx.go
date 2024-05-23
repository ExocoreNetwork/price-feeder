package exoclient

import (
	"context"
	"encoding/base64"
	"fmt"
	"math"
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
	//	"github.com/cosmos/cosmos-sdk/codec"
	cmdcfg "github.com/ExocoreNetwork/exocore/cmd/config"
	"github.com/cosmos/cosmos-sdk/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"

	authTypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc"
)

//type SID uint64

const (
	Chainlink uint64 = 1
)

var (
	chainID = "exocoretestnet_233-1"
)

var encCfg params.EncodingConfig
var txCfg client.TxConfig
var defaultGasPrice int64
var blockMaxGas uint64

var (
	privKey cryptotypes.PrivKey
	pubKey  cryptotypes.PubKey
)

func Init(mnemonic, privBase64, cID string) {
	config := sdk.GetConfig()
	cmdcfg.SetBech32Prefixes(config)

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
	}
	chainID = cID
	defaultGasPrice = int64(7)
	// TODO: set from exocore's params
	blockMaxGas = 10000000
}

func simulateTx(cc *grpc.ClientConn, txBytes []byte) (uint64, error) {
	// Simulate the tx via gRPC. We create a new client for the Protobuf Tx service
	txClient := sdktx.NewServiceClient(cc)

	// call the Simulate method on this client.
	grpcRes, err := txClient.Simulate(
		context.Background(),
		&sdktx.SimulateRequest{
			TxBytes: txBytes,
		},
	)
	if err != nil {
		fmt.Println("debug-simulateTx-err:", err)
		return 0, err
	}

	return grpcRes.GasInfo.GasUsed, nil
}

// func signMsg(cc *grpc.ClientConn, name string, gasPrice int64, msgs ...sdk.Msg) authsigning.Tx {
func signMsg(cc *grpc.ClientConn, gasPrice int64, msgs ...sdk.Msg) authsigning.Tx {
	txBuilder := txCfg.NewTxBuilder()
	_ = txBuilder.SetMsgs(msgs...)
	txBuilder.SetGasLimit(blockMaxGas)
	txBuilder.SetFeeAmount(sdk.Coins{types.NewInt64Coin("aexo", math.MaxInt64)})

	signMode := txCfg.SignModeHandler().DefaultMode()

	_ = txBuilder.SetSignatures(getSignature(nil, pubKey, signMode))
	txBytes, _ := txCfg.TxEncoder()(txBuilder.GetTx())
	gasLimit, _ := simulateTx(cc, txBytes)
	gasLimit *= 2
	if gasLimit > math.MaxInt {
		panic("gasLimit*2 exceeds maxInt64")
	}
	fee := gasLimit * uint64(gasPrice)

	if fee > math.MaxInt64 {
		panic("fee exceeds maxInt64")
	}
	txBuilder.SetGasLimit(gasLimit)
	txBuilder.SetFeeAmount(sdk.Coins{types.NewInt64Coin("aexo", int64(fee))})

	bytesToSign := getSignBytes(txCfg, txBuilder.GetTx(), chainID)
	sigBytes, err := privKey.Sign(bytesToSign)
	if err != nil {
		panic(fmt.Sprintf("priv_sign fail %s", err.Error()))
	}
	_ = txBuilder.SetSignatures(getSignature(sigBytes, pubKey, signMode))
	return txBuilder.GetTx()
}

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

func SendTx(cc *grpc.ClientConn, feederID uint64, baseBlock uint64, price, roundID string, decimal int, nonce int32, gasPrice int64) *sdktx.BroadcastTxResponse {
	if gasPrice == 0 {
		gasPrice = defaultGasPrice
	}

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
						Timestamp: time.Now().String(),
						DetID:     roundID,
					},
				},
				Desc: "",
			},
		},
		baseBlock,
		nonce,
	)
	signedTx := signMsg(cc, gasPrice, msg)

	txBytes, err := txCfg.TxEncoder()(signedTx)
	if err != nil {
		panic(err)
	}

	return broadcastTxBytes(cc, txBytes)
}

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

func queryAccount(grpcConn *grpc.ClientConn, myAddress sdk.AccAddress) (number, sequence uint64, err error) {
	authClient := authTypes.NewQueryClient(grpcConn)
	var accountRes *authTypes.QueryAccountResponse
	accountRes, err = authClient.Account(context.Background(), &authTypes.QueryAccountRequest{
		Address: myAddress.String(),
	})
	if err != nil {
		fmt.Println("debug-queryAccount-err:", err)
		return
	}
	var account authTypes.AccountI
	_ = encCfg.Codec.UnpackAny(accountRes.Account, &account)
	number = account.GetAccountNumber()
	sequence = account.GetSequence()

	return
}
