package cmd

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"os"
	"os/signal"
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
	database           *db.BboltStore
	backup_database    *db.BboltStore
	indices            = []structures.SearchFilter{}
	endpoint           string //= flag.String("endpoint", "", "-endpoint=<DAEMON_IP:PORT>")
	ws_endpoint        string //= flag.String("ws-endpoint", "", "-ws-endpoint=<IP:PORT>")
	api_endpoint       string //= flag.String("api-endpoint", "", "-api-endpoint=<IP:PORT>")
	starting_height    int64  //= flag.Int64("starting-height", -1, "-starting-height=123")
	ending_height      int64  //= flag.Int64("ending-height", -1, "-ending-height=123")
	search_filter      string //= flag.String("search-filter", "", `-search="one-term;second-term;;;another-term;second-term"`)
	exclusions         string //= flag.String("exclude", "", `-exclude=<SCID>;;;<SCID1>`)
	parallel_blocks    int64
	default_num_blocks int64 = 5
	fastsync           bool  //= flag.Bool("fastsync", false, "-fastsync")
	store_minis        bool  //= flag.Bool("store-minis", false, "-store-minis")
	progress           bool  //= flag.Bool("progress", false, "-progress")

	help_msg = `Usage: simple-gnomon [options]
A simple indexer for the DERO blockchain.

Options:
  --daemon-rpc-address=<DAEMON_IP:PORT>       Address of the daemon to connect to.
  --ws-address=<IP:PORT>                      Address to serve the ws.
  --api-address=<IP:PORT>                     Address to serve the api.
  --start-topoheight=<N>                      Height to start indexing from.
  --ending-topoheight=<N>                     Height to stop indexing at.
  --search-filter="<F;F>;;;<F;F>"             Exclusively search filter(s), overides search.json. 
  --sf-scid-exclusions="<F>;;;<F>"            Exclude SCID(s), overides exclude.json.
  --fastsync                                  Pulls gnomonSC and installs scid to index (disclaimer: automated-service subject to error)
  --enable-miniblock-lookup                   Store miniblock details within index
  --num-parallel-blocks=<N>                   Concurrently process blocks
  --progress                                  Show download progress stats.
  --help, -h                                  Show this help message.`

	established_backup      bool
	achieved_current_height int64
	lowest_height           int64
	day_of_blocks           int64

	// we are going to use these for later
	now         int64
	TOPO        int64
	IN_PROGRESS int64
	EXIT        = make(chan os.Signal, 1)
	RUNNING     bool
	ctx         context.Context
	cancel      context.CancelFunc
)

// a simple flag-parser
func parseFalgs() error {
	launch_args := os.Args[1:] // we'll skip the first one

	// launch help when present
	if slices.Contains(launch_args, "--help") || slices.Contains(launch_args, "-h") {
		fmt.Println(help_msg)
		return nil
	}

	for _, each := range launch_args {

		if strings.Contains(each, "=") {

			parts := strings.Split(each, "=")
			flag := parts[0]
			value := parts[1]

			switch flag {
			case `--daemon-rpc-address`:
				endpoint = value
			case `--ws-address`:
				ws_endpoint = value
			case `--api-address`:
				api_endpoint = value
			case `--start-topoheight`:
				n, err := strconv.Atoi(value)
				if err != nil {
					return err
				}
				starting_height = int64(n)
			case `--ending-topoheight`:
				n, err := strconv.Atoi(value)
				if err != nil {
					return err
				}

				ending_height = int64(max(n, -1))

			case `--search-filter`:
				search_filter = value
			case `--sf-scid-exclusions`:
				exclusions = value
			case `--num-parallel-blocks`:
				n, err := strconv.Atoi(value)
				if err != nil {
					return err
				}
				parallel_blocks = int64(max(n, 0))
			}

		} else {

			switch each {
			case `--fastsync`:
				fastsync = true
			case `--enable-miniblock-lookup`:
				store_minis = true
			case `--progress`:
				progress = true
			}

		}
	}
	return nil
}

