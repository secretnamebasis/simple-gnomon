package structures

import (
	"encoding/json"

	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
)

type (
	SearchFilter struct {
		Name  string
		Terms []string
	}

	SCIDToIndexStage struct {
		SCTXParse
		Headers string
		ScVars  []*SCIDVariable
		ScCode  string
		Class   string
		Tags    string
	}

	MBLInfo struct {
		Hash  string
		Miner string
	}
	NormalTXWithSCIDParse struct {
		Txid   string
		Scid   string
		Fees   uint64
		Height int64
	}

	SCTXParse struct {
		Txid       string
		Scid       string
		Entrypoint string
		Method     string
		Sc_args    rpc.Arguments
		Sender     string
		Payloads   []transaction.AssetPayload
		Fees       uint64
		Height     int64
	}

	SCIDVariable struct {
		Key   any
		Value any
	}

	GnomonSCIDVarsParams struct {
		SCID   string
		Value  any
		Height int64
		Max    bool
	}

	GnomonSCIDVarsResult struct {
		KeysString []string
		KeysUint64 []uint64
	}

	GnomonAddressQuery struct {
		Address string
	}

	GnomonSCIDQuery struct {
		SCID string
	}

	GnomonClassQuery struct {
		Class string
	}

	GnomonTagQuery struct {
		Tag string
	}

	GnomonAllSCIDInvokeDetailsByEntrypoint struct {
		SCID       string
		Entrypoint string
	}

	GnomonSCIDVariableDetailsAtTopoheight struct {
		SCID       string
		TopoHeight int64
	}

	GnomonInteractionIndex struct {
		Heights    []int64
		TopoHeight int64
		Max        bool
	}

	GnomonAllSCIDInvokeDetailsBySigner struct {
		SCID   string
		Signer string
	}

	GnomonTxCountQuery struct {
		Tx_Type string
	}

	GnomonMiniblockDetailsByHash struct {
		BLID string
	}

	JSONRpcReq struct {
		Id     *json.RawMessage `json:"id"`
		Method string           `json:"method"`
		Params *json.RawMessage `json:"params"`
	}

	JSONRpcResp struct {
		Id      *json.RawMessage `json:"id"`
		Version string           `json:"jsonrpc"`
		Result  interface{}      `json:"result"`
		Error   interface{}      `json:"error"`
	}
)
