package cmd

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strconv"
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
	database        *db.BboltStore
	backup_database *db.BboltStore
	indices         = []structures.SearchFilter{}
	endpoint        = flag.String("endpoint", "", "-endpoint=<DAEMON_IP:PORT>")
	ws_endpoint     = flag.String("ws-endpoint", "", "-ws-endpoint=<IP:PORT>")
	api_endpoint    = flag.String("api-endpoint", "", "-api-endpoint=<IP:PORT>")
	starting_height = flag.Int64("starting-height", -1, "-starting-height=123")
	ending_height   = flag.Int64("ending-height", -1, "-ending-height=123")
	search_filter   = flag.String("search-filter", "", `-search="one-term;second-term;;;another-term;second-term"`)
	exclusions      = flag.String("exclude", "", `-exclude=<SCID>;;;<SCID1>`)
	fastsync        = flag.Bool("fastsync", false, "-fastsync")
	store_minis     = flag.Bool("store-minis", false, "-store-minis")
	progress        = flag.Bool("progress", false, "-progress")
	help            = flag.Bool("help", false, "-help")
	help_msg        = `Usage: simple-gnomon [options]
A simple indexer for the DERO blockchain.

Options:
  -endpoint <DAEMON_IP:PORT>       Address of the daemon to connect to.
  -ws-endpoint <IP:PORT>    Address of the ws.
  -api-endpoint <IP:PORT>   Address of the api.
  -starting-height <N>             Height to start indexing from.
  -ending-height <N>               Height to stop indexing at.
  -search-filter "<F;F>;;;<F;F>"   Exclusively search filter(s), overides search.json. 
  -exclude "<F>;;;<F>"             Exclude SCID(s), overides exclude.json.
  -fastsync                        Pulls gnomonSC and installs scid to index (disclaimer: automated-service subject to error)
  -store-minis                     Store miniblock details within index 
  -progress                        Show download progress stats.
  -help                            Show this help message.`

	established_backup      bool
	achieved_current_height int64
	lowest_height           int64
	day_of_blocks           int64

	// we are going to use these for later
	now         int64
	TOPO        int64
	IN_PROGRESS int64
	RUNNING     bool

	STORE_MINIBLOCKS bool
)

