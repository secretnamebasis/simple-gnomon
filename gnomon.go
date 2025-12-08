package main

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/cryptography/crypto"
	network "github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	"github.com/secretnamebasis/simple-gnomon/connections"
	"github.com/secretnamebasis/simple-gnomon/db"
	"github.com/secretnamebasis/simple-gnomon/globals"
	"github.com/secretnamebasis/simple-gnomon/indexer"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

// establish some workers
var workers = make(map[string]*indexer.Worker)
var backups = make(map[string]*indexer.Indexer)

// this is the processing thread
func start_gnomon_indexer() {

	// we are going to use all the noise we can get
	indexer.InitLog(map[string]any{}, os.Stdout)

	// we are going to use this as an upper bound
	lowest_height = connections.Get_TopoHeight()

	// build separate databases for each index, for portability
	fmt.Println("opening dbs")

	// for now, these are the collections we are looking for
	indices := map[string][]string{
		// this is the base db, it contains all scids and contract interactions
		"all": {""},

		// TODO: we are not currently indexing contract interactions within search filters
		"g45":  {"G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"},
		"nfa":  {"ART-NFA-MS1"},
		"tela": {"docVersion", "telaVersion"},

		// other indices could exist...
		// "normal":{""}
		// "registrations":{""}
		// "invalid":{""}
		// "miniblocks":{""}
	}

	for index := range indices {
		if err := set_up_backend(index); err != nil {
			fmt.Println(err)
			return
		}
	}

	fmt.Println("setting up queue processors")

	// set up a listener for staged scids in the indexer queue
	for name := range workers {
		go asynchronously_process_queues(workers[name], backups[name])
	}

	// now that the backend is set up, start WS

	fmt.Println("setting up websocket")
	go connections.ListenWS(workers)

	fmt.Println("starting to index ", connections.Get_TopoHeight())

	fmt.Println("lowest_height ", fmt.Sprint(lowest_height))

	// we'll implement a simple concurrency pattern
	wg := sync.WaitGroup{}
	limit := make(chan struct{}, 10)

do_it_again: // simple-daemon

	// a simple backup strategy
	now := connections.Get_TopoHeight()

	if ending_height != nil && *ending_height > -1 {
		now = *ending_height
	}
	// in case db needs to re-parse from a desired height
	if starting_height != nil && *starting_height < now && *starting_height > -1 && achieved_current_height == 0 {
		lowest_height = *starting_height
	}

	// main processing loop
	for height := lowest_height; height < now; height++ {

		if achieved_current_height > 0 &&
			!established_backup &&
			find_lowest(backups, now) { // if the current height is greater than a day of blocks...

			backup(height, limit)
		}

		limit <- struct{}{}
		wg.Add(1)
		go indexing(workers, indices, height, limit, &wg)

	}
	wg.Wait()

	// height achieved
	achieved_current_height = connections.Get_TopoHeight()

	lowest_height = min(now, achieved_current_height)

	goto do_it_again

}

func set_up_backend(name string) error {

	db_name := fmt.Sprintf("%s_%s.db", "GNOMON", name)
	db_backup_name := db_name + ".bak"

	wd := network.GetDataDirectory()
	db_path := filepath.Join(wd, "gnomondb")

	var err error
	b, err := db.NewBBoltDB(db_path, db_name)
	if err != nil {
		return err
	}

	bb, err := db.NewBBoltDB(db_path, db_backup_name)
	if err != nil {
		return err
	}
	time.Sleep(time.Second * 1) // we need a second okay...

	height, err := b.GetLastIndexHeight()
	if err != nil {
		height = 0
	}

	// this will always be behind current topo height
	lowest_height = min(lowest_height, height)

	// initialize each indexer
	workers[name] = &indexer.Worker{
		Queue: make(chan structures.SCIDToIndexStage, 1000),
		Idx:   indexer.NewIndexer(b, height, []string{globals.MAINNET_GNOMON_SCID}),
	}

	backups[name] = indexer.NewIndexer(bb, height, []string{globals.MAINNET_GNOMON_SCID})
	if err != nil {
		return err
	}
	return nil
}

