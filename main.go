package main

import (
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/secretnamebasis/simple-gnomon/connections"
	"github.com/secretnamebasis/simple-gnomon/db"
	"github.com/secretnamebasis/simple-gnomon/globals"
	"github.com/secretnamebasis/simple-gnomon/indexer"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
	"github.com/ybbus/jsonrpc"
	"go.etcd.io/bbolt"

	"github.com/deroproject/derohe/cryptography/crypto"
	network "github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
)

var endpoint = flag.String("endpoint", "", "-endpoint=<DAEMON_IP:PORT>")
var starting_height = flag.Int64("starting_height", -1, "-starting_height=123")
var help = flag.Bool("help", false, "-help")
var established_backup bool

func main() {
	flag.Parse()
	if help != nil && *help {
		fmt.Println(`Usage: simple-gnomon [options]
A simple indexer for the DERO blockchain. 

Options:
  -endpoint <DAEMON_IP:PORT>   Address of the daemon to connect to.
  -starting_height <N>                Height to pop to the back to.
  -help                        Show this help message.`)
		return
	}

	if endpoint != nil && *endpoint == "" {

		// first call on the wallet ws for authorizations
		connections.Set_ws_conn()

		// next, establish the daemon endpoint for rpc calls, waaaaay faster than through the wallet
		daemon := connections.GetDaemonEndpoint()
		*endpoint = daemon.Endpoint
	}

	connections.RpcClient = jsonrpc.NewClient("http://" + *endpoint + "/json_rpc")

	// if you are getting a zero... yeah, you are not connected
	if connections.Get_TopoHeight() == 0 {
		panic(errors.New("please connect through rpc"))
	}

	// now go start gnomon
	start_gnomon_indexer()
}

// establish some workers
var workers = make(map[string]*indexer.Worker)
var backups = make(map[string]*indexer.Indexer)

