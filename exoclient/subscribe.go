package exoclient

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

const (
	subTypeNewBlock      = "tm.event='NewBlock'"
	subTypeTxUpdatePrice = "tm.event='Tx' AND create_price.price_update='success'"
	subTypeTxNativeToken = "tm.event='Tx' AND create_price.native_token_update='update'"
	sub                  = `{"jsonrpc":"2.0","method":"subscribe","id":0,"params":{"query":"%s"}}`
	reconnectInterval    = 3
	maxRetry             = 600
	success              = "success"
)

var (
	conn               *websocket.Conn
	rHeader            http.Header
	host               string
	eventNewBlock      = fmt.Sprintf(sub, subTypeNewBlock)
	eventTxPrice       = fmt.Sprintf(sub, subTypeTxUpdatePrice)
	eventTxNativeToken = fmt.Sprintf(sub, subTypeTxNativeToken)

	events = map[string]bool{
		eventNewBlock:      true,
		eventTxPrice:       true,
		eventTxNativeToken: true,
	}
)

type result struct {
	Result struct {
		Query string `json:"query"`
		Data  struct {
			Value struct {
				TxResult struct {
					Height string `json:"height"`
				} `json:"TxResult"`
				Block struct {
					Header struct {
						Height string `json:"height"`
					} `json:"header"`
				} `json:"block"`
			} `json:"value"`
		} `json:"data"`
		Events struct {
			Fee               []string `json:"fee_market.base_fee"`
			ParamsUpdate      []string `json:"create_price.params_update"`
			FinalPrice        []string `json:"create_price.final_price"`
			PriceUpdate       []string `json:"create_price.price_update"`
			FeederID          []string `json:"create_price.feeder_id"`
			FeederIDs         []string `json:"create_price.feeder_ids"`
			NativeTokenUpdate []string `json:"create_price.native_token_update"`
			NativeTokenChange []string `json:"create_price.native_token_change"`
		} `json:"events"`
	} `json:"result"`
}

type ReCh struct {
	Height       string
	Gas          string
	ParamsUpdate bool
	Price        []string
	FeederIDs    string
	TxHeight     string
	NativeETH    string
}

