package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type SqlStore struct {
	DB      *sql.DB
	DBPath  string
	Writing bool
	//Writer  string
	Closing bool
	//Buckets []string
}

func NewSqlDB(dbPath, dbName string) (*SqlStore, error) {
	var err error
	var Sql_backend *SqlStore = &SqlStore{}

	if err := os.MkdirAll(dbPath, 0700); err != nil {
		return nil, fmt.Errorf("directory creation err %s - dirpath %s", err, dbPath)
	}
	db_path := filepath.Join(dbPath, dbName)
	Sql_backend.DB, err = sql.Open("sqlite3", db_path)
	if err != nil {
		return Sql_backend, fmt.Errorf("[NewSqlDB] Could not create sql db store: %v", err)
	}
	createTables(Sql_backend.DB)
	//check tables

	go func() {
		for {
			amt, _ := time.ParseDuration("15s")
			time.Sleep(amt)
			viewTables(Sql_backend.DB)
		}

	}()
	Sql_backend.DBPath = dbPath
	//Db = Sql_backend.DB
	return Sql_backend, err
}

/*
	func InitDB() *sql.DB {
		// Initialize the database connection
		var err error

		Db, err = sql.Open("sqlite3", "./database/gnomon.db")

		if err != nil {
			panic(err)
		}

		// Test the database connection
		if err := Db.Ping(); err != nil {
			panic(err)
		}

		createTables()
		//insertSamplePage()
		return Db
	}
*/
func createTables(Db *sql.DB) {

	var startup = [5]string{}
	startup[0] = "CREATE TABLE IF NOT EXISTS state (" +
		"name  TEXT, " +
		"value  INTEGER)"

	startup[1] = "CREATE TABLE IF NOT EXISTS scs (" +
		"scid TEXT PRIMARY KEY, " +
		"owner TEXT NOT NULL, " +
		"height INTEGER, " +
		"class TEXT, " +
		"tags TEXT	)"

		//scid + "vars"
	startup[2] = "CREATE TABLE IF NOT EXISTS variables (" +
		"v_id INTEGER PRIMARY KEY, " +
		"height INTEGER NOT NULL, " +
		"scid TEXT NOT NULL, " +
		"vars TEXT NOT NULL)"
	//key := signer + ":" + invokedetails.Txid[0:3] + invokedetails.Txid[txidLen-3:txidLen] + ":" + strconv.FormatInt(topoheight, 10) + ":" + entrypoint

	//invoke details: signer:txid:height:entrypoint
	startup[3] = "CREATE TABLE IF NOT EXISTS invokes (" +
		"inv_id INTEGER PRIMARY KEY, " +
		"scid TEXT NOT NULL, " +
		"signer TEXT NOT NULL, " +
		"txid TEXT NOT NULL, " +
		"height INTEGER NOT NULL, " +
		"entrypoint TEXT NOT NULL)"

	//interactions at height
	startup[4] = "CREATE TABLE IF NOT EXISTS interactions (" +
		"int_id INTEGER PRIMARY KEY, " +
		"scid TEXT NOT NULL, " +
		//	"signer TEXT NOT NULL, " +
		"heights TEXT NOT NULL)"

		//		startup[len(startup)] = "CREATE TABLE IF NOT EXISTS relations (" +

	for _, create := range startup {
		executeQuery(Db, create)
	}
	var count int
	Db.QueryRow("SELECT COUNT(*) FROM state").Scan(&count)
	if count == 0 {
		fmt.Println("setting defaults")
		//set defaults
		statement, err := Db.Prepare("INSERT INTO state (name,value) VALUES('lastindexedheight'," + strconv.Itoa(int(startat)) + ");")
		handleError(err)
		statement.Exec()
	}

}

// executeQuery prepares and executes a SQL query.
func executeQuery(Db *sql.DB, query string) {
	statement, err := Db.Prepare(query)
	handleError(err)
	statement.Exec()
}

// handleError logs the error if LOGGING is enabled and then exits the program.
func handleError(err error) {
	if err != nil {
		log.Fatal(err)
	}
}