func asynchronously_process_queues(worker *indexer.Worker, backup *indexer.Indexer) {
	for staged := range worker.Queue {

		fmt.Printf(
			("staged scid: " +
				"%s:%s " +
				"%d / %d " +
				"%s %s " +
				"class:%s tags:%s\n"),
			staged.Scid, staged.Fsi.Owner,
			staged.Fsi.Height, connections.Get_TopoHeight(),
			staged.Fsi.Headers,
			func(staged structures.SCIDToIndexStage) string {
				varstring := ""
				for _, each := range staged.ScVars {
					varstring += fmt.Sprint(each.Key) + ":" + fmt.Sprint(each.Value) + " "
				}
				return varstring
			}(staged),
			staged.Class, staged.Tags,
		)

		if err := worker.Idx.AddSCIDToIndex(staged); err != nil {
			// if err.Error() != "no code" { // this is a contract interaction, we are not recording these right now
			fmt.Println("indexer error:", err, staged.Scid, staged.Fsi.Height)
			// }
			continue
		}

		fmt.Printf("scid at height indexed: %d / %d\n", staged.Fsi.Height, connections.Get_TopoHeight())

		if achieved_current_height > 0 { // once the indexer has reached the top...
			// do incremental backups
			if err := backup.AddSCIDToIndex(staged); err != nil {
				// if err.Error() != "no code" { // this is a contract interaction, we are not recording these right now
				fmt.Println("indexer error:", err, staged.Scid, staged.Fsi.Height)
				// }
				continue
			}
		}
	}
}

func find_lowest(backups map[string]*indexer.Indexer, now int64) bool {
	lowest := now
	for _, each := range backups {
		lowest = min(lowest, each.LastIndexedHeight)
	}
	return (achieved_current_height - day_of_blocks) > lowest
}

// this is the indexing action that will be done concurrently
func indexing(workers map[string]*indexer.Worker, indices map[string][]string, height int64, limit chan struct{}, wg *sync.WaitGroup) {

	// close up when done and remove item from limit
	defer func() { wg.Done(); <-limit }()

	// fmt.Printf("auditing block: %d / %d\r", height, connections.Get_TopoHeight())

	err := indexHeight(workers, indices, height)
	if err != nil {
		fmt.Printf("error: %s %s %d\n", err, "height:", height)
	}
}

// this will serve as the backup action
func backup(each int64, limit chan struct{}) {
	mu := sync.Mutex{}

	// wait for the other objects to finish
	for len(limit) != 0 {
		fmt.Println("allowing heights to clear before backing up db", each)
		time.Sleep(time.Second)

		continue
	}

	// full backup
	for _, worker := range workers {
		mu.Lock()
		worker.Idx.BBSBackend.BackUpDatabases(each)
		mu.Unlock()
	}

	storeHeight(workers, each)

	established_backup = true
}

func indexHeight(workers map[string]*indexer.Worker, indices map[string][]string, height int64) error {
	result := connections.GetBlockInfo(rpc.GetBlock_Params{Height: uint64(height)})

	// if there is nothing, move on
	count := result.Block_Header.TXCount
	if count == 0 {
		return nil
	}

	if count > 400 {
		fmt.Printf("large transaciont count detected: %d height:%d\n", count, height)
	}

	bl := indexer.GetBlockDeserialized(result.Blob)

	// like... just in case
	if len(bl.Tx_hashes) < 1 {
		return nil
	}

	processing(workers, indices, bl)

	return storeHeight(workers, height)
}

func processing(workers map[string]*indexer.Worker, indices map[string][]string, bl block.Block) {
	// fmt.Printf("%+v\n", bl)

	var ( // pick up only desired txs from the block,
		txs = []string{}

		// simple concurrency pattern
		wg    = sync.WaitGroup{}
		mu    = sync.Mutex{}
		limit = make(chan struct{}, 10)
	)

	// we are going to process these transactions as fast as simplicity will allow for
	for _, hash := range bl.Tx_hashes {

		wg.Add(1)

		go func(limit chan struct{}, wg *sync.WaitGroup, mu *sync.Mutex) {
			// close up when done
			defer func() { wg.Done(); <-limit }()

			// skip registrations; maybe process those another day
			succesful_registration := hash[0] == 0 && hash[1] == 0 && hash[2] == 0
			if succesful_registration {
				return
			}

			// lock the slice for safety
			mu.Lock()
			txs = append(txs, hash.String())
			mu.Unlock()

		}(limit, &wg, &mu)

	}

	wg.Wait()

	if len(txs) == 0 {
		return
	}

	transaction_result := connections.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: txs})

	for i, tx := range transaction_result.Txs_as_hex {

		related_info := transaction_result.Txs[i]

		signer := related_info.Signer

		go process(workers, indices, bl.Height, tx, signer)
	}

}

