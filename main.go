package main

import (
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	api "github.com/secretnamebasis/simple-gnomon/models"
)

func main() {
	var err error
	db_name := fmt.Sprintf("sql%s.db", "GNOMON")
	wd := globals.GetDataDirectory()
	db_path := filepath.Join(wd, "gnomondb")
	sqlite, err = NewSqlDB(db_path, db_name)
	if err != nil {
		fmt.Println("[Main] Err creating sqlite:", err)
		return
	}
	start_gnomon_indexer()
}

var speed = 40

// Request handling
var Processing = int64(0)
var Max_allowed = int64(128)
var Max_preferred_requests = int64(128)
var BPH = float64(0)
var Average = float64(0)

var TargetHeight = int64(0)
var HighestKnownHeight = api.Get_TopoHeight()
var sqlite = &SqlStore{}
var sqlindexer = &Indexer{}
var UseMem = true

func start_gnomon_indexer() {

	var lowest_height int64

	height, err := sqlite.GetLastIndexHeight()
	if err != nil {
		height = startat
		fmt.Println("err: ", err)
	}
	lowest_height = height

	sqlindexer = NewSQLIndexer(sqlite, height, []string{MAINNET_GNOMON_SCID})
	fmt.Println("SqlIndexer ", sqlindexer)

	//Logger.Info("starting to index ", api.Get_TopoHeight()) // program.wallet.Get_TopoHeight()
	//	fmt.Println("starting to index ", api.Get_TopoHeight())

	fmt.Println("lowest_height ", fmt.Sprint(lowest_height))
	start := time.Now()
	var quickstart = 0

	if TargetHeight < HighestKnownHeight-25000 {
		TargetHeight = lowest_height + 25000
	} else {
		TargetHeight = HighestKnownHeight
	}

	var wg sync.WaitGroup
	for bheight := lowest_height; bheight <= TargetHeight; bheight++ { //program.wallet.Get_TopoHeight()
		Processing = bheight
		if !api.Status_ok {
			break
		}

		if Average == 0 && quickstart <= 1000 {
			if quickstart == 1000 {
				Average = 1000 / time.Since(start).Hours()
			} else {
				quickstart++
			}

		}
		t, _ := time.ParseDuration(strconv.Itoa(speed) + "ms")
		time.Sleep(t)
		wg.Add(1) //
		go ProcessBlock(&wg, bheight)

	}
	//	wg.Wait() // Wait for all requests to finish
	fmt.Println("indexed")
	wg.Wait()

	BPH = float64(TargetHeight-lowest_height) / time.Since(start).Hours()
	if Average == 0 {
		Average = BPH
	} else {
		Average = (BPH + Average) / 2
	}
	if Average > 100000 {
		Max_allowed = 192
	}
	//Take a breather
	t, _ := time.ParseDuration("1s")
	time.Sleep(t)

	//check if there was a missing request
	if !api.Status_ok { //Start over from last saved.
		// Extract filename
		filename := filepath.Base(sqlite.DBPath)
		dir := filepath.Dir(sqlite.DBPath)
		ext := filepath.Ext(filename)
		//start from last saved to disk to ensure integrity (play it safe for now)
		sqlite, err = NewSqlDB(dir, filename+ext)
		if err != nil {
			fmt.Println("[Main] Err creating sqlite:", err)
			return
		}
		//	maxmet = true //not really being used
		speed += 5
		if Max_preferred_requests > 30 {
			Max_preferred_requests -= 20
		}

		api.Status_ok = true
		start_gnomon_indexer() //without saving
		return
	}
	//Request amount manager
	if float64(Max_preferred_requests) < float64(Max_allowed)*.8 {
		Max_preferred_requests += 20
		fmt.Println("Increasing max requests by 10 to:", Max_preferred_requests)
	}
	//Essentials...
	last := HighestKnownHeight
	HighestKnownHeight = api.Get_TopoHeight()

	fmt.Println("Saving Batch.............................................................")
	if UseMem {
		sqlite.BackupToDisk()
	}
	if TargetHeight == last {

		UseMem = false

		// Extract filename
		filename := filepath.Base(sqlite.DBPath)
		dir := filepath.Dir(sqlite.DBPath)
		ext := filepath.Ext(filename)

		sqlite, err = NewDiskDB(dir, filename+ext)
	}

	fmt.Println("Saving phase over...............................................................")
	sqlite.ViewTables()

	start_gnomon_indexer()

}

