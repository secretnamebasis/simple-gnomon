package connections

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/gorilla/mux"
	"github.com/secretnamebasis/simple-gnomon/db"
	"github.com/secretnamebasis/simple-gnomon/globals"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

type APIConfig struct {
	Enabled              bool   `json:"enabled"`
	Listen               string `json:"listen"`
	StatsCollectInterval string `json:"statsCollectInterval"`
	HashrateWindow       string `json:"hashrateWindow"`
	Payments             int64  `json:"payments"`
	Blocks               int64  `json:"blocks"`
	SSL                  bool   `json:"ssl"`
	SSLListen            string `json:"sslListen"`
	GetInfoSSLListen     string `json:"getInfoSSLListen"`
	CertFile             string `json:"certFile"`
	GetInfoCertFile      string `json:"getInfoCertFile"`
	KeyFile              string `json:"keyFile"`
	GetInfoKeyFile       string `json:"getInfoKeyFile"`
	MBLLookup            bool   `json:"mbblookup"`
	ApiThrottle          bool   `json:"apithrottle"`
}

type ApiServer struct {
	Config    *APIConfig
	Stats     atomic.Value
	StatsIntv time.Duration
	Database  *db.BboltStore
}

// Configures a new API server to be used
func NewApiServer(cfg *APIConfig, database *db.BboltStore) *ApiServer {
	return &ApiServer{
		Config:   cfg,
		Database: database,
	}
}

// Starts the api server
func (apiServer *ApiServer) Start() {

	apiServer.StatsIntv, _ = time.ParseDuration(apiServer.Config.StatsCollectInterval)
	statsTimer := time.NewTimer(apiServer.StatsIntv)
	fmt.Printf("[API] Set stats collect interval to %v\n", apiServer.StatsIntv)

	apiServer.collectStats()

	go func() {
		for range statsTimer.C {
			apiServer.collectStats()
			statsTimer.Reset(apiServer.StatsIntv)
		}
	}()

	// If SSL is configured, due to nature of listenandserve, put HTTP in go routine then call SSL afterwards so they can run in parallel. Otherwise, run http as normal
	if apiServer.Config.SSL {
		go apiServer.listen()
		go apiServer.listenSSL()
		apiServer.getInfoListenSSL()
	} else {
		apiServer.listen()
	}
}

// Sets up the non-SSL API listener
func (apiServer *ApiServer) listen() {
	fmt.Printf("[API] Starting API on %v\n", apiServer.Config.Listen)
	router := mux.NewRouter()
	router.HandleFunc("/api/indexedscs", apiServer.StatsIndex)
	router.HandleFunc("/api/indexbyscid", apiServer.InvokeIndexBySCID)
	router.HandleFunc("/api/scvarsbyheight", apiServer.InvokeSCVarsByHeight)
	router.HandleFunc("/api/invalidscids", apiServer.InvalidSCIDStats)
	router.HandleFunc("/api/scidprivtx", apiServer.NormalTxWithSCID)
	if apiServer.Config.MBLLookup {
		router.HandleFunc("/api/getmbladdrsbyhash", apiServer.MBLLookupByHash)
		router.HandleFunc("/api/getmblcountbyaddr", apiServer.MBLLookupByAddr)
	}
	router.HandleFunc("/api/getinfo", apiServer.GetInfo)
	router.NotFoundHandler = http.HandlerFunc(notFound)
	err := http.ListenAndServe(apiServer.Config.Listen, router)
	if err != nil {
		log.Fatalf("[API] Failed to start API: %v\n", err)
	}
}