// --- extras...
func viewTables(Db *sql.DB) {
	/// check tables

	fmt.Println("\nShowing State: ")
	rows, err := Db.Query("SELECT name, value FROM state WHERE name = 'lastindexedheight'", nil)
	if err != nil {
		fmt.Println(err)
	}
	var (
		name  string
		value string
	)

	for rows.Next() {
		rows.Scan(&name, &value)
		fmt.Println(name, value)
	}

	fmt.Println("Showing SCs / Owners: ")
	rows, err = Db.Query("SELECT scid, owner, class, tags FROM scs", nil)
	if err != nil {
		fmt.Println(err)
	}
	var (
		scid  string
		owner string
		class string
		tags  string
	)

	for rows.Next() {
		rows.Scan(&scid, &owner, &class, &tags)
		fmt.Println("owner - scid - class - tags", owner+"--"+scid+"--"+class+"--"+tags)
	}

	//INSERT INTO vars (height, scid, vars) VALUES (?,?,?)
	fmt.Println("Showing Vars: ")
	rows, err = Db.Query("SELECT height, scid FROM variables", nil)
	if err != nil {
		fmt.Println(err)
	}
	var (
		height string
	)
	for rows.Next() {
		rows.Scan(&height, &scid)
		fmt.Println("height - scid - vars (not shown) ", height+"--"+scid)
	}

	fmt.Println("Showing Interactions: ")

	rows, err = Db.Query("SELECT count(int_id) as count FROM interactions", nil) //"SELECT count(*) heights, scid FROM interactions ORDER BY heights DESC LIMIT 1;"
	if err != nil {
		fmt.Println(err)
	}
	var (
		count string
	)
	for rows.Next() {
		rows.Scan(&count)

	}
	fmt.Println("count ", count)
}

//-----------------

// Stores bbolt's last indexed height - this is for stateful stores on close and reference on open
func (ss *SqlStore) StoreLastIndexHeight(last_indexedheight int64) (changes bool, err error) {

	statement, err := ss.DB.Prepare("UPDATE state SET value = ? WHERE name = ?;")
	if err != nil {
		panic(err)
	}
	result, _ := statement.Exec(
		last_indexedheight,
		"lastindexedheight",
	)
	affected_rows, _ := result.RowsAffected()
	if affected_rows != 0 {
		changes = true
		return
	}
	return
}

// Gets bbolt's last indexed height - this is for stateful stores on close and reference on open
func (ss *SqlStore) GetLastIndexHeight() (topoheight int64, err error) {
	var lastindexedheight int
	ss.DB.QueryRow("SELECT value FROM state WHERE name = 'lastindexedheight' ").Scan(&lastindexedheight)
	if lastindexedheight > 0 {
		topoheight = int64(lastindexedheight)
	}
	if topoheight == 0 {
		fmt.Println("[bbs-GetLastIndexHeight] No stored last index height. Starting from 0 or latest if fastsync is enabled")
	}

	return
}

/*
// Stores bbolt's txcount by a given txType - this is for stateful stores on close and reference on open
func (ss *SqlStore) StoreTxCount(count int64, txType string) (changes bool, err error) {
	bName := "stats"

	err = ss.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		key := txType + "txcount"

		txCount := strconv.FormatInt(count, 10)

		err = b.Put([]byte(key), []byte(txCount))
		changes = true
		return
	})

	return
}

// Gets bbolt's txcount by a given txType - this is for stateful stores on close and reference on open
func (ss *SqlStore) GetTxCount(txType string) (txCount int64) {
	bName := "stats"

	ss.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := txType + "txcount"
			v := b.Get([]byte(key))

			if v != nil {
				txCount, err = strconv.ParseInt(string(v), 10, 64)
				if err != nil {
					return fmt.Errorf("[ss-GetLastIndexHeight] ERR - Error parsing stored int for txcount: %v", err)
				}
			}
		}
		return
	})

	return
}
*/

// Stores the owner (who deployed it) of a given scid
func (ss *SqlStore) StoreOwner(scid string, owner string, class string, tags string) (changes bool, err error) {
	fmt.Println("INSERT INTO scs (owner,scid,class,tags) VALUES (?,?,?,?)")
	statement, err := ss.DB.Prepare("INSERT INTO scs (owner,scid,class,tags) VALUES (?,?,?,?)")
	if err != nil {
		log.Fatal(err)
	}
	result, err := statement.Exec(
		owner,
		scid,
		class,
		tags,
	)

	last_insert_id, _ := result.LastInsertId()
	fmt.Println("ownerinsertid: ", last_insert_id)
	if err == nil && last_insert_id >= 0 {
		changes = true
		return
	}
	return
	/*
		bName := "scowner"

		err = ss.DB.Update(func(tx *bolt.Tx) (err error) {
			b, err := tx.CreateBucketIfNotExists([]byte(bName))
			if err != nil {
				return fmt.Errorf("bucket: %s", err)
			}

			err = b.Put([]byte(scid), []byte(owner))
			changes = true
			return
		})

		return
	*/
}

