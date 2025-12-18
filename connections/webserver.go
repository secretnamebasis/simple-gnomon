package connections

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"io"
	"log"
	"math/big"
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
	database map[string]*db.BboltStore
	srv      *http.Server
	mux      *http.ServeMux
	sync.RWMutex
	Writer io.WriteCloser
	Reader io.Reader
}

var WSS *WSServer = &WSServer{}

var options = &jrpc2.ServerOptions{AllowPush: true}

// let's make our own certs
//
//	"credit: https://gist.github.com/shaneutt/5e1995295cff6721c89a71d13a71c251"
func certification() {
	certificate_authority := x509.Certificate{
		SerialNumber: big.NewInt(9001),
		Subject: pkix.Name{
			Organization:  []string{"simple-gnomon"},
			Country:       []string{"DERO"},
			Province:      []string{"NETWORK1"},
			Locality:      []string{"MAINNET"},
			StreetAddress: []string{"1337 Street"},
			PostalCode:    []string{"00000"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add((time.Hour * 24) * 365),
		IsCA:      true,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageClientAuth,
			x509.ExtKeyUsageServerAuth,
		},
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	certificate_authority_priv_key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return
	}
	certificate_authority_bytes, err := x509.CreateCertificate(
		rand.Reader,
		&certificate_authority,
		&certificate_authority,
		certificate_authority_priv_key.PublicKey,
		certificate_authority_priv_key,
	)

	certificate_authority_priv_key_PEM := new(bytes.Buffer)
	pem.Encode(certificate_authority_priv_key_PEM, &pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certificate_authority_bytes,
	})

}

