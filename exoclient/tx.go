package exoclient

import (
	"context"
	"fmt"
	"math"
	"time"

	"github.com/ExocoreNetwork/exocore/app"
	oracleTypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	"github.com/evmos/evmos/v14/encoding"

	"github.com/cosmos/cosmos-sdk/client"
	"github.com/cosmos/cosmos-sdk/codec"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	"cosmossdk.io/simapp/params"
	//	"github.com/cosmos/cosmos-sdk/codec"
	cmdcfg "github.com/ExocoreNetwork/exocore/cmd/config"
	"github.com/cosmos/cosmos-sdk/client/tx"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"github.com/cosmos/cosmos-sdk/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"

	authTypes "github.com/cosmos/cosmos-sdk/x/auth/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	chainID = "exocoretestnet_233-1"
	homeDir = "/Users/xx/.tmp-exocored"
	appName = "exocore"
)

var encCfg params.EncodingConfig
var txCfg client.TxConfig
var kr keyring.Keyring
var gasPrice uint64
var blockMaxGas uint64

func init() {
	config := sdk.GetConfig()
	cmdcfg.SetBech32Prefixes(config)

	encCfg = encoding.MakeConfig(app.ModuleBasics)
	txCfg = encCfg.TxConfig

	var err error
	if kr, err = keyring.New(appName, keyring.BackendTest, homeDir, nil, encCfg.Codec); err != nil {
		panic(err)
	}

	gasPrice = uint64(7)
	blockMaxGas = 10000000
}

func CreateGrpcConn() *grpc.ClientConn {
	grpcConn, err := grpc.Dial(
		"127.0.0.1:9090",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(encCfg.InterfaceRegistry).GRPCCodec())),
	)
	if err != nil {
		panic(err)
	}

	return grpcConn
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

func signMsg(cc *grpc.ClientConn, name string, msgs ...sdk.Msg) authsigning.Tx {
	txBuilder := txCfg.NewTxBuilder()
	_ = txBuilder.SetMsgs(msgs...)
	txBuilder.SetGasLimit(blockMaxGas)
	txBuilder.SetFeeAmount(sdk.Coins{types.NewInt64Coin("aexo", math.MaxInt64)})

	info, _ := kr.Key(name)
	fromAddr, _ := info.GetAddress()

	number, sequence, err := queryAccount(cc, fromAddr)
	if err != nil {
		fmt.Println("debug-queryAccount-err:", err)
		panic(err)
	}

	txf := tx.Factory{}.
		WithChainID(chainID).
		WithKeybase(kr).
		WithTxConfig(txCfg).
		WithAccountNumber(number).
		WithSequence(sequence)

	if err = tx.Sign(txf, "dev0", txBuilder, true); err != nil {
		panic(err)
	}

	//simulate and sign again
	signedTx := txBuilder.GetTx()
	txBytes, _ := txCfg.TxEncoder()(signedTx)
	gasLimit, _ := simulateTx(cc, txBytes)
	gasLimit *= 2
	fee := gasLimit * gasPrice
	if fee > math.MaxInt64 {
		panic("fee exceeds maxInt64")
	}
	txBuilder.SetGasLimit(gasLimit)
	txBuilder.SetFeeAmount(sdk.Coins{types.NewInt64Coin("aexo", int64(fee))})
	//sign agin with simulated gas used
	if err = tx.Sign(txf, "dev0", txBuilder, true); err != nil {
		panic(err)
	}

	return txBuilder.GetTx()
}

func SendTx(cc *grpc.ClientConn, feederID uint64, baseBlock uint64, price, roundID string, decimal int) {
	info, _ := kr.Key("dev0")
	fromAddr, _ := info.GetAddress()

	msg := oracleTypes.NewMsgCreatePrice(
		sdk.ValAddress(fromAddr.Bytes()).String(),
		feederID,
		[]*oracleTypes.PriceSource{
			{
				SourceID: 1,
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
		1,
	)
	signedTx := signMsg(cc, "dev0", msg)

	txBytes, err := txCfg.TxEncoder()(signedTx)
	if err != nil {
		panic(err)
	}

	broadcastTxBytes(cc, txBytes)
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