/*
// Returns the owner (who deployed it) of a given scid
func (ss *SqlStore) GetOwner(scid string) string {
	var v []byte
	bName := "scowner"

	ss.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := scid
			v = b.Get([]byte(key))
		}

		return
	})

	if v != nil {
		return string(v)
	}

	logger.Printf("[GetOwner] No owner for %v", scid)

	return ""
}
*/
// Returns all of the deployed SCIDs with their corresponding owners (who deployed it)
func (ss *SqlStore) GetAllOwnersAndSCIDs() map[string]string {
	fmt.Println("SELECT scid, owner FROM scs")
	results := make(map[string]string)
	rows, _ := ss.DB.Query("SELECT scid, owner FROM scs", nil)
	var (
		scid  string
		owner string
	)

	for rows.Next() {
		rows.Scan(&scid, &owner)
		results[scid] = owner
	}
	return results

	/*
		bName := "scowner"
		results := make(map[string]string)
		ss.DB.View(func(tx *bolt.Tx) (err error) {
			b := tx.Bucket([]byte(bName))
			if b != nil {
				c := b.Cursor()

				for k, v := c.First(); err == nil; k, v = c.Next() {
					if k != nil && v != nil {
						results[string(k)] = string(v)
					} else {
						break
					}
				}
			}

			return
		})

		return results
	*/
}

/*
// Stores all scinvoke details of a given scid
func (ss *SqlStore) StoreInvokeDetails(scid string, signer string, entrypoint string, topoheight int64, invokedetails *SCTXParse) (changes bool, err error) {
	confBytes, err := json.Marshal(invokedetails)
	if err != nil {
		return changes, fmt.Errorf("[StoreInvokeDetails] could not marshal invokedetails info: %v", err)
	}

	bName := scid

	txidLen := len(invokedetails.Txid)
	key := signer + ":" + invokedetails.Txid[0:3] + invokedetails.Txid[txidLen-3:txidLen] + ":" + strconv.FormatInt(topoheight, 10) + ":" + entrypoint
	//signer:txid:height:entrypoint
	err = ss.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		err = b.Put([]byte(key), confBytes)
		changes = true
		return
	})

	return
}

// Returns all scinvoke calls from a given scid
func (ss *SqlStore) GetAllSCIDInvokeDetails(scid string) (invokedetails []*SCTXParse) {
	bName := scid

	ss.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for _, v := c.First(); err == nil; _, v = c.Next() {
				if v != nil {
					var currdetails *SCTXParse
					_ = json.Unmarshal(v, &currdetails)
					invokedetails = append(invokedetails, currdetails)
				} else {
					break
				}
			}
		}

		return
	})

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(invokedetails, func(i, j int) bool {
		return invokedetails[i].Height < invokedetails[j].Height
	})

	return invokedetails
}

// Retruns all scinvoke calls from a given scid that match a given entrypoint
func (ss *SqlStore) GetAllSCIDInvokeDetailsByEntrypoint(scid string, entrypoint string) (invokedetails []*SCTXParse) {
	bName := scid

	ss.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for _, v := c.First(); err == nil; _, v = c.Next() {
				if v != nil {
					var currdetails *SCTXParse
					_ = json.Unmarshal(v, &currdetails)
					if currdetails.Entrypoint == entrypoint {
						invokedetails = append(invokedetails, currdetails)
					}
				} else {
					break
				}
			}
		}

		return
	})

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(invokedetails, func(i, j int) bool {
		return invokedetails[i].Height < invokedetails[j].Height
	})

	return invokedetails
}

// Returns all scinvoke calls from a given scid that match a given signer
func (ss *SqlStore) GetAllSCIDInvokeDetailsBySigner(scid string, signerPart string) (invokedetails []*SCTXParse) {
	bName := scid

	ss.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for _, v := c.First(); err == nil; _, v = c.Next() {
				if v != nil {
					var currdetails *SCTXParse
					_ = json.Unmarshal(v, &currdetails)
					split := strings.Split(currdetails.Sender, signerPart)
					if len(split) > 1 {
						invokedetails = append(invokedetails, currdetails)
					}
				} else {
					break
				}
			}
		}

		return
	})

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(invokedetails, func(i, j int) bool {
		return invokedetails[i].Height < invokedetails[j].Height
	})

	return invokedetails
}
*/
// Stores SC variables at a given topoheight (called on any new scdeploy or scinvoke actions)
func (ss *SqlStore) StoreSCIDVariableDetails(scid string, variables []*SCIDVariable, topoheight int64) (changes bool, err error) {

	confBytes, err := json.Marshal(variables)
	if err != nil {
		return changes, fmt.Errorf("[StoreSCIDVariableDetails] could not marshal getinfo info: %v", err)
	}
	fmt.Println("INSERT INTO variables (height, scid, vars) VALUES (?,?,?)")
	statement, err := ss.DB.Prepare("INSERT INTO variables (height, scid, vars) VALUES (?,?,?)")
	if err != nil {
		log.Fatal(err)
	}
	result, err := statement.Exec(
		int(topoheight),
		scid,
		confBytes,
	)

	last_insert_id, _ := result.LastInsertId()
	if err == nil && last_insert_id >= 0 {
		changes = true
		return
	}

	/*

		confBytes, err := json.Marshal(variables)
		if err != nil {
			return changes, fmt.Errorf("[StoreSCIDVariableDetails] could not marshal getinfo info: %v", err)
		}

		bName := scid + "vars"

		key := strconv.FormatInt(topoheight, 10)

		err = ss.DB.Update(func(tx *bolt.Tx) (err error) {
			b, err := tx.CreateBucketIfNotExists([]byte(bName))
			if err != nil {
				return fmt.Errorf("bucket: %s", err)
			}

			err = b.Put([]byte(key), confBytes)
			changes = true
			return
		})
	*/
	return
}

