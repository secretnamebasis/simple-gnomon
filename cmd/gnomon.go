package cmd

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"strings"
	"sync/atomic"
	"time"

	"github.com/deroproject/derohe/config"
	"github.com/deroproject/derohe/rpc"
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

	established_backup      bool
	achieved_current_height int64
	lowest_height           int64
	day_of_blocks           int64
	now                     int64

	// we are going to use these for later
	TOPO        int64
	IN_PROGRESS atomic.Int64

	EXIT    = make(chan os.Signal, 1)
	RUNNING bool
	// ctx         context.Context
	// cancel      context.CancelFunc
)

var error_channel = make(chan error, 1)

// Starts gnomon indexer
func Start_gnomon_indexer() error {
	runtime.GC() // let's clean things up before beginning
	parseFlags()

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
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		signal.Notify(EXIT, os.Interrupt)
		for {
			select {
			case <-EXIT:
				RUNNING = false
				GracefullyStopAndExit(cancel)
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
	go scid_handling(ctx)
	go tx_handling(ctx)
	go block_handling(ctx)
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
		if err := fastsync_handling(ctx); err != nil {
			return err
		}
	}
	// default here... could be adjusted
	if parallel_blocks == 0 {
		parallel_blocks = default_num_blocks
	}
	now, _ = connections.Get_TopoHeight()
	result := now - lowest_height
	if starting_height < now && starting_height > 0 {
		result = now - starting_height
	}
	fmt.Println("starting to index ", now)

	// gather initial results
	info, _ = connections.GetDaemonInfo()
	database.StoreGetInfoDetails(&info)

	// at least one is required
	parallel_blocks = max(parallel_blocks, 1)
	fmt.Println("starting blocks in parallel", parallel_blocks)
	log.Printf("loading heights into queue %d", result)
	go start_printer(ctx, now)
	// simple-daemon
	go height_handling(ctx)
	return nil
}

func start_printer(ctx context.Context, last int64) { // Set up a listener for get info
	ticker := time.NewTicker(time.Second * 2)
	defer ticker.Stop()

	for RUNNING {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:

			now, _ = connections.Get_TopoHeight()

			if last < now {

				format := "now %d in_progress %d"
				a := []any{now, IN_PROGRESS.Load()}

				if achieved_current_height == 0 {
					percent := IN_PROGRESS.Load() * 100 / now
					format += " complete %d%%"
					a = append(a, percent)
				} else {
					format += " passive scan"
				}
				log.Printf(format, a...)

				last = now

				info, _ = connections.GetDaemonInfo()
				database.StoreGetInfoDetails(&info)

			}
		case staged := <-staged_for_writing:
			printLastStaged(staged, now)

		case err := <-error_channel:
			log.Fatalf("error: %s", err)
			return
		}
	}
}

func printLastStaged(staged structures.SCIDToIndexStage, now int64) {
	var lines []string
	line := fmt.Sprintf("scinstall:{"+
		"height:%07d,"+
		"sender:%s,"+
		"scid:%s,"+
		"headers:%s"+
		"}", []any{
		staged.Height,
		staged.Sender,
		staged.Scid,
		safeString(strings.Split(staged.Headers, ";")[0]),
	}...)

	lines = append(lines, line)

	if progress { // used to monitor queues
		format := "HEIGHT %07d" +
			" DOWNLOADS %05d" +
			" GOROUTINES: %05d" +
			" BLOCKS %05d" +
			" TXS_QUEUE %05d" +
			" SCIDS_QUEUE %05d" +
			" SCIDDB_QUEUE %03d"

		a := []any{
			IN_PROGRESS.Load(),
			connections.DOWNLOADS.Load(),
			runtime.NumGoroutine(),
			len(block_processing),
			len(transaction_processing),
			len(scid_processing),
			len(scid_db_queue),
		}

		line := []string{fmt.Sprintf(format, a...)}

		lines = append(line, lines...)
	}

	for _, each := range lines {
		fmt.Println(each)
	}
}
