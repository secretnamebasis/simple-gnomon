package main

import (
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
)

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
	Owner    string
	Height   uint64
	SCName   string
	SCDesc   string
	SCImgURL string
}