// Gets SC variables at a given topoheight
func (ss *SqlStore) GetSCIDVariableDetailsAtTopoheight(scid string, topoheight int64) (hVars []*SCIDVariable) {
	results := make(map[int64][]*SCIDVariable)
	var heights []int64

	bName := scid + "vars"
	fmt.Println("GetSCIDVariableDetailsAtTopoheight", bName)
	/*

		"height INTEGER NOT NULL, " +
		"scid TEXT NOT NULL, " +
		"vars TEXT NOT NULL)"

	*/
	fmt.Println("SELECT height,vars FROM variables")
	rows, _ := ss.DB.Query("SELECT height,vars FROM variables", nil)
	var (
		vars   string
		height string
	)

	for rows.Next() {
		rows.Scan(&height, &vars)
		topoheight, _ := strconv.ParseInt(string(height), 10, 64)
		heights = append(heights, topoheight)
		var variables []*SCIDVariable
		_ = json.Unmarshal([]byte(vars), &variables)
		results[topoheight] = variables
	}

	/*	ss.DB.View(func(tx *bolt.Tx) (err error) {
			b := tx.Bucket([]byte(bName))
			if b != nil {

				c := b.Cursor()

				for k, v := c.First(); err == nil; k, v = c.Next() {
					if k != nil && v != nil {
						topoheight, _ := strconv.ParseInt(string(k), 10, 64)
						heights = append(heights, topoheight)
						var variables []*SCIDVariable
						_ = json.Unmarshal(v, &variables)
						results[topoheight] = variables
					} else {
						break
					}
				}
			}

			return
		})
	*/
	if results != nil {
		// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
		sort.SliceStable(heights, func(i, j int) bool {
			return heights[i] < heights[j]
		})

		vs2k := make(map[interface{}]interface{})
		for _, v := range heights {
			if v > topoheight {
				break
			}
			for _, vs := range results[v] {
				switch ckey := vs.Key.(type) {
				case float64:
					switch cval := vs.Value.(type) {
					case float64:
						vs2k[uint64(ckey)] = uint64(cval)
					case uint64:
						vs2k[uint64(ckey)] = cval
					case string:
						vs2k[uint64(ckey)] = cval
					default:
						if cval != nil {
							fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' does not match string, uint64 or float64.", cval)
						} else {
							vs2k[uint64(ckey)] = cval
						}
					}
				case uint64:
					switch cval := vs.Value.(type) {
					case float64:
						vs2k[ckey] = uint64(cval)
					case uint64:
						vs2k[ckey] = cval
					case string:
						vs2k[ckey] = cval
					default:
						if cval != nil {
							fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' does not match string, uint64 or float64.", cval)
						} else {
							vs2k[ckey] = cval
						}
					}
				case string:
					switch cval := vs.Value.(type) {
					case float64:
						vs2k[ckey] = uint64(cval)
					case uint64:
						vs2k[ckey] = cval
					case string:
						vs2k[ckey] = cval
					default:
						if cval != nil {
							fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' does not match string, uint64 or float64.", cval)
						} else {
							vs2k[ckey] = cval
						}
					}
				default:
					if ckey != nil {
						fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Key '%v' does not match string, uint64 or float64.", ckey)
					}
				}
			}
		}

		for k, v := range vs2k {
			// If value is nil, no reason to add.
			if v == nil || (reflect.ValueOf(v).Kind() == reflect.Ptr && reflect.ValueOf(v).IsNil()) || k == nil || (reflect.ValueOf(k).Kind() == reflect.Ptr && reflect.ValueOf(k).IsNil()) {
				//logger.Debugf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' or Key '%v' is nil. Continuing.", fmt.Sprintf("%v", v), fmt.Sprintf("%v", k))
				continue
			}
			co := &SCIDVariable{}

			switch ckey := k.(type) {
			case float64:
				switch cval := v.(type) {
				case float64:
					co.Key = uint64(ckey)
					co.Value = uint64(cval)
				case uint64:
					co.Key = uint64(ckey)
					co.Value = cval
				case string:
					co.Key = uint64(ckey)
					co.Value = cval
				default:
					fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' or Key '%v' does not match string, uint64 or float64.", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", uint64(ckey)))
					continue
				}
			case uint64:
				switch cval := v.(type) {
				case float64:
					co.Key = ckey
					co.Value = uint64(cval)
				case uint64:
					co.Key = ckey
					co.Value = cval
				case string:
					co.Key = ckey
					co.Value = cval
				default:
					fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' or Key '%v' does not match string, uint64 or float64.", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", ckey))
					continue
				}
			case string:
				switch cval := v.(type) {
				case float64:
					co.Key = ckey
					co.Value = uint64(cval)
				case uint64:
					co.Key = ckey
					co.Value = cval
				case string:
					co.Key = ckey
					co.Value = cval
				default:
					fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' or Key '%v' does not match string, uint64 or float64.", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", ckey))
					continue
				}
			}

			hVars = append(hVars, co)
		}
	}

	return
}

