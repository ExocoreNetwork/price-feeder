package exoclient

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sync"

	"cosmossdk.io/simapp/params"
	oracletypes "github.com/ExocoreNetwork/exocore/x/oracle/types"
	feedertypes "github.com/ExocoreNetwork/price-feeder/types"
	"github.com/cosmos/cosmos-sdk/client"
	cryptotypes "github.com/cosmos/cosmos-sdk/crypto/types"
	"github.com/cosmos/cosmos-sdk/types/tx"
	sdktx "github.com/cosmos/cosmos-sdk/types/tx"
	"github.com/gorilla/websocket"
	"google.golang.org/grpc"
)

var _ exoClientInf = &exoClient{}

// exoClient implements exoClientInf interface to serve as a grpc client to interact with eoxored grpc service
type exoClient struct {
	logger   feedertypes.LoggerInf
	grpcConn *grpc.ClientConn
	// cancel used to cancel grpc connection
	cancel func()

	// params for sign/send transactions
	privKey cryptotypes.PrivKey
	pubKey  cryptotypes.PubKey
	encCfg  params.EncodingConfig
	txCfg   client.TxConfig
	chainID string

	// client to broadcast transactions to eoxocred
	txClient tx.ServiceClient

	// wsclient interact with exocored
	wsClient   *websocket.Conn
	wsEndpoint string
	wsDialer   *websocket.Dialer
	// wsStop channel used to signal ws close
	wsStop chan struct{}
	//	wsStopRet chan struct{}
	//	wsActiveRoutines *atomic.Int32
	wsLock           *sync.Mutex
	wsActiveRoutines *int
	wsActive         *bool
	wsEventsCh       chan EventRes

	// client to query from exocored
	oracleClient oracletypes.QueryClient
}

// NewExoClient creates a exocore-client used to do queries and send transactions to exocored
func NewExoClient(logger feedertypes.LoggerInf, endpoint, wsEndpoint string, privKey cryptotypes.PrivKey, encCfg params.EncodingConfig, chainID string) (*exoClient, error) {
	ec := &exoClient{
		logger:           logger,
		privKey:          privKey,
		pubKey:           privKey.PubKey(),
		encCfg:           encCfg,
		txCfg:            encCfg.TxConfig,
		wsEndpoint:       wsEndpoint,
		wsActiveRoutines: new(int),
		wsActive:         new(bool),
		wsLock:           new(sync.Mutex),
		wsStop:           make(chan struct{}),
		wsEventsCh:       make(chan EventRes),
	}

	var err error
	ec.grpcConn, ec.cancel, err = createGrpcConn(endpoint, encCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create new Exoclient, endpoint:%s, error:%w", err)
	}

	// setup txClient
	ec.txClient = sdktx.NewServiceClient(ec.grpcConn)
	// setup queryClient
	ec.oracleClient = oracletypes.NewQueryClient(ec.grpcConn)
	// setup wsClient
	u, err := url.Parse(wsEndpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to parse wsEndpoint, wsEndpoint:%s, error:%w", wsEndpoint, err)
	}
	ec.wsDialer = &websocket.Dialer{
		NetDial: func(_, _ string) (net.Conn, error) {
			return net.Dial("tcp", u.Host)
		},
		Proxy: http.ProxyFromEnvironment,
	}
	ec.wsClient, _, err = ec.wsDialer.Dial(wsEndpoint, http.Header{})
	if err != nil {
		return nil, fmt.Errorf("failed to create ws connection, error:%w", err)
	}
	ec.wsClient.SetPongHandler(func(string) error {
		return nil
	})

	return ec, nil
}

func (ec *exoClient) Close() {
	ec.CloseWs()
	ec.CloseGRPC()
}

// Close close grpc connection
func (ec *exoClient) CloseGRPC() {
	if ec.cancel == nil {
		return
	}
	ec.cancel()
}

func (ec *exoClient) CloseWs() {
	if ec.wsClient == nil {
		return
	}
	ec.wsClient.Close()
}

// GetClient returns defaultExoClient and a bool value to tell if that defaultExoClient has been initialized
func GetClient() (*exoClient, bool) {
	if defaultExoClient == nil {
		return nil, false
	}
	return defaultExoClient, true
}
