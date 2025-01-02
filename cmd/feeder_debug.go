package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/ExocoreNetwork/price-feeder/debugger"
	"github.com/ExocoreNetwork/price-feeder/exoclient"
	fetchertypes "github.com/ExocoreNetwork/price-feeder/fetcher/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type sendReq struct {
	// on which block committed send this transaction, required
	height int64

	feederID uint64
	price    *debugger.PriceMsg
	result   chan *debugger.SubmitPriceResponse
}
type sendBytesReq struct {
	txBytes []byte
	result  chan *debugger.SubmitPriceResponse
}
type pendingRequestManager map[int64][]*sendBytesReq

func newPendingRequestManager() pendingRequestManager {
	return make(map[int64][]*sendBytesReq)
}

func (p pendingRequestManager) add(height int64, req *sendBytesReq) {
	if pendings, ok := p[height]; ok {
		p[height] = append(pendings, req)
	} else {
		p[height] = []*sendBytesReq{req}
	}
}

func (p pendingRequestManager) process(height int64, handler func(*sendBytesReq)) {
	wg := sync.WaitGroup{}
	for h, pendings := range p {
		if h <= height {
			for _, req := range pendings {
				wg.Add(1)
				go func(req *sendBytesReq) {
					handler(req)
					wg.Done()
				}(req)
			}
			wg.Wait()
			delete(p, h)
		}
	}
}

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

func (p PriceJSON) validate() error {
	if len(p.Price) == 0 {
		return errors.New("price is required")
	}
	if len(p.DetID) == 0 {
		return errors.New("det_id is required")
	}
	if p.Nonce == 0 {
		return errors.New("nonce should be greater than 0")
	}
	return nil
}

func (p PriceJSON) getPriceInfo() fetchertypes.PriceInfo {
	return fetchertypes.PriceInfo{
		Price:     p.Price,
		Decimal:   p.Decimal,
		RoundID:   p.DetID,
		Timestamp: p.Timestamp,
	}
}

var (
	DebugRetryConfig = RetryConfig{
		MaxAttempts: 43200,
		Interval:    2 * time.Second,
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

	_, err := getOracleParamsWithMaxRetry(DebugRetryConfig.MaxAttempts, ec, logger)
	if err != nil {
		logger.Error("failed to get oracle params", "error", err)
		return
	}
	logger.Info("subscribe block heights...")
	ecClient, _ := exoclient.GetClient()
	defer ecClient.Close()
	ecClient.Subscribe()
	pendingReqs := newPendingRequestManager()
	sendCh := make(chan *sendReq, 10)
	go func() {
		for {
			select {
			case event := <-ecClient.EventsCh():
				if e, ok := event.(*exoclient.EventNewBlock); ok {
					logger.Info("new block commited", "height", e.Height())

					pendingReqs.process(e.Height(), func(req *sendBytesReq) {
						res, err := ecClient.SendSignedTxBytesDebug(req.txBytes)
						if err != nil {
							logger.Error("failed to send tx", "error", err)
							req.result <- &debugger.SubmitPriceResponse{
								Err: err.Error(),
							}
							return
						}
						req.result <- &debugger.SubmitPriceResponse{
							CheckTxSuccess:   res.CheckTx.Code == 0,
							CheckTxLog:       res.CheckTx.Log,
							DeliverTxSuccess: res.CheckTx.Code == 0 && res.DeliverTx.Code == 0,
							DeliverTxLog:     res.DeliverTx.Log,
							TxHash:           res.Hash.String(),
							Height:           res.Height,
						}
					})
				}
			case req := <-sendCh:
				logger.Info("add a new send request", "height", req.height, "feederID", req.feederID)
				_, txBytes, err := ecClient.GetSignedTxBytesDebug(req.feederID, req.price.BaseBlock, req.price.GetPriceInfo(), req.price.Nonce)
				if err != nil {
					req.result <- &debugger.SubmitPriceResponse{
						Err: fmt.Sprintf("failed to sign tx from req:%v", req),
					}
					break
				}
				pendingReqs.add(req.height, &sendBytesReq{
					txBytes: txBytes,
					result:  req.result,
				})
			}
		}
	}()
	lis, err := net.Listen("tcp", conf.Debugger.Grpc)
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

func sendTx(feederID uint64, height int64, price *debugger.PriceMsg, port string) (*debugger.SubmitPriceResponse, error) {
	conn, err := grpc.Dial(
		fmt.Sprintf("localhost%s", port),
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