/*
// Gets SC variables at all topoheights
func (ss *SqlStore) GetAllSCIDVariableDetails(scid string) (hVars []*SCIDVariable) {
	results := make(map[int64][]*SCIDVariable)
	var heights []int64

	bName := scid + "vars"

	ss.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for k, v := c.First(); err == nil; k, v = c.Next() {
				if k != nil && v != nil {
					topoheight, _ := strconv.ParseInt(string(k), 10, 64)
					heights = append(heights, topoheight)
					var variables []*SCIDVariable
					_ = json.Unmarshal(v, &variables)
					results[topoheight] = variables
				} else {
					break
				}
			}
		}

		return
	})

	if results != nil {
		// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
		sort.SliceStable(heights, func(i, j int) bool {
			return heights[i] < heights[j]
		})

		vs2k := make(map[interface{}]interface{})
		for _, v := range heights {
			for _, vs := range results[v] {
				switch ckey := vs.Key.(type) {
				case float64:
					switch cval := vs.Value.(type) {
					case float64:
						vs2k[uint64(ckey)] = uint64(cval)
					case uint64:
						vs2k[uint64(ckey)] = cval
					case string:
						vs2k[uint64(ckey)] = cval
					default:
						if cval != nil {
							logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' does not match string, uint64 or float64.", cval)
						} else {
							vs2k[uint64(ckey)] = cval
						}
					}
				case uint64:
					switch cval := vs.Value.(type) {
					case float64:
						vs2k[ckey] = uint64(cval)
					case uint64:
						vs2k[ckey] = cval
					case string:
						vs2k[ckey] = cval
					default:
						if cval != nil {
							logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' does not match string, uint64 or float64.", cval)
						} else {
							vs2k[ckey] = cval
						}
					}
				case string:
					switch cval := vs.Value.(type) {
					case float64:
						vs2k[ckey] = uint64(cval)
					case uint64:
						vs2k[ckey] = cval
					case string:
						vs2k[ckey] = cval
					default:
						if cval != nil {
							logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' does not match string, uint64 or float64.", cval)
						} else {
							vs2k[ckey] = cval
						}
					}
				default:
					if ckey != nil {
						logger.Errorf("[GetAllSCIDVariableDetails] Key '%v' does not match string, uint64 or float64.", ckey)
					}
				}
			}
		}

		for k, v := range vs2k {
			// If value is nil, no reason to add.
			if v == nil || (reflect.ValueOf(v).Kind() == reflect.Ptr && reflect.ValueOf(v).IsNil()) || k == nil || (reflect.ValueOf(k).Kind() == reflect.Ptr && reflect.ValueOf(k).IsNil()) {
				//logger.Debugf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' is nil. Continuing.", fmt.Sprintf("%v", v), fmt.Sprintf("%v", k))
				continue
			}
			co := &SCIDVariable{}

			switch ckey := k.(type) {
			case float64:
				switch cval := v.(type) {
				case float64:
					co.Key = uint64(ckey)
					co.Value = uint64(cval)
				case uint64:
					co.Key = uint64(ckey)
					co.Value = cval
				case string:
					co.Key = uint64(ckey)
					co.Value = cval
				default:
					logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' is does not match string, uint64 or float64.", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", uint64(ckey)))
					continue
				}
			case uint64:
				switch cval := v.(type) {
				case float64:
					co.Key = ckey
					co.Value = uint64(cval)
				case uint64:
					co.Key = ckey
					co.Value = cval
				case string:
					co.Key = ckey
					co.Value = cval
				default:
					logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' does not match string, uint64 or float64.", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", ckey))
					continue
				}
			case string:
				switch cval := v.(type) {
				case float64:
					co.Key = ckey
					co.Value = uint64(cval)
				case uint64:
					co.Key = ckey
					co.Value = cval
				case string:
					co.Key = ckey
					co.Value = cval
				default:
					logger.Errorf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' does not match string, uint64 or float64.", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", ckey))
					continue
				}
			}

			hVars = append(hVars, co)
		}
	}

	return
}
*/
// Gets SC variable keys at given topoheight who's value equates to a given interface{} (string/uint64)
func (ss *SqlStore) GetSCIDKeysByValue(scid string, val interface{}, height int64, rmax bool) (keysstring []string, keysuint64 []uint64) {
	scidInteractionHeights := ss.GetSCIDInteractionHeight(scid)

	interactionHeight := ss.GetInteractionIndex(height, scidInteractionHeights, rmax)

	// TODO: If there's no interaction height, do we go get scvars against daemon and store? Or do we just ignore and return nil
	variables := ss.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

	// Switch against the value passed. If it's a uint64 or string
	switch inpvar := val.(type) {
	case uint64:
		for _, v := range variables {
			switch cval := v.Value.(type) {
			case float64:
				if inpvar == uint64(cval) {
					switch ckey := v.Key.(type) {
					case float64:
						keysuint64 = append(keysuint64, uint64(ckey))
					case uint64:
						keysuint64 = append(keysuint64, ckey)
					default:
						// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
						keysstring = append(keysstring, v.Key.(string))
					}
				}
			case uint64:
				if inpvar == cval {
					switch ckey := v.Key.(type) {
					case float64:
						keysuint64 = append(keysuint64, uint64(ckey))
					case uint64:
						keysuint64 = append(keysuint64, ckey)
					default:
						// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
						keysstring = append(keysstring, v.Key.(string))
					}
				}
			default:
				// Nothing - expect only string/uint64 for value types
			}
		}
	case string:
		for _, v := range variables {
			switch cval := v.Value.(type) {
			case string:
				if inpvar == cval {
					switch ckey := v.Key.(type) {
					case float64:
						keysuint64 = append(keysuint64, uint64(ckey))
					case uint64:
						keysuint64 = append(keysuint64, ckey)
					default:
						// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
						keysstring = append(keysstring, v.Key.(string))
					}
				}
			default:
				// Nothing - expect only string/uint64 for value types
			}
		}
	default:
		// Nothing - expect only string/uint64 for value types
	}

	return keysstring, keysuint64
}

