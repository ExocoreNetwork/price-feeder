package exoclient

import (
	"github.com/cosmos/cosmos-sdk/codec"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func CreateGrpcConn(target string) *grpc.ClientConn {
	grpcConn, err := grpc.Dial(
		//		"127.0.0.1:9090",
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithDefaultCallOptions(grpc.ForceCodec(codec.NewProtoCodec(encCfg.InterfaceRegistry).GRPCCodec())),
	)
	if err != nil {
		panic(err)
	}

	return grpcConn
}
