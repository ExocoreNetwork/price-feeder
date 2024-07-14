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
	eventNewBlock = `{"jsonrpc":"2.0","method":"subscribe","id":0,"params":{"query":"tm.event='NewBlock'"}}`
	maxRetry      = 100
)

var (
	conn    *websocket.Conn
	rHeader http.Header
	host    string
)

type result struct {
	Result struct {
		Query string `json:"query"`
		Data  struct {
			Value struct {
				Block struct {
					Header struct {
						Height string `json:"height"`
					} `json:"header"`
				} `json:"block"`
			} `json:"value"`
		} `json:"data"`
		Events struct {
			Fee []string `json:"fee_market.base_fee"`
		} `json:"events"`
	} `json:"result"`
}

type ReCh struct {
	Height string
	Gas    string
}

// setup ws connection, and subscribe newblock events
func Subscriber(remoteAddr string, endpoint string) (ret chan ReCh, stop chan struct{}) {
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
				fmt.Println("read err:", err)
				if !websocket.IsUnexpectedCloseError(err, websocket.CloseNormalClosure) {
					return
				}
				// close write routine
				close(stopInternal)
				// reconnect ws
				attempt := 0
				for ; err != nil; conn, _, err = dialer.Dial("ws://"+host+endpoint, rHeader) {
					fmt.Println("failed to reconnect, retrying...", attempt)
					time.Sleep(1 << uint(attempt) * time.Second)
					attempt++
					if attempt > maxRetry {
						fmt.Println("failed to reconnect after max retry")
						return
					}
				}
				fmt.Println("reconnected.")
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
					attempt++
				}
				if attempt == maxRetry {
					fmt.Println("fail to subscribe event after max retry")
					return
				}
				continue
			}
			var response result
			err = json.Unmarshal(data, &response)
			if err != nil {
				fmt.Println("failed to parse response")
				continue
			}
			if len(response.Result.Events.Fee) > 0 {
				ret <- ReCh{
					response.Result.Data.Value.Block.Header.Height,
					response.Result.Events.Fee[0],
				}
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()

	// write message to subscribe newBlock event
	if err = conn.WriteMessage(websocket.TextMessage, []byte(eventNewBlock)); err != nil {
		panic("fail to subscribe event")
	}

	// write routine sends ping messages every 10 seconds
	go writeRoutine(conn, stopInternal)
	return
}

func writeRoutine(conn *websocket.Conn, stop chan struct{}) {
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
				fmt.Println("close err:", err)
				return
			}
		}
	}

}
