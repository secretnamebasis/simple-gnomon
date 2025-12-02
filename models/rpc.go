package rpc

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"log"
	"net/http"
	"strings"
	"time"

	"fyne.io/fyne/v2/storage"
	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/walletapi"
	"github.com/sirupsen/logrus"
	"github.com/ybbus/jsonrpc"
)

var Logger logrus.Logger
var daemon = "node.derofoundation.org:11012"

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

func handleResult[T any](method string, params any) (T, error) {
	var result T
	//var ctx context.Context

	var cancel context.CancelFunc
	var rpcClient jsonrpc.RPCClient
	_, cancel = context.WithTimeout(context.Background(), timeout)
	if method == "DERO.GetSC" {
		_, cancel = context.WithDeadline(context.Background(), time.Now().Add(deadline))
	}
	defer cancel()

	rpcClient = jsonrpc.NewClient("http://" + daemon + "/json_rpc")

	var err error
	if params == nil {
		err = rpcClient.CallFor(&result, method) // no params argument
	} else {
		err = rpcClient.CallFor(&result, method, params)
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

func GetSCIDImage(keys map[string]interface{}) image.Image {
	for k, v := range keys {
		if !strings.Contains(k, "image") && !strings.Contains(k, "icon") {
			continue
		}
		encoded := v.(string)
		b, e := hex.DecodeString(encoded)
		if e != nil {
			Logger.Error(e, encoded)
			continue
		}
		value := string(b)
		Logger.Info("scid", "key", k, "value", value)
		uri, err := storage.ParseURI(value)
		if err != nil {
			Logger.Error(err, value)
			return nil
		} else {
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			req, err := http.NewRequestWithContext(ctx, "GET", uri.String(), nil)
			if err != nil {
				Logger.Error(err, "get error")
				return nil
			}
			client := http.DefaultClient
			resp, err := client.Do(req)
			if err != nil || resp.StatusCode != http.StatusOK {
				return nil
			} else {
				defer resp.Body.Close()
				i, _, err := image.Decode(resp.Body)
				if err != nil {
					return nil
				}
				return i
			}
		}
	}
	return nil
}
func GetSCNameFromVars(keys map[string]interface{}) string {
	var text string

	for k, v := range keys {
		if !strings.Contains(k, "name") {
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
