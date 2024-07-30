package exoclient

import (
	"github.com/cosmos/cosmos-sdk/codec"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// CreateGrpcConn creates an grpc connection to the target
func CreateGrpcConn(target string) *grpc.ClientConn {
	grpcConn, err := grpc.Dial(
		target,
		// for internal usage, no need to set TSL
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(encCfg.InterfaceRegistry).GRPCCodec())),
	)
	if err != nil {
		panic(err)
	}

	return grpcConn
}