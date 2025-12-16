package main

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
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
	last_indexed,
	average_per_hour,
	estimated_hours,
	total_hours,
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
		"LAST INDEXED HEIGHT:",
		"AVG BLOCKS/HOUR:",
		"EST. HRS REMAIN:",
		"EST. HRS TOTAL:",
		"NORMAL TXS:",
		"REGISTRATIONS:",
		"SCIDS & OWNERS:",
		"G45s & OWNERS:",
		"NFAs & OWNERS:",
	}
)

func main() {
	closing := false
	a := app.NewWithID("simple-gnomon_" + rand.Text())
	w := a.NewWindow("simple-gnomon")
	w.Resize(fyne.NewSize(400, 600))
	w.SetCloseIntercept(func() {
		cmd.RUNNING = false
		closing = true
		os.Exit(0)
	})
	data_dir = filepath.Base(globals.GetDataDirectory())
	websocket_address = "127.0.0.1:9190/ws"
	endpoint := ""
	connection := widget.NewEntry()

	progress_bar := widget.NewProgressBar()
	progress_bar.Hide()
	connection.SetPlaceHolder("127.0.0.1:10102")
	var table *widget.Table

	tapped := func() {
		connection.Hide()
		progress_bar.Show()
		if cmd.RUNNING {
			return
		}

		// now go start gnomon
		endpoint = connection.Text

		endpoint_flag := "-endpoint=" + endpoint

		os.Args = append(os.Args, endpoint_flag)

		node_connection = endpoint

		go cmd.Start_gnomon_indexer()

		for !cmd.RUNNING {
			if closing {
				return
			}
			fmt.Println("gnomon is starting, please hold")
			time.Sleep(time.Second)
		}

		start := time.Now()

		go func() {
			var err error
			url := "ws://127.0.0.1:9190/ws"
			dialer := websocket.Dialer{

				TLSClientConfig: &tls.Config{

					InsecureSkipVerify: true}, // allow self-signed certs
			}
			indexer_connection, _, err = dialer.Dial(url, nil)
			if err != nil {
				panic(err)
			}

			var last int64

			height1, err := getLastIndexHeight(getAllParams{DB_Name: "all"})
			if err != nil {
				panic(err)
			}
			first := int64(height1.Result)

			action := func(now int64) {
				last = cmd.TOPO
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

				fyne.DoAndWait(func() {
					progress_bar.SetValue(float64(last / now))
				})
			}

			passive := func() {
				start = time.Now()
				average_per_hour = strconv.Itoa(int((4800 / 24)))
				estimated_hours = "PASSIVE MODE"
				total_hours = "NEXT BLOCK"

				fyne.DoAndWait(func() {
					progress_bar.SetValue(1)
				})

			}

			ticker := time.NewTicker(time.Second)

			for range ticker.C {
				now := connections.GetDaemonInfo().TopoHeight

				result, err := getTxCount(getTxCountParams{
					DB_Name: "all",
					Tx_Type: "normal",
				})
				if err != nil {
					panic(err)
				}

				all_normal = strconv.Itoa(int(result.Result))

				result, err = getTxCount(getTxCountParams{
					DB_Name: "all",
					Tx_Type: "registration",
				})
				if err != nil {
					panic(err)
				}

				all_registration = strconv.Itoa(int(result.Result))

				result, err = getTxCount(getTxCountParams{
					DB_Name: "all",
					Tx_Type: "scids",
				})
				if err != nil {
					panic(err)
				}
				all_scids = strconv.Itoa(int(result.Result))

				result, err = getTxCount(getTxCountParams{
					DB_Name: "g45",
					Tx_Type: "scids",
				})
				if err != nil {
					panic(err)
				}

				all_g45s = strconv.Itoa(int(result.Result))

				result, err = getTxCount(getTxCountParams{
					DB_Name: "nfa",
					Tx_Type: "scids",
				})
				if err != nil {
					panic(err)
				}
				all_nfas = strconv.Itoa(int(result.Result))

				height1, err := getLastIndexHeight(getAllParams{DB_Name: "all"})
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
					last_indexed,
					average_per_hour,
					estimated_hours,
					total_hours,
					all_normal,
					all_registration,
					all_scids,
					all_g45s,
					all_nfas,
				}
				fyne.DoAndWait(func() { table.Refresh() })
				switch now {
				case cmd.TOPO + 1:
					passive()
				default:
					action(now)
				}

			}
		}()
	}
	button := widget.NewButtonWithIcon("Start Gnomon Indexer", theme.MediaPlayIcon(), tapped)
	connection.OnSubmitted = func(s string) { button.OnTapped() }
	connection.ActionItem = button

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
	table = widget.NewTable(length, create, update)
	table.OnSelected = func(id widget.TableCellID) {
		table.UnselectAll()
	}
	table.SetColumnWidth(0, 200)
	table.SetColumnWidth(1, 150)
	content := container.NewBorder(
		container.NewVBox(
			progress_bar,
			connection,
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

type getAllParams struct {
	DB_Name string
}

var indexer_connection *websocket.Conn

func getAllSCIDSAndOwners(params getAllParams) (getAllSCIDSAndOwnersResult, error) {

	msg := map[string]any{
		"method": "GetAllOwnersAndSCIDs",
		"id":     "1",
		"params": params,
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

func getLastIndexHeight(params getAllParams) (getLastHeightResult, error) {

	msg := map[string]any{
		"method": "GetLastIndexHeight",
		"id":     "1",
		"params": params,
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
	DB_Name string
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
