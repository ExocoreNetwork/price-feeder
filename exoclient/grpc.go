package exoclient

import (
	"context"
	"fmt"
	"time"

	"cosmossdk.io/simapp/params"
	"github.com/cosmos/cosmos-sdk/codec"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/keepalive"
)

// CreateGrpcConn creates an grpc connection to the target
func createGrpcConn(target string, encCfg params.EncodingConfig) (conn *grpc.ClientConn, err error) {
	ctx, _ := context.WithTimeout(context.Background(), 10*time.Second)

	grpcConn, err := grpc.DialContext(
		ctx,
		target,
		// for internal usage, no need to set TSL
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(encCfg.InterfaceRegistry).GRPCCodec())),
		grpc.WithKeepaliveParams(keepalive.ClientParameters{
			Time:                150 * time.Second,
			Timeout:             5 * time.Second,
			PermitWithoutStream: true,
		}),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create grpc connection, error:%w", err)
	}

	return grpcConn, nil
}