func ProcessBlock(wg *sync.WaitGroup, bheight int64) {
	defer wg.Done()
	indexes := map[string][]string{
		"g45":   {"G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"},
		"nfa":   {"ART-NFA-MS1"},
		"swaps": {"StartSwap"},
		"tela":  {"docVersion", "telaVersion"},
	}

	storeHeight := func(bheight int64) {
		//--maybe replace by using add owner and add a height to there...
		if ok, err := sqlindexer.SSSBackend.StoreLastIndexHeight(int64(bheight)); !ok && err != nil {
			fmt.Println("Error Saving LastIndexHeight: ", err)
			return

		}
	}
	//---- MAIN PRINTOUT
	show := "Block:" + strconv.Itoa(int(bheight)) +
		" Max En Route:" + strconv.Itoa(int(Max_preferred_requests)) +
		" Speed:" + strconv.Itoa(speed) + "ms" +
		" " + strconv.Itoa((1000/speed)*60*60) + "BPH"
	if BPH != float64(0) {
		show += " Ave:" + strconv.FormatFloat(BPH, 'f', 2, 64)
	}

	fmt.Print("\r", show)

	result := api.GetBlockInfo(rpc.GetBlock_Params{
		Height: uint64(bheight),
	})
	//fmt.Println("result", result)
	bl := api.GetBlockDeserialized(result.Blob)

	if len(bl.Tx_hashes) < 1 {
		return
	}

	// not a mined transaction
	r := api.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: []string{bl.Tx_hashes[0].String()}})
	//let the rest go unsaved if one request fails
	if !api.Status_ok {
		return
	}
	//Speed tuning
	if Processing%100 == 0 {
		concreq := Processing - int64(bheight)
		if concreq > Max_preferred_requests {
			if concreq == Max_preferred_requests*2 {
				speed = speed + 2
			} else {
				speed = speed + 1
			}

		} else if concreq < Max_preferred_requests {
			if speed > 5 {
				speed = speed - 1
			}
		}
	}
	//likely an error
	if len(r.Txs_as_hex) == 0 {
		return
	}

	b, err := hex.DecodeString(r.Txs_as_hex[0])
	if err != nil {
		panic(err)
	}
	var tx transaction.Transaction
	if err := tx.Deserialize(b); err != nil {
		panic(err)
	}
	//fmt.Println("\nTX Height: ", tx.Height)
	//fmt.Println("\nReq: ", Processing-int64(bheight))
	//

	if tx.TransactionType != transaction.SC_TX {
		storeHeight(bheight)
		return
	}

	fmt.Print("scid found at height:", fmt.Sprint(bheight)+"\n")
	params := rpc.GetSC_Params{}
	if tx.SCDATA.HasValue(rpc.SCCODE, rpc.DataString) {

		params = rpc.GetSC_Params{
			SCID:       tx.GetHash().String(),
			Code:       true,
			Variables:  true,
			TopoHeight: bheight,
		}
	}

	if tx.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
		scid, ok := tx.SCDATA.Value(rpc.SCID, rpc.DataHash).(crypto.Hash)

		if !ok { // paranoia
			return
		}
		if scid.String() == "" { // yeah... weird
			return
		}

		params = rpc.GetSC_Params{
			SCID:       scid.String(),
			Code:       false,
			Variables:  false,
			TopoHeight: bheight,
		}
	}
	sc := api.GetSC(params) //Variables: true,

	vars, err := GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)
	if err != nil { //might be worth investigating what errors could occur
		return
	}

	kv := sc.VariableStringKeys
	//fmt.Println("key", kv)
	scname := api.GetSCNameFromVars(kv)
	scdesc := api.GetSCDescriptionFromVars(kv)
	scimgurl := api.GetSCIDImageURLFromVars(kv)

	//	fmt.Println("headers", headers)
	tags := ""
	class := ""
	// range the indexers and add to index 1 at a time to prevent out of memory error
	for key, name := range indexes {
		//fmt.Println("name: ", name)
		// if the code does not contain the filter, skip
		//probably could use some suring up here
		for _, filter := range name {
			if !strings.Contains(sc.Code, filter) {
				continue
			}
			class = key
			tags = tags + "," + filter
		}

		if tags != "" && tags[0:1] == "," {
			tags = tags[1:]
		}

	}
	staged := SCIDToIndexStage{
		Scid:   tx.GetHash().String(),
		Fsi:    &FastSyncImport{Height: uint64(bheight), Owner: r.Txs[0].Signer, SCName: scname, SCDesc: scdesc, SCImgURL: scimgurl}, //
		ScVars: vars,
		ScCode: sc.Code,
		Class:  class, //Class and tags are not in original gnomon
		Tags:   tags,
	}
	fmt.Println("staged scid:", staged.Scid, ":", fmt.Sprint(staged.Fsi.Height))
	//	wg.Add(1)
	// now add the scid to the index
	//go func(*Indexer) {
	// if the contract already exists, record the interaction
	if err := sqlindexer.AddSCIDToIndex(staged); err != nil {
		fmt.Println(err, " ", staged.Scid, " ", staged.Fsi.Height)
		return
	}
	//	}(sqlindexer)

	storeHeight(bheight)
}