// Gets SC values by key at given topoheight who's key equates to a given interface{} (string/uint64)
func (ss *SqlStore) GetSCIDValuesByKey(scid string, key interface{}, height int64, rmax bool) (valuesstring []string, valuesuint64 []uint64) {
	scidInteractionHeights := ss.GetSCIDInteractionHeight(scid)

	interactionHeight := ss.GetInteractionIndex(height, scidInteractionHeights, rmax)

	// TODO: If there's no interaction height, do we go get scvars against daemon and store? Or do we just ignore and return nil
	variables := ss.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

	// Switch against the value passed. If it's a uint64 or string
	switch inpvar := key.(type) {
	case uint64:
		for _, v := range variables {
			switch ckey := v.Key.(type) {
			case float64:
				if inpvar == uint64(ckey) {
					switch cval := v.Value.(type) {
					case float64:
						valuesuint64 = append(valuesuint64, uint64(cval))
					case uint64:
						valuesuint64 = append(valuesuint64, cval)
					default:
						// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
						valuesstring = append(valuesstring, v.Value.(string))
					}
				}
			case uint64:
				if inpvar == ckey {
					switch cval := v.Value.(type) {
					case float64:
						valuesuint64 = append(valuesuint64, uint64(cval))
					case uint64:
						valuesuint64 = append(valuesuint64, cval)
					default:
						// default just store as string. Keys should only ever be strings or uint64, however, but assume default to string
						valuesstring = append(valuesstring, v.Value.(string))
					}
				}
			default:
				// Nothing - expect only string/uint64 for value types
			}
		}
	case string:
		for _, v := range variables {
			switch ckey := v.Key.(type) {
			case string:
				if inpvar == ckey {
					switch cval := v.Value.(type) {
					case float64:
						valuesuint64 = append(valuesuint64, uint64(cval))
					case uint64:
						valuesuint64 = append(valuesuint64, cval)
					default:
						// default just store as string. Values should only ever be strings or uint64, however, but assume default to string
						valuesstring = append(valuesstring, v.Value.(string))
					}
				}
			default:
				// Nothing - expect only string/uint64 for value types
			}
		}
	default:
		// Nothing - expect only string/uint64 for value types
	}

	return valuesstring, valuesuint64
}

