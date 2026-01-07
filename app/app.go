package app

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/deroproject/derohe/globals"
	"github.com/gorilla/websocket"
	"github.com/secretnamebasis/simple-gnomon/cmd"
	"github.com/secretnamebasis/simple-gnomon/connections"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

var (
	data_dir,
	websocket_address,
	node_connection,
	current_height,
	topo_height,
	in_progress,
	last_indexed,
	average_per_hour,
	estimated_hours,
	total_hours,
	// all_minis,
	// all_miners,
	all_normal,
	all_registration,
	all_scids,
	all_g45s,
	all_nfas string

	row_values = []string{}

	row_headers = []string{
		"DATA DIRECTORY:",
		"WEBSOCKET:",
		"NODE ENDPOINT:",
		"CURRENT HEIGHT:",
		"TOPOLOGICAL HEIGHT:",
		"LAST PROCESSED HEIGHT",
		"LAST INDEXED HEIGHT:",
		"AVG BLOCKS/HOUR:",
		"EST. HRS REMAIN:",
		"EST. HRS TOTAL:",
		// "TOTAL MINIS",
		// "TOTAL MINERS",
		"NORMAL TXS:",
		"REGISTRATIONS:",
		"SCIDS & OWNERS:",
		"G45s & OWNERS:",
		"NFAs & OWNERS:",
	}
)

var indexer_connection *websocket.Conn
var start time.Time
var first, last, now int64
var height1 getLastHeightResult

