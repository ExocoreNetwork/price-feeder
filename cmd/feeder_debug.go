package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/ExocoreNetwork/price-feeder/debugger"
	"github.com/ExocoreNetwork/price-feeder/exoclient"
	fetchertypes "github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type server struct {
	sendCh chan *sendReq
	debugger.UnimplementedPriceSubmitServiceServer
}

func (s *server) SubmitPrice(ctx context.Context, req *debugger.SubmitPriceRequest) (*debugger.SubmitPriceResponse, error) {
	result := make(chan *debugger.SubmitPriceResponse, 1)
	s.sendCh <- &sendReq{
		height:   req.Height,
		feederID: req.FeederId,
		price:    req.Price,
		result:   result,
	}
	r := <-result
	return r, nil
}

func newServer(sendCh chan *sendReq) *server {
	return &server{
		sendCh: sendCh,
	}
}

type PriceJSON struct {
	Price     string `json:"price"`
	DetID     string `json:"det_id"`
	Decimal   int32  `json:"decimal"`
	Timestamp string `json:"timestamp"`
	Nonce     int32  `json:"nonce"`
	BaseBlock uint64 `json:"base_block"`
}

func (p PriceJSON) getPriceInfo() fetchertypes.PriceInfo {
	return fetchertypes.PriceInfo{
		Price:     p.Price,
		Decimal:   p.Decimal,
		RoundID:   p.DetID,
		Timestamp: p.Timestamp,
	}
}

type sendRes struct {
	err              error
	checkTxSuccess   bool
	checkTxLog       string
	deliverTxSuccess bool
	deliverTxLog     string
	txHash           string
	height           int64
}

type sendReq struct {
	// on which block committed send this transaction, required
	height int64

	feederID uint64
	price    *debugger.PriceMsg
	result   chan *debugger.SubmitPriceResponse
}

type statusCode string

const (
	overwrite statusCode = "overwrite the pending transaction"
	success   statusCode = "successfully sent the transaction"
	fail      statusCode = "failed to send transaction"
)

var (
	sendCh           = make(chan *sendReq)
	DebugRetryConfig = RetryConfig{
		MaxAttempts: 10,
		Interval:    3 * time.Second,
	}
)

func DebugPriceFeeder(conf *feedertypes.Config, logger feedertypes.LoggerInf, mnemonic string, sourcesPath string) {
	// init logger
	if logger = feedertypes.SetLogger(logger); logger == nil {
		panic("logger is not initialized")
	}
	// init exoclient
	// 1. subscribing for heights
	// 2. sending create-price tx
	count := 0
	for count < DebugRetryConfig.MaxAttempts {
		if err := exoclient.Init(conf, mnemonic, privFile, false, true); err != nil {
			if errors.Is(err, feedertypes.ErrInitConnectionFail) {
				logger.Info("retry initComponents due to connectionfailed", "count", count, "maxRetry", DefaultRetryConfig.MaxAttempts, "error", err)
				time.Sleep(DebugRetryConfig.Interval)
				continue
			}
			logger.Error("failed to init exoclient", "error", err)
			return
		}
		break
	}
	ec, _ := exoclient.GetClient()

	_, err := getOracleParamsWithMaxRetry(1, ec, logger)
	if err != nil {
		logger.Error("failed to get oracle params", "error", err)
		return
	}
	logger.Info("subscribe block heights...")
	ecClient, _ := exoclient.GetClient()
	defer ecClient.Close()
	ecClient.Subscribe()
	//	var pendingReq *sendReq
	pendingReqs := make(map[int64][]*sendReq)
	sendCh := make(chan *sendReq, 10)
	go func() {
		for {
			select {
			case event := <-ecClient.EventsCh():
				if e, ok := event.(*exoclient.EventNewBlock); ok {
					logger.Info("new block commited", "height", e.Height())
					for h, pendings := range pendingReqs {
						if h <= e.Height() {
							for i, req := range pendings {
								res, err := ecClient.SendTxDebug(req.feederID, req.price.BaseBlock, req.price.GetPriceInfo(), req.price.Nonce)
								if err != nil {
									logger.Error("failed to send tx", "error", err)
									req.result <- &debugger.SubmitPriceResponse{Err: err.Error()}
								}
								req.result <- &debugger.SubmitPriceResponse{
									CheckTxSuccess:   res.CheckTx.Code == 0,
									CheckTxLog:       res.CheckTx.Log,
									DeliverTxSuccess: res.CheckTx.Code == 0 && res.DeliverTx.Code == 0,
									DeliverTxLog:     res.DeliverTx.Log,
									TxHash:           res.Hash.String(),
									Height:           res.Height,
								}
								if i == len(pendings)-1 {
									delete(pendingReqs, h)
								} else {
									pendingReqs[h] = append(pendings[:i], pendings[i+1:]...)
								}
							}
						}
					}
				}
			case req := <-sendCh:
				logger.Info("add a new send request", "height", req.height)
				if pendings, ok := pendingReqs[req.height]; ok {
					pendingReqs[req.height] = append(pendings, req)
				} else {
					pendingReqs[req.height] = []*sendReq{req}
				}
			}
		}
	}()
	lis, err := net.Listen("tcp", ":50051")
	if err != nil {
		fmt.Printf("failed to listen: %v\r\n", err)
		return
	}
	grpcServer := grpc.NewServer()
	debugger.RegisterPriceSubmitServiceServer(grpcServer, newServer(sendCh))
	if err := grpcServer.Serve(lis); err != nil {
		fmt.Printf("failed to serve:%v\r\n", err)
		return
	}
}

func sendTx(feederID uint64, height int64, price *debugger.PriceMsg) (*debugger.SubmitPriceResponse, error) {
	conn, err := grpc.Dial(
		"localhost:50051",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	c := debugger.NewPriceSubmitServiceClient(conn)
	return c.SubmitPrice(context.Background(), &debugger.SubmitPriceRequest{
		Height:   height,
		FeederId: feederID,
		Price:    price,
	})
}

func sendTxImmediately(feederID uint64, price *PriceJSON) (*debugger.SubmitPriceResponse, error) {
	if err := exoclient.Init(feederConfig, mnemonic, privFile, true, true); err != nil {
		return nil, fmt.Errorf("failed to init exoclient in txOnly mode for debug, error:%w", err)
	}
	ec, _ := exoclient.GetClient()
	pInfo := price.getPriceInfo()

	res, err := ec.SendTxDebug(feederID, price.BaseBlock, pInfo, price.Nonce)
	if err != nil {
		return nil, err
	}
	protoRes := &debugger.SubmitPriceResponse{
		CheckTxSuccess:   res.CheckTx.Code == 0,
		CheckTxLog:       res.CheckTx.Log,
		DeliverTxSuccess: res.CheckTx.Code == 0 && res.DeliverTx.Code == 0,
		DeliverTxLog:     res.DeliverTx.Log,
		TxHash:           res.Hash.String(),
		Height:           res.Height,
	}
	return protoRes, nil
}
