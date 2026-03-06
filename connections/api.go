package connections

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
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
func (apiServer *ApiServer) Start(ctx context.Context) {
	apiServer.StatsIntv, _ = time.ParseDuration(apiServer.Config.StatsCollectInterval)
	statsTimer := time.NewTimer(apiServer.StatsIntv)
	fmt.Printf("Set stats collect interval to %v\n", apiServer.StatsIntv)
	apiServer.collectStats()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-statsTimer.C:
				apiServer.collectStats()
				statsTimer.Reset(apiServer.StatsIntv)
			}
		}
	}()
	// If SSL is configured, due to nature of listenandserve, put HTTP in go routine then call SSL afterwards so they can run in parallel. Otherwise, run http as normal
	if apiServer.Config.SSL {
		go apiServer.listen(ctx)
		apiServer.listenSSL(ctx)
	} else {
		apiServer.listen(ctx)
	}
}

type route struct {
	route string
	fn    func(http.ResponseWriter, *http.Request)
}

func (apiServer *ApiServer) newRouter() *mux.Router {
	r := mux.NewRouter()
	routes := []route{
		// simple api
		{"/api/getstats", apiServer.GetStats},
		{"/api/getscids", apiServer.GetSCIDs},
		{"/api/getscidsbyclass", apiServer.GetSCIDsByClass},

		// og api
		{"/api/getinfo", apiServer.GetInfo},
		{"/api/indexedscs", apiServer.StatsIndex},
		{"/api/indexbyscid", apiServer.InvokeIndexBySCID},
		{"/api/scvarsbyheight", apiServer.InvokeSCVarsByHeight},
		{"/api/invalidscids", apiServer.InvalidSCIDStats},
		{"/api/scidprivtx", apiServer.NormalTxWithSCID},
	}
	if apiServer.Config.MBLLookup {
		routes = append(routes,
			route{"/api/getmbladdrsbyhash", apiServer.MBLLookupByHash},
			route{"/api/getmblcountbyaddr", apiServer.MBLLookupByAddr},
		)
	}
	for _, each := range routes {
		r.HandleFunc(each.route, each.fn)
	}
	r.NotFoundHandler = http.HandlerFunc(notFound)
	return r
}

// Sets up the non-SSL API listener
func (apiServer *ApiServer) listen(ctx context.Context) {
	fmt.Printf("Starting API on %v\n", apiServer.Config.Listen)
	srv := &http.Server{Addr: apiServer.Config.Listen, Handler: apiServer.newRouter()}
	go func() {
		<-ctx.Done()
		if err := srv.Shutdown(ctx); err != nil {
			fmt.Println("error shutting down server", err)
			return
		}
	}()
	err := srv.ListenAndServe()
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to start api: %v", err)
	}
	log.Println("api server stopped cleanly")
}

// Sets up the SSL API listener
func (apiServer *ApiServer) listenSSL(ctx context.Context) {
	fmt.Printf("Starting SSL API on %v\n", apiServer.Config.SSLListen)
	srv := &http.Server{Addr: apiServer.Config.SSLListen, Handler: apiServer.newRouter()}
	go func() {
		<-ctx.Done()
		if err := srv.Shutdown(ctx); err != nil {
			fmt.Println("error shutting down server", err)
			return
		}
	}()
	err := srv.ListenAndServeTLS(apiServer.Config.CertFile, apiServer.Config.KeyFile)
	if err != nil && err != http.ErrServerClosed {
		log.Fatalf("Failed to start api: %v", err)
	}
	log.Println("api server stopped cleanly")
}

// Continuous check on number of validated scs etc. for base stats of service.
func (apiServer *ApiServer) collectStats() {
	if apiServer.Database.Closing {
		return
	}
	var (
		stats  = make(map[string]interface{})
		sclist = apiServer.Database.GetAllOwnersAndSCIDs()
	)
	stats["countSCs"] = len(sclist)
	stats["countRegTX"] = apiServer.Database.GetTxCount("registration")
	stats["countBurnTX"] = apiServer.Database.GetTxCount("burn")
	stats["countNormTX"] = apiServer.Database.GetTxCount("normal")
	height, _ := apiServer.Database.GetLastIndexHeight()
	stats["indexHeight"] = height
	stats["indexedscs"] = sclist
	apiServer.Stats.Store(stats)
}