// this is the processing thread
func start_gnomon_indexer() {

	// we are going to use all the noise we can get
	indexer.InitLog(map[string]any{}, os.Stdout)

	time.Sleep(time.Second * 1) // we need a second okay...

	// we are going to use this as an upper bound
	lowest_height := connections.Get_TopoHeight()
	achieved_current_height := int64(0)

	// build separate databases for each index, for portability
	fmt.Println("opening dbs")

	// for now, these are the collections we are looking for
	indicies := map[string][]string{
		// this is the base db, it contains all scids and contract interactions
		"": {""},

		// TODO: we are not currently indexing contract interactions within search filters
		"g45": {"G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"},
		"nfa": {"ART-NFA-MS1"},

		// other indicies could exist...
		// "normal":{""}
		// "registrations":{""}
		// "invalid":{""}
		// "miniblocks":{""}
	}

	for each := range indicies {

		db_name := fmt.Sprintf("%s_%s.db", "GNOMON", each)
		db_backup_name := db_name + ".bak"

		wd := network.GetDataDirectory()
		db_path := filepath.Join(wd, "gnomondb")

		var err error
		b, err := db.NewBBoltDB(db_path, db_name)
		if err != nil {
			fmt.Println("[Main] Err creating boltdb:", err)
			return
		}

		bb, err := db.NewBBoltDB(db_path, db_backup_name)
		if err != nil {
			fmt.Println("[Main] Err creating boltdb:", err)
			return
		}
		time.Sleep(time.Second * 1) // we need a second okay...

		height, err := b.GetLastIndexHeight()
		if err != nil {
			height = 0
		}

		// this will always be behind current topo height
		lowest_height = min(lowest_height, height)

		// initialize each indexer
		workers[each] = &indexer.Worker{
			Queue: make(chan structures.SCIDToIndexStage, 1000),
			Idx:   indexer.NewIndexer(b, height, []string{globals.MAINNET_GNOMON_SCID}),
		}

		backups[each] = indexer.NewIndexer(bb, height, []string{globals.MAINNET_GNOMON_SCID})
		if err != nil {
			fmt.Println("[Main] Err creating boltdb:", err)
			return
		}

		// set up a listener for staged scids in the indexer queue
		go func() {
			for staged := range workers[each].Queue {
				if err := workers[each].Idx.AddSCIDToIndex(staged); err != nil {
					// if err.Error() != "no code" { // this is a contract interaction, we are not recording these right now
					fmt.Println("indexer error:", err, staged.Scid, staged.Fsi.Height)
					// }
					continue
				}
				fmt.Println("scid at height indexed:",
					fmt.Sprint(staged.Fsi.Height), "/", fmt.Sprint(connections.Get_TopoHeight()),
				)

				if achieved_current_height > 0 { // once the indexer has reached the top...
					// do incremental backups
					if err := backups[each].AddSCIDToIndex(staged); err != nil {
						// if err.Error() != "no code" { // this is a contract interaction, we are not recording these right now
						fmt.Println("indexer error:", err, staged.Scid, staged.Fsi.Height)
						// }
						continue
					}
				}
			}
		}()

	}

	fmt.Println("starting to index ", connections.Get_TopoHeight())

	fmt.Println("lowest_height ", fmt.Sprint(lowest_height))

do_it_again: // simple-daemon

	// we'll implement a simple concurrency pattern
	wg := sync.WaitGroup{}
	limit := make(chan struct{}, runtime.GOMAXPROCS(0)-2)
	mu := sync.Mutex{}

	// a simple backup strategy
	now := connections.Get_TopoHeight()

	// this will serve as the backup action
	backup := func(each int64) {

		// wait for the other objects to finish
		for len(limit) != 0 {
			fmt.Println("allowing heights to clear before backing up db", each)
			time.Sleep(time.Second)

			continue
		}
		// full backup
		back_up_databases(workers, indicies, each, &mu)
		established_backup = true
	}

	// this is the indexing action that will be done concurrently
	indexing := func(workers map[string]*indexer.Worker, indicies map[string][]string, height int64, limit chan struct{}, wg *sync.WaitGroup) {
		fmt.Println("auditing block:", fmt.Sprint(height), "/", fmt.Sprint(connections.Get_TopoHeight()))
		err := indexHeight(workers, indicies, height)
		if err != nil {
			fmt.Printf("error: %s %s %d %d", err, "height:", height, now)
		}
		wg.Done()
		<-limit
	}

	// in case db needs to re-parse from a desired height
	if starting_height != nil && *starting_height < now && *starting_height > -1 && achieved_current_height == 0 {
		lowest_height = *starting_height
	}

	// main processing loop
	for height := lowest_height; height < now; height++ {

		if achieved_current_height > 0 && !established_backup && func() bool {
			lowest := now
			for _, each := range backups {
				lowest = min(lowest, each.LastIndexedHeight)
			}
			return achieved_current_height-4800 > lowest
		}() { // if the current height is greater than a day of blocks...
			backup(height)
		}

		limit <- struct{}{}
		wg.Add(1)
		go indexing(workers, indicies, height, limit, &wg)
	}
	wg.Wait()

	// height achieved
	achieved_current_height = connections.Get_TopoHeight()

	lowest_height = min(now, achieved_current_height)

	goto do_it_again

}

