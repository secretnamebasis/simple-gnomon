package main

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	api "github.com/secretnamebasis/simple-gnomon/models"
)

func main() {

	start_gnomon_indexer()
}

var startat = int64(3000000)
var bbolt = make(map[string]*BboltStore)
var indexers = make(map[string]*Indexer)

var sqlite = &SqlStore{}
var sqlindexer = &Indexer{}

func start_gnomon_indexer() {

	db_name := fmt.Sprintf("sql%s.db", "GNOMON")
	wd := globals.GetDataDirectory()
	db_path := filepath.Join(wd, "gnomondb")

	var err error
	sqlite, err = NewSqlDB(db_path, db_name)
	if err != nil {
		fmt.Println("[Main] Err creating sqlite:", err)
		return
	}

	var lowest_height int64
	indexes := map[string][]string{
		"":    {""},
		"g45": {"G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"},
		"nfa": {"ART-NFA-MS1"},
	}
	/*
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
			fmt.Println("indexers: ", indexers)
		}
	*/

	height, _ := sqlite.GetLastIndexHeight()
	if err != nil {
		height = 0
	}

	lowest_height = 0
	lowest_height = min(lowest_height, height)
	sqlindexer = NewSQLIndexer(sqlite, height, []string{MAINNET_GNOMON_SCID})
	fmt.Println("SqlIndexer ", sqlindexer)

	//	sqlindexer.SSSBackend.StoreLastIndexHeight()

	//Logger.Info("starting to index ", api.Get_TopoHeight()) // program.wallet.Get_TopoHeight()
	fmt.Println("starting to index ", api.Get_TopoHeight())
	storeHeight := func(bheight int64) {

		if ok, err := sqlindexer.SSSBackend.StoreLastIndexHeight(int64(bheight)); !ok && err != nil {
			fmt.Println("Error Saving LastIndexHeight: ", err)
			return

		}
	}

	//Logger.Info()
	fmt.Println("lowest_height ", fmt.Sprint(lowest_height))
	for bheight := lowest_height; bheight <= api.Get_TopoHeight(); bheight++ { //program.wallet.Get_TopoHeight()
		fmt.Print("\rHeight>", bheight)
		result := api.GetBlockInfo(rpc.GetBlock_Params{
			Height: uint64(bheight),
		})
		//fmt.Println("result", result)
		bl := api.GetBlockDeserialized(result.Blob)

		if len(bl.Tx_hashes) < 1 {
			continue
		}
		// not a mined transaction
		r := api.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: []string{bl.Tx_hashes[0].String()}})

		b, err := hex.DecodeString(r.Txs_as_hex[0])
		if err != nil {
			panic(err)
		}
		var tx transaction.Transaction
		if err := tx.Deserialize(b); err != nil {
			panic(err)
		}

		if tx.TransactionType != transaction.SC_TX || !tx.SCDATA.Has(rpc.SCCODE, rpc.DataString) {
			storeHeight(bheight)
			continue
		}

		//	Logger.Info("scid found", fmt.Sprint(each), fmt.Sprint(api.Get_TopoHeight())) //program.
		fmt.Print("\nscid found at height:", fmt.Sprint(bheight), " - ", fmt.Sprint(api.Get_TopoHeight()), "\n")
		sc := api.GetSC(rpc.GetSC_Params{SCID: tx.GetHash().String(), Code: true, TopoHeight: bheight})

		vars, err := GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)
		if err != nil {
			Logger.Error(err)
			continue
		}

		kv := api.GetSCValues(tx.GetHash().String()).VariableStringKeys
		//fmt.Println("key", kv.)
		headers := api.GetSCNameFromVars(kv) + ";" + api.GetSCDescriptionFromVars(kv) + ";" + api.GetSCIDImageURLFromVars(kv)
		fmt.Println("headers", headers)
		staged := SCIDToIndexStage{
			Scid:   tx.GetHash().String(),
			Fsi:    &FastSyncImport{Height: uint64(bheight), Owner: r.Txs[0].Signer, Headers: headers},
			ScVars: vars,
			ScCode: sc.Code,
			Tags:   "",
		}

		// range the indexers and add to index 1 at a time to prevent out of memory error

		for _, name := range indexes {
			fmt.Println("name: ", name)
			// if the code does not contain the filter, skip

			for _, filter := range name {
				if !strings.Contains(sc.Code, filter) {
					continue
				}
			}
		}
		// now add the scid to the index
		go func(*Indexer) {
			// if the contract already exists, record the interaction
			if err := sqlindexer.AddSCIDToIndex(staged); err != nil {
				Logger.Error(err, " ", staged.Scid, " ", staged.Fsi.Height)
				return
			}
		}(sqlindexer)
		storeHeight(bheight)
	}
	fmt.Println("indexed")
}