func (apiServer *ApiServer) setStats(reply map[string]any) {
	stats := apiServer.Stats.Load().(map[string]any)
	if stats != nil {
		reply["indexHeight"], reply["countSCs"], reply["countRegTX"], reply["countBurnTX"], reply["countNormTX"] =
			stats["indexHeight"], stats["countSCs"], stats["countRegTX"], stats["countBurnTX"], stats["countNormTX"]

	} else {
		// Default reply - for testing, initials etc.
		reply["hello"] = "world"
	}
}

func (apiServer *ApiServer) StatsIndex(writer http.ResponseWriter, _ *http.Request) {
	setHeaders(writer)
	reply := make(map[string]interface{})
	apiServer.setStats(reply)
	reply["indexedscs"] = apiServer.Stats.Load().(map[string]any)["indexedscs"]
	encodeReply(writer, reply)
}

func (apiServer *ApiServer) GetSCIDs(writer http.ResponseWriter, _ *http.Request) {
	setHeaders(writer)
	reply := make(map[string]interface{})
	apiServer.setStats(reply)
	reply["scids"] = apiServer.Database.GetAllSCIDs()
	encodeReply(writer, reply)
}

func (apiServer *ApiServer) GetSCIDsByClass(writer http.ResponseWriter, r *http.Request) {
	setHeaders(writer)
	reply := make(map[string]interface{})
	apiServer.setStats(reply)
	class := r.URL.Query().Get("class")
	reply["scids"] = apiServer.Database.GetAllSCIDsByClass(class)
	encodeReply(writer, reply)
}

func (apiServer *ApiServer) InvokeIndexBySCID(writer http.ResponseWriter, r *http.Request) {
	setHeaders(writer)
	var (
		reply   = make(map[string]interface{})
		scid    = r.URL.Query().Get("scid")
		address = r.URL.Query().Get("address")
		sclist  = apiServer.Database.GetAllOwnersAndSCIDs() // Get all scid:owner
	)
	apiServer.setStats(reply)
	switch {
	case address != "" && scid != "":
		// Return results that match both address and scid
		var addrscidinvokes []*structures.SCTXParse
		if _, ok := sclist[scid]; ok {
			addrscidinvokes = apiServer.Database.GetAllSCIDInvokeDetailsBySigner(scid, address)
		}
		// Case to ignore large variable returns
		if len(addrscidinvokes) > globals.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
			fmt.Printf("Tried to return more than %d sc indexes for %s... DENIED! Too much data...\n", globals.MAX_API_VAR_RETURN, scid)
			reply["addrscidinvokescount"], reply["addrscidinvokes"] = 0, nil
			encodeReply(writer, reply)
			return
		}
		reply["addrscidinvokescount"], reply["addrscidinvokes"] = len(addrscidinvokes), addrscidinvokes
	case address != "" && scid == "":
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
			fmt.Printf("Tried to return more than %d sc indexes for %s... DENIED! Too much data...\n", globals.MAX_API_VAR_RETURN, scid)
			reply["addrinvokescount"], reply["addrinvokes"] = 0, nil
			encodeReply(writer, reply)
			return
		}
		reply["addrinvokescount"], reply["addrinvokes"] = len(addrinvokes), addrinvokes
	case address == "" && scid != "":
		// If no address and scid only, return invokes of scid
		scidinvokes := apiServer.Database.GetAllSCIDInvokeDetails(scid)
		// Case to ignore large variable returns
		if len(scidinvokes) > globals.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle {
			fmt.Printf("Tried to return more than %d sc indexes for %s... DENIED! Too much data...\n", globals.MAX_API_VAR_RETURN, scid)
			reply["scidinvokescount"], reply["scidinvokes"] = 0, nil
			encodeReply(writer, reply)
			return
		}
		reply["scidinvokescount"], reply["scidinvokes"] = len(scidinvokes), scidinvokes
	}
	encodeReply(writer, reply)
}