// Stores SC interaction height and detail - height invoked upon and type (scinstall/scinvoke). This is separate tree & k/v since we can query it for other things at less data retrieval
func (ss *SqlStore) StoreSCIDInteractionHeight(scid string, height int64) (changes bool, err error) {
	var currSCIDInteractionHeight []byte
	var interactionHeight []int64
	var newInteractionHeight []byte
	fmt.Println("StoreSCIDInteractionHeight... ")
	fmt.Println("SELECT heights FROM interactions WHERE scid=?")
	err = ss.DB.QueryRow("SELECT heights FROM interactions WHERE scid=?", scid).Scan(&currSCIDInteractionHeight)
	//fmt.Println("currSCIDInteractionHeight:", currSCIDInteractionHeight)
	//fmt.Println("currSCIDInteractionHeight err:", err)
	if err == nil {
		interactionHeight = append(interactionHeight, height)
	} else {
		// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
		_ = json.Unmarshal(currSCIDInteractionHeight, &interactionHeight)

		for _, v := range interactionHeight {
			if v == height {
				// Return nil if already exists in array.
				// Clause for this is in event we pop backwards in time and already have this data stored.
				// TODO: What if interaction happened on false-chain and pop to retain correct chain. Bad data may be stored here still, as it isn't removed. Need fix for this in future.
				return
			}
		}

		interactionHeight = append(interactionHeight, height)
	}
	newInteractionHeight, err = json.Marshal(interactionHeight)
	if err != nil {
		fmt.Printf("[SQLITE] StoreSCIDInteractionHeight could not marshal interactionHeight info: %v", err)
	}

	//No record found, create one
	if len(currSCIDInteractionHeight) == 0 {
		fmt.Println("(sql, insert interaction) INSERT INTO interactions (heights, scid) VALUES (?,?)")
		statement, err := ss.DB.Prepare("INSERT INTO interactions (heights, scid) VALUES (?,?)")
		if err != nil {
			log.Fatal(err)
		}
		result, err := statement.Exec(
			newInteractionHeight,
			scid,
		)

		last_insert_id, _ := result.LastInsertId()
		if err == nil && last_insert_id >= 0 {
			changes = true
		}
	} else {

		fmt.Println("(sql, update interaction) UPDATE interactions SET heights=? WHERE scid=?;")
		statement, err := ss.DB.Prepare("UPDATE interactions SET heights=? WHERE scid=?;")
		if err != nil {
			log.Fatal(err)
		}
		result, err := statement.Exec(
			newInteractionHeight,
			scid,
		)

		affected, _ := result.RowsAffected()
		if err == nil && affected >= 0 {
			changes = true

		}

	}

	return
	/*
		err = b.Put([]byte(key), newInteractionHeight)
		changes = true

		err = ss.DB.View(func(tx *bolt.Tx) (err error) {
			b := tx.Bucket([]byte(bName))
			if b != nil {
				currSCIDInteractionHeight = b.Get([]byte(key))
			}
			return
		})

		err = ss.DB.Update(func(tx *bolt.Tx) (err error) {
			b, err := tx.CreateBucketIfNotExists([]byte(bName))
			if err != nil {
				return fmt.Errorf("bucket: %s", err)
			}

			if currSCIDInteractionHeight == nil {
				interactionHeight = append(interactionHeight, height)
			} else {
				// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
				_ = json.Unmarshal(currSCIDInteractionHeight, &interactionHeight)

				for _, v := range interactionHeight {
					if v == height {
						// Return nil if already exists in array.
						// Clause for this is in event we pop backwards in time and already have this data stored.
						// TODO: What if interaction happened on false-chain and pop to retain correct chain. Bad data may be stored here still, as it isn't removed. Need fix for this in future.
						return
					}
				}

				interactionHeight = append(interactionHeight, height)
			}
			newInteractionHeight, err = json.Marshal(interactionHeight)
			if err != nil {
				return fmt.Errorf("[BBolt] could not marshal interactionHeight info: %v", err)
			}

			err = b.Put([]byte(key), newInteractionHeight)
			changes = true
			return
		})
	*/
	return
}

// Gets SC interaction height and detail by a given SCID
func (ss *SqlStore) GetSCIDInteractionHeight(scid string) (scidinteractions []int64) {
	fmt.Println("GetSCIDInteractionHeight... ")
	fmt.Println("SELECT heights FROM interactions WHERE scid=?")
	heights := ""
	ss.DB.QueryRow("SELECT heights FROM interactions WHERE scid=?", scid).Scan(&heights)
	if heights != "" {
		_ = json.Unmarshal([]byte(heights), &scidinteractions)
	}
	return

	/*

	   bName := scid + "heights"
	   	ss.DB.View(func(tx *bolt.Tx) (err error) {
	   		b := tx.Bucket([]byte(bName))
	   		if b != nil {
	   			key := scid
	   			v := b.Get([]byte(key))

	   			if v != nil {
	   				_ = json.Unmarshal(v, &scidinteractions)
	   			}
	   		}
	   		return
	   	})

	   	return

	*/
}

