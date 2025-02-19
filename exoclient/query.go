package exoclient

import (
	"context"

	oracleTypes "github.com/imua-xyz/imuachain/x/oracle/types"
	"google.golang.org/grpc"
)

// GetParams queries oracle params
func GetParams(grpcConn *grpc.ClientConn) (oracleTypes.Params, error) {
	oracleClient := oracleTypes.NewQueryClient(grpcConn)
	paramsRes, err := oracleClient.Params(context.Background(), &oracleTypes.QueryParamsRequest{})
	if err != nil {
		return oracleTypes.Params{}, err
	}

	return paramsRes.Params, nil
}

// GetLatestPrice returns latest price of specific token
func GetLatestPrice(grpcConn *grpc.ClientConn, tokenID uint64) (oracleTypes.PriceTimeRound, error) {
	oracleClient := oracleTypes.NewQueryClient(grpcConn)
	priceRes, err := oracleClient.LatestPrice(context.Background(), &oracleTypes.QueryGetLatestPriceRequest{TokenId: tokenID})
	if err != nil {
		return oracleTypes.PriceTimeRound{}, err
	}
	return priceRes.Price, nil

}

// TODO: pagination
// GetStakerInfos get all stakerInfos for the assetID
func GetStakerInfos(grpcConn *grpc.ClientConn, assetID string) ([]*oracleTypes.StakerInfo, error) {
	oracleClient := oracleTypes.NewQueryClient(grpcConn)
	stakerInfoRes, err := oracleClient.StakerInfos(context.Background(), &oracleTypes.QueryStakerInfosRequest{AssetId: assetID})
	if err != nil {
		return []*oracleTypes.StakerInfo{}, err
	}
	return stakerInfoRes.StakerInfos, nil
}

// GetStakerInfos get the stakerInfos corresponding to stakerAddr for the assetID
func GetStakerInfo(grpcConn *grpc.ClientConn, assetID, stakerAddr string) ([]*oracleTypes.StakerInfo, error) {
	oracleClient := oracleTypes.NewQueryClient(grpcConn)
	stakerInfoRes, err := oracleClient.StakerInfos(context.Background(), &oracleTypes.QueryStakerInfosRequest{AssetId: assetID})
	if err != nil {
		return []*oracleTypes.StakerInfo{}, err
	}
	return stakerInfoRes.StakerInfos, nil
}