func (apiServer *ApiServer) InvokeSCVarsByHeight(writer http.ResponseWriter, r *http.Request) {
	setHeaders(writer)
	var (
		reply  = make(map[string]interface{})
		scid   = r.URL.Query().Get("scid")
		height = r.URL.Query().Get("height")
	)
	apiServer.setStats(reply)
	if scid == "" {
		log.Printf("URL Param 'scid' is missing. Debugging only.")
		reply["variables"] = nil
		encodeReply(writer, reply)
		return
	}
	if height != "" {
		var (
			// TODO: If there's no interaction height, do we go get scvars against daemon and store?
			topoheight, err        = strconv.ParseInt(height, 10, 64)
			scidInteractionHeights = apiServer.Database.GetSCIDInteractionHeight(scid)
			interactionHeight      = apiServer.Database.GetInteractionIndex(topoheight, scidInteractionHeights)
			variables              = apiServer.Database.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)
			throttle               = len(variables) > globals.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle
		)
		if err != nil {
			fmt.Printf("Err coverting '%v' to int64 - %v\n", height, err)
			encodeReply(writer, reply)
		}
		// Case to ignore large variable returns
		if throttle {
			fmt.Printf("Tried to return more than %d sc vars for %s... DENIED! Too much data...\n", globals.MAX_API_VAR_RETURN, scid)
			reply["variables"] = nil
			encodeReply(writer, reply)
			return
		}
		reply["variables"], reply["scidinteractionheight"] = variables, interactionHeight
	} else {
		var (
			variables              = apiServer.Database.GetAllSCIDVariableDetails(scid)
			scidInteractionHeights = apiServer.Database.GetSCIDInteractionHeight(scid)
			// Case to ignore all variable instance returns for builtin registration tx - large amount of data.
			ignorables = scid == globals.NAMESERVICE || scid == globals.MAINNET_GNOMON_SCID || scid == globals.TESTNET_GNOMON_SCID
			throttle   = len(variables) > globals.MAX_API_VAR_RETURN && apiServer.Config.ApiThrottle
		)
		if ignorables && apiServer.Config.ApiThrottle {
			fmt.Printf("Tried to return all the sc vars of everything at registration builtin... DENIED! Too much data...\n")
			reply["variables"] = nil
			encodeReply(writer, reply)
			return
		}
		// Case to ignore large variable returns
		if throttle {
			fmt.Printf("Tried to return more than %d sc vars for %s... DENIED! Too much data...\n", globals.MAX_API_VAR_RETURN, scid)
			reply["variables"] = nil
			encodeReply(writer, reply)
			return
		}
		reply["variables"], reply["scidinteractionheights"] = variables, scidInteractionHeights
	}
	encodeReply(writer, reply)
}
func (apiServer *ApiServer) NormalTxWithSCID(writer http.ResponseWriter, r *http.Request) {
	setHeaders(writer)
	var (
		reply                   = make(map[string]interface{})
		scid, address           = r.URL.Query().Get("scid"), r.URL.Query().Get("address")
		allNormTxWithSCIDByAddr = apiServer.Database.GetAllNormalTxWithSCIDByAddr(address)
		allNormTxWithSCIDBySCID = apiServer.Database.GetAllNormalTxWithSCIDBySCID(scid)
		tooBig                  = (len(allNormTxWithSCIDByAddr) > globals.MAX_API_VAR_RETURN || len(allNormTxWithSCIDBySCID) > globals.MAX_API_VAR_RETURN)
	)
	apiServer.setStats(reply)
	if address == "" && scid == "" {
		reply["variables"] = nil
		encodeReply(writer, reply)
		return
	}
	// Case to ignore large variable returns
	if tooBig && apiServer.Config.ApiThrottle {
		fmt.Printf("Tried to return more than %d... DENIED! Too much data...", globals.MAX_API_VAR_RETURN)
		reply["normtxwithscidbyaddr"], reply["normtxwithscidbyaddrcount"] = nil, 0
		reply["normtxwithscidbyscid"], reply["normtxwithscidbyscidcount"] = nil, 0
		encodeReply(writer, reply)
		return
	}
	reply["normtxwithscidbyaddr"], reply["normtxwithscidbyaddrcount"] = allNormTxWithSCIDByAddr, len(allNormTxWithSCIDByAddr)
	reply["normtxwithscidbyscid"], reply["normtxwithscidbyscidcount"] = allNormTxWithSCIDBySCID, len(allNormTxWithSCIDBySCID)
	encodeReply(writer, reply)
}

