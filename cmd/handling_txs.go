package cmd

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"runtime"
	"sync"
	"sync/atomic"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	"github.com/secretnamebasis/simple-gnomon/connections"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

var transaction_processing = make(chan *processingStruct, 100_000)

// this might be a good size...
var batch_size = 100

var holding_queue struct {
	registration atomic.Int64
	burn         atomic.Int64
	normal       atomic.Int64
}

func tx_handling(ctx context.Context) {
	// initial number collection
	holding_queue.registration.Swap(database.GetTxCount("registration"))
	holding_queue.burn.Swap(database.GetTxCount("burn"))
	holding_queue.normal.Swap(database.GetTxCount("normal"))

	for range runtime.GOMAXPROCS(0) - 2 {
		go work_on_txs(ctx, transaction_processing)
	}
}

func work_on_txs(ctx context.Context, transaction_processing chan *processingStruct) {
	for {
		select {
		case <-ctx.Done():
			for areQueuesEmpty() {
				for staged := range transaction_processing {
					operate_on_staged_tx(staged)
				}
			}
			return
		case staged := <-transaction_processing:
			operate_on_staged_tx(staged)
		}
	}
}

func operate_on_staged_tx(staged *processingStruct) {
	tx_count := len(staged.Tx_Hashes)

	//Find total number of batches
	batch_count := int(math.Ceil(float64(tx_count) / float64(batch_size)))

	// turn on the listener
	batch_processing := make(chan rpc.GetTransaction_Result, batch_count)
	go handle_tx_task(batch_processing)

	batchgroup := sync.WaitGroup{}
	// because the order of transactions processed doesn't matter..
	for batch := range batch_count {

		batchgroup.Add(1)
		// schedule each batch of transfers
		go batching(batch, batch_count, staged.Tx_Hashes, batch_processing, &batchgroup)
	}
	// wait for all the results to come in
	batchgroup.Wait()
	close(batch_processing)
}

func batching(batch, batch_count int, hashes []string, batch_processing chan rpc.GetTransaction_Result, wg *sync.WaitGroup) {
	defer wg.Done()

	end := batch_size * batch
	if batch == batch_count-1 {
		end = len(hashes)
	}

	// and dump them into the listener channel
	result, _ := connections.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: hashes[batch_size*batch : end]})
	// retry?
	batch_processing <- result
}

func handle_tx_task(batch_processing chan rpc.GetTransaction_Result) {
	// because order doesn't really matter here... just grab the first one
	for result := range batch_processing {
		handle_tx(result)
	}
}

// build a handle for results
func handle_tx(result rpc.GetTransaction_Result) {

	for i, each := range result.Txs_as_hex {

		b, err := hex.DecodeString(each)
		if err != nil {
			fmt.Println(err)
			continue
		}

		tx := transaction.Transaction{}

		if err := tx.Deserialize(b); err != nil {
			fmt.Println(err)
			continue
		}
		switch tx.TransactionType {
		case transaction.PREMINE, transaction.COINBASE: // not being processed
			continue
		case transaction.REGISTRATION:
			holding_queue.registration.Add(1)
		case transaction.BURN_TX:
			holding_queue.burn.Add(1)
		case transaction.NORMAL:
			holding_queue.normal.Add(1)
			if len(tx.Payloads) > 0 {
				tx_payload_callback(i, result.Txs[i].Block_Height, result, tx)
			}
		case transaction.SC_TX:
			// at this point the only thing that should remain is scids
			scid_processing <- &processingStruct{
				Height:      result.Txs[i].Block_Height,
				Tx:          result.Txs[i],
				Transaction: tx,
			}
		default:
			continue
		}
	}
}

func tx_payload_callback(i int, height int64, result rpc.GetTransaction_Result, tx transaction.Transaction) {
	for j, payload := range tx.Payloads {
		if payload.SCID != crypto.ZEROHASH {
			ringmember_callback(i, j, height, result, tx, payload)
		}
	}
}

// let's register some callbacks so that we don't re-define over and over again
func ringmember_callback(i, j int, height int64, result rpc.GetTransaction_Result, tx transaction.Transaction, payload transaction.AssetPayload) {
	for _, ring := range result.Txs[i].Ring[j] {
		normTxWithSCID := &structures.NormalTXWithSCIDParse{
			Txid:   tx.GetHash().String(),
			Scid:   payload.SCID.String(),
			Fees:   tx.Fees(),
			Height: height,
		}
		// fmt.Println("normal with scid", ring, normTxWithSCID)
		database.StoreNormalTxWithSCIDByAddr(ring, normTxWithSCID)
	}
}
