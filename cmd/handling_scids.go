package cmd

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/deroproject/derohe/cryptography/crypto"
	"github.com/deroproject/derohe/rpc"
	"github.com/deroproject/derohe/transaction"
	"github.com/secretnamebasis/simple-gnomon/connections"
	"github.com/secretnamebasis/simple-gnomon/globals"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

var scid_processing = make(chan *processingStruct, 100_000)

func scid_handling(ctx context.Context) {

	for {
		select {
		case <-ctx.Done():
			for areQueuesEmpty() {
				for staged := range scid_processing {
					work_on_scids(int64(staged.Height), staged.Tx.Signer, staged.Transaction)
				}
			}
			return
		case staged := <-scid_processing:
			// sift transactions over the sieve
			work_on_scids(int64(staged.Height), staged.Tx.Signer, staged.Transaction)
		}
	}
}

func work_on_scids(height int64, signer string, tx transaction.Transaction) {

	if len(tx.SCDATA) == 0 {
		return
	}

	// pull the contract anyway to determine that it installed
	var (
		scid       string
		method     string
		entrypoint string
		code       string
		class      string
		headers    string
		params     = rpc.GetSC_Params{}
		vars       []*structures.SCIDVariable
		tags       = []string{"all"} // catch all
	)

	// contract installs
	// https://github.com/deroproject/derohe/blob/e9df1205b6603c62f0651d0e18e5e77a2584b15e/walletapi/rpcserver/rpc_transfer.go#L64
	if tx.SCDATA.HasValue(rpc.SCCODE, rpc.DataString) && !tx.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
		scid = tx.GetHash().String()
		method = "install"
	}

	// contract interactions
	// https://github.com/deroproject/derohe/blob/e9df1205b6603c62f0651d0e18e5e77a2584b15e/walletapi/rpcserver/rpc_transfer.go#L69
	if tx.SCDATA.HasValue(rpc.SCID, rpc.DataHash) {
		value, ok := tx.SCDATA.Value(rpc.SCID, rpc.DataHash).(crypto.Hash)
		if !ok { // paranoia
			return
		}
		if value.String() == "" || value.IsZero() {
			return
		}
		entrypoint, ok = tx.SCDATA.Value("entrypoint", rpc.DataString).(string)
		if !ok {
			return
		}
		scid = value.String()
		method = "invoke"
	}
	if scid == "" {
		return
	}

	var sc rpc.GetSC_Result
	var err error
	if !slices.Contains(database.Exclusions, scid) {

		h := height

		// note here on why block height & get_sc_params height
		// are different: on account of the fast sync process...
		if fastsync { // only collect vars at current height
			h = -1
		}

		params = rpc.GetSC_Params{
			SCID:       scid,
			Variables:  true,
			TopoHeight: h,
		}

		switch method {
		case "install":
			params.Code = true
		case "invoke":
			params.Code = false
		}

		// error checking is the big thing here:
		// failed validation, attempts:4 DERO.GetSC
		sc, err = connections.GetSC(params)
		if err != nil {
			if params.Code {
				// this is an invalid contract
				if _, err := database.StoreInvalidSCIDDeploys(params.SCID, tx.Fees()); err != nil {
					fmt.Println(err)
					return
				}
			}

			if len(sc.VariableStringKeys) == 0 {
				// there are no vars?
				return
			}
			error_channel <- err
		}

		vars = GetSCVariables(sc.VariableStringKeys, sc.VariableUint64Keys)

		// currently not storing ScCode...
		code = sc.Code
		// the compromise, I think, is the entrypoint...
	}

	// unfortunately, there isn't a way to do this without checking twice

	// roll through the indices to obtain the class
	for _, search := range indices {

		// obtain the filters
		filters := search.Terms

		for _, filter := range filters { // range through the filters

			// if the code does not contain the filter, skip
			if !strings.Contains(code, filter) {
				continue
			}

			// if there is a match, add the name of the index to it's list of tags
			class = filter
			break
		}

		if class != "" {
			break
		}
	}

	// if they are providing a search filter, they are being specific
	// if none of the filters matches the sc code, skip this txid
	if search_filter != "" && class == "" {
		return
	}

	// catch globals
	switch scid {
	case globals.NAMESERVICE:
		class = "NAMESERVICE"
	case globals.MAINNET_GNOMON_SCID:
		class = "GNOMONSC"
	}

	// as class is currently the filter...
	// make sure to implement more classes as necessary
	switch class {
	case "": // catchall
		class = "null"
	case "docVersion":
		class = "TELA-DOC-1"
	case "telaVersion":
		class = "TELA-INDEX-1"
	}

	// roll through the indices again to obtain tags
	for _, search := range indices {

		// obtain the filters
		filters := search.Terms

		for _, filter := range filters { // range through the filters

			// if the code does not contina the filter, skip it
			if !strings.Contains(code, filter) {
				continue
			}

			// if there is a match, add the name of the index to it's list of tags
			tags = append(tags, search.Name)

		}
	}

	// lexicographical order
	slices.Sort(tags)

	if signer == "" { // when ringsize is greater than 2...
		signer = "null" // maybe empty is better?
	}

	nfa_signature := "Function Start(listType String, duration Uint64, startPrice Uint64, charityDonateAddr String, charityDonatePerc Uint64) Uint64"
	tela_doc_signature := `STORE("docVersion",`
	tela_index_signature := `STORE("telaVersion",`

	if strings.Contains(code, nfa_signature) || strings.Contains(code, tela_doc_signature) || strings.Contains(code, tela_index_signature) {
		headers = GetSCNameFromVars(sc.VariableStringKeys) + ";" + GetSCDescriptionFromVars(sc.VariableStringKeys) + ";" + GetSCIDImageURLFromVars(sc.VariableStringKeys)
	}

	if headers == "" && len(sc.VariableStringKeys) != 0 { // there could be a possability that it is a g45
		headers = GetSCHeaderFromMetaData(sc.VariableStringKeys)
	}

	if headers == "" {
		name, description, image := "null", "null", "null"
		headers = name + ";" + description + ";" + image
	}

	// because these are being processed asynchronously...
	// don't block on writing them to the db,
	// just queue em and write em when the writer has a moment
	scid_db_queue <- &structures.SCIDToIndexStage{
		SCTXParse: structures.SCTXParse{
			Height:     height,
			Txid:       tx.GetHash().String(),
			Scid:       scid,
			Entrypoint: entrypoint,
			Method:     method,
			Sc_args:    tx.SCDATA,
			Sender:     signer,
			Payloads:   tx.Payloads,
			Fees:       tx.Fees(),
		},
		Headers: headers,
		ScVars:  vars,
		ScCode:  code,
		Class:   class,
		// store as a single string
		Tags: strings.Join(tags, ","),
	}

}