func (ss *SqlStore) GetInteractionIndex(topoheight int64, heights []int64, rmax bool) (height int64) {
	if len(heights) <= 0 {
		return height
	}

	// Sort heights so most recent is index 0 [if preferred reverse, just swap > with <]
	sort.SliceStable(heights, func(i, j int) bool {
		return heights[i] > heights[j]
	})

	if topoheight > heights[0] || rmax {
		return heights[0]
	}

	for i := 1; i < len(heights); i++ {
		if heights[i] < topoheight {
			return heights[i]
		} else if heights[i] == topoheight {
			return heights[i]
		}
	}

	return height
}

/*
// Stores any SCIDs that were attempted to be deployed but not correct - log scid/fees burnt attempting it.
func (ss *SqlStore) StoreInvalidSCIDDeploys(scid string, fee uint64) (changes bool, err error) {
	var currSCIDInteractionHeight []byte

	currInvalidSCIDs := make(map[string]uint64)
	var newInvalidSCIDs []byte

	bName := "invalidscids"
	key := "invalid"

	err = ss.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			currSCIDInteractionHeight = b.Get([]byte(key))
		}
		return
	})

	err = ss.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		if currSCIDInteractionHeight == nil {
			currInvalidSCIDs[scid] = fee
		} else {
			// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
			_ = json.Unmarshal(currSCIDInteractionHeight, &currInvalidSCIDs)

			currInvalidSCIDs[scid] = fee
		}
		newInvalidSCIDs, err = json.Marshal(currInvalidSCIDs)
		if err != nil {
			return fmt.Errorf("[bbs-StoreInvalidSCIDDeploys] could not marshal interactionHeight info: %v", err)
		}

		err = b.Put([]byte(key), newInvalidSCIDs)
		changes = true
		return
	})

	return
}

// Gets any SCIDs that were attempted to be deployed but not correct and their fees
func (ss *SqlStore) GetInvalidSCIDDeploys() map[string]uint64 {
	invalidSCIDs := make(map[string]uint64)

	bName := "invalidscids"

	ss.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := "invalid"
			v := b.Get([]byte(key))

			if v != nil {
				_ = json.Unmarshal(v, &invalidSCIDs)
			}
		}
		return
	})

	return invalidSCIDs
}

// Stores counts of miniblock finders by address
func (ss *SqlStore) StoreMiniblockCountByAddress(addr string) (changes bool, err error) {
	currCount := ss.GetMiniblockCountByAddress(addr)

	// Add 1 to currCount
	currCount++

	confBytes, err := json.Marshal(currCount)
	if err != nil {
		return changes, fmt.Errorf("[StoreMiniblockCountByAddress] could not marshal getinfo info: %v", err)
	}

	bName := "blockcount"

	key := addr

	err = ss.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		err = b.Put([]byte(key), confBytes)
		changes = true
		return
	})

	return
}

// Gets counts of miniblock finders by address
func (ss *SqlStore) GetMiniblockCountByAddress(addr string) (miniblocks int64) {
	bName := "blockcount"

	ss.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := addr
			v := b.Get([]byte(key))

			if v != nil {
				_ = json.Unmarshal(v, &miniblocks)
			}
		}
		return
	})

	return
}

// Stores the integrator addrs who submit blocks
func (ss *SqlStore) StoreIntegrators(integrator string) (changes bool, err error) {
	bName := "integrators"
	key := "integrators"

	var currIntegrators []byte
	newIntegratorsStag := make(map[string]int64)
	var newIntegrators []byte

	err = ss.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			currIntegrators = b.Get([]byte(key))
		}
		return
	})

	err = ss.DB.Update(func(tx *bolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		if currIntegrators == nil {
			newIntegratorsStag[integrator]++
		} else {
			// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
			_ = json.Unmarshal(currIntegrators, &newIntegratorsStag)

			newIntegratorsStag[integrator]++
		}

		newIntegrators, err = json.Marshal(newIntegratorsStag)
		if err != nil {
			return fmt.Errorf("[bbs-StoreInvalidSCIDDeploys] could not marshal integrators info: %v", err)
		}

		err = b.Put([]byte(key), newIntegrators)
		changes = true
		return
	})

	return
}

// Gets integrators and their counts
func (ss *SqlStore) GetIntegrators() (integrators map[string]int64) {
	bName := "integrators"
	key := "integrators"

	ss.DB.View(func(tx *bolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			v := b.Get([]byte(key))

			if v != nil {
				_ = json.Unmarshal(v, &integrators)
			}
		}
		return
	})

	return
}
*/