func (apiServer *ApiServer) InvalidSCIDStats(writer http.ResponseWriter, _ *http.Request) {
	setHeaders(writer)
	var (
		reply        = make(map[string]interface{})
		invalidscids = apiServer.Database.GetInvalidSCIDDeploys()
		tooBig       = len(invalidscids) > globals.MAX_API_VAR_RETURN
	)
	// Case to ignore large variable returns
	if tooBig && apiServer.Config.ApiThrottle {
		fmt.Printf("Tried to return more than %d.. DENIED! Too much data...", globals.MAX_API_VAR_RETURN)
		reply["invalidscids"] = nil
		encodeReply(writer, reply)
		return
	}
	reply["invalidscids"] = invalidscids
	encodeReply(writer, reply)
}

func (apiServer *ApiServer) MBLLookupByHash(writer http.ResponseWriter, r *http.Request) {
	setHeaders(writer)
	var (
		reply               = make(map[string]interface{})
		blid                = r.URL.Query().Get("blid")
		allMiniBlocksByBlid = apiServer.Database.GetMiniblockDetailsByHash(blid)
		tooBig              = len(allMiniBlocksByBlid) > globals.MAX_API_VAR_RETURN
	)
	apiServer.setStats(reply)
	if blid == "" {
		fmt.Println("URL Param 'blid' is missing. Debugging only.")
		reply["mbl"] = nil
		encodeReply(writer, reply)
		return
	}
	// Case to ignore large variable returns
	if tooBig && apiServer.Config.ApiThrottle {
		fmt.Printf("Tried to return more than %d.. DENIED! Too much data...", globals.MAX_API_VAR_RETURN)
		reply["mbl"] = nil
		encodeReply(writer, reply)
		return
	}
	reply["mbl"] = allMiniBlocksByBlid
	encodeReply(writer, reply)
}

func (apiServer *ApiServer) MBLLookupByAddr(writer http.ResponseWriter, r *http.Request) {
	setHeaders(writer)
	var (
		reply               = make(map[string]interface{})
		addr                = r.URL.Query().Get("address")
		allMiniBlocksByAddr = apiServer.Database.GetMiniblockCountByAddress(addr)
	)
	apiServer.setStats(reply)
	if addr == "" {
		fmt.Println("URL Param 'address' is missing. Debugging only.")
		reply["mbl"] = nil
		encodeReply(writer, reply)
		return
	}
	reply["mbl"] = allMiniBlocksByAddr
	encodeReply(writer, reply)
}

func (apiServer *ApiServer) MBLLookupAll(writer http.ResponseWriter, r *http.Request) {
	setHeaders(writer)

	var (
		reply         = make(map[string]interface{})
		allMiniBlocks = apiServer.Database.GetAllMiniblockDetails()
		tooBig        = len(allMiniBlocks) > globals.MAX_API_VAR_RETURN
	)

	apiServer.setStats(reply)

	// Case to ignore large variable returns
	if tooBig && apiServer.Config.ApiThrottle {
		fmt.Printf("Tried to return more than %d.. DENIED! Too much data...", globals.MAX_API_VAR_RETURN)
		reply["mbl"] = nil

		encodeReply(writer, reply)
		return
	}

	reply["mbl"] = allMiniBlocks

	encodeReply(writer, reply)
}

func (apiServer *ApiServer) GetInfo(writer http.ResponseWriter, _ *http.Request) {
	setHeaders(writer)

	var (
		reply = make(map[string]interface{})
		info  = apiServer.Database.GetGetInfoDetails()
	)

	reply["getinfo"] = info

	encodeReply(writer, reply)
}

func (apiServer *ApiServer) GetStats(writer http.ResponseWriter, _ *http.Request) {
	setHeaders(writer)
	reply := make(map[string]interface{})
	apiServer.setStats(reply)

	encodeReply(writer, reply)
}

// Default 404 not found response if api entry wasn't caught
func notFound(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusNotFound)
}

func setHeaders(writer http.ResponseWriter) {
	writer.Header().Set("Content-Type", "application/json; charset=UTF-8")
	writer.Header().Set("Access-Control-Allow-Origin", "*")
	writer.Header().Set("Cache-Control", "no-cache")
	writer.WriteHeader(http.StatusOK)
}

func encodeReply(writer http.ResponseWriter, reply map[string]interface{}) {
	err := json.NewEncoder(writer).Encode(reply)
	if err != nil {
		fmt.Printf("Error serializing API response: %v\n", err)
	}
}
