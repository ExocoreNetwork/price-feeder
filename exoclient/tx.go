package exoclient

import (
	"context"
	"fmt"
	"time"

	oracletypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	fetchertypes "github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"

	authsigning "github.com/cosmos/cosmos-sdk/x/auth/signing"

	coretypes "github.com/cometbft/cometbft/rpc/core/types"
	sdk "github.com/cosmos/cosmos-sdk/types"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/cosmos/cosmos-sdk/types/tx/signing"
)

// SendTx signs a create-price transaction and send it to exocored
func (ec exoClient) SendTx(feederID uint64, baseBlock uint64, price fetchertypes.PriceInfo, nonce int32) (*sdktx.BroadcastTxResponse, error) {
	msg, txBytes, err := ec.getSignedTxBytes(feederID, baseBlock, price, nonce)
	if err != nil {
		return nil, err
	}
	// broadcast txBytes
	res, err := ec.txClient.BroadcastTx(
		context.Background(),
		&sdktx.BroadcastTxRequest{
			Mode:    sdktx.BroadcastMode_BROADCAST_MODE_SYNC,
			TxBytes: txBytes,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to braodcast transaction, msg:%v, valConsAddr:%s, error:%w", msg, sdk.ConsAddress(ec.pubKey.Address()), err)
	}
	return res, nil
}

// signMsg signs the message with consensusskey
func (ec exoClient) signMsg(msgs ...sdk.Msg) (authsigning.Tx, error) {
	txBuilder := ec.txCfg.NewTxBuilder()
	_ = txBuilder.SetMsgs(msgs...)
	txBuilder.SetGasLimit(blockMaxGas)
	txBuilder.SetFeeAmount(sdk.Coins{sdk.NewInt64Coin(denom, 0)})

	if err := txBuilder.SetSignatures(ec.getSignature(nil)); err != nil {
		ec.logger.Error("failed to SetSignatures", "errro", err)
		return nil, err
	}

	bytesToSign, err := ec.getSignBytes(txBuilder.GetTx())
	if err != nil {
		return nil, fmt.Errorf("failed to getSignBytes, error:%w", err)
	}
	sigBytes, err := ec.privKey.Sign(bytesToSign)
	if err != nil {
		ec.logger.Error("failed to sign txBytes", "error", err)
		return nil, err
	}
	// _ = txBuilder.SetSignatures(getSignature(sigBytes, ec.pubKey, signMode))
	_ = txBuilder.SetSignatures(ec.getSignature(sigBytes))
	return txBuilder.GetTx(), nil
}

// getSignBytes reteive the bytes from tx for signing
func (ec exoClient) getSignBytes(tx authsigning.Tx) ([]byte, error) {
	b, err := ec.txCfg.SignModeHandler().GetSignBytes(
		ec.txCfg.SignModeHandler().DefaultMode(),
		authsigning.SignerData{
			ChainID: ec.chainID,
		},
		tx,
	)
	if err != nil {
		return nil, fmt.Errorf("Get bytesToSign fail, %w", err)
	}

	return b, nil
}

// getSignature assembles a siging.SignatureV2 structure
func (ec exoClient) getSignature(sigBytes []byte) signing.SignatureV2 {
	signature := signing.SignatureV2{
		PubKey: ec.pubKey,
		Data: &signing.SingleSignatureData{
			SignMode:  ec.txCfg.SignModeHandler().DefaultMode(),
			Signature: sigBytes,
		},
	}
	return signature
}

func (ec exoClient) getSignedTxBytes(feederID uint64, baseBlock uint64, price fetchertypes.PriceInfo, nonce int32) (*oracletypes.MsgCreatePrice, []byte, error) {
	// build create-price message
	msg := oracletypes.NewMsgCreatePrice(
		sdk.AccAddress(ec.pubKey.Address()).String(),
		feederID,
		[]*oracletypes.PriceSource{
			{
				SourceID: Chainlink,
				Prices: []*oracletypes.PriceTimeDetID{
					{
						Price:     price.Price,
						Decimal:   price.Decimal,
						Timestamp: time.Now().UTC().Format(feedertypes.TimeLayout),
						DetID:     price.RoundID,
					},
				},
				Desc: "",
			},
		},
		baseBlock,
		nonce,
	)

	// sign the message with validator consensus-key configured
	signedTx, err := ec.signMsg(msg)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to sign message, msg:%v, valConsAddr:%s, error:%w", msg, sdk.ConsAddress(ec.pubKey.Address()), err)
	}

	// encode transaction to broadcast
	txBytes, err := ec.txCfg.TxEncoder()(signedTx)
	if err != nil {
		// this should not happen
		return nil, nil, fmt.Errorf("failed to encode singedTx, txBytes:%b, msg:%v, valConsAddr:%s, error:%w", txBytes, msg, sdk.ConsAddress(ec.pubKey.Address()), err)
	}
	return msg, txBytes, nil
}

func (ec exoClient) SendTxDebug(feederID uint64, baseBlock uint64, price fetchertypes.PriceInfo, nonce int32) (*coretypes.ResultBroadcastTxCommit, error) {
	msg, txBytes, err := ec.getSignedTxBytes(feederID, baseBlock, price, nonce)
	if err != nil {
		return nil, err
	}
	// broadcast txBytes
	res, err := ec.txClientDebug.BroadcastTxCommit(context.Background(), txBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to braodcast transaction, msg:%v, valConsAddr:%s, error:%w", msg, sdk.ConsAddress(ec.pubKey.Address()), err)
	}
	return res, nil
}

func (ec exoClient) GetSignedTxBytesDebug(feederID uint64, baseBlock uint64, price fetchertypes.PriceInfo, nonce int32) (*oracletypes.MsgCreatePrice, []byte, error) {
	return ec.getSignedTxBytes(feederID, baseBlock, price, nonce)
}

func (ec exoClient) SendSignedTxBytesDebug(txBytes []byte) (*coretypes.ResultBroadcastTxCommit, error) {
	// broadcast txBytes
	res, err := ec.txClientDebug.BroadcastTxCommit(context.Background(), txBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to braodcast transaction, txBytes:%v, valConsAddr:%s, error:%w", txBytes, sdk.ConsAddress(ec.pubKey.Address()), err)
	}
	return res, nil
}
