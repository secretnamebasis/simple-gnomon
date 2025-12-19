package cmd

import (
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

	"github.com/deroproject/derohe/block"
	"github.com/deroproject/derohe/config"
	"github.com/deroproject/derohe/cryptography/crypto"
	network "github.com/deroproject/derohe/globals"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	"github.com/secretnamebasis/simple-gnomon/connections"
	"github.com/secretnamebasis/simple-gnomon/db"
	"github.com/secretnamebasis/simple-gnomon/globals"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
	"github.com/ybbus/jsonrpc"
)

var (
	databases = make(map[string]*db.BboltStore)
	backups   = make(map[string]*db.BboltStore)

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
  -help                        Show this help message.`

	established_backup      bool
	achieved_current_height int64
	lowest_height           int64
	day_of_blocks           int64

	// we are going to use these for later
	download atomic.Int64

	TOPO    int64
	DELTA   int64
	RUNNING bool

	// skip these by default
	EXCLUSIONS = []string{
		globals.NAMESERVICE,
		globals.MAINNET_GNOMON_SCID,
		// "bb43c3eb626ee767c9f305772a6666f7c7300441a0ad8538a0799eb4f12ebcd2", // 43Mb of vars is pretty big
	}

	/* currently not storing  */
	// but for those adventurous folks...
	STORE_MINIBLOCKS bool
)

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
	opts := &jsonrpc.RPCClientOpts{HTTPClient: &http.Client{Timeout: time.Minute * 5}} // this is insane... but, let's find out.
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

	go db_writer()
	go filtering(indices)
	go tx_handling()
	go indexing()
	// now that the backend is set up, start WS

	fmt.Println("setting up websocket")
	go connections.ListenWS(databases)

	fmt.Println("starting to index ", connections.Get_TopoHeight())

	fmt.Println("Pulling Latest Copy of NameService Contract")
	// there are two contracts that need to be processed with special consideration:
	// - nameservice: pull this one first, as it has no height
	// - gnomonSC: skip this one
	// let's go get the name service contract
	params := rpc.GetSC_Params{
		SCID:       globals.NAMESERVICE,
		Code:       true,
		Variables:  true,
		TopoHeight: -1,
	}

	sc := connections.GetSC(params)

	staged := &structures.SCIDToIndexStage{
		SCTXParse: structures.SCTXParse{
			Scid:   globals.NAMESERVICE,
			Sender: config.Mainnet.Dev_Address,
			Height: -1,
		},
		Headers: "",
		ScVars:  GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys),
		ScCode:  sc.Code,
		Class:   "NAMESERVICE",
		Tags:    "all",
	}

	if err := databases["all"].AddSCIDToIndex(*staged); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("lowest_height ", fmt.Sprint(lowest_height))

	RUNNING = true

	// simple-daemon
	for RUNNING {
		if achieved_current_height != 0 {
			time.Sleep(time.Second * 1)
		}

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

			if achieved_current_height > 0 && !established_backup && find_lowest_height(backups, now) { // if the current height is greater than a day of blocks...
				// a simple backup strategy
				backup(height)
			}

			height_stage <- height

		}
		if achieved_current_height == 0 {
			fmt.Println("current height acheived, proceeding to passively index")

		}
		// height achieved
		achieved_current_height = connections.Get_TopoHeight()

		lowest_height = min(now-1, achieved_current_height)
	}
}

// anything less and it will drain the queue
var height_stage = make(chan int64, 1)

type processingStruct struct {
	Start        time.Time
	Result       rpc.GetBlock_Result
	Block        block.Block
	Tx_Hashes    []string
	Txs          []rpc.Tx_Related_Info
	Transactions []transaction.Transaction
	Staged       []structures.SCIDToIndexStage
}

var start_chan = make(chan *processingStruct, 1)
var (
	soft_limit         int64 = 1
	hard_limit         int64 = soft_limit * 10
	critical_threshold int64 = hard_limit * 10000
)

// this is the indexing action
func indexing() {
	go func() {
		for staged := range start_chan {

			count := staged.Result.Block_Header.TXCount

			if count > 400 {
				fmt.Printf("large transacion count detected: %d height:%d\n", count, staged.Result.Block_Header.TopoHeight)
			}

			if !STORE_MINIBLOCKS {
				if count == 0 {
					continue
				}
			}

			bl := GetBlockDeserialized(staged.Result.Blob)

			if STORE_MINIBLOCKS { // should this be supported?
				var minis []*structures.MBLInfo

				for i, miner := range staged.Result.Block_Header.Miners {

					mini := &structures.MBLInfo{
						Hash:  bl.MiniBlocks[i].GetHash().String(),
						Miner: miner,
					}

					minis = append(minis, mini)
				}

				databases["all"].StoreMiniblockDetailsByHash(bl.GetHash().String(), minis)

				for _, mini := range minis {
					currCount := databases["all"].GetMiniblockCountByAddress(mini.Miner)
					currCount++
					newCount := currCount
					databases["all"].StoreMiniblockCountByAddress(newCount, mini.Miner)
				}
			}

			staged.Block = bl

			block_stage <- staged
			// fmt.Println("ENTERED TX HANDLING:", time.Since(staged.Start).Milliseconds())
		}
	}()
	wg := sync.WaitGroup{}
	for height := range height_stage {
		// if len(height_stage) == 0 || len(start_chan) != 0 || len(block_stage) != 0 || len(transaction_stage) != 0 {
		// fmt.Printf("HEIGHT%07d DOWNLOADS%05d HEIGHTS%4d RESULTS%d BLOCKS%d TXS%d\n", height, connections.DOWNLOADS.Load(), len(height_stage), len(start_chan), len(block_stage), len(transaction_stage))
		// }
		if connections.DOWNLOADS.Load() > soft_limit {
			time.Sleep(time.Millisecond * time.Duration(connections.DOWNLOADS.Load()))
		}

		for connections.DOWNLOADS.Load() > hard_limit {
			time.Sleep(time.Millisecond * time.Duration(connections.DOWNLOADS.Load()))
		}

		for DELTA > critical_threshold { // this really should be memory based
			fmt.Println("topo and last index have diverged too far, sleeping until caught up", "TOPO", TOPO, "LAST", databases["all"].LastIndexHeight)
			time.Sleep(time.Second * 10)
		}

		// there is more activity now, cut critical in half
		if TOPO > 1_000_000 {
			critical_threshold /= 2
		}

		wg.Add(1)
		go func(height int64, wg *sync.WaitGroup) {
			defer wg.Done()
			start_chan <- &processingStruct{
				Start:  time.Now(),
				Result: connections.GetBlockInfo(rpc.GetBlock_Params{Height: uint64(height)}),
			}
		}(height, &wg)
	}
	wg.Wait()
}

var block_stage = make(chan *processingStruct, 1)

func tx_handling() {
	for staged := range block_stage {

		if len(staged.Block.Tx_hashes) == 0 {
			continue
		}

		mu := sync.Mutex{}
		wg := sync.WaitGroup{}

		hashes := []string{}
		txs := []rpc.Tx_Related_Info{}
		transactions := []transaction.Transaction{}

		// because this is just cpu, schedule it
		for _, each := range staged.Block.Tx_hashes {
			wg.Add(1)

			go func(each crypto.Hash, wg *sync.WaitGroup) {
				defer wg.Done()

				// this short cut doesn't actually work
				// if each[0] == 0 && each[1] == 0 && each[2] == 0 {
				// 	holding_queue.registration++
				// 	continue
				// }

				mu.Lock()
				hashes = append(hashes, each.String())
				mu.Unlock()
			}(each, &wg)
		}
		wg.Wait()

		tx_count := len(hashes)

		if tx_count == 0 {
			continue
		}

		// credit: https://github.com/siteraiser/simple-gnomon/commit/d756b2b3c1c98cf30a63d759ad483fe13c3beba9
		batch_size := 4
		//Find total number of batches
		batch_count := int(math.Ceil(float64(tx_count) / float64(batch_size)))
		//Make an array to hold the result sets
		//Go through the array of batches and collect the results
		result_chan := make(chan rpc.GetTransaction_Result, 1)

		// build a listener for results
		go func() {
			mu := sync.Mutex{}
			wg := sync.WaitGroup{}
			// because order doesn't really matter here... just gram the first one
			for result := range result_chan {
				// end credit
				for i, each := range result.Txs_as_hex {
					wg.Add(1)
					go func(wg *sync.WaitGroup) {
						defer wg.Done()
						b, err := hex.DecodeString(each)
						if err != nil {
							fmt.Println(err)
							return
						}

						tx := transaction.Transaction{}

						if err := tx.Deserialize(b); err != nil {
							fmt.Println(err)
							return
						}
						// if tx.Height == 0 {
						// fun fact, registrations don't have a tx height
						// }
						mu.Lock()
						txs = append(txs, result.Txs[i])
						transactions = append(transactions, tx)
						mu.Unlock()

					}(&wg)
				}
				wg.Wait()
				staged.Tx_Hashes = hashes
				staged.Txs = txs
				staged.Transactions = transactions

				transaction_stage <- staged
			}

			// done_chan <- struct{}{}
		}()

		// because the order of transactions processed doesn't matter..
		for i := range batch_count {
			if connections.DOWNLOADS.Load() > soft_limit {
				time.Sleep(time.Millisecond * time.Duration(connections.DOWNLOADS.Load()))
			}

			for connections.DOWNLOADS.Load() > hard_limit {
				time.Sleep(time.Millisecond * time.Duration(connections.DOWNLOADS.Load()))
			}

			wg.Add(1)
			// schedule each batch of transfers
			go func(i int, wg *sync.WaitGroup) {
				defer wg.Done()
				end := batch_size * i
				if i == batch_count-1 {
					end = tx_count
				}
				// and dump them into the listener channel
				result_chan <- connections.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: hashes[batch_size*i : end]})
			}(i, &wg)
		}
		// wait for all the results to come in
		wg.Wait()

		// fmt.Println("Ended", staged.Block.Height, time.Since(staged.Start).Milliseconds())

	}
}

var transaction_stage = make(chan *processingStruct, 1)
var holding_queue struct {
	registration int64
	burn         int64
	normal       int64
}

func filtering(indices map[string][]string) {
	// initial number collection
	count := databases["all"].GetTxCount("registration")
	holding_queue.registration = count
	count = databases["all"].GetTxCount("burn")
	holding_queue.burn = count
	count = databases["all"].GetTxCount("normal")
	holding_queue.normal = count

	sieve := func(staged *processingStruct, i int, each transaction.Transaction, wg *sync.WaitGroup) {
		defer wg.Done()
		defer func() { DELTA = TOPO - int64(staged.Block.Height) }()

		switch each.TransactionType {

		case transaction.PREMINE, transaction.COINBASE: // not being processed
			return
		case transaction.REGISTRATION: // already processed
			// but just in case there are some that get by...
			holding_queue.registration++
			return
		case transaction.BURN_TX:
			holding_queue.burn++
			return
		case transaction.NORMAL:
			holding_queue.normal++

			if len(each.Payloads) > 0 {
				for j, payload := range each.Payloads {
					if payload.SCID != crypto.ZEROHASH {
						for _, ring := range staged.Txs[i].Ring[j] {
							normTxWithSCID := &structures.NormalTXWithSCIDParse{
								Txid:   each.GetHash().String(),
								Scid:   payload.SCID.String(),
								Fees:   each.Fees(),
								Height: int64(staged.Block.Height),
							}
							fmt.Println("normal with scid", ring, normTxWithSCID)
							databases["all"].StoreNormalTxWithSCIDByAddr(ring, normTxWithSCID)
						}
					}
				}
			}

			return

		case transaction.SC_TX:
			parsed_transaction := &structures.SCIDToIndexStage{}

			parsed_transaction.Txid = each.GetHash().String()

			if len(each.SCDATA) == 0 {
				return
			}

			// we go pull the contract anyway to determine that it installed
			params := rpc.GetSC_Params{}

			// contract installs
			// https://github.com/deroproject/derohe/blob/e9df1205b6603c62f0651d0e18e5e77a2584b15e/walletapi/rpcserver/rpc_transfer.go#L64
			if each.SCDATA.HasValue(rpc.SCCODE, rpc.DataString) && !each.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
				scid := each.GetHash().String()
				parsed_transaction.Method = "installsc"
				parsed_transaction.Scid = scid
				params = rpc.GetSC_Params{SCID: scid, Code: true, Variables: true, TopoHeight: int64(staged.Block.Height)}
			}

			// contract interactions
			// https://github.com/deroproject/derohe/blob/e9df1205b6603c62f0651d0e18e5e77a2584b15e/walletapi/rpcserver/rpc_transfer.go#L69
			if each.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
				value, ok := each.SCDATA.Value(rpc.SCID, rpc.DataHash).(crypto.Hash)
				if !ok { // paranoia
					return
				}
				if value.String() == "" || value.IsZero() {
					return
				}
				entrypoint, ok := each.SCDATA.Value("entrypoint", rpc.DataString).(string)
				if !ok {
					return
				}
				scid := value.String()
				parsed_transaction.Scid = scid
				parsed_transaction.Method = "scinvoke"
				parsed_transaction.Entrypoint = entrypoint
				params = rpc.GetSC_Params{SCID: scid, Code: false, Variables: true, TopoHeight: int64(staged.Block.Height)}
			}

			parsed_transaction.Sc_args = each.SCDATA

			related_info := staged.Txs[i]
			signer := related_info.Signer
			if signer == "" { // when ringsize is greater than 2...
				signer = "null" // maybe empty is better?
			}
			parsed_transaction.Sender = signer

			parsed_transaction.Payloads = each.Payloads

			parsed_transaction.Fees = each.Fees()

			parsed_transaction.Height = int64(staged.Block.Height)

			if parsed_transaction.Scid == "" {
				return
			}

			var sc rpc.GetSC_Result

			if !slices.Contains(EXCLUSIONS, params.SCID) {
				sc = connections.GetSC(params)

				if _, ok := sc.VariableStringKeys["C"]; !ok {
					// this is an invalid contract
					if _, err := databases["all"].StoreInvalidSCIDDeploys(params.SCID, each.Fees()); err != nil {
						fmt.Println(err)
						return
					}
				}

				// currently not storing ScCode...
				parsed_transaction.ScCode = sc.Code
				// the compromise, I think, is the entrypoint...
			}

			kv := sc.VariableStringKeys

			nfa_signature := "Function Start(listType String, duration Uint64, startPrice Uint64, charityDonateAddr String, charityDonatePerc Uint64) Uint64"

			if strings.Contains(sc.Code, nfa_signature) {
				parsed_transaction.Headers = GetSCNameFromVars(kv) + ";" + GetSCDescriptionFromVars(kv) + ";" + GetSCIDImageURLFromVars(kv)
			}

			if parsed_transaction.Headers == "" && len(kv) != 0 { // there could be a possability that it is a g45
				parsed_transaction.Headers = GetSCHeaderFromMetaData(kv)
			}

			if parsed_transaction.Headers == "" {
				name, description, image := "null", "null", "null"
				parsed_transaction.Headers = name + ";" + description + ";" + image
			}

			parsed_transaction.ScVars = GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)

			// unfortunately, there isn't a way to do this without checking twice
			class := ""
			// roll through the indices to obtain the class
			for name := range indices {

				// obtain the filters
				filters := indices[name]

				for _, filter := range filters { // range through the filters

					// if the code does not contain the filter, skip
					if !strings.Contains(parsed_transaction.ScCode, filter) {
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
				parsed_transaction.Class = "null"
			case indices["tela"][0]:
				parsed_transaction.Class = "TELA-DOC-1"
			case indices["tela"][1]:
				parsed_transaction.Class = "TELA-INDEX-1"
			default:
				parsed_transaction.Class = class
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
			parsed_transaction.Tags = strings.Join(tags, ",")

			// because these are being processed asynchronously...
			// don't block on writing them to the db,
			// just queue em and write em when the writer has a moment
			db_queue <- parsed_transaction

			// fmt.Println("ENTERED DB WRITE:", time.Since(staged.Start).Milliseconds())
		default:
			log.Fatal("unknown transaction", staged)
		}
	}

	// sift transactions over the sieve
	for staged := range transaction_stage {
		wg := sync.WaitGroup{}
		for i, each := range staged.Transactions {
			wg.Add(1)
			go sieve(staged, i, each, &wg)
		}
		wg.Wait()
	}
}

var db_queue = make(chan *structures.SCIDToIndexStage, 1)

func db_writer() {

	for staged := range db_queue {

		format := "staged scid: %s:%s %d / %d %s %d class:%s tags:%s\n"
		a := []any{
			staged.Scid,
			staged.Sender,
			staged.Height,
			connections.Get_TopoHeight(),
			staged.Headers,
			len(staged.ScVars),
			staged.Class,
			staged.Tags,
		}

		fmt.Printf(format, a...)

		for name := range strings.SplitSeq(staged.Tags, ",") {
			if err := databases[name].AddSCIDToIndex(*staged); err != nil {
				log.Fatal("indexer error:", err, staged.Scid, staged.Height)
				continue
			}

			if achieved_current_height > 0 { // once the indexer has reached the top...
				// do incremental backups
				if err := backups[name].AddSCIDToIndex(*staged); err != nil {
					log.Fatal("indexer error:", err, staged.Scid, staged.Height)
					continue
				}
			}
		}

		// store counts
		databases["all"].StoreTxCount(holding_queue.registration, "registration")
		databases["all"].StoreTxCount(holding_queue.burn, "burn")
		databases["all"].StoreTxCount(holding_queue.normal, "normal")
		storeHeight(int64(staged.Height))

	}
}

func storeHeight(height int64) error {
	for _, index := range databases {
		if ok, err := index.StoreLastIndexHeight(height); !ok && err != nil {
			return err
		}
	}
	return nil
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

	b.Exclusions = EXCLUSIONS

	bb, err := db.NewBBoltDB(db_path, db_backup_name)
	if err != nil {
		return err
	}
	time.Sleep(time.Second * 1) // we need a second okay...

	bb.Exclusions = EXCLUSIONS

	height, err := b.GetLastIndexHeight()
	if err != nil {
		height = 0
	}

	// this will always be behind current topo height
	lowest_height = min(lowest_height, height)

	// initialize each indexer
	databases[name] = b

	backups[name] = bb

	return nil
}

func find_lowest_height(backups map[string]*db.BboltStore, now int64) bool {

	lowest := now
	for _, each := range backups {
		height, err := each.GetLastIndexHeight()
		if err != nil {
			fmt.Println(err)
			continue // what else could you do?
		}
		lowest = min(lowest, height)
	}
	return (achieved_current_height - day_of_blocks) > lowest
}

// this will serve as the backup action
func backup(each int64) {
	mu := sync.Mutex{}

	// full backup
	for _, index := range databases {
		mu.Lock()
		index.BackUpDatabases()
		mu.Unlock()
	}

	storeHeight(each)

	established_backup = true
}
