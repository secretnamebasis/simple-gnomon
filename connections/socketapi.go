package connections

import (
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/deroproject/derohe/walletapi/xswd"
	"github.com/gorilla/websocket"
)

var Conn *websocket.Conn

func Set_ws_conn() {
	websocket_endpoint := "ws://127.0.0.1:44326/xswd"
	var err error

	fmt.Printf("Connecting to %s\n", websocket_endpoint)

	dialer := websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // allow self-signed certs
	}

	Conn, _, err = dialer.Dial(websocket_endpoint, nil)
	if err != nil {
		panic(err)
	}
	fmt.Println("WebSocket connected")
	appData := xswd.ApplicationData{}
	signature := `-----BEGIN DERO SIGNED MESSAGE-----
Address: dero1qyc96tgvz8fz623snpfwjgdhlqznamcsuh8rahrh2yvsf2gqqxdljqg9a9kka
C: dff7a5c2bfea7fc232cf92fad5e0db205717ab81d246fb283c59c07b733123d
S: 1f88f595909431546b518651796f1d47c4bf60e65167ac589ce8ef9a8d74684

NGUzZWQxMDA5OWQ5ZWVjOTc2NzUyNDQwMjE2NGI5NzUwNmNjMGQ3NzJmMmU2NTdl
MzE3MzYxNTI1NTQ3M2VlOQ==
-----END DERO SIGNED MESSAGE-----`
	appData.Signature = []byte(signature)
	appData.Name = "simple-gnomon-lite"
	appData.Description = "indexing the blockchain is fun!"
	appData.Url = "http://localhost:8080"
	appData.Permissions = map[string]xswd.Permission{}
	appData.Id = "4e3ed10099d9eec9767524402164b97506cc0d772f2e657e3173615255473ee9"
	if err := Conn.WriteJSON(appData); err != nil {
		panic(err)
	}
	fmt.Println("Auth handshake sent")

	_, msg, err := Conn.ReadMessage()
	if err != nil {
		panic(err)
	}

	var res xswd.AuthorizationResponse
	if err := json.Unmarshal(msg, &res); err != nil {
		panic(err)
	}
	if !res.Accepted {
		panic(errors.New("app not accepted"))
	}
}

func postBytes(b []byte) []byte {

	err := Conn.WriteMessage(websocket.TextMessage, b)
	if err != nil {
		panic(err)
	}

	_, msg, err := Conn.ReadMessage()
	if err != nil {
		panic(err)
	}
	return msg
}

func GetDaemonEndpoint() xswd.GetDaemon_Result {

	// fmt.Println(estimate)
	payload := map[string]any{
		"jsonrpc": "2.0",
		"id":      "GetDaemon",
		"method":  "GetDaemon",
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return xswd.GetDaemon_Result{}
	}

	var r xswd.RPCResponse
	if err := json.Unmarshal(postBytes(jsonBytes), &r); err != nil {
		return xswd.GetDaemon_Result{}
	}

	raw, err := json.Marshal(r.Result)
	if err != nil {
		return xswd.GetDaemon_Result{}
	}

	result := xswd.GetDaemon_Result{}
	if err := json.Unmarshal(raw, &result); err != nil {
		return xswd.GetDaemon_Result{}
	}

	return result

}
