# simple-gnomon
_this fork has been optimized for [simple-wallet](https://github.com/secretnamebasis/simple-wallet)_
## Decentralized Search Engine
Gnomon is an DERO blockchain indexer. It parses each block of DEROHE's blockchain, and aggregates results into various indexes for ease of use. The indexes of primary focus are SCID installs and SCID invocations; however, many other useful indexes are available ~~(see below)~~.

### Core Features
- Fast & Memory-Efficient Indexing
- SCID Queries: invocations & variables at height
- WS & HTTP query support (emphasis on WS for TELA dapps)

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
> N.B. There some useful options when running simple-gnomon, just call on `--help` to see more.

### DAEMON
> If an endpoint is not provided as a flag (`--daemon-rpc-address=<daemon_ip:port>`), simple-gnomon will attempt to connect to an xswd websocket (`ws://127.0.0.1:44326/xswd`) to get daemon endpoint.

Minimal set up is required: `./simple-gnomon --daemon-rpc-address=127.0.0.1:10102`, and the indexer will begin. 


### PACKAGE
To use simple gnomon in a go application; 
```go 
// include import in program
import 	"github.com/secretnamebasis/simple-gnomon/cmd"


// somewhere in the code...
func foobar(){

    // provide a valid node endpoint
    endpoint := "127.0.0.1:10102" 

    // build endpoint flag to parse
    endpoint_flag := "--daemon-rpc-address=" + endpoint

    // load flag into os arguments
    os.Args = append(os.Args, endpoint_flag)

    // start the indexer
    if err := cmd.Start_gnomon_indexer(); err != nil {
        fmt.Println(err)
        return
    }

    select{} // block to prevent gnomon_indexer go routine closure
}
```

## ENDPOINTS
> N.B. `https` & `wss`: these endpoints will likely be configurable in the future. The code is all there...

### `http`
This endpoint, if not set by the `--api-address=<ip:port>` flag, defaults to `http://127.0.0.1:8082`

```html
/api/indexedscs => {"indexdetails":null,"indexedscs": [scid,scid,scid], "numscs":49573}
/api/indexbyscid ? scid="" & address=""
/api/scvarsbyheight ? scid="" & height=""
/api/invalidscids
/api/scidprivtx ? scid="" & address=""
/api/getmbladdrsbyhash ? hash=""
/api/getmblcountbyaddr ? address=""
/api/getinfo
```

### `ws`
This endpoint defaults to `ws://127.0.0.1:9190/ws`. However, by using the `--ws-address=<ip:port>` flag, one cane set it to what is desired. 

## FILTERS
In addition to a catch all search filter, simple-gnomon contains 3 filters found in `config/search.json`: "g45", "nfa", "tela" ; these named searches are applied to indexed scids in a comma separated format, eg `tags`. The `--search-filter="<F;F>;;;<F;F>"` flag allows users do override the `search.json` file with multiple searches (using the `;;;` separator), with multiple search terms (using the `;` separator), eg `one-term;with a second term;;;another search;with another term`.

## EXCLUSIONS
There may be contracts that users would like to skip on account of their size or lack of apparent usefulness. An exclusions file has been included within the repo as `config/exclude.json`. Each contract can be named, defined, and given reason for exclusion.  The `--sf-scid-exclusions="<F>;;;<F>" ` flag allows users do override the `exclude.json` file with multiple searches (using the `;;;` separator), eg `<SCID1>;;;<SCID2>`.
