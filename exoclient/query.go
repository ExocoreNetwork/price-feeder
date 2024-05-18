package exoclient

import (
	"context"

	oracleTypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	"google.golang.org/grpc"
)

func GetParams(grpcConn *grpc.ClientConn) (oracleTypes.Params, error) {
	oracleClient := oracleTypes.NewQueryClient(grpcConn)
	paramsRes, err := oracleClient.Params(context.Background(), &oracleTypes.QueryParamsRequest{})
	if err != nil {
		return oracleTypes.Params{}, err
	}

	return paramsRes.Params, nil
}
