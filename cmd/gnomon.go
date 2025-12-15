package cmd

import (
	"crypto"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"math"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/ybbus/jsonrpc"

	"github.com/deroproject/derohe/block"
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

var (
	endpoint        = flag.String("endpoint", "", "-endpoint=<DAEMON_IP:PORT>")
	starting_height = flag.Int64("starting_height", -1, "-starting_height=123")
	ending_height   = flag.Int64("ending_height", -1, "-ending_height=123")
	help            = flag.Bool("help", false, "-help")
	help_msg        = `Usage: simple-gnomon [options]
A simple indexer for the DERO blockchain.

Options:
  -endpoint <DAEMON_IP:PORT>   Address of the daemon to connect to.
  -starting_height <N>         Height to start indexing from.
  -ending_height <N>           Height to stop indexing at.
  -progress                    Show current block height under audit.
  -help                        Show this help message.`
	progress = flag.Bool("progress", false, "-progress")
)
var established_backup bool
var achieved_current_height int64
var lowest_height int64
var day_of_blocks int64

// we are going to use these for later
var download atomic.Int64

// var governor atomic.Int64
var TOPO int64
var RUNNING bool

func asynchronously_process_queues(name string) {

	for staged := range workers[name].Queue {

		format := "staged scid: %s:%s %d / %d %s %d class:%s tags:%s\n"
		a := []any{
			staged.Scid,
			staged.Fsi.Owner,
			staged.Fsi.Height,
			connections.Get_TopoHeight(),
			staged.Fsi.Headers,
			len(staged.ScVars),
			staged.Class,
			staged.Tags,
		}

		fmt.Printf(format, a...)

		if err := workers[name].Idx.AddSCIDToIndex(staged); err != nil {
			// if err.Error() != "no code" { // this is a contract interaction, we are not recording these right now
			fmt.Println("indexer error:", err, staged.Scid, staged.Fsi.Height)
			// }
			continue
		}

		if achieved_current_height > 0 { // once the indexer has reached the top...
			// do incremental backups
			if err := backups[name].AddSCIDToIndex(staged); err != nil {
				// if err.Error() != "no code" { // this is a contract interaction, we are not recording these right now
				fmt.Println("indexer error:", err, staged.Scid, staged.Fsi.Height)
				// }
				continue
			}
		}

		// store counts
		workers["all"].Idx.BBSBackend.StoreTxCount(holding_queue.registration, "registration")
		workers["all"].Idx.BBSBackend.StoreTxCount(holding_queue.burn, "burn")
		workers["all"].Idx.BBSBackend.StoreTxCount(holding_queue.normal, "normal")
		storeHeight(int64(staged.Fsi.Height))

	}
}
func storeHeight(height int64) error {
	for _, worker := range workers {
		if ok, err := worker.Idx.BBSBackend.StoreLastIndexHeight(height); !ok && err != nil {
			return err
		}
	}
	return nil
}

// this is the processing thread
func Start_gnomon_indexer() {
	flag.Parse()
	if help != nil && *help {
		fmt.Println(help_msg)
		return
	}

	if endpoint != nil && *endpoint == "" {

		// first call on the wallet ws for authorizations
		connections.Set_ws_conn()

		// next, establish the daemon endpoint for rpc calls, waaaaay faster than through the wallet
		daemon := connections.GetDaemonEndpoint()
		*endpoint = daemon.Endpoint
	}
	opts := &jsonrpc.RPCClientOpts{HTTPClient: &http.Client{Timeout: time.Second * 30}}
	url := "http://" + *endpoint + "/json_rpc"
	connections.RpcClient = jsonrpc.NewClientWithOpts(url, opts)

	// if you are getting a zero... yeah, you are not connected
	if connections.Get_TopoHeight() == 0 {
		panic(errors.New("please connect through rpc"))
	}

	day_of_blocks = ((60 * 60 * 24) / int64(connections.GetDaemonInfo().Target))

	// we are going to use this as an upper bound
	lowest_height = connections.Get_TopoHeight()

	// build separate databases for each index, for portability
	fmt.Println("opening dbs")

	// for now, these are the collections we are looking for
	indices := map[string][]string{
		// this is the base db, it contains all scids and contract interactions
		"all": {""},

		// TODO: we are not currently indexing contract interactions within search filters
		"g45":  {"G45-NFT", "G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"},
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
	for name := range workers {
		go asynchronously_process_queues(name)
	}
	go filtering(indices)
	go tx_handling()
	go indexing()
	// now that the backend is set up, start WS

	fmt.Println("setting up websocket")
	go connections.ListenWS(workers)

	fmt.Println("starting to index ", connections.Get_TopoHeight())

	fmt.Println("lowest_height ", fmt.Sprint(lowest_height))

	// we'll implement a simple concurrency pattern
	// wg := sync.WaitGroup{}
	// limit := make(chan struct{}, 10)

	RUNNING = true

	// simple-daemon
	for RUNNING {

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
			TOPO = height
			if !RUNNING {
				return
			}
			if achieved_current_height > 0 &&
				!established_backup &&
				find_lowest_height(backups, now) {
				// if the current height is greater than a day of blocks...
				backup(height)
			}

			height_stage <- height

		}
		if achieved_current_height == 0 {
			fmt.Println("current height acheived, proceeding to passively index")
		}
		// height achieved
		achieved_current_height = connections.Get_TopoHeight()

		lowest_height = min(now, achieved_current_height)
	}
}

var height_stage = make(chan int64, 1000)

type processingStruct struct {
	Start        time.Time
	Result       rpc.GetBlock_Result
	Block        block.Block
	Tx_Hashes    []string
	Txs          []rpc.Tx_Related_Info
	Transactions []transaction.Transaction
	Staged       []structures.SCIDToIndexStage
}

var start_chan = make(chan processingStruct, 1)
var soft_limit int64 = 10

// this is the indexing action
func indexing() {
	// wg := sync.WaitGroup{}
	go func() {
		for staged := range start_chan {

			count := staged.Result.Block_Header.TXCount
			if count == 0 {
				continue
			}
			if count > 400 {
				fmt.Printf("large transacion count detected: %d height:%d\n", count, staged.Result.Block_Header.TopoHeight)
			}
			bl := indexer.GetBlockDeserialized(staged.Result.Blob)

			tx_count := float64(len(bl.Tx_hashes))

			// like... just in case
			if tx_count < 1 {
				continue
			}

			staged.Block = bl

			block_stage <- staged
			// fmt.Println("ENTERED TX HANDLING:", time.Since(staged.Start).Milliseconds())
		}
	}()
	wg := sync.WaitGroup{}
	for height := range height_stage {
		// if len(height_stage) == 0 || len(start_chan) != 0 || len(block_stage) != 0 || len(transaction_stage) != 0 {
		fmt.Printf("DOWNLOADS%3d HEIGHTS%4d RESULTS%d BLOCKS%d TXS%d\n", download.Load(), len(height_stage), len(start_chan), len(block_stage), len(transaction_stage))
		// }
		if download.Load() > soft_limit {
			time.Sleep(time.Millisecond * time.Duration(download.Load()))
		}
		wg.Add(1)
		go func(height int64, wg *sync.WaitGroup) {
			defer download.Add(-1)
			defer wg.Done()
			download.Add(1)
			start_chan <- processingStruct{
				Start:  time.Now(),
				Result: connections.GetBlockInfo(rpc.GetBlock_Params{Height: uint64(height)}),
			}
		}(height, &wg)
	}
	wg.Wait()
}

var block_stage = make(chan processingStruct, 1)

func tx_handling() {
	for staged := range block_stage {

		hashes := []string{}
		txs := []rpc.Tx_Related_Info{}
		transactions := []transaction.Transaction{}

		for _, each := range staged.Block.Tx_hashes {

			hashes = append(hashes, each.String())
		}

		tx_count := len(hashes)

		if tx_count == 0 {
			continue
		}

		batch_size := 4
		//Find total number of batches
		batch_count := int(math.Ceil(float64(tx_count) / float64(batch_size)))
		//Make an array to hold the result sets

		//Go through the array of batches and collect the results
		for i := range batch_count {
			//var transaction_result rpc.GetTransaction_Result
			end := batch_size * i
			if i == batch_count-1 {
				end = tx_count
			}
			result := connections.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: hashes[batch_size*i : end]})

			for i, each := range result.Txs_as_hex {

				b, err := hex.DecodeString(each)
				if err != nil {
					fmt.Println(err)
					continue
				}
				var tx transaction.Transaction
				if err := tx.Deserialize(b); err != nil {
					fmt.Println(err)
					continue
				}
				txs = append(txs, result.Txs[i])
				transactions = append(transactions, tx)

			}
		}

		staged.Tx_Hashes = hashes
		staged.Txs = txs
		staged.Transactions = transactions

		transaction_stage <- staged
		// fmt.Println("ENTERED FILTERING:", time.Since(staged.Start).Milliseconds())

	}
}

