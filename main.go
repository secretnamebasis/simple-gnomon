package main

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/deroproject/derohe/rpc"
	"github.com/gorilla/websocket"
	"github.com/secretnamebasis/simple-gnomon/cmd"
	"github.com/secretnamebasis/simple-gnomon/connections"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
	"github.com/ybbus/jsonrpc"
)

func main() {
	var now int64
	closing := false
	a := app.NewWithID("simple-gnomon_" + rand.Text())
	w := a.NewWindow("simple-gnomon")
	w.Resize(fyne.NewSize(400, 200))
	w.SetCloseIntercept(func() {
		cmd.RUNNING = false
		closing = true
		os.Exit(0)
	})
	endpoints := ""
	connection := widget.NewEntry()
	readout := widget.NewLabel("")
	indexed_height := widget.NewLabel("")
	current_height := widget.NewLabel("")
	average_blocks_per_hour := widget.NewLabel("")
	estimated_time_to_completion := widget.NewLabel("")
	progress_bar := widget.NewProgressBar()
	connection.SetPlaceHolder("127.0.0.1:10102")
	tapped := func() {
		ends := []string{}

		connects := connection.Text

		if strings.Contains(connects, ",") {
			ends = append(ends, strings.Split(connects, ",")...)
		} else if strings.Contains(connects, "\n") {
			ends = append(ends, strings.Split(connects, "\n")...)
		} else if strings.Contains(connects, " ") {
			ends = append(ends, strings.Split(connects, " ")...)
		} else {
			ends = append(ends, connects)
		}

		if len(ends) == 0 {
			return
		}

		var connect jsonrpc.RPCClient

		for _, endpoint := range ends {

			opts := &jsonrpc.RPCClientOpts{HTTPClient: &http.Client{Timeout: time.Second * 3}}
			url := "http://" + endpoint + "/json_rpc"
			c := jsonrpc.NewClientWithOpts(url, opts)

			// test connection current
			now = connections.Get_TopoHeight(c)
			if now == 0 {
				continue
			}

			result := connections.GetBlockInfo(c, rpc.GetBlock_Params{Hash: "c1cbc9e888d76dc0d6d4bad7e9144aa198155618e7eb8628e18e8127b1aa5ac9"})

			if result.Block_Header.Height != 420 {
				continue
			}

			// the one that works works
			connect = c
			// add it to the string
			endpoints += endpoint + ","
		}

		// if there happens to be one
		endpoints = strings.TrimSuffix(endpoints, ",")

		// paranoia
		endpoints = strings.TrimPrefix(endpoints, ",")

		// squeeky clean
		endpoints = strings.TrimSpace(endpoints)

		os.Args = append(os.Args,
			"-endpoints="+endpoints,
			// the first g45 nft starts at 678864

			// "-progress",
		)
		if cmd.RUNNING {
			return
		}
		go func() {
			defer func() {
				if r := recover(); r != nil {
					// Handle/log the panic here
					fyne.DoAndWait(func() { readout.SetText(fmt.Sprintf("gnomon failed: \n%v", r)) })
				}
			}()
			// now go start gnomon
			cmd.Start_gnomon_indexer()
		}()

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
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, // allow self-signed certs
			}
			indexer_connection, _, err = dialer.Dial(url, nil)
			if err != nil {
				panic(err)
			}
			last := float64(0)
			height1, err := getLastIndexHeight(getParams{IDX: "all"})
			if err != nil {
				panic(err)
			}
			first := height1.Result
			for range time.NewTicker(time.Second).C {
				result, err := getAllSCIDsAndOwners(getParams{IDX: "all"})
				if err != nil {
					panic(err)
				}
				all := strconv.Itoa(len(result.Result))
				text := "ALL SCIDS & OWNERS: " + all + "\n"
				result, err = getAllSCIDsAndOwners(getParams{IDX: "g45"})
				if err != nil {
					panic(err)
				}
				g45 := strconv.Itoa(len(result.Result))
				text += "ALL G45 & OWNERS: " + g45 + "\n"
				result, err = getAllSCIDsAndOwners(getParams{IDX: "nfa"})
				if err != nil {
					panic(err)
				}
				nfa := strconv.Itoa(len(result.Result))
				text += "ALL NFAs & OWNERS: " + nfa
				height1, err := getLastIndexHeight(getParams{IDX: "all"})
				if err != nil {
					panic(err)
				}
				now := connections.GetDaemonInfo(connect).TopoHeight
				if last == 0 {
					last = height1.Result
				}
				tick := height1.Result - last
				last = height1.Result
				tick *= 60 * 60
				duration := time.Since(start).Hours()
				average := last - first
				if duration == 0 {
					duration = 1 // avoid division by zero
				}
				average /= duration
				if average == 0 {
					average = 1
				}
				estimated := now / int64(average)
				fyne.DoAndWait(func() {
					readout.SetText(text)
					current_height.SetText("current height:" + strconv.Itoa(int(now)))
					indexed_height.SetText("indexed height:" + strconv.Itoa(int(height1.Result)))
					average_blocks_per_hour.SetText("average blocks per hour:" + strconv.Itoa(int(average)))
					estimated_time_to_completion.SetText("estimated hours until completion:" + strconv.Itoa(int(estimated)))
					progress_bar.SetValue(last / float64(now))
				})
			}

		}()
	}
	button := widget.NewButtonWithIcon("Start Gnomon Indexer", theme.MediaPlayIcon(), tapped)
	connection.OnSubmitted = func(s string) { button.OnTapped() }
	connection.ActionItem = button
	content := container.NewVBox(
		readout,
		current_height,
		indexed_height,
		average_blocks_per_hour,
		estimated_time_to_completion,
		progress_bar,
		connection,
	)
	w.SetContent(content)
	w.ShowAndRun()
}

type getAllSCIDsAndOwnersResult struct {
	Result map[string]any `json:"result"`
}

type getParams struct {
	IDX string
}

var indexer_connection *websocket.Conn

func getAllSCIDsAndOwners(params getParams) (getAllSCIDsAndOwnersResult, error) {

	msg := map[string]any{
		"method": "GetAllOwnersAndSCIDs",
		"id":     "1",
		"params": params,
	}

	var err error

	if err := indexer_connection.WriteJSON(msg); err != nil {
		return getAllSCIDsAndOwnersResult{}, errors.New("failed to write")
	}

	_, b, err := indexer_connection.ReadMessage()
	if err != nil {
		return getAllSCIDsAndOwnersResult{}, errors.New("failed to read")
	}

	var r structures.JSONRpcResp
	if err := json.Unmarshal(b, &r); err != nil {
		return getAllSCIDsAndOwnersResult{}, errors.New("failed to unmarshal")
	}

	return getAllSCIDsAndOwnersResult{r.Result.(map[string]any)}, nil
}

type getLastHeightResult struct {
	Result float64 `json:"result"`
}

func getLastIndexHeight(params getParams) (getLastHeightResult, error) {

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