func RenderGUI() {
	ctx, cancel := context.WithCancel(context.Background())
	closing := false

	a := app.NewWithID("simple-gnomon_" + rand.Text())
	w := a.NewWindow("simple-gnomon")
	w.Resize(fyne.NewSize(400, 600))
	w.SetCloseIntercept(func() {
		closing = true
		if cmd.RUNNING {
			cmd.RUNNING = false
			cmd.EXIT <- os.Interrupt
			cmd.GracefullyStopAndExit(cancel)
		}
		cancel()
		w.Close()
	})
	data_dir = filepath.Base(globals.GetDataDirectory())
	endpoint := ""
	connection := widget.NewEntry()
	connection.SetPlaceHolder("dero node: 127.0.0.1:10102")
	parallel := widget.NewEntry()
	parallel.SetPlaceHolder("n blocks processed in parallel: 5")
	msg := "NOTICE:\n" +
		"Number fo parallel blocks increases load on machine,\n" +
		"results may vary, and are subject to error.\nPlease be advised.\n"
	parallel_notice := widget.NewLabel(msg)
	ws_endpoint := widget.NewEntry()
	ws_endpoint.SetPlaceHolder("serve ws on: 127.0.0.1:9190")
	api_endpoint := widget.NewEntry()
	api_endpoint.SetPlaceHolder("serve api on: 127.0.0.1:8082")
	starting_height := widget.NewEntry()
	starting_height.SetPlaceHolder("defaults to 0")
	ending_height := widget.NewEntry()
	ending_height.SetPlaceHolder("defaults to current height")
	search_filter := widget.NewEntry()
	search_filter.SetPlaceHolder(`search-term;second-term;;;search-term1;second-term1`)
	exclusions := widget.NewEntry()
	exclusions.SetPlaceHolder("SCID;;;SCID1")
	fastsync := widget.NewCheck("fastsync?", func(b bool) {})
	msg = "NOTICE:\n" +
		"Fastsync data is provided by gnomonSC,\n" +
		"as an automated service, it is subject to error.\nPlease be advised.\n"
	fastsync_notice := widget.NewLabel(msg)
	fastsync_notice.Alignment = fyne.TextAlignCenter
	minis := widget.NewCheck("store mini block data?", func(b bool) {})
	msg = "NOTICE:\n" +
		"Storing miniblocks adds overhead to indexing,\n" +
		"and takes considerably more time.\nPlease be advised.\n"
	mini_notice := widget.NewLabel(msg)
	mini_notice.Alignment = fyne.TextAlignCenter
	drop_down := widget.NewAccordion(
		widget.NewAccordionItem("indexer endpoints", container.NewVBox(ws_endpoint, api_endpoint)),
		widget.NewAccordionItem("Block Parallelization", container.NewVBox(parallel, parallel_notice)),
		widget.NewAccordionItem("starting height", starting_height),
		widget.NewAccordionItem("ending height", ending_height),
		widget.NewAccordionItem("search filter", search_filter),
		widget.NewAccordionItem("exclusions", exclusions),
		widget.NewAccordionItem("fastsync", container.NewVBox(fastsync_notice, container.NewCenter(fastsync))),
		widget.NewAccordionItem("store minis", container.NewVBox(mini_notice, container.NewCenter(minis))),
	)

	// var table *widget.Table
	length := func() (int, int) { return len(row_headers), 2 }
	create := func() fyne.CanvasObject { return widget.NewLabel("") }
	update := func(id widget.TableCellID, co fyne.CanvasObject) {
		switch id.Col {
		case 0:
			if id.Row >= len(row_headers) {
				return
			}
			co.(*widget.Label).SetText(row_headers[id.Row])
		case 1:
			if id.Row >= len(row_values) {
				return
			}
			co.(*widget.Label).SetText(row_values[id.Row])
		}
	}
	table := widget.NewTable(length, create, update)
	progress_bar := widget.NewProgressBar()
	progress_bar.Hide()

	stop_btn := widget.NewButtonWithIcon("Stop Gnomon", theme.MediaStopIcon(), nil)
	stop_btn.Hide()

	stop_btn.OnTapped = func() {
		cancel()
		cmd.RUNNING = false
		stop_btn.Hide()
		table.Hide()
		progress_bar.Hide()
		drop_down.Show()
		connection.Show()
	}

	updateTable := func() {
		result, err := getTxCount(getTxCountParams{"normal"})
		if err != nil {
			panic(err)
		}

		all_normal = strconv.Itoa(int(result.Result))

		result, err = getTxCount(getTxCountParams{"registration"})
		if err != nil {
			panic(err)
		}
		all_registration = strconv.Itoa(int(result.Result))

		result, err = getTxCount(getTxCountParams{"scids"})
		if err != nil {
			panic(err)
		}
		all_scids = strconv.Itoa(int(result.Result))
		g45s, err := getSCIDsByTag(getSCIDsByTagParams{"g45"})
		if err != nil {
			panic(err)
		}

		all_g45s = strconv.Itoa(len(g45s.Result))

		nfas, err := getSCIDsByTag(getSCIDsByTagParams{"nfa"})
		if err != nil {
			panic(err)
		}

		all_nfas = strconv.Itoa(len(nfas.Result))
		height1, err := getLastIndexHeight()
		if err != nil {
			panic(err)
		}
		current_height = strconv.Itoa(int(now))
		topo_height = strconv.Itoa(int(cmd.TOPO))
		last_indexed = strconv.Itoa(int(height1.Result))
		row_values = []string{
			data_dir,
			websocket_address,
			node_connection,
			current_height,
			topo_height,
			in_progress,
			last_indexed,
			average_per_hour,
			estimated_hours,
			total_hours,
			// all_minis,
			// all_miners,
			all_normal,
			all_registration,
			all_scids,
			all_g45s,
			all_nfas,
		}

	}
	var progressbar_value float64

	action := func() {
		if last == 0 {
			last = int64(height1.Result)
		}

		duration := time.Since(start).Seconds()
		average := last - first
		if int64(duration) == 0 {
			duration = 1 // avoid division by zero
		}
		average /= int64(duration)
		if average == 0 {
			average = 1
		}
		estimated := now / average
		remaining := (now - int64(last)) / int64(average)

		average_per_hour = strconv.Itoa(int(average * 60 * 60))
		estimated_hours = strconv.Itoa(int(remaining / 60 / 60))
		total_hours = strconv.Itoa(int(estimated / 60 / 60))
		progressbar_value = float64(last) / float64(now)

	}

	passive := func() {
		start = time.Now()
		average_per_hour = strconv.Itoa(int((4800 / 24)))
		estimated_hours = "PASSIVE MODE"
		total_hours = "NEXT BLOCK"
		progressbar_value = 1

	}

	set_connection := func() {
		var err error
		ws := "127.0.0.1:9190"
		if ws_endpoint.Text != "" {
			ws = ws_endpoint.Text
		}
		url := "ws://" + ws + "/ws"
		websocket_address = url
		dialer := websocket.Dialer{TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, // allow self-signed certs
		}}

		indexer_connection, _, err = dialer.Dial(url, nil)
		if err != nil {
			log.Fatal(err)
			return
		}
	}

	obtain_initial_height := func() getLastHeightResult {
		height1, err := getLastIndexHeight()
		if err != nil {
			log.Fatal(err)
			return getLastHeightResult{}
		}

		first = int64(height1.Result)

		if starting_height.Text != "" {
			v, err := strconv.Atoi(starting_height.Text)
			if err != nil {
				fmt.Println(err)
				dialog.ShowError(err, w)
				return getLastHeightResult{}
			}
			first = int64(v)
		}

		last = cmd.TOPO
		info, _ := connections.GetDaemonInfo()
		now = info.TopoHeight

		return height1
	}

	start_gnomon := func(ctx context.Context) {
		set_connection()
		if indexer_connection != nil {
			defer indexer_connection.Close()
		}

		height1 = obtain_initial_height()
		updateTable()
		action()

		ticker := time.NewTicker(time.Second * 10)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !cmd.RUNNING {
					return
				}
				info, _ := connections.GetDaemonInfo()
				now = info.TopoHeight
				in_progress = strconv.Itoa(int(cmd.IN_PROGRESS))

				switch now {
				case cmd.TOPO + 1:
					passive()
				default:
					action()
				}

				updateTable()

				fyne.Do(func() {
					table.Refresh()
					progress_bar.SetValue(progressbar_value)
				})
			}
		}
	}
	tapped := func() {
		connection.Hide()
		stop_btn.Show()
		drop_down.Hide()
		table.Show()
		progress_bar.Show()
		if cmd.RUNNING {
			return
		}

		// now go start gnomon
		endpoint = connection.Text

		if endpoint == "" {
			return
		}

		endpoint_flag := "--daemon-rpc-address=" + endpoint

		os.Args = append(os.Args, endpoint_flag)

		if parallel.Text != "" {
			os.Args = append(os.Args, "--num-parallel-blocks="+parallel.Text)
		}

		if ws_endpoint.Text != "" {
			os.Args = append(os.Args, "--ws-endpoint="+ws_endpoint.Text)
		}
		if api_endpoint.Text != "" {
			os.Args = append(os.Args, "--api-endpoint="+api_endpoint.Text)
		}
		if starting_height.Text != "" {
			os.Args = append(os.Args, "--starting-topoheight="+starting_height.Text)
		}
		if ending_height.Text != "" {
			os.Args = append(os.Args, "--ending-topoheight="+ending_height.Text)
		}
		if search_filter.Text != "" {
			os.Args = append(os.Args, "--search-filter="+search_filter.Text)
		}
		if exclusions.Text != "" {
			os.Args = append(os.Args, "--exclude="+exclusions.Text)
		}
		if fastsync.Checked {
			os.Args = append(os.Args, "--fastsync")
		}
		if minis.Checked {
			os.Args = append(os.Args, "--store-minis")
		}

		node_connection = endpoint

		if err := cmd.Start_gnomon_indexer(); err != nil {
			fmt.Println(err)
			dialog.ShowError(err, w)
			connection.Show()
			drop_down.Show()
			table.Hide()
			progress_bar.Hide()
			return
		}

		for !cmd.RUNNING {
			if closing {
				os.Exit(0)
			}
			fmt.Println("gnomon is starting, please hold")
			time.Sleep(time.Second * 1)
		}

		start = time.Now()
		go start_gnomon(ctx)
	}

	button := widget.NewButtonWithIcon("Start Gnomon Indexer", theme.MediaPlayIcon(), tapped)
	connection.OnSubmitted = func(s string) { button.OnTapped() }
	connection.ActionItem = button

	table.OnSelected = func(id widget.TableCellID) {
		table.UnselectAll()
	}
	table.SetColumnWidth(0, 200)
	table.SetColumnWidth(1, 150)
	table.Hide()
	content := container.NewBorder(
		container.NewVBox(
			connection,
			stop_btn,
			progress_bar,
			drop_down,
		),
		nil, nil, nil,
		table,
	)
	w.SetContent(content)
	w.ShowAndRun()
}

