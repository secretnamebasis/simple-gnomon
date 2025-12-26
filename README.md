# simple-gnomon
_this fork has been optimized for [simple-wallet](github.com/secretnamebasis/simple-wallet)_
## Decentralized Search Engine
Gnomon is an DERO blockchain indexer. It parses each block of DEROHE's blockchain, and aggregates results into various indexes for ease of use. The indexes of primary focus are SCID installs and SCID invocations; however, many other useful indexes are available ~~(see below)~~.

### Core Features
- Fast & Memory-Efficient Indexing
- SCID Queries: invocations & variables at height
- WS & HTTP query support (emphasis on WS for TELA dapps)
- GUI & GUI-less modes

## Contributing
Post an issue whenever; or fork the code and do what you want (see MIT LICENSE). Most importantly, have fun learning. 

## Documentation
More to come in the future. Pls wait, or just go read the code. 

## RELEASE
Once development has reached "simpatico", releases will be included in the repo. 

## Install 
Installation is easy:

### `git`
```sh
git clone https://github.com/secretnamebasis/simple-gnomon
cd simple-gnomon
go build .
```
And there weill be an Operating System specific copy of `simple-gnomon` in the directory for use.

### `go`
Assuming you have a properly configured go environment...
```sh
go install gihub.com/secretnamebasis/simple-gnomon@latest
simple-gnomon
```

## Run 
As simple-gnomon aims to be quite versitle as a tool, there are three means of starting simple-gnomon: gui, no-gui, or as a package

> If an enpoint is not provided either in the endpoint entry of the GUI or as a flag (`-endpoint=<daemon_ip:port>`), simple-gnomon will attempt to connect to an xswd websocket (`ws://127.0.0.1:44326/xswd`) to get daemon endpoint.

### GUI
Simple as it gets, `./simple-gnomon`. 

The primary dashboard will appear asking for a endpoint to connect to for node queries. 

### NO-GUI
Minimal set up is required: `./simple-gnomon -no-gui -endpoint=127.0.0.1:10102`, and the indexer will begin. 

> N.B. There some useful options when running simple-gnomon, just call on `-help` to see more.

### PACKAGE
To use simple gnomon in a go application; 
```go 
// include import in program
import 	"github.com/secretnamebasis/simple-gnomon/cmd"

// example blocker
var exit chan struct{}

// somewhere in the code...
func foobar(){

    // provide a valid node endpoint
    endpoint := "127.0.0.1:10102" 

    // build endpoint flag to parse
    endpoint_flag := "-endpoint=" + endpoint

    // load flag into os arguments
    os.Args = append(os.Args, endpoint_flag)

    // start the indexer
    if err := cmd.Start_gnomon_indexer(); err != nil {
        fmt.Println(err)
        return
    }

    <-exit // block to prevent gnomon_indexer go routine closure
}
```

## ENDPOINTS
> N.B. `https` & `wss`: these endpoints will likely be configurable in the future. The code is all there...

### `http`
This endpoint, if not set by the `-api_endpoint=<ip:port>` flag, defaults to `http://127.0.0.1:8082`

```html
/api/indexedscs => {"indexdetails":null,"indexedscs": [scid,scid,scid], "numscs":49573}
/api/indexbyscid ? scid="" & address=""
/api/scvarsbyheight ? scid="" & height=""
/api/invalidscids
/api/scidprivtx ? scid="" & address=""
/api/getmbladdrsbyhash ? hash=""
/api/getmblcountbyaddr ? address
/api/getinfo
```

### `ws`
This endpoint defaults to `ws://127.0.0.1:9190/ws`. 