// this is the processing thread
func Start_gnomon_indexer() error {
	runtime.GC() // let's clean things up before beginning
	flag.Parse()
	if help != nil && *help {
		fmt.Println(help_msg)
		return nil
	}

	if endpoint != nil && *endpoint == "" {

		// first call on the wallet ws for authorizations
		if err := connections.Set_xswd_conn(); err != nil {
			return err
		}

		// next, establish the daemon endpoint for rpc calls, waaaaay faster than through the wallet
		daemon := connections.GetDaemonEndpoint()
		*endpoint = daemon.Endpoint
	}

	transport := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		MaxConnsPerHost:     20,
		DisableKeepAlives:   false,
		IdleConnTimeout:     time.Second * 90,
		DisableCompression:  false,
	}
	opts := &jsonrpc.RPCClientOpts{HTTPClient: &http.Client{Timeout: time.Second * 30, Transport: transport}} // this is insane... but, let's find out.
	url := "http://" + *endpoint + "/json_rpc"
	connections.RpcClient = jsonrpc.NewClientWithOpts(url, opts)

	api := "127.0.0.1:8082"
	if api_endpoint != nil && *api_endpoint != "" {
		api = *api_endpoint
	}

	if store_minis != nil && *store_minis {
		STORE_MINIBLOCKS = *store_minis
	}

	// if you are getting a zero... yeah, you are not connected
	if connections.Get_TopoHeight() == 0 {
		return errors.New("please connect through rpc")
	}

	day_of_blocks = ((60 * 60 * 24) / int64(connections.GetDaemonInfo().Target))

	// we are going to use this as an upper bound
	lowest_height = connections.Get_TopoHeight()

	// build separate databases for each index, for portability
	fmt.Println("opening dbs")
	if database == nil || backup_database == nil {
		if err := set_up_backend(); err != nil {
			return err
		}
	}

	height, err := database.GetLastIndexHeight()
	if err != nil {
		height = 0
	}

	// this will always be behind current topo height
	lowest_height = min(lowest_height, height)

	if STORE_MINIBLOCKS {
		fmt.Println("STORING MINIBLOCKS")
		go mini_db_writer()
	}

	go scid_db_writer()
	go filtering()
	go tx_handling()
	go indexing()
	// now that the backend is set up, start WS

	fmt.Println("setting up websocket")
	address := "127.0.0.1:9190"
	if ws_endpoint != nil && *ws_endpoint != "" {
		address = *ws_endpoint
	}
	go connections.ListenWS(ctx, database, address)

	fmt.Println("setting up api")
	api := "127.0.0.1:8082"
	if api_endpoint != nil && *api_endpoint != "" {
		api = *api_endpoint
	}
	server := connections.NewApiServer(&connections.APIConfig{
		Enabled:              true,
		Listen:               api,
		StatsCollectInterval: "5s",
		MBLLookup:            STORE_MINIBLOCKS, // default is false
	}, database)

	// serving api
	go server.Start()

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

	if err := database.AddSCIDToIndex(*staged); err != nil {
		return err
	}

	fmt.Println("lowest_height ", fmt.Sprint(lowest_height))
	if fastsync != nil && *fastsync {

		fmt.Println("fastsync activated")

		params = rpc.GetSC_Params{
			SCID:       globals.MAINNET_GNOMON_SCID,
			Code:       true,
			Variables:  true,
			TopoHeight: -1,
		}

		sc = connections.GetSC(params)
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
		staged = &structures.SCIDToIndexStage{
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

			tx := connections.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: []string{important.hash}})
			var transact transaction.Transaction
			b, err := hex.DecodeString(tx.Txs_as_hex[0])
			if err != nil {
				log.Fatal(err)
			}
			transact.Deserialize(b)
			if transact.Version != 1 {
				log.Fatal("key", important.hash, "tx", tx, "transact", transact)
			}

			if starting_height != nil && *starting_height > important.height {
				return
			}
			fmt.Println("fast syncing", important.hash, important.height)

			scid_processing <- &processingStruct{
				Height:      tx.Txs[0].Block_Height,
				Tx:          tx.Txs[0],
				Transaction: transact,
			}

		}
		work := func(imports chan importable, wg *sync.WaitGroup) {
			defer wg.Done()
			for importable := range imports {
				task(importable)
			}
		}

		wg := sync.WaitGroup{}
		for range runtime.GOMAXPROCS(0) {
			wg.Add(1)
			go work(imports, &wg)
		}
		// let's do this really fast
		for _, importable := range importables {
			imports <- importable
		}
		close(imports)
		wg.Wait()
		fmt.Println("setting lowest height current block")

		lowest_height = connections.Get_TopoHeight()
	}
	go gnomon_indexer()
	return nil
}