type getAllSCIDSAndOwnersResult struct {
	Result map[string]any `json:"result"`
}

func getAllSCIDSAndOwners() (getAllSCIDSAndOwnersResult, error) {

	msg := map[string]any{
		"method": "GetAllOwnersAndSCIDs",
		"id":     "1",
	}

	var err error

	if err := indexer_connection.WriteJSON(msg); err != nil {
		return getAllSCIDSAndOwnersResult{}, errors.New("failed to write")
	}

	_, b, err := indexer_connection.ReadMessage()
	if err != nil {
		return getAllSCIDSAndOwnersResult{}, errors.New("failed to read")
	}

	var r structures.JSONRpcResp
	if err := json.Unmarshal(b, &r); err != nil {
		return getAllSCIDSAndOwnersResult{}, errors.New("failed to unmarshal")
	}

	return getAllSCIDSAndOwnersResult{r.Result.(map[string]any)}, nil
}

type getLastHeightResult struct {
	Result float64 `json:"result"`
}

func getLastIndexHeight() (getLastHeightResult, error) {

	msg := map[string]any{
		"method": "GetLastIndexHeight",
		"id":     "1",
	}

	var err error

	if err := indexer_connection.WriteJSON(msg); err != nil {
		return getLastHeightResult{}, errors.New("failed to write")
	}

	_, b, err := indexer_connection.ReadMessage()
	if err != nil {
		return getLastHeightResult{}, errors.New("failed to read")
	}

	var r structures.JSONRpcResp
	if err := json.Unmarshal(b, &r); err != nil {
		return getLastHeightResult{}, errors.New("failed to unmarshal")
	}

	return getLastHeightResult{r.Result.(float64)}, nil
}

