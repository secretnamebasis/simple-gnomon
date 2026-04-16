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
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/coder/websocket/wsjson"

	"github.com/creachadair/jrpc2"
	"github.com/secretnamebasis/simple-gnomon/db"
	sgs "github.com/secretnamebasis/simple-gnomon/structs"
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
	var origins []string
	for port := 8000; port <= 9000; port++ {
		origins = append(origins, fmt.Sprintf("127.0.0.1:%d", port))
	}
	for port := 8000; port <= 9000; port++ {
		origins = append(origins, fmt.Sprintf("localhost:%d", port))
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		OriginPatterns: origins,
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

	var req *sgs.JSONRpcReq
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
	m := &sgs.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil}
	if err := reply(wss.database, req, m); err != nil {
		return err
	}
	return handleMashalError(wsjson.Write(ctx, c, m))
}

func reply(d *db.BboltStore, req *sgs.JSONRpcReq, msg *sgs.JSONRpcResp) (err error) {
	// ws_methods := []string{
	// 	"GetAllOwnersAndSCIDs",                // map[scid]owner IMO, func/method name is backwards
	// 	"GetAllSCIDs",                         // []scid
	// 	"GetAllOwners",                        // []owner
	// 	"GetAllClasses",                       // []class
	// 	"GetAllTags",                          // []tag
	// 	"GetAllHeaders",                       // []header
	// 	"GetAllSCIDsAndHeaders",               // map[scid]header
	// 	"GetAllSCIDsByClass",                  // map[scid]class
	// 	"GetAllSCIDsByTag",                    // map[scid]tag
	// 	"GetLastIndexHeight",                  // height
	// 	"GetTxCount",                          // count
	// 	"GetOwner",                            // owner
	// 	"GetAllNormalTxWithSCIDByAddr",        // map[addr]NormalTXWithSCIDParse
	// 	"GetAllNormalTxWithSCIDBySCID",        // map[scid]NormalTXWithSCIDParse
	// 	"GetAllSCIDInvokeDetails",             // []SCTXParse
	// 	"GetAllSCIDInvokeDetailsByEntrypoint", // []SCTXParse
	// 	"GetAllSCIDInvokeDetailsBySigner",     // []SCTXParse
	// 	"GetGetInfoDetails",                   // info
	// 	"GetSCIDVariableDetailsAtTopoheight",  // []SCIDVariable
	// 	"GetAllSCIDVariableDetails",           // []SCIDVariable
	// 	"GetSCIDKeysByValue",                  // GnomonSCIDVarsResult
	// 	"GetSCIDValuesByKey",                  // GnomonSCIDVarsResult
	// 	"GetLiveSCIDKeysByValue",              // GnomonSCIDVarsResult
	// 	"GetLiveSCIDValuesByKey",              // GnomonSCIDVarsResult
	// 	"GetSCIDInteractionByAddr",            // []scid
	// 	"GetSCIDInteractionHeight",            // []scid
	// 	"GetInteractionIndex",                 // height
	// 	"GetInvalidSCIDDeploys",               // map[scid]height
	// 	"GetAllMiniblockDetails",              // map[addr][]MBLInfo
	// 	"GetMiniblockDetailsByHash",           // []MBLInfo
	// 	"GetMiniblockCountByAddress",          // count
	// 	"test",
	// }
	fmt.Println(req.Method)
	switch req.Method {
	case "GetAllOwnersAndSCIDs":
		msg.Result = d.GetAllOwnersAndSCIDs()
	case "GetAllSCIDs":
		msg.Result = d.GetAllSCIDs()
	case "GetAllOwners":
		msg.Result = d.GetAllOwners()
	case "GetAllClasses":
		msg.Result = d.GetAllClasses()
	case "GetAllTags":
		msg.Result = d.GetAllTags()
	case "GetAllHeaders":
		msg.Result = d.GetAllHeaders()
	case "GetAllSCIDsAndHeaders":
		msg.Result = d.GetAllSCIDsAndHeaders()
	case "GetAllSCIDsByClass":
		var params *sgs.GnomonClassQuery
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		scids := d.GetAllSCIDsByClass(params.Class)
		list := []string{}
		for _, each := range d.GetAllSCIDs() { // these are in height order
			if slices.Contains(scids, each) {
				list = append(list, each)
			}
		}
		msg.Result = list
	case "GetAllSCIDsByTag":
		var params *sgs.GnomonTagQuery
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		scids := d.GetAllSCIDsByTag(params.Tag)
		list := []string{}
		for _, each := range d.GetAllSCIDs() { // these are in height order
			if slices.Contains(scids, each) {
				list = append(list, each)
			}
		}
		msg.Result = list
	case "GetLastIndexHeight":
		var h int64
		h, err = d.GetLastIndexHeight()
		if err != nil {
			msg.Error = errDisconnected
			break
		}
		msg.Result = h
	case "GetTxCount":
		var params *sgs.GnomonTxCountQuery
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		var result int64
		switch params.Tx_Type {
		case "registration", "burn", "normal":
			result = d.GetTxCount(params.Tx_Type)
		case "scids":
			result = int64(len(d.GetAllOwnersAndSCIDs()))
		}
		msg.Result = result
	case "GetOwner":
		var params *sgs.GnomonSCIDQuery
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetOwner(params.SCID)
	case "GetAllNormalTxWithSCIDByAddr":
		var params *sgs.GnomonAddressQuery
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetAllNormalTxWithSCIDByAddr(params.Address)
	case "GetAllNormalTxWithSCIDBySCID":
		var params *sgs.GnomonSCIDQuery
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetAllNormalTxWithSCIDBySCID(params.SCID)
	case "GetAllSCIDInvokeDetails":
		var params *sgs.GnomonSCIDQuery
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetAllSCIDInvokeDetails(params.SCID)
	case "GetAllSCIDInvokeDetailsByEntrypoint":
		var params *sgs.GnomonAllSCIDInvokeDetailsByEntrypoint
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetAllSCIDInvokeDetailsByEntrypoint(params.SCID, params.Entrypoint)
	case "GetAllSCIDInvokeDetailsBySigner":
		var params *sgs.GnomonAllSCIDInvokeDetailsBySigner
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetAllSCIDInvokeDetailsBySigner(params.SCID, params.Signer)
	case "GetGetInfoDetails":
		msg.Result = d.GetGetInfoDetails()
	case "GetSCIDVariableDetailsAtTopoheight":
		var params *sgs.GnomonSCIDVariableDetailsAtTopoheight
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetSCIDVariableDetailsAtTopoheight(params.SCID, params.TopoHeight)
	case "GetAllSCIDVariableDetails":
		var params *sgs.GnomonSCIDQuery
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		vars := d.GetAllSCIDVariableDetails(params.SCID)
		if vars == nil {
			msg.Error = fmt.Errorf("vars are nil")
			break
		}
		msg.Result = vars
	case "GetSCIDKeysByValue":
		var params *sgs.GnomonSCIDVarsParams
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		ks, ku := d.GetSCIDKeysByValue(params.SCID, params.Value, params.Height)
		result := &sgs.GnomonSCIDVarsResult{KeysString: ks, KeysUint64: ku}
		msg.Result = result
	case "GetSCIDValuesByKey":
		var params *sgs.GnomonSCIDVarsParams
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		vs, vu := d.GetSCIDValuesByKey(params.SCID, params.Value, params.Height)
		result := &sgs.GnomonSCIDVarsResult{KeysString: vs, KeysUint64: vu}
		msg.Result = result
	case "GetLiveSCIDKeysByValue":
		var params *sgs.GnomonSCIDVarsParams
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		height, err := Get_TopoHeight()
		if err != nil {
			msg.Error = err
			break
		}
		ks, ku := d.GetSCIDKeysByValue(params.SCID, params.Value, height)
		result := &sgs.GnomonSCIDVarsResult{KeysString: ks, KeysUint64: ku}
		msg.Result = result
	case "GetLiveSCIDValuesByKey":
		var params *sgs.GnomonSCIDVarsParams
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		height, err := Get_TopoHeight()
		if err != nil {
			msg.Error = err
			break
		}
		vs, vu := d.GetSCIDValuesByKey(params.SCID, params.Value, height)
		result := &sgs.GnomonSCIDVarsResult{KeysString: vs, KeysUint64: vu}
		msg.Result = result
	case "GetSCIDInteractionByAddr":
		var params *sgs.GnomonAddressQuery
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetSCIDInteractionByAddr(params.Address)
	case "GetSCIDInteractionHeight":
		var params *sgs.GnomonSCIDQuery
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetSCIDInteractionHeight(params.SCID)
	case "GetInteractionIndex":
		var params *sgs.GnomonInteractionIndex
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetInteractionIndex(params.TopoHeight, params.Heights)
	case "GetInvalidSCIDDeploys":
		var params *sgs.GnomonInteractionIndex
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetInvalidSCIDDeploys()
	case "GetAllMiniblockDetails":
		var params *sgs.GnomonInteractionIndex
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetAllMiniblockDetails()
	case "GetMiniblockDetailsByHash":
		var params *sgs.GnomonMiniblockDetailsByHash
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetMiniblockDetailsByHash(params.BLID)
	case "GetMiniblockCountByAddress":
		var params *sgs.GnomonAddressQuery
		if err = json.Unmarshal(*req.Params, &params); err != nil {
			msg.Error = err
			break
		}
		msg.Result = d.GetMiniblockCountByAddress(params.Address)
	case "test":
		msg.Result = "test"
	default:
		fmt.Printf("Not login or submit method\n")
		fmt.Printf("server disconnect request\n")
		return errDisconnected
	}
	return nil
}