var transaction_stage = make(chan processingStruct, 1)
var holding_queue struct {
	registration int64
	burn         int64
	normal       int64
}

func filtering(indices map[string][]string) {
	// initial number collection
	count := workers["all"].Idx.BBSBackend.GetTxCount("registration")
	holding_queue.registration = count
	count = workers["all"].Idx.BBSBackend.GetTxCount("burn")
	holding_queue.burn = count
	count = workers["all"].Idx.BBSBackend.GetTxCount("normal")
	holding_queue.normal = count

	for staged := range transaction_stage {

		wg := sync.WaitGroup{}
		for i, each := range staged.Transactions {

			wg.Add(1)
			go func(wg *sync.WaitGroup) {
				defer wg.Done()

				related_info := staged.Txs[i]
				switch each.TransactionType {

				case transaction.PREMINE, transaction.COINBASE: // not being processed
					return
				case transaction.REGISTRATION: // already processed
					holding_queue.registration++
					return
				case transaction.BURN_TX:
					holding_queue.burn++
					return
				case transaction.NORMAL:
					holding_queue.normal++
					return
				case transaction.SC_TX:
					if len(each.SCDATA) == 0 {
						return
					}
					params := rpc.GetSC_Params{}

					if each.SCDATA.HasValue(rpc.SCCODE, rpc.DataString) {
						scid := each.GetHash().String()
						params = rpc.GetSC_Params{SCID: scid, Code: true, Variables: true, TopoHeight: int64(staged.Block.Height)}
					}

					// contract interactions
					if each.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
						value, ok := each.SCDATA.Value(rpc.SCID, rpc.DataHash).(crypto.Hash)
						if !ok { // paranoia
							return
						}
						if value.String() == "" { // yeah... weird
							return
						}
						scid := value.String()
						params = rpc.GetSC_Params{SCID: scid, Code: false, Variables: false, TopoHeight: int64(staged.Block.Height)}
					}

					if params.SCID == "" {
						return
					}
					sc := connections.GetSC(params)
					signer := related_info.Signer

					if signer == "" { // when ringsize is greater than 2...
						signer = "null"
					}

					to_be_indexed := stageSCIDForIndexers(sc, params.SCID, signer, staged.Block.Height)

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

					// as class is currently the filter...
					// make sure to implement more classes as necessary
					switch class {
					case "": // catchall
						to_be_indexed.Class = "null"
					case indices["tela"][0]:
						to_be_indexed.Class = "TELA-DOC-1"
					case indices["tela"][1]:
						to_be_indexed.Class = "TELA-INDEX-1"
					default:
						to_be_indexed.Class = class
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

					// lexicographical order
					slices.Sort(tags)

					// store as a single string
					to_be_indexed.Tags = strings.Join(tags, ",")

					// for each tag, queue up for writing
					for _, tag := range tags {
						// because these are being processed asynchronously...
						// don't block on writing them to the db,
						// just queue em and write em when the writer has a moment
						workers[tag].Queue <- to_be_indexed
					}
					// fmt.Println("ENTERED DB WRITE:", time.Since(staged.Start).Milliseconds())
				default:
					log.Fatal("unknown transaction", staged)
				}
			}(&wg)
		}
		wg.Wait()
	}
}
func stageSCIDForIndexers(sc rpc.GetSC_Result, scid, owner string, height uint64) structures.SCIDToIndexStage {

	fast_sync_import := &structures.FastSyncImport{Height: height, Owner: owner}

	if sc.Code == "" && len(sc.VariableStringKeys) == 0 && len(sc.VariableUint64Keys) == 0 {
		return structures.SCIDToIndexStage{Scid: scid, Fsi: fast_sync_import}
	}

	kv := sc.VariableStringKeys

	nfa_signature := "Function Start(listType String, duration Uint64, startPrice Uint64, charityDonateAddr String, charityDonatePerc Uint64) Uint64"

	if strings.Contains(sc.Code, nfa_signature) {
		fast_sync_import.Headers = indexer.GetSCNameFromVars(kv) + ";" + indexer.GetSCDescriptionFromVars(kv) + ";" + indexer.GetSCIDImageURLFromVars(kv)
	}

	if fast_sync_import.Headers == "" && len(kv) != 0 { // there could be a possability that it is a g45
		fast_sync_import.Headers = indexer.GetSCHeaderFromMetaData(kv)
	}

	if fast_sync_import.Headers == "" {
		name, description, image := "null", "null", "null"
		fast_sync_import.Headers = name + ";" + description + ";" + image
	}

	vars := indexer.GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)

	return structures.SCIDToIndexStage{Scid: scid, Fsi: fast_sync_import, ScVars: vars, ScCode: sc.Code}
}

// BACKEND & BACKUPS
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
		Queue: make(chan structures.SCIDToIndexStage, 1),
		Idx:   indexer.NewIndexer(b, height, []string{globals.MAINNET_GNOMON_SCID}),
	}

	backups[name] = indexer.NewIndexer(bb, height, []string{globals.MAINNET_GNOMON_SCID})
	if err != nil {
		return err
	}
	return nil
}

func find_lowest_height(backups map[string]*indexer.Indexer, now int64) bool {

	lowest := now
	for _, each := range backups {
		lowest = min(lowest, each.LastIndexedHeight)
	}
	return (achieved_current_height - day_of_blocks) > lowest
}

// this will serve as the backup action
func backup(each int64) {
	mu := sync.Mutex{}

	// wait for the other objects to finish
	// for len(limit) != 0 {
	// 	fmt.Println("allowing heights to clear before backing up db", each)
	// 	time.Sleep(time.Second)

	// 	continue
	// }

	// full backup
	for _, worker := range workers {
		mu.Lock()
		worker.Idx.BBSBackend.BackUpDatabases()
		mu.Unlock()
	}

	storeHeight(each)

	established_backup = true
}
