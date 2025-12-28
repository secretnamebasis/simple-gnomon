package connections

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/coder/websocket/wsjson"

	"github.com/creachadair/jrpc2"
	"github.com/secretnamebasis/simple-gnomon/db"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

var ioTimeout = flag.Duration("io_timeout", time.Millisecond*100, "i/o operations timeout")

type WSServer struct {
	database *db.BboltStore
	srv      *http.Server
	mux      *http.ServeMux
	sync.RWMutex
	Writer io.WriteCloser
	Reader io.Reader
}

var WSS *WSServer = &WSServer{}

var options = &jrpc2.ServerOptions{AllowPush: true}

// Starts websocket listening for web miners
func ListenWS(ctx context.Context, databases *db.BboltStore, address string) {

	// srvTLS, clientTLS, err := certification()
	// if err != nil {
	// 	log.Fatal(err)
	// }

	bindAddr := "127.0.0.1:9190"

	// Err check to ensure address resolves fine
	addr, err := net.ResolveTCPAddr("tcp", address)
	if err != nil {
		log.Fatalf("[ListenWS] Error: %v", err)
	}
	_ = addr
	WSS.database = databases
	WSS.mux = http.NewServeMux()

	WSS.Lock()
	WSS.srv = &http.Server{Addr: address, Handler: WSS.mux}
	// WSS.srv = &http.Server{Addr: address, Handler: WSS.mux, TLSConfig: srvTLS}
	WSS.Unlock()

	// Setup handler for /ws directory which web miners will connect through
	WSS.mux.HandleFunc("/ws", WSS.wshandler)

	fmt.Printf("Starting WSServer on %v\n", address)

	// err = WSS.srv.ListenAndServeTLS("", "")
	err = WSS.srv.ListenAndServe()
	if err != nil {
		log.Fatalf("[ListenWS] Failed to start WSServer: %v", err)
	}
}

func (wss *WSServer) wshandler(w http.ResponseWriter, r *http.Request) {

	// TODO - do we need to implement the maximum connections here as well? - need upper end testing/stability confirmation
	// TODO - ensure you add the originpatters allowed for api urls, miner urls etc. as needed. Perhaps defined within config.json instead

	var err error
	// fmt.Printf("%v\n", w.Header())
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		//OriginPatterns: []string{"127.0.0.1:9090", "127.0.0.1:8080"},
	})
	if err != nil {
		fmt.Printf("[wshandler] Err on connection being established. %v\n", err)
		return
	}

	defer conn.Close(websocket.StatusInternalError, "[wshandler] Disconnected")

	for {
		// log.Printf("[wshandler] Handling client...")
		err = wss.wsHandleClient(r.Context(), conn, r)
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
			fmt.Printf("[wshandler] Websocket close status: %v\n", websocket.CloseStatus(err))
			return
		}
		if err != nil {
			fmt.Printf("[wshandler] Disconnected %v: %v\n", r.RemoteAddr, err)
			return
		}
	}
}

var errDisconnected = fmt.Errorf("server disconnect request")

func handleMashalError(err error) error {
	if err != nil {
		fmt.Printf("err writing message: err: %v\n", err)

		fmt.Printf("server disconnect request\n")
		return errDisconnected
	}
	return nil
}

