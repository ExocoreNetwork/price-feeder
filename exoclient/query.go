package exoclient

import (
	"context"

	oracleTypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
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

// GetParams queries oracle params
// func GetLatestPrice(grpcConn *grpc.ClientConn, tokenID uint64) (oracleTypes.PriceTimeRound, error) {
// 	oracleClient := oracleTypes.NewQueryClient(grpcConn)
// 	priceRes, err := oracleClient.LatestPrice(context.Background(), &oracleTypes.QueryGetLatestPriceRequest{TokenId: tokenID})
// 	if err != nil {
// 		return oracleTypes.PriceTimeRound{}, err
// 	}
// 	return priceRes.Price, nil
//
// }
