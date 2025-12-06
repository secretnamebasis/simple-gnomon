package connections

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/walletapi"
	"github.com/sirupsen/logrus"
	"github.com/ybbus/jsonrpc"
)

var Logger logrus.Logger

type WalletConn struct {
	Api  string
	User string
	Pass string
}

type WebAPIConn struct {
	Api    string
	User   string
	Wallet string
	Api_id string
}

func getClient() jsonrpc.RPCClient {
	opts := &jsonrpc.RPCClientOpts{
		CustomHeaders: map[string]string{
			"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("secret:pass")),
		},
	}
	return jsonrpc.NewClientWithOpts("http://127.0.0.1:10103/json_rpc", opts)
}

func getNewClientWithOpts() (jsonrpc.RPCClient, context.Context, context.CancelFunc) {
	opts := &jsonrpc.RPCClientOpts{
		CustomHeaders: map[string]string{
			"Authorization": "Basic " + base64.StdEncoding.EncodeToString([]byte("secret:pass")),
		},
	}
	client := jsonrpc.NewClientWithOpts("http://127.0.0.1:10103/json_rpc", opts)
	ctx, cancel := context.WithTimeout(context.Background(), timeout*time.Second)

	return client, ctx, cancel
}

// a simple way to convert units
const atomic_units = 100000

// simple way to set file permissions
const default_file_permissions = 0644

// simple way to set dismiss
const dismiss = `dismiss`

// simple way to set confirm
const confirm = `confirm`

// simple way to set timeouts
const timeout = time.Second * 9    // the world is a really big place
const deadline = time.Second * 300 // some content is just bigger

// simple way to identify gnomon
//const gnomonSC = `a05395bb0cf77adc850928b0db00eb5ca7a9ccbafd9a38d021c8d299ad5ce1a4`

func callRPC[t any](method string, params any, validator func(t) bool) t {
	result, err := handleResult[t](method, params)
	if err != nil {
		//	log.Fatal(err)
		var zero t
		return zero
	}

	if !validator(result) {
		Logger.Error(errors.New("failed validation"), method)
		var zero t
		return zero
	}

	return result
}

var RpcClient jsonrpc.RPCClient

func handleResult[T any](method string, params any) (T, error) {
	var result T
	//var ctx context.Context

	var cancel context.CancelFunc
	_, cancel = context.WithTimeout(context.Background(), timeout)
	if method == "DERO.GetSC" {
		_, cancel = context.WithDeadline(context.Background(), time.Now().Add(deadline))
	}
	defer cancel()

	var err error
	if params == nil {
		err = RpcClient.CallFor(&result, method) // no params argument
	} else {
		err = RpcClient.CallFor(&result, method, params)
	}

	if err != nil {
		log.Fatal(err)
		var zero T
		return zero, err
	}

	return result, nil
}
func Get_TopoHeight() int64 {
	validator := func(r rpc.GetInfo_Result) bool {
		return r.TopoHeight != 0
	}
	result := callRPC("DERO.GetInfo", nil, validator)
	return result.TopoHeight
}
func GetTransaction(params rpc.GetTransaction_Params) rpc.GetTransaction_Result {
	validator := func(r rpc.GetTransaction_Result) bool {
		return r.Status != ""
	}
	result := callRPC("DERO.GetTransaction", params, validator)
	return result
}

func GetBlockInfo(params rpc.GetBlock_Params) rpc.GetBlock_Result {
	validator := func(r rpc.GetBlock_Result) bool {
		return r.Block_Header.Depth != 0
	}
	result := callRPC("DERO.GetBlock", params, validator)
	return result
}

func GetTxPool() rpc.GetTxPool_Result {
	validator := func(r rpc.GetTxPool_Result) bool {
		return r.Status != ""
	}
	result := callRPC("DERO.GetTxPool", nil, validator)
	return result
}

func GetDaemonInfo() rpc.GetInfo_Result {
	validator := func(r rpc.GetInfo_Result) bool {
		return r.TopoHeight != 0
	}
	result := callRPC("DERO.GetInfo", nil, validator)
	return result
}

func GetSC(scParam rpc.GetSC_Params) rpc.GetSC_Result {
	validator := func(r rpc.GetSC_Result) bool {
		if scParam.Code {
			return r.Code != ""
		}
		return true
	}
	result := callRPC("DERO.GetSC", scParam, validator)
	return result
}

func GetSCCode(scid string) rpc.GetSC_Result {
	return GetSC(rpc.GetSC_Params{
		SCID:       scid,
		Code:       true,
		Variables:  false,
		TopoHeight: walletapi.Get_Daemon_Height(),
	})
}

func GetSCValues(scid string) rpc.GetSC_Result {
	return GetSC(rpc.GetSC_Params{
		SCID:       scid,
		Code:       false,
		Variables:  true,
		TopoHeight: walletapi.Get_Daemon_Height(),
	})
}

func GetSCNameFromVars(keys map[string]interface{}) string {
	var text string

	for k, v := range keys {
		key := strings.ToLower(k)
		if key != "name" || //explicit check
			// infer a name
			!strings.Contains(key, "name") ||
			!strings.HasSuffix(key, "name") ||
			!strings.HasPrefix(key, "name") {

			continue
		}

		str, ok := v.(string)
		if !ok {
			continue
		}
		b, e := hex.DecodeString(str)
		if e != nil {
			continue // what else can we do ?
		}
		text = string(b)
	}
	if text == "" {
		return "N/A"
	}
	return text
}
func GetSCDescriptionFromVars(keys map[string]interface{}) string {
	var text string

	for k, v := range keys {
		if !strings.Contains(k, "description") {
			continue
		}
		b, e := hex.DecodeString(v.(string))
		if e != nil {
			continue // what else can we do ?
		}
		text = string(b)
	}
	if text == "" {
		return "N/A"
	}
	return text
}

func GetSCIDImageURLFromVars(keys map[string]interface{}) string {
	var text string

	for k, v := range keys {
		if !strings.Contains(k, "imageurl") {
			continue
		}
		if _, ok := v.(string); !ok {
			continue
		}
		b, e := hex.DecodeString(v.(string))
		if e != nil {
			continue // what else can we do ?
		}
		text = string(b)
	}
	if text == "" {
		return "null"
	}
	u, err := url.Parse(text)
	if err != nil {
		return "null"
	}
	return u.String()
}
func GetBlockDeserialized(blob string) block.Block {

	var bl block.Block
	b, err := hex.DecodeString(blob)
	if err != nil {
		// should probably log or handle this error
		fmt.Println(err.Error())
		return block.Block{}
	}
	bl.Deserialize(b)
	return bl
}