func back_up_databases(workers map[string]*indexer.Worker, indicies map[string][]string, height int64, mu *sync.Mutex) {
	mu.Lock()
	defer mu.Unlock()
	fmt.Println("backing up databases")
	// lock this up so we don't break it
	for index := range indicies {
		fmt.Println("Preparing snapshot for", index)

		// capture the db
		database := workers[index].Idx.BBSBackend.DB

		// sync the database
		if err := database.Sync(); err != nil {
			fmt.Println(err)
			return
		}

		// capture the db name
		scr_name := database.Path()

		// establish a source db file
		src, err := os.Open(scr_name)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer src.Close()

		// make a snapshot file
		dst_name := database.Path() + ".snapshot"

		// this will be the destination of the snapshot
		dst, err := os.Create(dst_name)
		if err != nil {
			fmt.Println(err)
			return
		}
		defer dst.Close()

		// remove this file when we are done
		defer os.Remove(dst_name)

		// now copy the src db file to the dst file
		if _, err = io.Copy(dst, src); err != nil {
			fmt.Println(err)
			return
		}

		// and commit.
		if err = dst.Sync(); err != nil {
			fmt.Println(err)
			return
		}

		fmt.Println("Snapshot complete", index)

		fmt.Println("Preparing backup", index)

		// open the snapshot db as read only
		source, _ := bbolt.Open(dst_name, fs.FileMode(0444), &bbolt.Options{ReadOnly: true})
		defer source.Close()

		// establish as backup
		backup_name := database.Path() + ".bak"

		// establish a tmp backup
		tmp_backup := backup_name + ".tmp"

		// remove tmp backup when are done
		defer os.Remove(tmp_backup)

		// move this file to protect it
		if err := os.Rename(backup_name, tmp_backup); err != nil {
			fmt.Println(err)
			return
		}

		// open the backup db
		destination, _ := bbolt.Open(backup_name, fs.FileMode(0644), &bbolt.Options{})
		defer destination.Close()

		// we are going to use 64MB pagination as a place to start, tune as necessary
		txMaxSize := 64 * 1024 * 1024 // 64 MB

		// now copy the read only snapshot to the backup
		if err := bbolt.Compact(destination, source, int64(txMaxSize)); err != nil {
			fmt.Println(err)
			return
		}

		fmt.Println("Backup complete", index)

		storeHeight(workers, height)

	}

	fmt.Println("All databases are backed")

}
func indexHeight(
	workers map[string]*indexer.Worker,
	indicies map[string][]string,
	each int64,
) error {
	result := connections.GetBlockInfo(rpc.GetBlock_Params{
		Height: uint64(each),
	})

	bl := connections.GetBlockDeserialized(result.Blob)

	if len(bl.Tx_hashes) < 1 {
		return nil
	}
	for _, tx_hash := range bl.Tx_hashes {
		r := connections.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: []string{tx_hash.String()}})

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

		sc := connections.GetSC(params)

		// fmt.Printf("%v\n", sc)

		staged, err := stageSCIDForIndexers(sc, params.SCID, r.Txs[0].Signer, tx.Height)
		if err != nil {
			return err
		}

		// fmt.Printf("%v\n", staged)

		fmt.Println("staged scid:", staged.Scid, ":", fmt.Sprint(staged.Fsi.Height), "/", fmt.Sprint(connections.Get_TopoHeight()))

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

	}
	return storeHeight(workers, each)
}

func storeHeight(indexers map[string]*indexer.Worker, each int64) error {
	for _, worker := range indexers {
		if ok, err := worker.Idx.BBSBackend.StoreLastIndexHeight(int64(each)); !ok && err != nil {
			return err
		}
	}
	return nil
}

func stageSCIDForIndexers(sc rpc.GetSC_Result, scid, owner string, height uint64) (structures.SCIDToIndexStage, error) {

	vars, err := indexer.GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)
	if err != nil {
		return structures.SCIDToIndexStage{}, err
	}

	kv := sc.VariableStringKeys

	headers := connections.GetSCNameFromVars(kv) + ";" + connections.GetSCDescriptionFromVars(kv) + ";" + connections.GetSCIDImageURLFromVars(kv)
	fast_sync_import := &structures.FastSyncImport{Height: height, Owner: owner, Headers: headers}

	// because empty string is a valid code entry for scids...
	// if sc.Code == "" {
	// 	return simple_gnomon.SCIDToIndexStage{}, errors.New("[staging] no code")
	// }

	// because empty vars could be a interaction...
	// if len(vars) == 0 { // there would always be more than 0 pair; stringKeys:{ "C":{ <SC CODE> } }
	// 	return simple_gnomon.SCIDToIndexStage{}, errors.New("[staging] no vars")
	// }

	staged := structures.SCIDToIndexStage{Scid: scid, Fsi: fast_sync_import, ScVars: vars, ScCode: sc.Code}

	return staged, nil
}