// Sets up the SSL API listener
func (apiServer *ApiServer) listenSSL() {
	fmt.Printf("[API] Starting SSL API on %v\n", apiServer.Config.SSLListen)
	routerSSL := mux.NewRouter()
	routerSSL.HandleFunc("/api/indexedscs", apiServer.StatsIndex)
	routerSSL.HandleFunc("/api/indexbyscid", apiServer.InvokeIndexBySCID)
	routerSSL.HandleFunc("/api/scvarsbyheight", apiServer.InvokeSCVarsByHeight)
	routerSSL.HandleFunc("/api/invalidscids", apiServer.InvalidSCIDStats)
	routerSSL.HandleFunc("/api/scidprivtx", apiServer.NormalTxWithSCID)
	if apiServer.Config.MBLLookup {
		routerSSL.HandleFunc("/api/getmbladdrsbyhash", apiServer.MBLLookupByHash)
		routerSSL.HandleFunc("/api/getmblcountbyaddr", apiServer.MBLLookupByAddr)
	}
	routerSSL.HandleFunc("/api/getinfo", apiServer.GetInfo)
	routerSSL.NotFoundHandler = http.HandlerFunc(notFound)
	err := http.ListenAndServeTLS(apiServer.Config.SSLListen, apiServer.Config.CertFile, apiServer.Config.KeyFile, routerSSL)
	if err != nil {
		log.Fatalf("[API] Failed to start SSL API: %v\n", err)
	}
}

// Sets up a separate getinfo SSL listener. Use cases is for things like benchmark.dero.network and others that may want to consume a https endpoint of derod getinfo or other future command output
func (apiServer *ApiServer) getInfoListenSSL() {
	fmt.Printf("[API] Starting GetInfo SSL API on %v\n", apiServer.Config.GetInfoSSLListen)
	routerSSL := mux.NewRouter()
	routerSSL.HandleFunc("/api/getinfo", apiServer.GetInfo)
	routerSSL.NotFoundHandler = http.HandlerFunc(notFound)

	err := http.ListenAndServeTLS(apiServer.Config.GetInfoSSLListen, apiServer.Config.GetInfoCertFile, apiServer.Config.GetInfoKeyFile, routerSSL)
	if err != nil {
		log.Fatalf("[API] Failed to start GetInfo SSL API: %v\n", err)
	}
}

// Default 404 not found response if api entry wasn't caught
func notFound(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusNotFound)
}

// Continuous check on number of validated scs etc. for base stats of service.
func (apiServer *ApiServer) collectStats() {

	if apiServer.Database.Closing {
		return
	}

	stats := make(map[string]interface{})

	// TODO: Removeme
	var scinstalls []*structures.SCTXParse

	sclist := apiServer.Database.GetAllOwnersAndSCIDs()
	for k := range sclist {

		if apiServer.Database.Closing {
			return
		}

		invokedetails := apiServer.Database.GetAllSCIDInvokeDetails(k)
		// i := 0
		for _, v := range invokedetails {
			sc_action := fmt.Sprintf("%v\n", v.Sc_args.Value("SC_ACTION", "U"))
			if sc_action == "1" {
				// i++
				scinstalls = append(scinstalls, v)
			}
		}
	}

	if len(scinstalls) > 0 {
		// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
		sort.SliceStable(scinstalls, func(i, j int) bool {
			return scinstalls[i].Height < scinstalls[j].Height
		})
	}

	var lastQueries []*structures.GnomonSCIDQuery

	for _, v := range scinstalls {
		curr := &structures.GnomonSCIDQuery{Owner: v.Sender, Height: uint64(v.Height), SCID: v.Scid}
		lastQueries = append(lastQueries, curr)
	}

	// Get all scid:owner
	// TODO: Re-add
	//sclist := apiServer.Backend.GetAllOwnersAndSCIDs()

	stats["countSCs"] = len(sclist)
	stats["countRegTX"] = apiServer.Database.GetTxCount("registration")
	stats["countBurnTX"] = apiServer.Database.GetTxCount("burn")
	stats["countNormTX"] = apiServer.Database.GetTxCount("normal")
	stats["indexedscs"] = sclist
	stats["indexdetails"] = lastQueries

	apiServer.Stats.Store(stats)
}