// Starts gnomon indexer
func Start_gnomon_indexer() error {
	runtime.GC() // let's clean things up before beginning
	parseFalgs()

	if endpoint == "" {
		// call on the wallet ws for authorizations
		if err := connections.Set_xswd_conn(); err != nil {
			return err
		}

		// next, establish the daemon endpoint for rpc calls, waaaaay faster than through the wallet
		daemon := connections.GetDaemonEndpoint()
		endpoint = daemon.Endpoint
	}

	transport := &http.Transport{ // this is insane... but, let's find out.
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 100,
		MaxConnsPerHost:     20,
		DisableKeepAlives:   false,
		IdleConnTimeout:     time.Second * 90,
		DisableCompression:  false,
	}

	client := &http.Client{
		Timeout:   time.Second * 30, // things might take a moment
		Transport: transport,
	}

	opts := &jsonrpc.RPCClientOpts{HTTPClient: client}

	url := "http://" + endpoint + "/json_rpc"

	connections.RpcClient = jsonrpc.NewClientWithOpts(url, opts)

	// if you are getting a zero... yeah, you are not connected

	if result, _ := connections.Get_TopoHeight(); result == 0 {
		return errors.New("please connect through rpc")
	}
	info, _ := connections.GetDaemonInfo()

	day_of_blocks = ((60 * 60 * 24) / int64(info.Target))

	// we are going to use this as an upper bound
	lowest_height, _ = connections.Get_TopoHeight()

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

	fmt.Println("setting up queue processors")
	ctx, cancel = context.WithCancel(context.Background())

	go func() {
		signal.Notify(EXIT, os.Interrupt)
		for {
			select {
			case <-EXIT:
				RUNNING = false
				GracefullyStopAndExit()
				return
			case <-ctx.Done():
			}
		}
	}()
	if store_minis {
		fmt.Println("STORING MINIBLOCKS")
		go mini_db_writer(ctx)
	}
	go scid_db_writer(ctx)
	go filtering(ctx)
	go tx_handling(ctx)
	go indexing(ctx)
	// now that the backend is set up, start WS

	fmt.Println("setting up websocket")
	address := "127.0.0.1:9190"
	if ws_endpoint != "" {
		address = ws_endpoint
	}

	go connections.ListenWS(ctx, database, address)

	fmt.Println("setting up api")
	api := "127.0.0.1:8082"
	if api_endpoint != "" {
		api = api_endpoint
	}

	server := connections.NewApiServer(&connections.APIConfig{
		Enabled:              true,
		Listen:               api,
		StatsCollectInterval: "5s",
		MBLLookup:            store_minis, // default is false
	}, database)

	// serving api
	go server.Start(ctx)

	RUNNING = true

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

	sc, _ := connections.GetSC(params)

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
	now, _ = connections.Get_TopoHeight()
	// and in the event that the user wants to fast sync
	if fastsync && now-lowest_height > (day_of_blocks/4) {
		fmt.Println("fastsync activated")

		params = rpc.GetSC_Params{
			SCID:       globals.MAINNET_GNOMON_SCID,
			Code:       true,
			Variables:  true,
			TopoHeight: -1,
		}

		sc, _ = connections.GetSC(params)
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

			if height < lowest_height {
				continue
			}

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

			tx, _ := connections.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: []string{important.hash}})
			// retry
			var transact transaction.Transaction
			b, err := hex.DecodeString(tx.Txs_as_hex[0])
			if err != nil {
				log.Fatal(err)
			}
			transact.Deserialize(b)
			if transact.Version != 1 {
				log.Fatal("key", important.hash, "tx", tx, "transact", transact)
			}

			if int64(starting_height) > important.height {
				return
			}
			if progress {
				fmt.Printf("fast syncing %s %d \n", important.hash, important.height)
			}
			scid_processing <- &processingStruct{
				Height:      tx.Txs[0].Block_Height,
				Tx:          tx.Txs[0],
				Transaction: transact,
			}

		}
		work := func(imports chan importable, wg *sync.WaitGroup) {
			defer wg.Done()
			for importable := range imports {
				if !RUNNING {
					return
				}
				if areQueuesEmpty() {
					time.Sleep(time.Millisecond * time.Duration(longestQueue()))
				}
				completion := float64(importable.height) / float64(now)
				rate := completion * 100
				to_int := int64(rate)
				fmt.Printf("\rcompleted %d %%", to_int)
				task(importable)
			}
		}

		wg := sync.WaitGroup{}
		for range runtime.GOMAXPROCS(0) {
			wg.Add(1)
			go work(imports, &wg)
		}

		fmt.Println("fast sync started")
		// let's do this really fast
		for _, importable := range importables {
			select {
			case <-ctx.Done():
				return nil
			default:
				if !RUNNING {
					return nil
				}
				imports <- importable
			}
		}
		close(imports)
		waitForAllQueues()
		wg.Wait()

		fmt.Println("fast sync done")
		fmt.Println("setting lowest height current block")

		lowest_height, _ = connections.Get_TopoHeight()
	}

	// default here... could be adjusted
	if parallel_blocks == 0 {
		parallel_blocks = default_num_blocks
	}

	// simple-daemon
	go gnomon_indexer(ctx)
	return nil
}

