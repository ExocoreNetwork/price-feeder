package exoclient

import (
	"context"
	"fmt"

	oracleTypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
)

// GetParams queries oracle params
func (ec exoClient) GetParams() (oracleTypes.Params, error) {
	paramsRes, err := ec.oracleClient.Params(context.Background(), &oracleTypes.QueryParamsRequest{})
	if err != nil {
		return oracleTypes.Params{}, fmt.Errorf("failed to query oracle params from oracleClient, error:%w", err)
	}
	return paramsRes.Params, nil

}

// GetLatestPrice returns latest price of specific token
func (ec exoClient) GetLatestPrice(tokenID uint64) (oracleTypes.PriceTimeRound, error) {
	priceRes, err := ec.oracleClient.LatestPrice(context.Background(), &oracleTypes.QueryGetLatestPriceRequest{TokenId: tokenID})
	if err != nil {
		return oracleTypes.PriceTimeRound{}, fmt.Errorf("failed to get latest price from oracleClient, error:%w", err)
	}
	return priceRes.Price, nil

}

// TODO: pagination
// GetStakerInfos get all stakerInfos for the assetID
func (ec exoClient) GetStakerInfos(assetID string) ([]*oracleTypes.StakerInfo, error) {
	stakerInfoRes, err := ec.oracleClient.StakerInfos(context.Background(), &oracleTypes.QueryStakerInfosRequest{AssetId: assetID})
	if err != nil {
		return []*oracleTypes.StakerInfo{}, fmt.Errorf("failed to get stakerInfos from oracleClient, error:%w", err)
	}
	return stakerInfoRes.StakerInfos, nil
}

// GetStakerInfos get the stakerInfos corresponding to stakerAddr for the assetID
func (ec exoClient) GetStakerInfo(assetID, stakerAddr string) ([]*oracleTypes.StakerInfo, error) {
	stakerInfoRes, err := ec.oracleClient.StakerInfos(context.Background(), &oracleTypes.QueryStakerInfosRequest{AssetId: assetID})
	if err != nil {
		return []*oracleTypes.StakerInfo{}, fmt.Errorf("failed to get stakerInfo from oracleClient, error:%w", err)
	}
	return stakerInfoRes.StakerInfos, nil
}
