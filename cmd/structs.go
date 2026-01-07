package cmd

import (
	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

type processingStruct struct {
	// Stage 1
	Height int64
	Result rpc.GetBlock_Result
	// stage 2a
	Block block.Block
	// stage 2b
	Tx_Hashes []string
	// stage 3
	Tx          rpc.Tx_Related_Info
	Transaction transaction.Transaction
}

type miniStructure struct {
	Minis []*structures.MBLInfo
	Hash  string
}
