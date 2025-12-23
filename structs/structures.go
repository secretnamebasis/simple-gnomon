package structures

import (
	"encoding/json"

	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
)

type (
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
		Tag    string
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
		Tag    string
		SCID   string
		Value  any
		Height int64
		Max    bool
	}

	GnomonSCIDKeysByValueResult struct {
		KeysString []string
		KeysUint64 []uint64
	}

	GnomonAllOwnersAndSCIDsQuery struct {
		Tag string
	}

	GnomonGetInfoParams struct {
		Tag string
	}

	GnomonAllNormalTxWithSCIDByAddrQuery struct {
		Tag     string
		Address string
	}

	GnomonMiniblockDetailsByAddress struct {
		Tag     string
		Address string
	}

	GnomonAllNormalTxWithSCIDBySCIDQuery struct {
		Tag  string
		SCID string
	}

	GnomonAllSCIDInteractionHeight struct {
		Tag  string
		SCID string
	}

	GnomonAllSCIDInteractionAddr struct {
		Tag  string
		Addr string
	}

	GnomonAllSCIDInvokeDetails struct {
		Tag  string
		SCID string
	}

	GnomonAllSCIDInvokeDetailsByEntrypoint struct {
		Tag        string
		SCID       string
		Entrypoint string
	}

	GnomonSCIDVariableDetailsAtTopoheight struct {
		Tag        string
		SCID       string
		TopoHeight int64
	}

	GnomonInteractionIndex struct {
		Tag        string
		Heights    []int64
		TopoHeight int64
		Max        bool
	}

	GnomonAllSCIDInvokeDetailsBySigner struct {
		Tag    string
		SCID   string
		Signer string
	}

	GnomonTxCountQuery struct {
		Tag     string
		Tx_Type string
	}

	GnomonOwnerQuery struct {
		Tag  string
		SCID string
	}

	GnomonMiniblockDetailsByHash struct {
		Tag  string
		BLID string
	}

	GnomonAllMiniblockDetails struct {
		Tag string
	}
	GnomonSCIDQuery struct {
		Owner  string
		Height uint64
		SCID   string
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