// height processing point
func gnomon_indexer(ctx context.Context) {

	now, _ = connections.Get_TopoHeight()
	last := now

	fmt.Println("starting to index ", now)

	// gather initial results
	info, _ := connections.GetDaemonInfo()
	database.StoreGetInfoDetails(&info)
	var stagedLines = 9
	if progress {
		stagedLines++
	}
	moveUp := func(n int) {
		fmt.Printf("\033[%dA", n)
	}

	clearLine := func() {
		fmt.Print("\r\033[K")
	}
	safeString := func(s string) string {
		if len(s) > 64 {
			return s[:64]
		} else if s == "" {
			return "null"
		}
		return s
	}
	printLastStaged := func(staged structures.SCIDToIndexStage, now int64) {
		// Move back to the top of the block
		moveUp(stagedLines)

		lines := []string{
			"last staged:{",
			fmt.Sprintf("\theight   %07d", staged.Height),
			fmt.Sprintf("\tnow      %07d", now),
			fmt.Sprintf("\ttxid     %s", staged.Txid),
			fmt.Sprintf("\tmethod   %s", staged.Method),
			fmt.Sprintf("\tsender   %s", staged.Sender),
			fmt.Sprintf("\tscid     %s", staged.Scid),
			fmt.Sprintf("\theaders  %s", safeString(strings.Split(staged.Headers, ";")[0])),
			"}",
		}
		if progress {
			clearLine()
			format := "HEIGHT %07d DOWNLOADS %05d GOROUTINES: %05d BLOCKS %05d TXS_QUEUE %05d SCIDS_QUEUE %05d SCIDDB_QUEUE %03d\n"

			a := []any{
				IN_PROGRESS,
				connections.DOWNLOADS.Load(),
				runtime.NumGoroutine(),
				len(block_processing),
				len(transaction_processing),
				len(scid_processing),
				len(scid_db_queue),
			}

			fmt.Printf(format, a...)
		}

		for _, line := range lines {
			clearLine()
			fmt.Println(line)
		}
	}
	go func() { // Set up a listener for get info
		ticker := time.NewTicker(time.Second * 2)
		for RUNNING {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now, _ = connections.Get_TopoHeight()
				if last < now {
					last = now
					info, _ = connections.GetDaemonInfo()
					database.StoreGetInfoDetails(&info)
				}
			case err := <-error_channel:
				log.Fatalf("error: %s", err)
				return
			case staged := <-staged_for_writing:
				printLastStaged(staged, now)
			}
		}
	}()

	task := func(height int64) {

		// wg.Add(1)
		// go func(height int64, wg *sync.WaitGroup) {
		// 	defer wg.Done()
		result, _ := connections.GetBlockInfo(rpc.GetBlock_Params{Height: uint64(height)})
		block_processing <- &processingStruct{Height: height,
			Result: result,
		}
	}

	work := func(height_processing chan int64, wg *sync.WaitGroup) {
		defer wg.Done()
		for height := range height_processing {
			IN_PROGRESS = height

			if !RUNNING {
				return
			}

			// a simple backup strategy
			if achieved_current_height > 0 && !established_backup &&
				find_lowest_height(backup_database, now) { // if the current height is greater than a day of blocks...

				waitForAllQueues()

				backup(height)
			}

			measurements := []int{
				int(connections.DOWNLOADS.Load()),
				// runtime.NumGoroutine() / 10, // managing
				len(block_processing),
				len(transaction_processing),
				len(scid_processing),
				len(scid_db_queue),
			}

			if store_minis {
				measurements = append(measurements, len(mini_queue), len(mini_db_queue))
			}
			var m int
			for _, each := range measurements {
				m = max(m, each)
			}

			if m > 0 {
				time.Sleep(time.Millisecond * time.Duration(m))
			}
			task(height)
		}
	}
	// at least one is required
	parallel_blocks = max(parallel_blocks, 1)
	wg := sync.WaitGroup{}
	fmt.Println("starting blocks in parallel", parallel_blocks)
	result := now - lowest_height
	if starting_height < now && starting_height > 0 {
		result = now - starting_height
	}
	log.Printf("loading heights into queue %d", result)
	for i := 0; i < stagedLines; i++ {
		fmt.Println()
	}
	// simple-daemon
	for RUNNING {

		if achieved_current_height != 0 {
			time.Sleep(time.Second * time.Duration(info.Target))
		}

		if ending_height > 0 {
			now = min(ending_height, now)
		}
		// in case db needs to re-parse from a desired height
		if starting_height < now && starting_height > 0 && achieved_current_height == 0 {
			lowest_height = starting_height
		}
		result = now - lowest_height

		if result != 0 {
			height_processing := make(chan int64, result)
			for range parallel_blocks {
				wg.Add(1)
				go work(height_processing, &wg)
			}
			for height := lowest_height; height < now; height++ {
				if !RUNNING {
					return
				}
				TOPO = height

				height_processing <- height
			}
			close(height_processing)
			wg.Wait()
		}
		if achieved_current_height == 0 {
			fmt.Println("current height acheived, proceeding to passively index")
		}
		// height achieved
		achieved_current_height, _ = connections.Get_TopoHeight()

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
func indexing(ctx context.Context) {

	process_minis := func(ctx context.Context, mini_queue chan *processingStruct) {
		work := func(staged *processingStruct) {
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
		for {
			select {
			case <-ctx.Done():
				for areQueuesEmpty() {
					for staged := range mini_queue {
						work(staged)
					}
				}
			case staged := <-mini_queue:
				work(staged)
			}
		}
	}

	if store_minis {
		for range runtime.GOMAXPROCS(0) - 2 {
			go process_minis(ctx, mini_queue)
		}
	}
	work := func(staged *processingStruct) {

		bl := GetBlockDeserialized(staged.Result.Blob)

		if store_minis {
			mini_queue <- &processingStruct{
				Block:  bl,
				Result: staged.Result,
			}
		}

		count := staged.Result.Block_Header.TXCount

		if len(bl.Tx_hashes) == 0 || count == 0 { // paranoia...
			return
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
	}
	for {
		select {
		case <-ctx.Done():
			for areQueuesEmpty() {
				for staged := range block_processing {
					work(staged)
				}
			}
			return
		case staged := <-block_processing:
			work(staged)
		}
	}
}

var transaction_processing = make(chan *processingStruct, 100_000)

var holding_queue struct {
	registration atomic.Int64
	burn         atomic.Int64
	normal       atomic.Int64
}

func tx_handling(ctx context.Context) {
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
	handle := func(result rpc.GetTransaction_Result) {

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
					payload_callback(i, result.Txs[i].Block_Height, result, tx)
				}
			case transaction.SC_TX:
				// at this point the only thing that should remain is scids
				scid_processing <- &processingStruct{
					Height:      result.Txs[i].Block_Height,
					Tx:          result.Txs[i],
					Transaction: tx,
				}
			default:
				continue
			}
		}
	}

	task := func(batch_processing chan rpc.GetTransaction_Result) {
		// because order doesn't really matter here... just grab the first one
		for result := range batch_processing {
			handle(result)
		}
	}

	// this might be a good size...
	batch_size := 100

	batching := func(batch, batch_count int, hashes []string, batch_processing chan rpc.GetTransaction_Result, wg *sync.WaitGroup) {
		defer wg.Done()

		end := batch_size * batch
		if batch == batch_count-1 {
			end = len(hashes)
		}

		// and dump them into the listener channel
		result, _ := connections.GetTransaction(rpc.GetTransaction_Params{Tx_Hashes: hashes[batch_size*batch : end]})
		// retry?
		batch_processing <- result
	}

	operation := func(staged *processingStruct) {
		tx_count := len(staged.Tx_Hashes)

		//Find total number of batches
		batch_count := int(math.Ceil(float64(tx_count) / float64(batch_size)))

		// turn on the listener
		batch_processing := make(chan rpc.GetTransaction_Result, batch_count)
		go task(batch_processing)

		batchgroup := sync.WaitGroup{}
		// because the order of transactions processed doesn't matter..
		for batch := range batch_count {

			batchgroup.Add(1)
			// schedule each batch of transfers
			go batching(batch, batch_count, staged.Tx_Hashes, batch_processing, &batchgroup)
		}
		// wait for all the results to come in
		batchgroup.Wait()
		close(batch_processing)
	}

	work := func(transaction_processing chan *processingStruct) {
		for {
			select {
			case <-ctx.Done():
				for areQueuesEmpty() {
					for staged := range transaction_processing {
						operation(staged)
					}
				}
				return
			case staged := <-transaction_processing:
				operation(staged)
			}
		}
	}
	for range runtime.GOMAXPROCS(0) - 2 {
		go work(transaction_processing)
	}
}

