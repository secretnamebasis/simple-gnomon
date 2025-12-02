package main

import (
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	walletapi "github.com/secretnamebasis/simple-gnomon/models"
)

func main() {
	walletapi.Set_ws_conn()
	if walletapi.Get_TopoHeight() == 0 {
		panic(errors.New("please connect through rpc"))
	}
	start_gnomon_indexer()
}

var workers = make(map[string]*Worker)

func start_gnomon_indexer() {
	// we are going to use all the noise we can get
	InitLog(map[string]any{}, os.Stdout)

	time.Sleep(time.Second * 1) // we need a second okay...

	lowest_height := walletapi.Get_TopoHeight()

	// build separate databases for each index, for portability
	fmt.Println("opening  dbs")

	indicies := map[string][]string{
		"":    {""},
		"g45": {"G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"},
		"nfa": {"ART-NFA-MS1"},
	}

	for each := range indicies {

		db_name := fmt.Sprintf("%s_%s.db", "GNOMON", each)
		wd := globals.GetDataDirectory()
		db_path := filepath.Join(wd, "gnomondb")

		var err error
		b, err := NewBBoltDB(db_path, db_name)
		if err != nil {
			fmt.Println("[Main] Err creating boltdb:", err)
			return
		}
		time.Sleep(time.Second * 1) // we need a second okay...

		height, err := b.GetLastIndexHeight()
		if err != nil {
			height = 0
		}

		lowest_height = min(lowest_height, height)

		// initialize each indexer
		workers[each] = &Worker{
			Queue: make(chan SCIDToIndexStage, 1000),
			Idx:   NewIndexer(b, height, []string{MAINNET_GNOMON_SCID}),
		}

		go func() {
			for staged := range workers[each].Queue {
				if err := workers[each].Idx.AddSCIDToIndex(staged); err != nil {
					// if err.Error() != "no code" { // this is a contract interaction, we are not recording these right now
					fmt.Println("indexer error:", err, staged.Scid, staged.Fsi.Height)
					// }
					continue
				}
				fmt.Println("scid at height indexed:",
					fmt.Sprint(staged.Fsi.Height), "/", fmt.Sprint(walletapi.Get_TopoHeight()),
				)
			}
		}()

	}

	fmt.Println("starting to index ", walletapi.Get_TopoHeight())

	fmt.Println("lowest_height ", fmt.Sprint(lowest_height))

do_it_again: // simple-daemon

	// let's just make sure things are clean when we come in.

	now := walletapi.Get_TopoHeight()
	wg := sync.WaitGroup{}
	limit := make(chan struct{}, runtime.GOMAXPROCS(0)-2)
	// lowest_height = 1491500
	for each := lowest_height; each < now; each++ {
		limit <- struct{}{}
		wg.Add(1)
		go func(
			workers map[string]*Worker,
			indicies map[string][]string,
			each int64,
			limit chan struct{},
			wg *sync.WaitGroup,
		) {
			fmt.Println("auditing block:", fmt.Sprint(each), "/", fmt.Sprint(walletapi.Get_TopoHeight()))
			err := indexHeight(workers, indicies, each)
			if err != nil {
				fmt.Printf("error: %s %s %d %d", err, "height:", each, now)
			}
			wg.Done()
			<-limit
		}(workers, indicies, each, limit, &wg)
	}
	wg.Wait()
	fmt.Println("indexed")

	lowest_height = min(now, walletapi.Get_TopoHeight())

	goto do_it_again

}

func indexHeight(
	workers map[string]*Worker,
	indicies map[string][]string,
	each int64,
) error {
	result := walletapi.GetBlockInfo(rpc.GetBlock_Params{
		Height: uint64(each),
	})

	bl := walletapi.GetBlockDeserialized(result.Blob)

	if len(bl.Tx_hashes) < 1 {
		return nil
	}

	r := walletapi.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: []string{bl.Tx_hashes[0].String()}})

	b, err := hex.DecodeString(r.Txs_as_hex[0])
	if err != nil {
		return err
	}
	var tx transaction.Transaction
	if err := tx.Deserialize(b); err != nil {
		return err
	}

	if tx.TransactionType != transaction.SC_TX {
		return nil
	}

	storeHeight(workers, each)
	// fmt.Printf("%+v\n", bl)
	// fmt.Printf("%+v\n", tx)

	params := rpc.GetSC_Params{}

	if tx.SCDATA.HasValue(rpc.SCCODE, rpc.DataString) {
		params = rpc.GetSC_Params{
			SCID:       tx.GetHash().String(),
			Code:       true,
			Variables:  true,
			TopoHeight: int64(bl.Height),
		}
	}

	if tx.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
		scid, ok := tx.SCDATA.Value(rpc.SCID, rpc.DataHash).(crypto.Hash)
		if !ok { // paranoia
			return nil
		}
		if scid.String() == "" { // yeah... weird
			return nil
		}
		params = rpc.GetSC_Params{
			SCID:       scid.String(),
			Code:       false,
			Variables:  false,
			TopoHeight: int64(bl.Height),
		}
	}

	// fmt.Printf("%v\n", params)

	sc := walletapi.GetSC(params)

	// fmt.Printf("%v\n", sc)

	staged, err := stageSCIDForIndexers(sc, params.SCID, r.Txs[0].Signer, tx.Height)
	if err != nil {
		return err
	}

	// fmt.Printf("%v\n", staged)

	fmt.Println("staged scid:", staged.Scid, ":", fmt.Sprint(staged.Fsi.Height), "/", fmt.Sprint(walletapi.Get_TopoHeight()))

	// range the indexers and add to index 1 at a time to prevent out of memory error
	for name := range workers {
		for _, filter := range indicies[name] {
			// if the code does not contain the filter, skip
			if !strings.Contains(sc.Code, filter) {
				continue
			}
			workers[name].Queue <- staged
		}
	}

	return storeHeight(workers, each)

}

func storeHeight(indexers map[string]*Worker, each int64) error {
	for _, worker := range indexers {
		if ok, err := worker.Idx.BBSBackend.StoreLastIndexHeight(int64(each)); !ok && err != nil {
			return err
		}
	}
	return nil
}

func stageSCIDForIndexers(sc rpc.GetSC_Result, scid, owner string, height uint64) (SCIDToIndexStage, error) {

	vars, err := GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)
	if err != nil {
		return SCIDToIndexStage{}, err
	}

	kv := sc.VariableStringKeys

	headers := walletapi.GetSCNameFromVars(kv) + ";" + walletapi.GetSCDescriptionFromVars(kv) + ";" + walletapi.GetSCIDImageURLFromVars(kv)
	fast_sync_import := &FastSyncImport{Height: height, Owner: owner, Headers: headers}

	// because empty string is a valid code entry for scids...
	// if sc.Code == "" {
	// 	return simple_gnomon.SCIDToIndexStage{}, errors.New("[staging] no code")
	// }

	// because empty vars could be a interaction...
	// if len(vars) == 0 { // there would always be more than 0 pair; stringKeys:{ "C":{ <SC CODE> } }
	// 	return simple_gnomon.SCIDToIndexStage{}, errors.New("[staging] no vars")
	// }

	staged := SCIDToIndexStage{Scid: scid, Fsi: fast_sync_import, ScVars: vars, ScCode: sc.Code}

	return staged, nil
}
