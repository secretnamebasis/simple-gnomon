package connections

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
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
	go func() {
		<-ctx.Done()
		if err := WSS.srv.Shutdown(ctx); err != nil {
			fmt.Println("error shutting down server", err)
			return
		}
	}()
	// err = WSS.srv.ListenAndServeTLS("", "")
	err = WSS.srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("[ListenWS] Failed to start WSServer: %v", err)
	}

	log.Println("WS server stopped cleanly")
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

		if err == nil {
			continue
		}

		if isNormalWSDisconnect(err) {
			return
		}

		fmt.Printf("[wshandler] Disconnected %v: %v\n", r.RemoteAddr, err)
	}
}

func isNormalWSDisconnect(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, context.Canceled) {
		return true
	}

	if errors.Is(err, io.EOF) {
		return true
	}

	if errors.Is(err, net.ErrClosed) {
		return true
	}

	switch websocket.CloseStatus(err) {
	case websocket.StatusNormalClosure, websocket.StatusGoingAway:
		return true
	}

	// Fallback for wrapped EOFs
	if strings.Contains(err.Error(), "EOF") {
		return true
	}

	return false
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
	go func() {
		<-ctx.Done()
		_ = c.Close(websocket.StatusGoingAway, "server shutdown")
	}()
	var err error

	var req *structures.JSONRpcReq
	// log.Printf("Reader")
	// TODO: If we can't guarantee that it's a json buffer, reader hangs until client-side WS disconnects
	err = wsjson.Read(ctx, c, &req)
	if err != nil {
		if err == io.EOF {
			fmt.Printf("io.EOF - disconnected\n")
		}
		if errors.Is(err, context.Canceled) {
			return err
		}
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure ||
			websocket.CloseStatus(err) == websocket.StatusGoingAway {
			return err
		}
		if errors.Is(err, net.ErrClosed) ||
			strings.Contains(err.Error(), "use of closed network connection") {
			return err
		}

		return err
	}
	var message = &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil}
	switch req.Method {
	case "GetAllOwnersAndSCIDs":
		message.Result = wss.database.GetAllOwnersAndSCIDs()
	case "GetAllSCIDs":
		message.Result = wss.database.GetAllSCIDs()
	case "GetAllOwners":
		message.Result = wss.database.GetAllOwners()
	case "GetAllClasses":
		message.Result = wss.database.GetAllClasses()
	case "GetAllTags":
		message.Result = wss.database.GetAllTags()
	case "GetAllHeaders":
		message.Result = wss.database.GetAllHeaders()
	case "GetAllSCIDsAndHeaders":
		message.Result = wss.database.GetAllSCIDsAndHeaders()
	case "GetAllSCIDsByClass":
		var params *structures.GnomonClassQuery
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetAllSCIDsByClass(params.Class)
	case "GetAllSCIDsByTag":
		var params *structures.GnomonTagQuery
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetAllSCIDsByTag(params.Tag)
	case "GetLastIndexHeight":
		result, err := wss.database.GetLastIndexHeight()
		if err != nil {
			message.Error = errDisconnected
			break
		}
		message.Result = result
	case "GetTxCount":
		var params *structures.GnomonTxCountQuery
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		var result int64
		switch params.Tx_Type {
		case "registration", "burn", "normal":
			result = wss.database.GetTxCount(params.Tx_Type)
		case "scids":
			result = int64(len(wss.database.GetAllOwnersAndSCIDs()))
		}
		message.Result = result
	case "GetOwner":
		var params *structures.GnomonSCIDQuery
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetOwner(params.SCID)
	case "GetAllNormalTxWithSCIDByAddr":
		var params *structures.GnomonAddressQuery
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetAllNormalTxWithSCIDByAddr(params.Address)
	case "GetAllNormalTxWithSCIDBySCID":
		var params *structures.GnomonSCIDQuery
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetAllNormalTxWithSCIDBySCID(params.SCID)
	case "GetAllSCIDInvokeDetails":
		var params *structures.GnomonSCIDQuery
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetAllSCIDInvokeDetails(params.SCID)
	case "GetAllSCIDInvokeDetailsByEntrypoint":
		var params *structures.GnomonAllSCIDInvokeDetailsByEntrypoint
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetAllSCIDInvokeDetailsByEntrypoint(params.SCID, params.Entrypoint)
	case "GetAllSCIDInvokeDetailsBySigner":
		var params *structures.GnomonAllSCIDInvokeDetailsBySigner
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetAllSCIDInvokeDetailsBySigner(params.SCID, params.Signer)
	case "GetGetInfoDetails":
		message.Result = wss.database.GetGetInfoDetails()
	case "GetSCIDVariableDetailsAtTopoheight":
		var params *structures.GnomonSCIDVariableDetailsAtTopoheight
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetSCIDVariableDetailsAtTopoheight(params.SCID, params.TopoHeight)
	case "GetAllSCIDVariableDetails":
		var params *structures.GnomonSCIDQuery
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		vars := wss.database.GetAllSCIDVariableDetails(params.SCID)
		if vars == nil {
			message.Error = fmt.Errorf("vars are nil")
			break
		}
		message.Result = vars
	case "GetSCIDKeysByValue":
		var params *structures.GnomonSCIDKeysByValue
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		ks, ku := wss.database.GetSCIDKeysByValue(params.SCID, params.Value, params.Height, params.Max)
		result := &structures.GnomonSCIDKeysByValueResult{KeysString: ks, KeysUint64: ku}
		message.Result = result
	case "GetSCIDValuesByKey":
		var params *structures.GnomonSCIDKeysByKey
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		vs, vu := wss.database.GetSCIDValuesByKey(params.SCID, params.Value, params.Height, params.Max)
		result := &structures.GnomonSCIDKeysByKeyResult{KeysString: vs, KeysUint64: vu}
		message.Result = result
	case "GetLiveSCIDKeysByValue":
		var params *structures.GnomonSCIDKeysByValue
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		ks, ku := wss.database.GetSCIDKeysByValue(params.SCID, params.Value, 0, params.Max)
		result := &structures.GnomonSCIDKeysByValueResult{KeysString: ks, KeysUint64: ku}
		message.Result = result
	case "GetLiveSCIDValuesByKey":
		var params *structures.GnomonSCIDKeysByKey
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		vs, vu := wss.database.GetSCIDValuesByKey(params.SCID, params.Value, 0, params.Max)
		result := &structures.GnomonSCIDKeysByKeyResult{KeysString: vs, KeysUint64: vu}
		message.Result = result
	case "GetSCIDInteractionByAddr":
		var params *structures.GnomonAddressQuery
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetSCIDInteractionByAddr(params.Address)
	case "GetSCIDInteractionHeight":
		var params *structures.GnomonSCIDQuery
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetSCIDInteractionHeight(params.SCID)
	case "GetInteractionIndex":
		var params *structures.GnomonInteractionIndex
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetInteractionIndex(params.TopoHeight, params.Heights, params.Max)
	case "GetInvalidSCIDDeploys":
		var params *structures.GnomonInteractionIndex
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetInvalidSCIDDeploys()
	case "GetAllMiniblockDetails":
		var params *structures.GnomonInteractionIndex
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetAllMiniblockDetails()
	case "GetMiniblockDetailsByHash":
		var params *structures.GnomonMiniblockDetailsByHash
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetMiniblockDetailsByHash(params.BLID)
	case "GetMiniblockCountByAddress":
		var params *structures.GnomonAddressQuery
		if err := json.Unmarshal(*req.Params, &params); err != nil {
			message.Error = err
			break
		}
		message.Result = wss.database.GetMiniblockCountByAddress(params.Address)
	case "test":
		message.Result = "test"
	default:
		fmt.Printf("Not login or submit method\n")
		fmt.Printf("server disconnect request\n")
		return errDisconnected
	}

	return handleMashalError(wsjson.Write(ctx, c, message))

	// return err
}
