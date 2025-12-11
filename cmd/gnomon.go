package cmd

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/ybbus/jsonrpc"

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

var (
	endpoint        = flag.String("endpoint", "", "-endpoint=<DAEMON_IP:PORT>")
	starting_height = flag.Int64("starting_height", -1, "-starting_height=123")
	ending_height   = flag.Int64("ending_height", -1, "-ending_height=123")
	help            = flag.Bool("help", false, "-help")
	progress        = flag.Bool("progress", false, "-progress")
)
var established_backup bool
var achieved_current_height int64
var lowest_height int64
var day_of_blocks int64
var speed = time.Duration(time.Millisecond * 5)

var RUNNING bool

// this is the processing thread
func Start_gnomon_indexer() {
	flag.Parse()
	if help != nil && *help {
		fmt.Println(`Usage: simple-gnomon [options]
A simple indexer for the DERO blockchain.

Options:
  -endpoint <DAEMON_IP:PORT>   Address of the daemon to connect to.
  -starting_height <N>         Height to start indexing from.
  -ending_height <N>           Height to stop indexing at.
  -progress                    Show current block height under audit.
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
		wg := sync.WaitGroup{}
		for height := lowest_height; height < now; height++ {
			if !RUNNING {
				return
			}
			wg.Add(1)
			if achieved_current_height > 0 &&
				!established_backup &&
				find_lowest_height(backups, now) {
				// if the current height is greater than a day of blocks...

				backup(height)
			}

			// limit <- struct{}{}
			// wg.Add(1)
			// fmt.Println(speed)
			time.Sleep(speed)

			go indexing(workers, indices, height, &wg)
			// continue

			// if time.Since(n) > time.Duration(time.Millisecond*20) {
			// 	panic("slow block:" + strconv.Itoa(int(height)))
			// }
		}
		wg.Wait()
		if achieved_current_height == 0 {
			fmt.Println("current height acheived, proceeding to passively index")
		}
		// height achieved
		achieved_current_height = connections.Get_TopoHeight()

		lowest_height = min(now, achieved_current_height)

	}
}

// this is the indexing action that will be done concurrently
func indexing(workers map[string]*indexer.Worker, indices map[string][]string, height int64, wg *sync.WaitGroup) {
	defer wg.Done()
	defer storeHeight(workers, height)
	// close up when done and remove item from limit
	if progress != nil && *progress {

		fmt.Printf("auditing block: %d / %d\n", height, connections.Get_TopoHeight())
	}
	measuring := time.Now()
	result := connections.GetBlockInfo(rpc.GetBlock_Params{Height: uint64(height)})
	if progress != nil && *progress {
		fmt.Println(height, time.Since(measuring))
	}

	if time.Since(measuring) >= speed {
		speed = time.Duration(measuring.Unix()) / 1000
		time.Sleep(time.Duration(measuring.Unix()))
	}

	// if there is nothing, move on
	count := result.Block_Header.TXCount
	if count == 0 {
		return
	}

	if count > 400 {
		fmt.Printf("large transacion count detected: %d height:%d\n", count, height)
	}

	bl := indexer.GetBlockDeserialized(result.Blob)

	// like... just in case
	if len(bl.Tx_hashes) < 1 {
		return
	}

	var ( // pick up only desired txs from the block,
		txs = []string{}

		// simple concurrency pattern

	)

	// we are going to process these transactions as fast as simplicity will allow for
	for _, hash := range bl.Tx_hashes {

		// skip registrations; maybe process those another day
		succesful_registration := hash[0] == 0 && hash[1] == 0 && hash[2] == 0
		if succesful_registration {
			return
		}

		txs = append(txs, hash.String())

	}

	if len(txs) == 0 {
		return
	}
	for _, each := range txs {

		if time.Since(measuring) >= speed {
			speed = time.Duration(measuring.Unix()) / 1000
			time.Sleep(time.Duration(measuring.Unix()))
		}

		transaction_result := connections.GetTransaction(rpc.GetTransaction_Params{ // presumably,
			// one could pass an array of transaction hashes...
			// but noooooooo.... that's a vector for spam...
			// so we'll so this one at a time
			Tx_Hashes: []string{each},
		})

		measuring = time.Now()

		if progress != nil && *progress {
			fmt.Println(height, time.Since(measuring))
		}

		related_info := transaction_result.Txs[0]

		if related_info.ValidBlock != result.Block_Header.Hash || len(related_info.InvalidBlock) > 0 {
			continue
		}
		signer := related_info.Signer

		b, err := hex.DecodeString(transaction_result.Txs_as_hex[0])
		if err != nil {
			continue
		}

		// because a possible panic arrises from unknown transaction types...
		dryrun := b
		testing, done := binary.Uvarint(dryrun)
		if done <= 0 {
			// fmt.Println("Invalid Version in Transaction")
			continue
		}
		dryrun = dryrun[done:]

		if testing != 1 {
			// fmt.Println("Transaction version not equal to 1 ")
			continue
		}

		_, done = binary.Uvarint(dryrun)
		if done <= 0 {
			// fmt.Println("Invalid SourceNetwork in Transaction")
			continue
		}
		dryrun = dryrun[done:]

		_, done = binary.Uvarint(dryrun)
		if done <= 0 {
			// fmt.Println("Invalid DestNetwork in Transaction")
			continue
		}
		dryrun = dryrun[done:]

		testing, done = binary.Uvarint(dryrun)
		if done <= 0 {
			// fmt.Println("Invalid TransactionType in Transaction")
			continue
		}
		switch transaction.TransactionType(testing) {
		case transaction.PREMINE,
			transaction.REGISTRATION,
			transaction.COINBASE,
			transaction.BURN_TX,
			transaction.NORMAL,
			transaction.SC_TX:
			// these are all valid
		default:
			continue
		}

		var tx transaction.Transaction
		if err := tx.Deserialize(b); err != nil {
			continue
		}

		// fmt.Printf("%+v\n", tx)

		if tx.TransactionType != transaction.SC_TX {
			continue
		}

		if len(tx.SCDATA) == 0 {
			continue
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
				continue
			}
			if value.String() == "" { // yeah... weird
				continue
			}
			scid := value.String()
			params = rpc.GetSC_Params{SCID: scid, Code: false, Variables: false, TopoHeight: int64(height)}
		}

		if params.SCID == "" {
			continue
		}

		// fmt.Printf("%v\n", params)

		if time.Since(measuring) >= speed {
			speed = time.Duration(measuring.Unix()) / 1000
			time.Sleep(time.Duration(measuring.Unix()))
		}

		measuring = time.Now()
		sc := connections.GetSC(params)
		if progress != nil && *progress {
			fmt.Println(height, time.Since(measuring))
		}

		// fmt.Printf("%v\n", sc)

		if signer == "" { // when ringsize is greater than 2...
			signer = "null"
		}

		staged := stageSCIDForIndexers(sc, params.SCID, signer, bl.Height)

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
			staged.Class = "null"
		case indices["tela"][0]:
			staged.Class = "TELA-DOC-1"
		case indices["tela"][1]:
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

		// lexicographical order
		slices.Sort(tags)

		// store as a single string
		staged.Tags = strings.Join(tags, ",")

		// for each tag, queue up for writing
		for _, tag := range tags {
			workers[tag].Queue <- staged
		}
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

	if sc.Code == "" && len(sc.VariableStringKeys) == 0 && len(sc.VariableUint64Keys) == 0 {
		return structures.SCIDToIndexStage{Scid: scid, Fsi: fast_sync_import}
	}

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

		vars := func(staged structures.SCIDToIndexStage) string {
			varstring := ""
			for _, each := range staged.ScVars {
				varstring += fmt.Sprint(each.Key) + ":" + fmt.Sprint(each.Value) + " "
			}
			return varstring
		}(staged)

		format := "staged scid: %s:%s %d / %d %s %s class:%s tags:%s\n"
		a := []any{
			staged.Scid,
			staged.Fsi.Owner,
			staged.Fsi.Height,
			connections.Get_TopoHeight(),
			staged.Fsi.Headers,
			vars,
			staged.Class,
			staged.Tags,
		}

		fmt.Printf(format, a...)

		if err := worker.Idx.AddSCIDToIndex(staged); err != nil {
			// if err.Error() != "no code" { // this is a contract interaction, we are not recording these right now
			fmt.Println("indexer error:", err, staged.Scid, staged.Fsi.Height)
			// }
			continue
		}

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

	storeHeight(workers, each)

	established_backup = true
}
