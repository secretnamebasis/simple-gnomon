package main

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	walletapi "github.com/secretnamebasis/simple-gnomon/models"
)

func main() {

	start_gnomon_indexer()
}

var bbolt = make(map[string]*BboltStore)
var indexers = make(map[string]*Indexer)

func start_gnomon_indexer() {
	var lowest_height int64
	indexes := map[string][]string{
		"":    {""},
		"g45": {"G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"},
		"nfa": {"ART-NFA-MS1"},
	}
	for each := range indexes {

		db_name := fmt.Sprintf("%s_%s.db", "GNOMON", each)
		wd := globals.GetDataDirectory()
		db_path := filepath.Join(wd, "gnomondb")

		var err error
		bbolt[each], err = NewBBoltDB(db_path, db_name)
		if err != nil {
			Logger.Errorf("[Main] Err creating boltdb: %v", err)
			return
		}

		height, err := bbolt[each].GetLastIndexHeight()

		if err != nil {
			height = 0
		}

		lowest_height = 0
		lowest_height = min(lowest_height, height)

		// initialize each indexer
		indexers[each] = NewIndexer(bbolt[each], height, []string{MAINNET_GNOMON_SCID})
		fmt.Println(indexers) //	panic(err)
	}

	//Logger.Info("starting to index ", walletapi.Get_TopoHeight()) // program.wallet.Get_TopoHeight()
	fmt.Println("starting to index ", walletapi.Get_TopoHeight()) //	panic(err)
	storeHeight := func(each int64) {
		for _, db := range bbolt {
			if ok, err := db.StoreLastIndexHeight(int64(each)); !ok && err != nil {
				Logger.Error(err)
				return
			}
		}
	}

	//Logger.Info()
	fmt.Println("lowest_height ", fmt.Sprint(lowest_height))                //	panic(err)
	for each := lowest_height; each <= walletapi.Get_TopoHeight(); each++ { //program.wallet.Get_TopoHeight()

		result := walletapi.GetBlockInfo(rpc.GetBlock_Params{
			Height: uint64(each),
		})
		//fmt.Println("result", result)
		bl := walletapi.GetBlockDeserialized(result.Blob)

		if len(bl.Tx_hashes) < 1 {
			continue
		}
		// not a mined transaction
		r := walletapi.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: []string{bl.Tx_hashes[0].String()}})

		b, err := hex.DecodeString(r.Txs_as_hex[0])
		if err != nil {
			panic(err)
		}
		var tx transaction.Transaction
		if err := tx.Deserialize(b); err != nil {
			panic(err)
		}

		if tx.TransactionType != transaction.SC_TX || !tx.SCDATA.Has(rpc.SCCODE, rpc.DataString) {
			storeHeight(each)
			continue
		}

		//	Logger.Info("scid found", fmt.Sprint(each), fmt.Sprint(walletapi.Get_TopoHeight())) //program.
		fmt.Print("scid found", fmt.Sprint(each), " - ", fmt.Sprint(walletapi.Get_TopoHeight()))
		sc := walletapi.GetSC(rpc.GetSC_Params{SCID: tx.GetHash().String(), Code: true, TopoHeight: each})

		vars, err := GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)
		if err != nil {
			Logger.Error(err)
			continue
		}

		kv := walletapi.GetSCValues(tx.GetHash().String()).VariableStringKeys

		headers := walletapi.GetSCNameFromVars(kv) + ";" + walletapi.GetSCDescriptionFromVars(kv) + ";" + walletapi.GetSCIDImageURLFromVars(kv)
		fmt.Println("headers", headers)
		staged := SCIDToIndexStage{
			Scid:   tx.GetHash().String(),
			Fsi:    &FastSyncImport{Height: uint64(each), Owner: r.Txs[0].Signer, Headers: headers},
			ScVars: vars,
			ScCode: sc.Code,
		}

		// range the indexers and add to index 1 at a time to prevent out of memory error
		for name := range indexers {
			index := indexers[name]
			for _, filter := range indexes[name] {
				// if the code does not contain the filter, skip
				if !strings.Contains(sc.Code, filter) {
					continue
				}
				// now add the scid to the index
				go func(*Indexer) {
					// if the contract already exists, record the interaction
					if err := index.AddSCIDToIndex(staged); err != nil {
						Logger.Error(err, " ", staged.Scid, " ", staged.Fsi.Height)
						return
					}
				}(index)
			}
		}
		storeHeight(each)
	}
	fmt.Println("indexed")
}