// setup ws connection, and subscribe newblock events
func Subscriber(remoteAddr string, endpoint string) (ret chan ReCh, stop chan struct{}) {
	// logger := getLogger()
	u, err := url.Parse(remoteAddr)
	if err != nil {
		panic(err)
	}

	dialer := &websocket.Dialer{
		NetDial: func(_, _ string) (net.Conn, error) {
			return net.Dial("tcp", u.Host)
		},
		Proxy: http.ProxyFromEnvironment,
	}
	rHeader = http.Header{}
	host = u.Host
	conn, _, err = dialer.Dial("ws://"+host+endpoint, rHeader)

	if err != nil {
		panic(fmt.Sprintf("dail ws failed, error:%s", err))
	}

	stop = make(chan struct{})
	stopInternal := make(chan struct{})
	ret = make(chan ReCh)

	// read routine reads events(newBlock) from websocket
	go func() {
		defer func() {
			conn.Close()
		}()
		conn.SetPongHandler(func(string) error {
			return nil
		})
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				logger.Error("read from publisher failed", "error", err)
				if !websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure) {
					return
				}
				// close write routine
				close(stopInternal)
				// reconnect ws
				attempt := 0
				for ; err != nil; conn, _, err = dialer.Dial("ws://"+host+endpoint, rHeader) {
					logger.Error("failed to reconnect to publisher, retrying...", "error", err, "attempt_count", attempt)
					time.Sleep(reconnectInterval * time.Second)
					attempt++
					if attempt > maxRetry {
						logger.Error("failed to reconnect to publisher after max retry")
						return
					}
				}
				logger.Info("reconnected to publisher successfully.")
				conn.SetPongHandler(func(string) error {
					return nil
				})
				// rest stopInternal
				stopInternal = make(chan struct{})
				// setup write routine to set ping messages
				go writeRoutine(conn, stopInternal)
				// resubscribe event
				attempt = 0
				for attempt < maxRetry {
					if err = conn.WriteMessage(websocket.TextMessage, []byte(eventNewBlock)); err == nil {
						break
					}
					logger.Error("failed to subscribe to new_block event, retrying", "error", err, "attempt_cout", attempt)
					time.Sleep(1 * time.Second)
					attempt++
				}
				if attempt == maxRetry {
					logger.Error("fail to subscribe new_block event after max retry")
					return
				}
				continue
			}
			var response result
			err = json.Unmarshal(data, &response)
			if err != nil {
				logger.Error("failed to pase response from publisher, skip", "error", err)
				continue
			}
			rec := ReCh{}

			switch response.Result.Query {
			case subTypeNewBlock:
				rec.Height = response.Result.Data.Value.Block.Header.Height
				events := response.Result.Events
				if len(events.Fee) > 0 {
					rec.Gas = events.Fee[0]
				}
				if len(events.ParamsUpdate) > 0 {
					rec.ParamsUpdate = true
				}
				// TODO: for oracle v1, this should not happen, since this event only emitted in tx, But if we add more modes to support final price generation in endblock, this would be necessaray.
				if len(events.PriceUpdate) > 0 && events.PriceUpdate[0] == success {
					rec.FeederIDs = events.FeederIDs[0]
				}
				ret <- rec
			case subTypeTxUpdatePrice:
				// as we filtered for price_udpate=success, this means price has been updated this block
				events := response.Result.Events
				rec.Price = events.FinalPrice
				rec.TxHeight = response.Result.Data.Value.TxResult.Height
				ret <- rec
			case subTypeTxNativeToken:
				// update validator list for staker
				rec.NativeETH = response.Result.Events.NativeTokenChange[0]
			default:
			}

			select {
			case <-stop:
				return
			default:
			}
		}
	}()

	// write message to subscribe tx event for price update
	if err = conn.WriteMessage(websocket.TextMessage, []byte(eventTxPrice)); err != nil {
		panic("fail to subscribe tx event")
	}

	// write message to subscribe tx event for native token validator list change
	if err = conn.WriteMessage(websocket.TextMessage, []byte(eventTxPrice)); err != nil {
		panic("fail to subscribe tx event")
	}

	// write message to subscribe newBlock event
	if err = conn.WriteMessage(websocket.TextMessage, []byte(eventNewBlock)); err != nil {
		panic("fail to subscribe new_block event")
	}

	// write routine sends ping messages every 10 seconds
	go writeRoutine(conn, stopInternal)
	return
}

// writeRoutine sends ping messages every 10 second
func writeRoutine(conn *websocket.Conn, stop chan struct{}) {
	// logger := getLogger()
	ticker := time.NewTicker(10 * time.Second)
	defer func() {
		ticker.Stop()
	}()

	for {
		select {
		case <-ticker.C:
			if err := conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				panic(err)
			}
		case <-stop:
			if err := conn.WriteMessage(
				websocket.CloseMessage,
				websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
			); err != nil {
				logger.Error("failed to write close message to publisher", "error", err)
				return
			}
		}
	}
}

func ResetEvents() {
	for event, _ := range events {
		events[event] = true
	}
}

func SubEvents(conn *websocket.Conn, retryCount int, retryInterval time.Duration) {
	// logger := getLogger()
	for event, ok := range events {
		retryCount--
		if ok {
			if err := conn.WriteMessage(websocket.TextMessage, []byte(event)); err == nil {
				logger.Info("subscribes event succussfully", "event", event)
				events[event] = false
			}
		}
		if retryCount == 0 {
			logger.Error("failed to subscribe to all events", "events", events)
			panic("fail to subscribe events")
		}
		time.Sleep(retryInterval * time.Second)
	}
}