func process(workers map[string]*indexer.Worker, indices map[string][]string, height uint64, hash, signer string) {

	b, err := hex.DecodeString(hash)
	if err != nil {
		return
	}

	var tx transaction.Transaction
	if err := tx.Deserialize(b); err != nil {
		return
	}

	// fmt.Printf("%+v\n", tx)

	if tx.TransactionType != transaction.SC_TX {
		return
	}

	if len(tx.SCDATA) == 0 {
		return
	}
	params := rpc.GetSC_Params{}

	if tx.SCDATA.HasValue(rpc.SCCODE, rpc.DataString) {
		scid := tx.GetHash().String()
		params = rpc.GetSC_Params{SCID: scid, Code: true, Variables: true, TopoHeight: int64(height)}
	}

	// contract interactions
	if tx.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
		value, ok := tx.SCDATA.Value(rpc.SCID, rpc.DataHash).(crypto.Hash)
		if !ok { // paranoia
			return
		}
		if value.String() == "" { // yeah... weird
			return
		}
		scid := value.String()
		params = rpc.GetSC_Params{SCID: scid, Code: false, Variables: false, TopoHeight: int64(height)}
	}

	if params.SCID == "" {
		return
	}

	// fmt.Printf("%v\n", params)

	sc := connections.GetSC(params)

	// fmt.Printf("%v\n", sc)

	if signer == "" { // when ringsize is greater than 2...
		signer = "null"
	}

	staged := stageSCIDForIndexers(sc, params.SCID, signer, height)

	// unfortunately, there isn't a way to do this without checking twice
	class := ""
	// roll through the indices to obtain the class
	for name := range indices {

		// obtain the filters
		filters := indices[name]

		for _, filter := range filters { // range through the filters

			// if the code does not contain the filter, skip
			if !strings.Contains(sc.Code, filter) {
				continue
			}

			// if there is a match, add the name of the index to it's list of tags
			class = filter
			break
		}

		if class != "" {
			break
		}
	}

	switch class {
	case "docVersion":
		staged.Class = "TELA-DOC-1"
	case "telaVersion":
		staged.Class = "TELA-INDEX-1"
	default:
		staged.Class = class
	}

	tags := []string{}

	// roll through the indices again to obtain tags
	for name := range indices {

		// obtain the filters
		filters := indices[name]

		for _, filter := range filters { // range through the filters

			// if the code does not contina the filter, skip it
			if !strings.Contains(sc.Code, filter) {
				continue
			}

			// if there is a match, add the name of the index to it's list of tags
			tags = append(tags, name)

		}
	}

	slices.Sort(tags)
	staged.Tags = strings.Join(tags, ",")

	for _, tag := range tags {
		workers[tag].Queue <- staged
	}
}

func storeHeight(indexers map[string]*indexer.Worker, height int64) error {
	for _, worker := range indexers {
		if ok, err := worker.Idx.BBSBackend.StoreLastIndexHeight(height); !ok && err != nil {
			return err
		}
	}
	return nil
}

func stageSCIDForIndexers(sc rpc.GetSC_Result, scid, owner string, height uint64) structures.SCIDToIndexStage {

	fast_sync_import := &structures.FastSyncImport{Height: height, Owner: owner}

	kv := sc.VariableStringKeys

	nfa_signature := "Function Start(listType String, duration Uint64, startPrice Uint64, charityDonateAddr String, charityDonatePerc Uint64) Uint64"

	if strings.Contains(sc.Code, nfa_signature) {

		fast_sync_import.Headers = indexer.GetSCNameFromVars(kv) + ";"

		fast_sync_import.Headers += indexer.GetSCDescriptionFromVars(kv) + ";"

		fast_sync_import.Headers += indexer.GetSCIDImageURLFromVars(kv)

	}

	if fast_sync_import.Headers == "" && len(kv) != 0 { // there could be a possability that it is a g45
		fast_sync_import.Headers = indexer.GetSCHeaderFromMetaData(kv)
	}

	if fast_sync_import.Headers == "" {
		name, description, image := "null", "null", "null"
		fast_sync_import.Headers = name + ";" + description + ";" + image
	}

	vars := indexer.GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)

	staged := structures.SCIDToIndexStage{Scid: scid, Fsi: fast_sync_import, ScVars: vars, ScCode: sc.Code}

	return staged
}
