package exoclient

import (
	"context"
	"fmt"
	"time"

	oracleTypes "github.com/ExocoreNetwork/exocore/x/oracle/types"

	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"

	"github.com/cosmos/cosmos-sdk/client"
	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	"github.com/cosmos/cosmos-sdk/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"

	"google.golang.org/grpc"
)

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
