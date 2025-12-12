package main

import (
	"crypto/rand"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/app"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
	"github.com/gorilla/websocket"
	"github.com/secretnamebasis/simple-gnomon/cmd"
	"github.com/secretnamebasis/simple-gnomon/connections"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

func main() {
	closing := false
	a := app.NewWithID("simple-gnomon_" + rand.Text())
	w := a.NewWindow("simple-gnomon")
	w.Resize(fyne.NewSize(400, 200))
	w.SetCloseIntercept(func() {
		cmd.RUNNING = false
		closing = true
		os.Exit(0)
	})
	endpoint := ""
	connection := widget.NewEntry()
	readout := widget.NewLabel("")
	indexed_height := widget.NewLabel("")
	current_height := widget.NewLabel("")
	average_blocks_per_hour := widget.NewLabel("")
	estimated_time_to_completion := widget.NewLabel("")
	progress_bar := widget.NewProgressBar()
	connection.SetPlaceHolder("127.0.0.1:10102")
	tapped := func() {
		// now go start gnomon
		endpoint = connection.Text
		os.Args = append(os.Args,
			"-endpoint="+endpoint,
			// the first g45 nft starts at 678864
			// "-starting_height=154000",
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

				TLSClientConfig: &tls.Config{

					InsecureSkipVerify: true}, // allow self-signed certs
			}
			indexer_connection, _, err = dialer.Dial(url, nil)
			if err != nil {
				panic(err)
			}
			last := float64(0)

			height1, err := getLastIndexHeight(getAllParams{IDX: "all"})
			if err != nil {
				panic(err)
			}
			first := height1.Result
			action := func() {
				result, err := getTxCount(getTxCountParams{
					IDX:     "all",
					Tx_Type: "normal",
				})
				if err != nil {
					panic(err)
				}
				normal := strconv.Itoa(int(result.Result))

				text := "ALL Normal: " + normal + "\n"

				result, err = getTxCount(getTxCountParams{
					IDX:     "all",
					Tx_Type: "registration",
				})
				if err != nil {
					panic(err)
				}
				registrations := strconv.Itoa(int(result.Result))

				text += "ALL Registrations: " + registrations + "\n"

				result, err = getTxCount(getTxCountParams{
					IDX:     "all",
					Tx_Type: "scids",
				})
				if err != nil {
					panic(err)
				}
				all := strconv.Itoa(int(result.Result))

				text += "ALL SCIDS & OWNERS: " + all + "\n"

				result, err = getTxCount(getTxCountParams{
					IDX:     "g45",
					Tx_Type: "scids",
				})
				if err != nil {
					panic(err)
				}
				g45 := strconv.Itoa(int(result.Result))

				text += "ALL G45 & OWNERS: " + g45 + "\n"

				result, err = getTxCount(getTxCountParams{
					IDX:     "nfa",
					Tx_Type: "scids",
				})
				if err != nil {
					panic(err)
				}
				nfa := strconv.Itoa(int(result.Result))

				text += "ALL NFAs & OWNERS: " + nfa

				height1, err := getLastIndexHeight(getAllParams{IDX: "all"})
				if err != nil {
					panic(err)
				}
				now := connections.GetDaemonInfo().TopoHeight

				last = float64(cmd.TOPO.Load())
				if last == 0 {
					last = height1.Result
				}
				duration := time.Since(start).Seconds()
				average := last - first
				if int64(duration) == 0 {
					duration = 1 // avoid division by zero
				}
				average /= duration
				if int64(average) == 0 {
					average = 1
				}
				estimated := now / int64(average)

				fyne.DoAndWait(func() {
					readout.SetText(text)
					current_height.SetText("current height:" + strconv.Itoa(int(now)))
					indexed_height.SetText("indexed height:" + strconv.Itoa(int(height1.Result)))
					average_blocks_per_hour.SetText("average blocks per second:" + strconv.Itoa(int(average)))
					estimated_time_to_completion.SetText("estimated hours until completion:" + strconv.Itoa(int(estimated/60/60)))
					progress_bar.SetValue(last / float64(now))
				})

			}

			ticker := time.NewTicker(time.Second)

			for range ticker.C {
				action()
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

type getAllSCIDSAndOwnersResult struct {
	Result map[string]any `json:"result"`
}

type getAllParams struct {
	IDX string
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
	IDX     string
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
