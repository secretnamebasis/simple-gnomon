package structures

import (
	"encoding/json"

	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
)

type SCIDToIndexStage struct {
	Scid   string
	Fsi    *FastSyncImport
	ScVars []*SCIDVariable
	ScCode string
	Class  string
	Tags   string
}

type SCTXParse struct {
	Txid       string
	Scid       string
	Scid_hex   []byte
	Entrypoint string
	Method     string
	Sc_args    rpc.Arguments
	Sender     string
	Payloads   []transaction.AssetPayload
	Fees       uint64
	Height     int64
}

type SCIDVariable struct {
	Key   any
	Value any
}

type FastSyncImport struct {
	Owner   string
	Height  uint64
	Headers string
}

type GnomonAllOwnersAndSCIDsQuery struct {
	IDX string
}
type GnomonTxCountQuery struct {
	IDX     string
	Tx_Type string
}
type GnomonSCIDQuery struct {
	Owner  string
	Height uint64
	SCID   string
}
type JSONRpcReq struct {
	Id     *json.RawMessage `json:"id"`
	Method string           `json:"method"`
	Params *json.RawMessage `json:"params"`
}

type JSONRpcResp struct {
	Id      *json.RawMessage `json:"id"`
	Version string           `json:"jsonrpc"`
	Result  interface{}      `json:"result"`
	Error   interface{}      `json:"error"`
}
