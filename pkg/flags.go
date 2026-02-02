package pkg

import (
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"

	dero "github.com/deroproject/derohe/globals"
	"github.com/secretnamebasis/simple-gnomon/globals"
)

var (
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
	version            bool

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
  --testnet                                   Start in testnet mode.
  --simulator                                 Start in simulation mode.
  --version, -v                               Show version.
  --help, -h                                  Show this help message.`
)

// a simple flag-parser
func parseFlags() error {
	launch_args := os.Args[1:] // we'll skip the first one

	// launch help when present
	if slices.Contains(launch_args, "--help") || slices.Contains(launch_args, "-h") {
		fmt.Println(help_msg)
		os.Exit(0)
	}

	if slices.Contains(launch_args, "--version") || slices.Contains(launch_args, "-v") {
		fmt.Println(globals.Version.String())
		os.Exit(0)
	}

	if !slices.Contains(launch_args, "--testnet") {
		dero.Arguments[`--testnet`] = false
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
			case `--simulator`:
				dero.Arguments[`--simulator`] = true
			case `--testnet`:
				dero.Arguments[`--testnet`] = true
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
