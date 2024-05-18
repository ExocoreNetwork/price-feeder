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

func Subscriber(remoteAddr string, endpoint string) (ret chan ReCh) {
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
	rHeader := http.Header{}

	conn, _, err := dialer.Dial("ws://"+u.Host+endpoint, rHeader)

	if err != nil {
		panic(err)
	}

	stop := make(chan struct{})
	ret = make(chan ReCh)

	// read
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
				// TODO: reconnect
				fmt.Println("read err")
				panic(err)
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

	_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"jsonrpc":"2.0","method":"subscribe","id":0,"params":{"query":"tm.event='NewBlock'"}}`))
	// write
	go func() {
		ticker := time.NewTicker(10 * time.Second)
		defer func() {
			ticker.Stop()
			//			conn.Close()
		}()

		for {
			select {
			case <-ticker.C:
				if err := conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
					fmt.Println("write-tic-err")
					panic(err)
				}
			case <-stop:
				if err := conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
				); err != nil {
					fmt.Println("write-stop-err")
					panic(err)
				}
			}
		}
	}()
	return
}