func gnomon_indexer() {
	RUNNING = true
	now = connections.Get_TopoHeight()

	fmt.Println("starting to index ", now)

	// gather initial results
	info := connections.GetDaemonInfo()
	database.StoreGetInfoDetails(&info)

	last := now
	go func() { // Set up a listener for get info

		for RUNNING {
			now = connections.Get_TopoHeight()
			time.Sleep(time.Second * 1)
			if last < now {
				last = now
				info = connections.GetDaemonInfo()
				database.StoreGetInfoDetails(&info)
			}
		}
	}()

	// simple-daemon
	for RUNNING {

		if achieved_current_height != 0 {
			time.Sleep(time.Second * time.Duration(info.Target))
		}

		if ending_height != nil && *ending_height > -1 {
			now = *ending_height
		}
		// in case db needs to re-parse from a desired height
		if starting_height != nil && *starting_height < now && *starting_height > -1 && achieved_current_height == 0 {
			lowest_height = *starting_height
		}

		wg := sync.WaitGroup{}

		for height := lowest_height; height < now; height++ {
			if !RUNNING {
				return
			}

			TOPO = height

			// a simple backup strategy
			if achieved_current_height > 0 && !established_backup &&
				find_lowest_height(backup_database, now) { // if the current height is greater than a day of blocks...

				for len(block_processing) != 0 ||
					len(transaction_processing) != 0 ||
					len(scid_processing) != 0 ||
					len(scid_db_queue) != 0 {
					time.Sleep(time.Millisecond * 200)
				}

				backup(height)
			}

			if progress != nil && *progress {
				format := "HEIGHT %07d DOWNLOADS %05d GOROUTINES: %d BLOCKS %d TRANSACTIONS %d SCIDS %d SCIDDB %d "

				a := []any{
					height,
					connections.DOWNLOADS.Load(),
					runtime.NumGoroutine(),
					len(block_processing),
					len(transaction_processing),
					len(scid_processing),
					len(scid_db_queue),
				}

				format += "\n"

				fmt.Printf(format, a...)
			}

			measurements := []int{
				int(connections.DOWNLOADS.Load()),
				// runtime.NumGoroutine() / 10, // managing
				len(block_processing),
				len(transaction_processing),
				len(scid_processing),
				len(scid_db_queue),
			}

			if STORE_MINIBLOCKS {
				measurements = append(measurements, len(mini_queue), len(mini_db_queue))
			}

			var m int
			for _, each := range measurements {
				m = max(m, each)
			}

			if m > 0 {
				time.Sleep(time.Millisecond * time.Duration(m))
			}

			wg.Add(1)
			go func(height int64, wg *sync.WaitGroup) {
				defer wg.Done()
				block_processing <- &processingStruct{Height: height,
					Result: connections.GetBlockInfo(rpc.GetBlock_Params{Height: uint64(height)}),
				}

			}(height, &wg)

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

type processingStruct struct {
	// Stage 1
	Height int64
	Result rpc.GetBlock_Result
	// stage 2a
	Block block.Block
	// stage 2b
	Tx_Hashes []string
	// stage 3
	Tx          rpc.Tx_Related_Info
	Transaction transaction.Transaction
}

var block_processing = make(chan *processingStruct, 100_000)

var mini_queue = make(chan *processingStruct, 100_000)

// this is the indexing action
func indexing() {

	process_minis := func(mini_queue chan *processingStruct) {
		for staged := range mini_queue {
			var minis []*structures.MBLInfo
			for i, miner := range staged.Result.Block_Header.Miners {
				mini := &structures.MBLInfo{
					Hash:  staged.Block.MiniBlocks[i].GetHash().String(),
					Miner: miner,
				}
				minis = append(minis, mini)
			}

			mini_db_queue <- miniStructure{
				Hash:  staged.Result.Block_Header.Hash,
				Minis: minis,
			}

		}
	}

	if STORE_MINIBLOCKS {
		for range runtime.GOMAXPROCS(0) - 2 {
			go process_minis(mini_queue)
		}
	}

	for staged := range block_processing {

		bl := GetBlockDeserialized(staged.Result.Blob)

		if STORE_MINIBLOCKS {
			mini_queue <- &processingStruct{
				Block:  bl,
				Result: staged.Result,
			}
		}

		count := staged.Result.Block_Header.TXCount

		if count > 400 {
			fmt.Printf("large transacion count detected: %d height:%d\n", count, staged.Result.Block_Header.TopoHeight)
		}

		if len(bl.Tx_hashes) == 0 || count == 0 { // paranoia...
			continue
		}

		hashes := []string{}

		// because this is just cpu, schedule it
		for _, each := range bl.Tx_hashes {

			hashes = append(hashes, each.String())

		}

		transaction_processing <- &processingStruct{
			Height:    staged.Height,
			Tx_Hashes: hashes,
		}
		// fmt.Println("ENTERED TX HANDLING:", time.Since(staged.Start).Milliseconds())
	}
}

var transaction_processing = make(chan *processingStruct, 100_000)

var holding_queue struct {
	registration atomic.Int64
	burn         atomic.Int64
	normal       atomic.Int64
}

func tx_handling() {
	// initial number collection
	holding_queue.registration.Swap(database.GetTxCount("registration"))
	holding_queue.burn.Swap(database.GetTxCount("burn"))
	holding_queue.normal.Swap(database.GetTxCount("normal"))

	// let's register some callbacks so that we don't re-define over and over again
	ringmember_callback := func(
		i, j int,
		height int64,
		result rpc.GetTransaction_Result,
		tx transaction.Transaction,
		payload transaction.AssetPayload,
	) {
		for _, ring := range result.Txs[i].Ring[j] {
			normTxWithSCID := &structures.NormalTXWithSCIDParse{
				Txid:   tx.GetHash().String(),
				Scid:   payload.SCID.String(),
				Fees:   tx.Fees(),
				Height: height,
			}
			// fmt.Println("normal with scid", ring, normTxWithSCID)
			database.StoreNormalTxWithSCIDByAddr(ring, normTxWithSCID)
		}
	}

	payload_callback := func(
		i int,
		height int64,
		result rpc.GetTransaction_Result,
		tx transaction.Transaction,
	) {
		for j, payload := range tx.Payloads {
			if payload.SCID != crypto.ZEROHASH {
				ringmember_callback(i, j, height, result, tx, payload)
			}
		}
	}

	// build a handle for results
	handle := func(height int64, result rpc.GetTransaction_Result) {

		for i, each := range result.Txs_as_hex {

			b, err := hex.DecodeString(each)
			if err != nil {
				fmt.Println(err)
				continue
			}

			tx := transaction.Transaction{}

			if err := tx.Deserialize(b); err != nil {
				fmt.Println(err)
				continue
			}
			switch tx.TransactionType {
			case transaction.PREMINE, transaction.COINBASE: // not being processed
				continue
			case transaction.REGISTRATION:
				holding_queue.registration.Add(1)
			case transaction.BURN_TX:
				holding_queue.burn.Add(1)
			case transaction.NORMAL:
				holding_queue.normal.Add(1)
				if len(tx.Payloads) > 0 {
					payload_callback(i, height, result, tx)
				}
			case transaction.SC_TX:
				// at this point the only thing that should remain is scids
				scid_processing <- &processingStruct{
					Height:      height,
					Tx:          result.Txs[i],
					Transaction: tx,
				}
			default:
				continue
			}
		}
	}

	task := func(height int64, result_chan chan rpc.GetTransaction_Result) {
		// because order doesn't really matter here... just grab the first one
		for result := range result_chan {
			handle(height, result)
		}
	}

	// this might be a good size...
	batch_size := 100

	batching := func(batch, batch_count int, hashes []string, result_chan chan rpc.GetTransaction_Result, wg *sync.WaitGroup) {
		defer wg.Done()

		end := batch_size * batch
		if batch == batch_count-1 {
			end = len(hashes)
		}

		// and dump them into the listener channel
		result_chan <- connections.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: hashes[batch_size*batch : end]})
	}

	work := func(transaction_processing chan *processingStruct) {
		for staged := range transaction_processing {
			tx_count := len(staged.Tx_Hashes)

			//Find total number of batches
			batch_count := int(math.Ceil(float64(tx_count) / float64(batch_size)))

			result_chan := make(chan rpc.GetTransaction_Result, batch_count)

			// turn on the listener
			go task(staged.Height, result_chan)

			batchgroup := sync.WaitGroup{}

			// because the order of transactions processed doesn't matter..
			for batch := range batch_count {

				batchgroup.Add(1)
				// schedule each batch of transfers
				go batching(batch, batch_count, staged.Tx_Hashes, result_chan, &batchgroup)
			}
			// wait for all the results to come in
			batchgroup.Wait()
			close(result_chan)
		}
	}

	for range runtime.GOMAXPROCS(0) - 2 {
		go work(transaction_processing)
	}
}

var scid_processing = make(chan *processingStruct, 100_000)

func filtering() {

	sieve := func(height int64, tx_related_info rpc.Tx_Related_Info, each transaction.Transaction) {
		defer func() { IN_PROGRESS = height }()

		if len(each.SCDATA) == 0 {
			return
		}

		// we go pull the contract anyway to determine that it installed
		var (
			scid       string
			method     string
			entrypoint string
			code       string
			class      string
			headers    string
			params     = rpc.GetSC_Params{}
			tags       = []string{"all"} // catch all
		)

		// contract installs
		// https://github.com/deroproject/derohe/blob/e9df1205b6603c62f0651d0e18e5e77a2584b15e/walletapi/rpcserver/rpc_transfer.go#L64
		if each.SCDATA.HasValue(rpc.SCCODE, rpc.DataString) && !each.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
			scid = each.GetHash().String()
			method = "install"
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
			entrypoint, ok = each.SCDATA.Value("entrypoint", rpc.DataString).(string)
			if !ok {
				return
			}
			scid = value.String()
			method = "invoke"
		}
		if scid == "" {
			return
		}

		var sc rpc.GetSC_Result
		if !slices.Contains(database.Exclusions, scid) {

			params = rpc.GetSC_Params{
				SCID:       scid,
				Code:       true,
				Variables:  true,
				TopoHeight: height,
			}
			// 	tries := 0
			// try_again:
			sc = connections.GetSC(params)

			// if sc.Code == "" && len(sc.VariableStringKeys) == 0 {
			// 	tries++
			// 	if tries <= 3 {
			// 		goto try_again
			// 	}
			// 	fmt.Println("failed")
			// 	return
			// }
			// if tries > 0 {
			// 	fmt.Println("recovered", height, tx_related_info, each)
			// }

			if _, ok := sc.VariableStringKeys["C"]; !ok {
				// this is an invalid contract
				if _, err := database.StoreInvalidSCIDDeploys(params.SCID, each.Fees()); err != nil {
					fmt.Println(err)
					return
				}
			}

			// currently not storing ScCode...
			code = sc.Code
			// the compromise, I think, is the entrypoint...
		}

		// unfortunately, there isn't a way to do this without checking twice

		// roll through the indices to obtain the class
		for _, search := range indices {

			// obtain the filters
			filters := search.Terms

			for _, filter := range filters { // range through the filters

				// if the code does not contain the filter, skip
				if !strings.Contains(code, filter) {
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

		// if they are providing a search filter, they are being specific
		// if none of the filters matches the sc code, skip this txid
		if search_filter != nil && *search_filter != "" && class == "" {
			return
		}

		// catch globals
		switch scid {
		case globals.NAMESERVICE:
			class = "NAMESERVICE"
		case globals.MAINNET_GNOMON_SCID:
			class = "GNOMONSC"
		}

		// as class is currently the filter...
		// make sure to implement more classes as necessary
		switch class {
		case "": // catchall
			class = "null"
		case "docVersion":
			class = "TELA-DOC-1"
		case "telaVersion":
			class = "TELA-INDEX-1"
		}

		// roll through the indices again to obtain tags
		for _, search := range indices {

			// obtain the filters
			filters := search.Terms

			for _, filter := range filters { // range through the filters

				// if the code does not contina the filter, skip it
				if !strings.Contains(code, filter) {
					continue
				}

				// if there is a match, add the name of the index to it's list of tags
				tags = append(tags, search.Name)

			}
		}

		// lexicographical order
		slices.Sort(tags)

		signer := tx_related_info.Signer
		if signer == "" { // when ringsize is greater than 2...
			signer = "null" // maybe empty is better?
		}

		nfa_signature := "Function Start(listType String, duration Uint64, startPrice Uint64, charityDonateAddr String, charityDonatePerc Uint64) Uint64"

		if strings.Contains(sc.Code, nfa_signature) {
			headers = GetSCNameFromVars(sc.VariableStringKeys) + ";" + GetSCDescriptionFromVars(sc.VariableStringKeys) + ";" + GetSCIDImageURLFromVars(sc.VariableStringKeys)
		}

		if headers == "" && len(sc.VariableStringKeys) != 0 { // there could be a possability that it is a g45
			headers = GetSCHeaderFromMetaData(sc.VariableStringKeys)
		}

		if headers == "" {
			name, description, image := "null", "null", "null"
			headers = name + ";" + description + ";" + image
		}

		// because these are being processed asynchronously...
		// don't block on writing them to the db,
		// just queue em and write em when the writer has a moment
		scid_db_queue <- &structures.SCIDToIndexStage{
			SCTXParse: structures.SCTXParse{
				Height:     height,
				Txid:       each.GetHash().String(),
				Scid:       scid,
				Entrypoint: entrypoint,
				Method:     method,
				Sc_args:    each.SCDATA,
				Sender:     signer,
				Payloads:   each.Payloads,
				Fees:       each.Fees(),
			},
			Headers: headers,
			ScVars:  GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys),
			ScCode:  code,
			Class:   class,
			// store as a single string
			Tags: strings.Join(tags, ","),
		}

	}

	// sift transactions over the sieve
	for staged := range scid_processing {
		sieve(int64(staged.Height), staged.Tx, staged.Transaction)
	}
}

type miniStructure struct {
	Minis []*structures.MBLInfo
	Hash  string
}

var mini_db_queue = make(chan miniStructure, 100_000)

func mini_db_writer() {
	all_details := database.GetAllMiniblockDetails()
	miner_map := map[string]int64{}
	for _, each := range all_details {
		for _, mini := range each {
			miner_map[mini.Miner]++
		}
	}
	for staged := range mini_db_queue {
		if _, ok := all_details[staged.Hash]; ok {
			continue
		}
		database.StoreMiniblockDetailsByHash(staged.Hash, staged.Minis)
		for _, mini := range staged.Minis {
			miner_map[mini.Miner]++
			database.StoreMiniblockCountByAddress(miner_map[mini.Miner], mini.Miner)
		}
	}
}

var scid_db_queue = make(chan *structures.SCIDToIndexStage, 100_000)

func scid_db_writer() {
	for staged := range scid_db_queue {

		format := "staged txid %s sender %s | %s | scid: %s %d / %d %s %d class:%s tags:%s\n"
		a := []any{
			staged.Txid,
			staged.Sender,
			staged.Method,
			staged.Scid,
			staged.Height,
			now,
			staged.Headers,
			len(staged.ScVars),
			staged.Class,
			staged.Tags,
		}

		fmt.Printf(format, a...)
		// store scid by tag
		if err := database.AddSCIDToIndex(*staged); err != nil {
			log.Fatal("indexer error:", err, staged.Scid, staged.Height)
			continue
		}

		if achieved_current_height > 0 { // once the indexer has reached the top...
			// do incremental backups
			if err := backup_database.AddSCIDToIndex(*staged); err != nil {
				log.Fatal("indexer error:", err, staged.Scid, staged.Height)
				continue
			}
		}

		// store counts
		database.StoreTxCount(holding_queue.registration.Load(), "registration")
		database.StoreTxCount(holding_queue.burn.Load(), "burn")
		database.StoreTxCount(holding_queue.normal.Load(), "normal")

		// store height
		storeHeight(int64(staged.Height))
	}
}

func storeHeight(height int64) error {
	if ok, err := database.StoreLastIndexHeight(height); !ok && err != nil {
		return err
	}
	return nil
}

// BACKEND & BACKUPS
func set_up_backend() error {

	// if there is a search filter...
	if search_filter != nil && *search_filter != "" {
		search := *search_filter

		action := func(i int, terms []string) {
			indices = append(indices, structures.SearchFilter{
				Name:  "Filter " + strconv.Itoa(i),
				Terms: terms,
			})
		}

		callback := func(i int, filter string) {
			if strings.Contains(filter, ";") {
				terms := strings.Split(filter, ";")
				action(i, terms)
			} else {
				action(i, []string{filter})
			}
		}

		switch {
		case strings.Contains(search, ";;;"):
			for i, filter := range strings.Split(search, ";;;") {
				callback(i, filter)
			}
		case !strings.Contains(search, ";;;"):
			callback(0, search)
		}

	}

	if search_filter != nil && *search_filter == "" && len(indices) == 0 {
		cfg := filepath.Join("config", "search.json")
		if _, err := os.Stat(cfg); err != nil {
			// for now, these are the collections we are looking for
			// title, search terms
			indices = []structures.SearchFilter{
				{Name: "g45", Terms: []string{"G45-NFT", "G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"}},
				{Name: "nfa", Terms: []string{"ART-NFA-MS1"}},
				{Name: "tela", Terms: []string{"docVersion", "telaVersion"}},
			}

			if err := os.Mkdir(filepath.Dir(cfg), 0700); err != nil {
				if errors.Is(err, os.ErrExist) {
					fmt.Println(err)
				} else {
					return err
				}
			}

			b, err := json.MarshalIndent(indices, "", "\t")
			if err != nil {
				return err
			}

			if err := os.WriteFile(cfg, b, 0600); err != nil {
				return err
			}

		} else {

			fi, err := os.OpenFile(cfg, os.O_RDONLY, 0600)
			if err != nil {
				return err
			}

			b, err := io.ReadAll(fi)
			if err != nil {
				return err
			}

			if err := json.Unmarshal(b, &indices); err != nil {
				return err
			}
		}
	}

	fmt.Println("searches indices:", len(indices))

	excluded := []struct {
		Name   string
		SCID   string
		Reason string
	}{}

	// if exclusions are provided...
	if exclusions != nil && *exclusions != "" {
		exclude := *exclusions

		callback := func(i int, scid string) {
			excluded = append(excluded, struct {
				Name   string
				SCID   string
				Reason string
			}{
				Name:   "Exclusion " + strconv.Itoa(i),
				SCID:   scid,
				Reason: "exclusion flag",
			})
		}

		switch {
		case strings.Contains(exclude, ";;;"):
			for i, filter := range strings.Split(exclude, ";;;") {
				callback(i, filter)
			}
		case !strings.Contains(exclude, ";;;"):
			callback(0, exclude)
		}
	}

	// otherwise if there is no flag
	if exclusions == nil && *exclusions == "" && len(excluded) == 0 {
		excludes := filepath.Join("config", "exclude.json")
		if _, err := os.Stat(excludes); err != nil {
			// for now, these are the collections we are looking for
			// title, search terms
			excluded = []struct {
				Name   string
				SCID   string
				Reason string
			}{
				{Name: "NAMESERVICE", SCID: globals.NAMESERVICE, Reason: "Hardcoded Contract"},
				{Name: "Gnomon Smart Contract", SCID: globals.MAINNET_GNOMON_SCID, Reason: "Large Contract"},
			}

			if err := os.Mkdir(filepath.Dir(excludes), 0700); err != nil {

				if errors.Is(err, os.ErrExist) {
					fmt.Println(err)
				} else {
					return err
				}
			}

			b, err := json.MarshalIndent(excluded, "", "\t")
			if err != nil {
				return err
			}

			if err := os.WriteFile(excludes, b, 0600); err != nil {
				return err
			}
		} else {
			fi, err := os.OpenFile(excludes, os.O_RDONLY, 0600)
			if err != nil {
				return err
			}

			b, err := io.ReadAll(fi)
			if err != nil {
				return err
			}

			if err := json.Unmarshal(b, &excluded); err != nil {
				return err
			}
		}
	}

	// DB SETUP
	db_name := fmt.Sprintf("%s.db", "GNOMON")
	db_backup_name := db_name + ".bak"
	wd := network.GetDataDirectory()
	db_path := filepath.Join(wd, "gnomondb")
	var b *db.BboltStore
	var bb *db.BboltStore

	var err error

	b, err = db.NewBBoltDB(db_path, db_name)
	if err != nil {
		return err
	}

	bb, err = db.NewBBoltDB(db_path, db_backup_name)
	if err != nil {
		return err
	}
	time.Sleep(time.Second * 1) // we need a second okay...

	for _, exclude := range excluded {
		b.Exclusions = append(b.Exclusions, exclude.SCID)
		bb.Exclusions = append(bb.Exclusions, exclude.SCID)
	}

	// initialize each indexer
	database = b

	backup_database = bb

	return nil
}

func find_lowest_height(backup *db.BboltStore, now int64) bool {
	lowest := now
	height, err := backup.GetLastIndexHeight()
	if err != nil {
		fmt.Println(err)
		return false
	}
	lowest = min(lowest, height)
	return (achieved_current_height - day_of_blocks) > lowest
}

// this will serve as the backup action
func backup(each int64) {
	mu := sync.Mutex{}

	// full backup
	mu.Lock()
	database.BackUpDatabases()
	mu.Unlock()

	storeHeight(each)

	established_backup = true
}