func (apiServer *ApiServer) StatsIndex(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["countSCs"] = stats["numscs"]
		reply["countRegTX"] = stats["regTxCount"]
		reply["countBurnTX"] = stats["burnTxCount"]
		reply["countNormTX"] = stats["normTxCount"]
		reply["indexedscs"] = stats["indexedscs"]
		reply["indexdetails"] = stats["indexdetails"]
	} else {
		// Default reply - for testing etc.
		reply["hello"] = "world"
	}

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		fmt.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) InvokeIndexBySCID(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["countSCs"] = stats["numscs"]
		reply["countRegTX"] = stats["regTxCount"]
		reply["countBurnTX"] = stats["burnTxCount"]
		reply["countNormTX"] = stats["normTxCount"]
	} else {
		// Default reply - for testing etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	scidkeys, ok := r.URL.Query()["scid"]
	var scid string
	var address string

	if !ok || len(scidkeys[0]) < 1 {
		log.Printf("[API] URL Param 'scid' is missing. Debugging only.")
	} else {
		scid = scidkeys[0]
	}

	// Query for address
	addresskeys, ok := r.URL.Query()["address"]

	if !ok || len(addresskeys[0]) < 1 {
		log.Printf("[API] URL Param 'address' is missing.")
	} else {
		address = addresskeys[0]
	}

	// Get all scid:owner

	sclist := apiServer.Database.GetAllOwnersAndSCIDs()

	if address != "" && scid != "" {
		// Return results that match both address and scid
		var addrscidinvokes []*structures.SCTXParse

		for k := range sclist {

			if k != scid {
				continue
			}

			addrscidinvokes = apiServer.Database.GetAllSCIDInvokeDetailsBySigner(scid, address)
		}

		// Case to ignore large variable returns
		if len(addrscidinvokes) > globals.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
			fmt.Printf("[API-InvokeIndexBySCID] Tried to return more than %d sc indexes for %s... DENIED! Too much data...\n", globals.MAX_API_VAR_RETURN, scid)
			reply["addrscidinvokescount"] = 0
			reply["addrscidinvokes"] = nil

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				fmt.Printf("[API] Error serializing API response: %v\n", err)
			}
			return
		}
		reply["addrscidinvokescount"] = len(addrscidinvokes)
		reply["addrscidinvokes"] = addrscidinvokes
	} else if address != "" && scid == "" {
		// If address and no scid, return combined results of all instances address is defined (invokes and installs)
		var addrinvokes [][]*structures.SCTXParse

		for k := range sclist {

			currinvokedetails := apiServer.Database.GetAllSCIDInvokeDetailsBySigner(k, address)

			if currinvokedetails != nil {
				addrinvokes = append(addrinvokes, currinvokedetails)
			}
		}

		// Case to ignore large variable returns
		if len(addrinvokes) > globals.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
			fmt.Printf("[API-InvokeIndexBySCID] Tried to return more than %d sc indexes for %s... DENIED! Too much data...\n", globals.MAX_API_VAR_RETURN, scid)
			reply["addrinvokescount"] = 0
			reply["addrinvokes"] = nil

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				fmt.Printf("[API] Error serializing API response: %v\n", err)
			}
			return
		}

		reply["addrinvokescount"] = len(addrinvokes)
		reply["addrinvokes"] = addrinvokes
	} else if address == "" && scid != "" {
		// If no address and scid only, return invokes of scid

		scidinvokes := apiServer.Database.GetAllSCIDInvokeDetails(scid)

		// Case to ignore large variable returns
		if len(scidinvokes) > globals.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
			fmt.Printf("[API-InvokeIndexBySCID] Tried to return more than %d sc indexes for %s... DENIED! Too much data...\n", globals.MAX_API_VAR_RETURN, scid)
			reply["scidinvokescount"] = 0
			reply["scidinvokes"] = nil

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				fmt.Printf("[API] Error serializing API response: %v\n", err)
			}
			return
		}

		reply["scidinvokescount"] = len(scidinvokes)
		reply["scidinvokes"] = scidinvokes
	}

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		fmt.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) InvokeSCVarsByHeight(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["countSCs"] = stats["numscs"]
		reply["countRegTX"] = stats["regTxCount"]
		reply["countBurnTX"] = stats["burnTxCount"]
		reply["countNormTX"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	scidkeys, ok := r.URL.Query()["scid"]
	var scid string
	var height string

	if !ok || len(scidkeys[0]) < 1 {
		log.Printf("[API] URL Param 'scid' is missing. Debugging only.")
		reply["variables"] = nil
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			fmt.Printf("[API] Error serializing API response: %v\n", err)
		}
		return
	} else {
		scid = scidkeys[0]
	}

	// Query for address
	heightkey, ok := r.URL.Query()["height"]

	if !ok || len(heightkey[0]) < 1 {
		log.Printf("[API] URL Param 'height' is missing.")
	} else {
		height = heightkey[0]
	}

	if height != "" {
		var variables []*structures.SCIDVariable
		var scidInteractionHeights []int64
		var interactionHeight int64

		var err error

		var topoheight int64
		topoheight, err = strconv.ParseInt(height, 10, 64)
		if err != nil {
			fmt.Printf("[API] Err converting '%v' to int64 - %v\n", height, err)

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				fmt.Printf("[API] Error serializing API response: %v\n", err)
			}
		}

		scidInteractionHeights = apiServer.Database.GetSCIDInteractionHeight(scid)

		interactionHeight = apiServer.Database.GetInteractionIndex(topoheight, scidInteractionHeights, false)

		// TODO: If there's no interaction height, do we go get scvars against daemon and store?
		variables = apiServer.Database.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

		// Case to ignore large variable returns
		if len(variables) > globals.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
			fmt.Printf("[API-InvokeSCVarsByHeight] Tried to return more than %d sc vars for %s... DENIED! Too much data...\n", globals.MAX_API_VAR_RETURN, scid)
			reply["variables"] = nil

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				fmt.Printf("[API] Error serializing API response: %v\n", err)
			}
			return
		}

		reply["variables"] = variables
		reply["scidinteractionheight"] = interactionHeight
	} else {
		// TODO: Do we need this case? Should we always require a height to be defined so as not to slow the api return due to large dataset? Do we keep but put a limit on return amount?

		var variables []*structures.SCIDVariable
		var scidInteractionHeights []int64

		// Case to ignore all variable instance returns for builtin registration tx - large amount of data.
		if (scid == globals.NAMESERVICE || scid == globals.MAINNET_GNOMON_SCID || scid == globals.TESTNET_GNOMON_SCID) && apiServer.Config.ApiThrottle {
			fmt.Printf("[API-InvokeSCVarsByHeight] Tried to return all the sc vars of everything at registration builtin... DENIED! Too much data...\n")
			reply["variables"] = nil

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				fmt.Printf("[API] Error serializing API response: %v\n", err)
			}
			return
		}

		scidInteractionHeights = apiServer.Database.GetSCIDInteractionHeight(scid)
		variables = apiServer.Database.GetAllSCIDVariableDetails(scid)

		// Case to ignore large variable returns
		if len(variables) > globals.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
			fmt.Printf("[API-InvokeSCVarsByHeight] Tried to return more than %d sc vars for %s... DENIED! Too much data...\n", globals.MAX_API_VAR_RETURN, scid)
			reply["variables"] = nil

			err := json.NewEncoder(writer).Encode(reply)
			if err != nil {
				fmt.Printf("[API] Error serializing API response: %v\n", err)
			}
			return
		}

		reply["variables"] = variables
		reply["scidinteractionheights"] = scidInteractionHeights
	}

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		fmt.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) NormalTxWithSCID(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["countSCs"] = stats["numscs"]
		reply["countRegTX"] = stats["regTxCount"]
		reply["countBurnTX"] = stats["burnTxCount"]
		reply["countNormTX"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	scidkeys, ok := r.URL.Query()["scid"]
	var scid string
	var address string

	if !ok || len(scidkeys[0]) < 1 {
		fmt.Println("[API] URL Param 'scid' is missing. Debugging only.")
	} else {
		scid = scidkeys[0]
	}

	// Query for address
	addresskeys, ok := r.URL.Query()["address"]

	if !ok || len(addresskeys[0]) < 1 {
		fmt.Println("[API] URL Param 'address' is missing.")
	} else {
		address = addresskeys[0]
	}

	if address == "" && scid == "" {
		reply["variables"] = nil
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			fmt.Printf("[API] Error serializing API response: %v\n", err)
		}
		return
	}

	allNormTxWithSCIDByAddr := apiServer.Database.GetAllNormalTxWithSCIDByAddr(address)
	allNormTxWithSCIDBySCID := apiServer.Database.GetAllNormalTxWithSCIDBySCID(scid)

	// Case to ignore large variable returns
	if (len(allNormTxWithSCIDByAddr) > globals.MAX_API_VAR_RETURN || len(allNormTxWithSCIDBySCID) > globals.MAX_API_VAR_RETURN) && apiServer.Config.ApiThrottle {
		fmt.Printf("[API-NormalTxWithSCID] Tried to return more than %d... DENIED! Too much data...", globals.MAX_API_VAR_RETURN)
		reply["normtxwithscidbyaddr"] = nil
		reply["normtxwithscidbyaddrcount"] = 0
		reply["normtxwithscidbyscid"] = nil
		reply["normtxwithscidbyscidcount"] = 0

		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			fmt.Printf("[API] Error serializing API response: %v\n", err)
		}
		return
	}

	reply["normtxwithscidbyaddr"] = allNormTxWithSCIDByAddr
	reply["normtxwithscidbyaddrcount"] = len(allNormTxWithSCIDByAddr)
	reply["normtxwithscidbyscid"] = allNormTxWithSCIDBySCID
	reply["normtxwithscidbyscidcount"] = len(allNormTxWithSCIDBySCID)

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		fmt.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) InvalidSCIDStats(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	invalidscids := apiServer.Database.GetInvalidSCIDDeploys()

	// Case to ignore large variable returns
	if len(invalidscids) > globals.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
		fmt.Printf("[API-InvalidSCIDStats] Tried to return more than %d.. DENIED! Too much data...", globals.MAX_API_VAR_RETURN)
		reply["invalidscids"] = nil

		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			fmt.Printf("[API] Error serializing API response: %v\n", err)
		}
		return
	}

	reply["invalidscids"] = invalidscids

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		fmt.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) MBLLookupByHash(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	blidkeys, ok := r.URL.Query()["blid"]
	var blid string

	if !ok || len(blidkeys[0]) < 1 {
		fmt.Println("[API] URL Param 'blid' is missing. Debugging only.")
		reply["mbl"] = nil
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			fmt.Printf("[API] Error serializing API response: %v\n", err)
		}
		return
	} else {
		blid = blidkeys[0]
	}

	allMiniBlocksByBlid := apiServer.Database.GetMiniblockDetailsByHash(blid)

	// Case to ignore large variable returns
	if len(allMiniBlocksByBlid) > globals.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
		fmt.Printf("[API-MBLLookupByHash] Tried to return more than %d.. DENIED! Too much data...", globals.MAX_API_VAR_RETURN)
		reply["mbl"] = nil

		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			fmt.Printf("[API] Error serializing API response: %v\n", err)
		}
		return
	}

	reply["mbl"] = allMiniBlocksByBlid

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		fmt.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) MBLLookupByAddr(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	// Query for SCID
	addrkeys, ok := r.URL.Query()["address"]
	var addr string

	if !ok || len(addrkeys[0]) < 1 {
		fmt.Println("[API] URL Param 'address' is missing. Debugging only.")
		reply["mbl"] = nil
		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			fmt.Printf("[API] Error serializing API response: %v\n", err)
		}
		return
	} else {
		addr = addrkeys[0]
	}

	allMiniBlocksByAddr := apiServer.Database.GetMiniblockCountByAddress(addr)

	reply["mbl"] = allMiniBlocksByAddr

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		fmt.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) MBLLookupAll(writer http.ResponseWriter, r *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	stats := apiServer.getStats()
	if stats != nil {
		reply["numscs"] = stats["numscs"]
		reply["regTxCount"] = stats["regTxCount"]
		reply["burnTxCount"] = stats["burnTxCount"]
		reply["normTxCount"] = stats["normTxCount"]
	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}

	allMiniBlocks := apiServer.Database.GetAllMiniblockDetails()

	// Case to ignore large variable returns
	if len(allMiniBlocks) > globals.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
		fmt.Printf("[API-MBLLookupAll] Tried to return more than %d.. DENIED! Too much data...", globals.MAX_API_VAR_RETURN)
		reply["mbl"] = nil

		err := json.NewEncoder(writer).Encode(reply)
		if err != nil {
			fmt.Printf("[API] Error serializing API response: %v\n", err)
		}
		return
	}

	reply["mbl"] = allMiniBlocks

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		fmt.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) GetInfo(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)

	reply := make(map[string]interface{})

	info := apiServer.Database.GetGetInfoDetails()

	reply["getinfo"] = info

	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		fmt.Printf("[API] Error serializing API response: %v\n", err)
	}
}

func (apiServer *ApiServer) getStats() map[string]interface{} {
	stats := apiServer.Stats.Load()
	if stats != nil {
		return stats.(map[string]interface{})
	}
	return nil
}