// Starts websocket listening for web miners
func ListenWS(databases map[string]*db.BboltStore) {

	bindAddr := "127.0.0.1:9190"

	// Err check to ensure address resolves fine
	addr, err := net.ResolveTCPAddr("tcp", bindAddr)
	if err != nil {
		log.Fatalf("[ListenWS] Error: %v", err)
	}
	_ = addr
	WSS.database = databases
	WSS.mux = http.NewServeMux()

	WSS.Lock()
	WSS.srv = &http.Server{Addr: bindAddr, Handler: WSS.mux}
	WSS.Unlock()

	// Setup handler for /ws directory which web miners will connect through
	WSS.mux.HandleFunc("/ws", WSS.wshandler)

	fmt.Printf("Starting WSServer on %v\n", bindAddr)

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

		result := wss.database[params.Tag].GetAllOwnersAndSCIDs()

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}

	case "GetLastIndexHeight": // "Gnomon.GetLastIndexHeight": handler.New(GetLastIndexHeight),
		var params *structures.GnomonAllOwnersAndSCIDsQuery
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}

		result, err := wss.database[params.Tag].GetLastIndexHeight()
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}

	case "GetTxCount": // "Gnomon.GetTxCount": handler.New(GetTxCount)
		var params *structures.GnomonTxCountQuery
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		var result int64
		switch params.Tx_Type {
		case "registration", "burn", "normal":
			result = wss.database[params.Tag].GetTxCount(params.Tx_Type)
		case "scids":
			result = int64(len(wss.database[params.Tag].GetAllOwnersAndSCIDs()))
		}

		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetOwner": // "Gnomon.GetOwner": handler.New(GetOwner)
		var params *structures.GnomonOwnerQuery
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		owner := wss.database[params.Tag].GetOwner(params.SCID)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: owner}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}

		// this isn't being stored at the moment...
	case "GetAllNormalTxWithSCIDByAddr":
		var params *structures.GnomonAllNormalTxWithSCIDByAddrQuery
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		txs := wss.database[params.Tag].GetAllNormalTxWithSCIDByAddr(params.Address)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: txs}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetAllNormalTxWithSCIDBySCID":
		var params *structures.GnomonAllNormalTxWithSCIDBySCIDQuery
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		txs := wss.database[params.Tag].GetAllNormalTxWithSCIDBySCID(params.SCID)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: txs}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetAllSCIDInvokeDetails":
		var params *structures.GnomonAllSCIDInvokeDetails
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		txs := wss.database[params.Tag].GetAllSCIDInvokeDetails(params.SCID)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: txs}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetAllSCIDInvokeDetailsByEntrypoint":
		var params *structures.GnomonAllSCIDInvokeDetailsByEntrypoint
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		txs := wss.database[params.Tag].GetAllSCIDInvokeDetailsByEntrypoint(params.SCID, params.Entrypoint)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: txs}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetAllSCIDInvokeDetailsBySigner":
		var params *structures.GnomonAllSCIDInvokeDetailsBySigner
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		txs := wss.database[params.Tag].GetAllSCIDInvokeDetailsBySigner(params.SCID, params.Signer)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: txs}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetGetInfoDetails":
		var params *structures.GnomonGetInfoParams
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		info := wss.database[params.Tag].GetGetInfoDetails()
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: info}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetSCIDVariableDetailsAtTopoheight":
		var params *structures.GnomonSCIDVariableDetailsAtTopoheight
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		vars := wss.database[params.Tag].GetSCIDVariableDetailsAtTopoheight(params.SCID, params.TopoHeight)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: vars}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetAllSCIDVariableDetails":
		var params *structures.GnomonSCIDVariableDetailsAtTopoheight
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		vars := wss.database[params.Tag].GetAllSCIDVariableDetails(params.SCID)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: vars}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetSCIDKeysByValue":
		var params *structures.GnomonSCIDKeysByValue
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		ks, ku := wss.database[params.Tag].GetSCIDKeysByValue(params.SCID, params.Value, params.Height, params.Max)
		result := &structures.GnomonSCIDKeysByValueResult{KeysString: ks, KeysUint64: ku}
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetSCIDValuesByKey":
		var params *structures.GnomonSCIDKeysByKey
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		vs, vu := wss.database[params.Tag].GetSCIDValuesByKey(params.SCID, params.Value, params.Height, params.Max)
		result := &structures.GnomonSCIDKeysByKeyResult{KeysString: vs, KeysUint64: vu}
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetLiveSCIDKeysByValue":
		var params *structures.GnomonSCIDKeysByValue
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		ks, ku := wss.database[params.Tag].GetSCIDKeysByValue(params.SCID, params.Value, 0, params.Max)
		result := &structures.GnomonSCIDKeysByValueResult{KeysString: ks, KeysUint64: ku}
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetLiveSCIDValuesByKey":
		var params *structures.GnomonSCIDKeysByKey
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		vs, vu := wss.database[params.Tag].GetSCIDValuesByKey(params.SCID, params.Value, 0, params.Max)
		result := &structures.GnomonSCIDKeysByKeyResult{KeysString: vs, KeysUint64: vu}
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetSCIDInteractionByAddr":

	case "GetSCIDInteractionHeight":
		var params *structures.GnomonAllSCIDInteractionHeight
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		interactions := wss.database[params.Tag].GetSCIDInteractionHeight(params.SCID)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: interactions}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetInteractionIndex":
		var params *structures.GnomonInteractionIndex
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		height := wss.database[params.Tag].GetInteractionIndex(params.TopoHeight, params.Heights, params.Max)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: height}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetInvalidSCIDDeploys":
		var params *structures.GnomonInteractionIndex
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		result := wss.database[params.Tag].GetInvalidSCIDDeploys()
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}

	case "GetAllMiniblockDetails":
		var params *structures.GnomonInteractionIndex
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		result := wss.database[params.Tag].GetAllMiniblockDetails()
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetMiniblockDetailsByHash":
		var params *structures.GnomonMiniblockDetailsByHash
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		result := wss.database[params.Tag].GetMiniblockDetailsByHash(params.BLID)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	case "GetMiniblockCountByAddress":
		var params *structures.GnomonMiniblockDetailsByAddress
		err = json.Unmarshal(*req.Params, &params)
		if err != nil {
			fmt.Printf("Unable to parse params\n")
			return err
		}
		result := wss.database[params.Tag].GetMiniblockCountByAddress(params.Address)
		message := &structures.JSONRpcResp{Id: req.Id, Version: "2.0", Error: nil, Result: result}
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v\n", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
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
		err = wsjson.Write(ctx, c, message)
		if err != nil {
			fmt.Printf("err writing message: err: %v", err)

			fmt.Printf("server disconnect request\n")
			return fmt.Errorf("server disconnect request")
		}
	default:
		fmt.Printf("Not login or submit method\n")

		fmt.Printf("server disconnect request\n")
		return fmt.Errorf("server disconnect request")
	}

	return err
}