// func handleUnmarshalError(err error) error {}
func (wss *WSServer) wsHandleClient(ctx context.Context, c *websocket.Conn, request *http.Request) error {
	var err error

	var req *structures.JSONRpcReq
	// log.Printf("Reader")
	// TODO: If we can't guarantee that it's a json buffer, reader hangs until client-side WS disconnects
	err = wsjson.Read(ctx, c, &req)
	if err != nil {
		if err == io.EOF {
			fmt.Printf("io.EOF - disconnected\n")
		}

		return err
	}

	switch req.Method {
	case "GetAllOwnersAndSCIDs":
		var params *structures.GnomonAllOwnersAndSCIDsQuery

		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}

		result := wss.database.GetAllOwnersAndSCIDs()

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetLastIndexHeight":
		var params *structures.GnomonAllOwnersAndSCIDsQuery
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}

		result, err := wss.database.GetLastIndexHeight()
		if err != nil {
			fmt.Printf("err geting last indexed height: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return errDisconnected
		}
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetTxCount":
		var params *structures.GnomonTxCountQuery
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		var result int64
		switch params.Tx_Type {
		case "registration", "burn", "normal":
			result = wss.database.GetTxCount(params.Tx_Type)
		case "scids":
			result = int64(len(wss.database.GetAllOwnersAndSCIDs()))
		}

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetOwner":
		var params *structures.GnomonOwnerQuery
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		owner := wss.database.GetOwner(params.SCID)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: owner}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetAllNormalTxWithSCIDByAddr":
		var params *structures.GnomonAllNormalTxWithSCIDByAddrQuery
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		txs := wss.database.GetAllNormalTxWithSCIDByAddr(params.Address)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: txs}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetAllNormalTxWithSCIDBySCID":
		var params *structures.GnomonAllNormalTxWithSCIDBySCIDQuery
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		txs := wss.database.GetAllNormalTxWithSCIDBySCID(params.SCID)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: txs}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetAllSCIDInvokeDetails":
		var params *structures.GnomonAllSCIDInvokeDetails
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		txs := wss.database.GetAllSCIDInvokeDetails(params.SCID)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: txs}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetAllSCIDInvokeDetailsByEntrypoint":
		var params *structures.GnomonAllSCIDInvokeDetailsByEntrypoint
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		txs := wss.database.GetAllSCIDInvokeDetailsByEntrypoint(params.SCID, params.Entrypoint)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: txs}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetAllSCIDInvokeDetailsBySigner":
		var params *structures.GnomonAllSCIDInvokeDetailsBySigner
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		txs := wss.database.GetAllSCIDInvokeDetailsBySigner(params.SCID, params.Signer)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: txs}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetGetInfoDetails":
		var params *structures.GnomonGetInfoParams
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		info := wss.database.GetGetInfoDetails()
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: info}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetSCIDVariableDetailsAtTopoheight":
		var params *structures.GnomonSCIDVariableDetailsAtTopoheight
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		vars := wss.database.GetSCIDVariableDetailsAtTopoheight(params.SCID, params.TopoHeight)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: vars}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetAllSCIDVariableDetails":
		var params *structures.GnomonSCIDVariableDetailsAtTopoheight
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		vars := wss.database.GetAllSCIDVariableDetails(params.SCID)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: vars}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetSCIDKeysByValue":
		var params *structures.GnomonSCIDKeysByValue
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		ks, ku := wss.database.GetSCIDKeysByValue(params.SCID, params.Value, params.Height, params.Max)
		result := &structures.GnomonSCIDKeysByValueResult{KeysString: ks, KeysUint64: ku}
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetSCIDValuesByKey":
		var params *structures.GnomonSCIDKeysByKey
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		vs, vu := wss.database.GetSCIDValuesByKey(params.SCID, params.Value, params.Height, params.Max)
		result := &structures.GnomonSCIDKeysByKeyResult{KeysString: vs, KeysUint64: vu}
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetLiveSCIDKeysByValue":
		var params *structures.GnomonSCIDKeysByValue
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		ks, ku := wss.database.GetSCIDKeysByValue(params.SCID, params.Value, 0, params.Max)
		result := &structures.GnomonSCIDKeysByValueResult{KeysString: ks, KeysUint64: ku}
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetLiveSCIDValuesByKey":
		var params *structures.GnomonSCIDKeysByKey
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		vs, vu := wss.database.GetSCIDValuesByKey(params.SCID, params.Value, 0, params.Max)
		result := &structures.GnomonSCIDKeysByKeyResult{KeysString: vs, KeysUint64: vu}
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetSCIDInteractionByAddr":
		var params *structures.GnomonAllSCIDInteractionAddr
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		interactions := wss.database.GetSCIDInteractionByAddr(params.Addr)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: interactions}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetSCIDInteractionHeight":
		var params *structures.GnomonAllSCIDInteractionHeight
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		interactions := wss.database.GetSCIDInteractionHeight(params.SCID)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: interactions}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetInteractionIndex":
		var params *structures.GnomonInteractionIndex
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		height := wss.database.GetInteractionIndex(params.TopoHeight, params.Heights, params.Max)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: height}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetInvalidSCIDDeploys":
		var params *structures.GnomonInteractionIndex
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		result := wss.database.GetInvalidSCIDDeploys()
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetAllMiniblockDetails":
		var params *structures.GnomonInteractionIndex
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		result := wss.database.GetAllMiniblockDetails()
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetMiniblockDetailsByHash":
		var params *structures.GnomonMiniblockDetailsByHash
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		result := wss.database.GetMiniblockDetailsByHash(params.BLID)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "GetMiniblockCountByAddress":
		var params *structures.GnomonMiniblockDetailsByAddress
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		result := wss.database.GetMiniblockCountByAddress(params.Address)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		return handleMashalError(wsjson.Write(ctx, c, message))
	case "test":
		var params *structures.GnomonSCIDQuery

		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}

		log.Printf("Method: %v", req.Method)
		log.Printf("GnomonSCIDQuery: %v", params)

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: "test"}
		return handleMashalError(wsjson.Write(ctx, c, message))
	default:
		fmt.Printf("Not login or submit method\n")

		fmt.Printf("server disconnect request\n")
		return errDisconnected
	}

	// return err
}
