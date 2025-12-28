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

	GnomonSCIDKeysByKey struct {
		SCID   string
		Value  any
		Height int64
		Max    bool
	}

	GnomonSCIDKeysByKeyResult struct {
		KeysString []string
		KeysUint64 []uint64
	}

	GnomonSCIDKeysByValue struct {
		SCID   string
		Value  any
		Height int64
		Max    bool
	}

	GnomonSCIDKeysByValueResult struct {
		KeysString []string
		KeysUint64 []uint64
	}

	GnomonAllOwnersAndSCIDsQuery struct{}

	GnomonGetInfoParams struct{}

	GnomonAllNormalTxWithSCIDByAddrQuery struct {
		Address string
	}

	GnomonMiniblockDetailsByAddress struct {
		Address string
	}

	GnomonAllNormalTxWithSCIDBySCIDQuery struct {
		SCID string
	}

	GnomonAllSCIDInteractionHeight struct {
		SCID string
	}

	GnomonAllSCIDInteractionAddr struct {
		Addr string
	}

	GnomonAllSCIDInvokeDetails struct {
		SCID string
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

	GnomonOwnerQuery struct {
		SCID string
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