type getTxCountParams struct {
	Tx_Type string
}
type getTxCountResult struct {
	Result float64 `json:"result"`
}

func getTxCount(params getTxCountParams) (getTxCountResult, error) {

	msg := map[string]any{
		"method": "GetTxCount",
		"id":     "1",
		"params": params,
	}

	var err error

	if err := indexer_connection.WriteJSON(msg); err != nil {
		return getTxCountResult{}, errors.New("failed to write")
	}

	_, b, err := indexer_connection.ReadMessage()
	if err != nil {
		return getTxCountResult{}, errors.New("failed to read")
	}

	var r structures.JSONRpcResp
	if err := json.Unmarshal(b, &r); err != nil {
		return getTxCountResult{}, errors.New("failed to unmarshal")
	}

	return getTxCountResult{r.Result.(float64)}, nil
}

type getSCIDsByTagParams struct {
	Tag string
}
type getSCIDsByTagResult struct {
	Result []any `json:"result"`
}

func getSCIDsByTag(params getSCIDsByTagParams) (getSCIDsByTagResult, error) {

	msg := map[string]any{
		"method": "GetAllSCIDsByTag",
		"id":     "1",
		"params": params,
	}

	var err error

	if err := indexer_connection.WriteJSON(msg); err != nil {
		return getSCIDsByTagResult{}, errors.New("failed to write")
	}

	_, b, err := indexer_connection.ReadMessage()
	if err != nil {
		return getSCIDsByTagResult{}, errors.New("failed to read")
	}

	var r structures.JSONRpcResp
	if err := json.Unmarshal(b, &r); err != nil {
		return getSCIDsByTagResult{}, errors.New("failed to unmarshal")
	}

	return getSCIDsByTagResult{r.Result.([]any)}, nil
}

// type getMiniDetailsParams struct {
// 	Tag string
// }
// type getMiniDetailsResult struct {
// 	Result map[string]any `json:"result"`
// }

// func getMiniDetails(params getMiniDetailsParams) (getMiniDetailsResult, error) {

// 	msg := map[string]any{
// 		"method": "GetAllMiniblockDetails",
// 		"id":     "1",
// 		"params": params,
// 	}

// 	var err error

// 	if err := indexer_connection.WriteJSON(msg); err != nil {
// 		return getMiniDetailsResult{}, errors.New("failed to write")
// 	}

// 	_, b, err := indexer_connection.ReadMessage()
// 	if err != nil {
// 		return getMiniDetailsResult{}, errors.New("failed to read")
// 	}

// 	var r structures.JSONRpcResp
// 	if err := json.Unmarshal(b, &r); err != nil {
// 		return getMiniDetailsResult{}, errors.New("failed to unmarshal")
// 	}

// 	return getMiniDetailsResult{r.Result.(map[string]any)}, nil
// }