var scid_processing = make(chan *processingStruct, 100_000)
var error_channel = make(chan error, 1)

func filtering(ctx context.Context) {

	sieve := func(height int64, tx_related_info rpc.Tx_Related_Info, each transaction.Transaction) {

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
			vars       []*structures.SCIDVariable
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
		var err error
		if !slices.Contains(database.Exclusions, scid) {

			h := height // note here

			if fastsync { // because we need the vars at the height of current
				h = -1
			}

			params = rpc.GetSC_Params{
				SCID:       scid,
				Variables:  true,
				TopoHeight: h,
			}

			switch method {
			case "install":
				params.Code = true
			case "invoke":
				params.Code = false
			}
			// 	tries := 0
			// try_again:
			sc, err = connections.GetSC(params)
			if err != nil {
				if params.Code {
					// this is an invalid contract
					if _, err := database.StoreInvalidSCIDDeploys(params.SCID, each.Fees()); err != nil {
						fmt.Println(err)
						return
					}
				}

				if len(sc.VariableStringKeys) == 0 {
					// there are no vars?
					return
				}
				error_channel <- err
			}
			// error checking is the big thing here: failed validation, attempts:4 DERO.GetSC

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

			vars = GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)

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
		if search_filter != "" && class == "" {
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
		tela_doc_signature := `STORE("docVersion",`
		tela_index_signature := `STORE("telaVersion",`

		if strings.Contains(code, nfa_signature) || strings.Contains(code, tela_doc_signature) || strings.Contains(code, tela_index_signature) {
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
			ScVars:  vars,
			ScCode:  code,
			Class:   class,
			// store as a single string
			Tags: strings.Join(tags, ","),
		}

	}

	for {
		select {
		case <-ctx.Done():
			for areQueuesEmpty() {
				for staged := range scid_processing {
					sieve(int64(staged.Height), staged.Tx, staged.Transaction)
				}
			}
			return
		case staged := <-scid_processing:
			// sift transactions over the sieve
			sieve(int64(staged.Height), staged.Tx, staged.Transaction)
		}
	}
}

type miniStructure struct {
	Minis []*structures.MBLInfo
	Hash  string
}

var mini_db_queue = make(chan miniStructure, 100_000)

func mini_db_writer(ctx context.Context) {
	all_details := database.GetAllMiniblockDetails()
	miner_map := map[string]int64{}
	for _, each := range all_details {
		for _, mini := range each {
			miner_map[mini.Miner]++
		}
	}
	work := func(staged miniStructure) {
		if _, ok := all_details[staged.Hash]; ok {
			return
		}
		database.StoreMiniblockDetailsByHash(staged.Hash, staged.Minis)
		for _, mini := range staged.Minis {
			miner_map[mini.Miner]++
			database.StoreMiniblockCountByAddress(miner_map[mini.Miner], mini.Miner)
		}
	}
	for {
		select {
		case <-ctx.Done():
			for areQueuesEmpty() {
				for staged := range mini_db_queue {
					work(staged)
				}
			}
			return
		case staged := <-mini_db_queue:
			work(staged)
		}
	}
}

var scid_db_queue = make(chan *structures.SCIDToIndexStage, 100_000)
var staged_for_writing = make(chan structures.SCIDToIndexStage, 100_000)

func scid_db_writer(ctx context.Context) {
	work := func(staged *structures.SCIDToIndexStage) {

		staged_for_writing <- *staged
		// store scid by tag
		if err := database.AddSCIDToIndex(*staged); err != nil {
			log.Fatal("indexer error:", err, staged.Scid, staged.Height)
			return
		}

		if achieved_current_height > 0 { // once the indexer has reached the top...
			// do incremental backups
			if err := backup_database.AddSCIDToIndex(*staged); err != nil {
				log.Fatal("indexer error:", err, staged.Scid, staged.Height)
				return
			}
		}

		// store counts
		database.StoreTxCount(holding_queue.registration.Load(), "registration")
		database.StoreTxCount(holding_queue.burn.Load(), "burn")
		database.StoreTxCount(holding_queue.normal.Load(), "normal")

		// store height
		storeHeight(int64(staged.Height))
	}
	for {
		select {
		case <-ctx.Done():
			for areQueuesEmpty() {
				for staged := range scid_db_queue {
					work(staged)
				}
			}
			return
		case staged := <-scid_db_queue:
			work(staged)
		}
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
	if search_filter != "" {
		search := search_filter

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

	if search_filter == "" && len(indices) == 0 {
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

	type excluded struct {
		Name   string
		SCID   string
		Reason string
	}

	var excludes []excluded

	// if exclusions are provided...
	if exclusions != "" {
		exclude := exclusions

		callback := func(i int, scid string) {
			excludes = append(excludes, excluded{
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
	if exclusions == "" && len(excludes) == 0 {
		exclude := filepath.Join("config", "exclude.json")
		if _, err := os.Stat(exclude); err != nil {
			// for now, these are the collections we are looking for
			// title, search terms
			excludes = []excluded{
				{Name: "NAMESERVICE", SCID: globals.NAMESERVICE, Reason: "Hardcoded Contract"},
				{Name: "Gnomon Smart Contract", SCID: globals.MAINNET_GNOMON_SCID, Reason: "Large Contract"},
			}

			if err := os.Mkdir(filepath.Dir(exclude), 0700); err != nil {

				if errors.Is(err, os.ErrExist) {
					fmt.Println(err)
				} else {
					return err
				}
			}

			b, err := json.MarshalIndent(excludes, "", "\t")
			if err != nil {
				return err
			}

			if err := os.WriteFile(exclude, b, 0600); err != nil {
				return err
			}
		} else {
			fi, err := os.OpenFile(exclude, os.O_RDONLY, 0600)
			if err != nil {
				return err
			}

			b, err := io.ReadAll(fi)
			if err != nil {
				return err
			}

			if err := json.Unmarshal(b, &excludes); err != nil {
				return err
			}
		}
	}
	fmt.Println("exclusions", len(excludes))

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

	for _, exclude := range excludes {
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
