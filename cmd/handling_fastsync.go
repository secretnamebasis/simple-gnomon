package cmd

import (
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	"github.com/secretnamebasis/simple-gnomon/connections"
	"github.com/secretnamebasis/simple-gnomon/globals"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

func fastsync_handling() error {
	fmt.Println("fastsync activated")
	start := time.Now()
	params := rpc.GetSC_Params{
		SCID:       globals.MAINNET_GNOMON_SCID,
		Code:       true,
		Variables:  true,
		TopoHeight: -1,
	}

	sc, _ := connections.GetSC(params)
	fmt.Println("gnomonSC collected")
	kv := sc.VariableStringKeys

	sig, err := hex.DecodeString(kv["signature"].(string))
	if err != nil {
		return err
	}

	validated, signer, err := ValidateSCSignature(sc.Code, string(sig))
	fmt.Println("gnomonSC validated")

	if err != nil {
		return err
	} else if !validated {
		return errors.New("gnomonSC is not validated")
	}
	vars := GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)
	fmt.Println("vars collected")
	staged := &structures.SCIDToIndexStage{
		SCTXParse: structures.SCTXParse{
			Scid:   globals.MAINNET_GNOMON_SCID,
			Sender: signer,
			Height: -1,
		},
		Headers: GetSCNameFromVars(kv) + ";" + GetSCDescriptionFromVars(kv) + ";" + GetSCIDImageURLFromVars(kv),
		ScVars:  vars,
		ScCode:  sc.Code,
		Class:   "GNOMONSC",
		Tags:    "all",
	}

	if err := database.AddSCIDToIndex(*staged); err != nil {
		return err
	}
	fmt.Println("gnomonSC added to index")
	type importable struct {
		hash   string
		height int64
	}

	importables := []importable{}
	for k := range kv {
		if len(k) != 64 {
			continue
		}
		// we can't rely any of the data other than scids
		v := kv[k+"height"].(float64)
		height := int64(v)

		if height < lowest_height {
			continue
		}

		// fmt.Println(v, int(v), int64(v), uint(v), uint64(v))
		importables = append(importables, importable{
			hash:   k,
			height: height,
		})
	}

	sort.Slice(importables, func(i, j int) bool {
		return importables[i].height < importables[j].height
	})

	imports := make(chan importable, 10)

	task := func(important importable) {

		// invalid tx version
		if important.hash == "a7ef2d109900158bcd84794cd70de64b9e08e9268345e6b64270561a2f1985fb" ||
			important.hash == globals.NAMESERVICE {
			return
		}

		tx, _ := connections.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: []string{important.hash}})
		// retry
		var transact transaction.Transaction
		b, err := hex.DecodeString(tx.Txs_as_hex[0])
		if err != nil {
			log.Fatal(err)
		}
		transact.Deserialize(b)
		if transact.Version != 1 {
			log.Fatal("key", important.hash, "tx", tx, "transact", transact)
		}

		if int64(starting_height) > important.height {
			return
		}
		if progress {
			fmt.Printf("fast syncing %s %d \n", important.hash, important.height)
		}
		scid_processing <- &processingStruct{
			Height:      tx.Txs[0].Block_Height,
			Tx:          tx.Txs[0],
			Transaction: transact,
		}

	}

	completed := atomic.Int64{}
	total := int64(len(importables))
	unique := sync.Map{}
	work := func(imports chan importable, wg *sync.WaitGroup) {
		defer wg.Done()
		for importable := range imports {
			if !RUNNING {
				return
			}
			if areQueuesEmpty() {
				time.Sleep(time.Millisecond * time.Duration(longestQueue()))
			}
			task(importable)
			done := completed.Add(1)
			percent := done * 100 / total
			if _, ok := unique.Load(percent); !ok {
				fmt.Printf("completed %d %%\n", percent)
				unique.Store(percent, struct{}{})
			}
		}
	}

	wg := sync.WaitGroup{}
	for range runtime.GOMAXPROCS(0) {
		wg.Add(1)
		go work(imports, &wg)
	}

	fmt.Println("fast sync started")
	// let's do this really fast
	for _, importable := range importables {
		select {
		case <-ctx.Done():
			return nil
		default:
			if !RUNNING {
				return nil
			}
			imports <- importable
		}
	}
	close(imports)
	waitForAllQueues()
	wg.Wait()

	fmt.Println("fast sync done", time.Since(start))

	fmt.Println("setting lowest height current block")
	lowest_height, _ = connections.Get_TopoHeight()

	return nil
}
