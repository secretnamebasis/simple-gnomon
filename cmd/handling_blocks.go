package cmd

import (
	"context"
	"runtime"

	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

var block_processing = make(chan *processingStruct, 100_000)

// this is the indexing action
func block_handling(ctx context.Context) {

	if store_minis {
		for range runtime.GOMAXPROCS(0) - 2 {
			go process_minis(ctx, mini_queue)
		}
	}

	for {
		select {
		case <-ctx.Done():
			for areQueuesEmpty() {
				for staged := range block_processing {
					work_on_blocks(staged)
				}
			}
			return
		case staged := <-block_processing:
			work_on_blocks(staged)
		}
	}
}

func work_on_blocks(staged *processingStruct) {

	bl := GetBlockDeserialized(staged.Result.Blob)

	if store_minis {
		mini_queue <- &processingStruct{
			Block:  bl,
			Result: staged.Result,
		}
	}

	count := staged.Result.Block_Header.TXCount

	if len(bl.Tx_hashes) == 0 || count == 0 { // paranoia...
		return
	}

	hashes := []string{}

	// because this is just cpu, schedule it
	for _, each := range bl.Tx_hashes {

		hashes = append(hashes, each.String())

	}

	transaction_processing <- &processingStruct{
		Height:    staged.Height,
		Tx_Hashes: hashes,
	}
}

var mini_queue = make(chan *processingStruct, 100_000)

func process_minis(ctx context.Context, mini_queue chan *processingStruct) {
	for {
		select {
		case <-ctx.Done():
			for areQueuesEmpty() {
				for staged := range mini_queue {
					work_on_minis(staged)
				}
			}
		case staged := <-mini_queue:
			work_on_minis(staged)
		}
	}
}

func work_on_minis(staged *processingStruct) {
	var minis []*structures.MBLInfo
	for i, miner := range staged.Result.Block_Header.Miners {
		mini := &structures.MBLInfo{
			Hash:  staged.Block.MiniBlocks[i].GetHash().String(),
			Miner: miner,
		}
		minis = append(minis, mini)
	}

	mini_db_queue <- miniStructure{
		Hash:  staged.Result.Block_Header.Hash,
		Minis: minis,
	}
}
