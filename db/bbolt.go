package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/deroproject/derohe/rpc"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
	"go.etcd.io/bbolt"
)

type BboltStore struct {
	DB         *bbolt.DB
	DBPath     string
	Writing    bool
	Exclusions []string
	//Writer  string
	Closing bool
	Buckets []string
}

func NewBBoltDB(dbPath, dbName string) (*BboltStore, error) {
	var err error
	var Bbolt_backend *BboltStore = &BboltStore{}

	if err := os.MkdirAll(dbPath, 0700); err != nil {
		return nil, fmt.Errorf("directory creation err %s - dirpath %s", err, dbPath)
	}
	db_path := filepath.Join(dbPath, dbName)
	Bbolt_backend.DB, err = bbolt.Open(db_path, 0600, &bbolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return Bbolt_backend, fmt.Errorf("[NewBBoltDB] Coult not create bbolt db store: %v", err)
	}

	Bbolt_backend.DBPath = dbPath

	return Bbolt_backend, err
}

// Manually add/inject a SCID to be indexed. Checks validity and then stores within owner tree (no signer addr) and stores a set of current variables.
func (bbs *BboltStore) AddSCIDToIndex(scidstoadd structures.SCIDToIndexStage) (err error) {

	defer func() { bbs.Writing = false }()

	if scidstoadd.Scid == "" {
		return errors.New("no scid")
	}

	writeWait, _ := time.ParseDuration("10ms")

	for bbs.Writing {

		time.Sleep(writeWait)
	}

	bbs.Writing = true

	// By returning valid variables of a given Scid (GetSC --> parse vars), we can conclude it is a valid SCID. Otherwise, skip adding to validated scids
	if len(scidstoadd.ScVars) != 0 {

		height := scidstoadd.Height

		// we'll just always keep the same topo height to prevent massive data bloat
		if !slices.Contains(bbs.Exclusions, scidstoadd.Scid) {
			height = -1
		}

		changed, err := bbs.StoreSCIDVariableDetails(scidstoadd.Scid, scidstoadd.ScVars, height)
		if err != nil {
			return err
		}

		if !changed {
			return errors.New("did not store scid/vars")
		}

		changed, err = bbs.StoreOwner(scidstoadd.Scid, scidstoadd.Sender, scidstoadd.Headers, scidstoadd.Class, scidstoadd.Tags)
		if err != nil {
			return err
		}
		if !changed {
			return errors.New("did not store scid/owner")
		}

	} else {

		changed, err := bbs.StoreSCIDInteractionHeight(scidstoadd.Scid, int64(scidstoadd.Fsi.Height))
		if err != nil {
			return err
		}

		// multiple interactions are possible
		if !changed {
			return nil
		}

	}

	return nil
}

func (bbs *BboltStore) BackUpDatabases() {

	fmt.Println("backing up database", bbs.DB.Path())
	// lock this up so we don't break it
	fmt.Println("Preparing snapshot")

	// capture the db
	database := bbs.DB
	//workers[index].Idx.BBSBackend.DB

	// sync the database
	if err := database.Sync(); err != nil {
		fmt.Println(err)
		return
	}

	// capture the db name
	scr_name := database.Path()

	// establish a source db file
	src, err := os.Open(scr_name)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer src.Close()

	// make a snapshot file
	dst_name := database.Path() + ".snapshot"

	// this will be the destination of the snapshot
	dst, err := os.Create(dst_name)
	if err != nil {
		fmt.Println(err)
		return
	}
	defer dst.Close()

	// remove this file when we are done
	defer os.Remove(dst_name)

	// now copy the src db file to the dst file
	if _, err = io.Copy(dst, src); err != nil {
		fmt.Println(err)
		return
	}

	// and commit.
	if err = dst.Sync(); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("Snapshot complete")

	fmt.Println("Preparing backup")

	// open the snapshot db as read only
	source, _ := bbolt.Open(dst_name, fs.FileMode(0444), &bbolt.Options{ReadOnly: true})
	defer source.Close()

	// establish as backup
	backup_name := database.Path() + ".bak"

	// establish a tmp backup
	tmp_backup := backup_name + ".tmp"

	// remove tmp backup when are done
	defer os.Remove(tmp_backup)

	// move this file to protect it
	if err := os.Rename(backup_name, tmp_backup); err != nil {
		fmt.Println(err)
		return
	}

	// open the backup db
	destination, _ := bbolt.Open(backup_name, fs.FileMode(0644), &bbolt.Options{})
	defer destination.Close()

	// we are going to use 64MB pagination as a place to start, tune as necessary
	txMaxSize := 64 * 1024 * 1024 // 64 MB

	// now copy the read only snapshot to the backup
	if err := bbolt.Compact(destination, source, int64(txMaxSize)); err != nil {
		fmt.Println(err)
		return
	}

	fmt.Println("database backed up")

}

// Stores bbolt's last indexed height - this is for stateful stores on close and reference on open
func (bbs *BboltStore) StoreLastIndexHeight(last_indexedheight int64) (changes bool, err error) {
	bName := "stats"

	err = bbs.DB.Update(func(tx *bbolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		key := "lastindexedheight"
		topoheight := strconv.FormatInt(last_indexedheight, 10)

		err = b.Put([]byte(key), []byte(topoheight))
		changes = true
		return
	})

	return
}

// Gets bbolt's last indexed height - this is for stateful stores on close and reference on open
func (bbs *BboltStore) GetLastIndexHeight() (topoheight int64, err error) {
	bName := "stats"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := "lastindexedheight"
			v := b.Get([]byte(key))

			if v != nil {
				topoheight, err = strconv.ParseInt(string(v), 10, 64)
				if err != nil {
					return fmt.Errorf("[bbs-GetLastIndexHeight] ERR - Error parsing stored int for lastindexheight: %v", err)
				}
			}
		}
		return
	})

	if topoheight == 0 {
		fmt.Printf("[bbs-GetLastIndexHeight] No stored last index height. Starting from 0 or latest if fastsync is enabled\n")
	}

	return
}

// Stores bbolt's txcount by a given txType - this is for stateful stores on close and reference on open
func (bbs *BboltStore) StoreTxCount(count int64, txType string) (changes bool, err error) {
	bName := "stats"

	err = bbs.DB.Update(func(tx *bbolt.Tx) (err error) {
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
func (bbs *BboltStore) GetTxCount(txType string) (txCount int64) {
	bName := "stats"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := txType + "txcount"
			v := b.Get([]byte(key))

			if v != nil {
				txCount, err = strconv.ParseInt(string(v), 10, 64)
				if err != nil {
					return fmt.Errorf("[bbs-GetLastIndexHeight] ERR - Error parsing stored int for txcount: %v", err)
				}
			}
		}
		return
	})

	return
}

// Stores the owner (who deployed it) of a given scid
func (bbs *BboltStore) StoreOwner(scid string, owner, headers, class, tags string) (changes bool, err error) {
	err = bbs.DB.Update(func(tx *bbolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte("scowner"))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}
		err = b.Put([]byte(scid), []byte(owner))
		if err != nil {
			return err
		}
		b, err = tx.CreateBucketIfNotExists([]byte("scheaders"))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}
		err = b.Put([]byte(scid), []byte(owner))
		if err != nil {
			return err
		}
		b, err = tx.CreateBucketIfNotExists([]byte("scowner"))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}
		err = b.Put([]byte(scid), []byte(owner))
		if err != nil {
			return err
		}
		b, err = tx.CreateBucketIfNotExists([]byte("scclass"))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}
		err = b.Put([]byte(scid), []byte(owner))
		if err != nil {
			return err
		}
		b, err = tx.CreateBucketIfNotExists([]byte("sctags"))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}
		err = b.Put([]byte(scid), []byte(owner))
		if err != nil {
			return err
		}
		changes = true
		return
	})

	return
}

// Returns the owner (who deployed it) of a given scid
func (bbs *BboltStore) GetOwner(scid string) string {
	var v []byte
	bName := "scowner"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
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

	fmt.Printf("[GetOwner] No owner for %v\n", scid)

	return ""
}

// Returns all of the deployed SCIDs with their corresponding owners (who deployed it)
func (bbs *BboltStore) GetAllOwnersAndSCIDs() map[string]string {
	results := make(map[string]string)

	bName := "scowner"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
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
}

// Returns all normal txs with SCIDs based on a given address
func (bbs *BboltStore) GetAllNormalTxWithSCIDByAddr(addr string) (normTxsWithSCID []*structures.NormalTXWithSCIDParse) {
	bName := "normaltxwithscid"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := addr
			v := b.Get([]byte(key))

			if v != nil {
				_ = json.Unmarshal(v, &normTxsWithSCID)
			}
		}
		return
	})

	return
}

// Returns all normal txs with SCIDs based on a given SCID
func (bbs *BboltStore) GetAllNormalTxWithSCIDBySCID(scid string) (normTxsWithSCID []*structures.NormalTXWithSCIDParse) {
	var resultset []string

	bName := "normaltxwithscid"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for _, v := c.First(); err == nil; _, v = c.Next() {
				if v != nil {
					var currdetails []*structures.NormalTXWithSCIDParse
					_ = json.Unmarshal(v, &currdetails)
					for _, cv := range currdetails {
						if cv.Scid == scid && !slices.Contains(resultset, cv.Txid) {
							normTxsWithSCID = append(normTxsWithSCID, cv)
							resultset = append(resultset, cv.Txid)
						}
					}
				} else {
					break
				}
			}
		}

		return
	})

	return
}

// Stores all normal txs with SCIDs and their respective ring members for future balance/interaction reference
func (bbs *BboltStore) StoreNormalTxWithSCIDByAddr(addr string, normTxWithSCID *structures.NormalTXWithSCIDParse) (changes bool, err error) {
	var newNormTxsWithSCID []byte
	var currNormTxsWithSCID []byte
	var normTxsWithSCID []*structures.NormalTXWithSCIDParse
	bName := "normaltxwithscid"
	key := addr

	err = bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			currNormTxsWithSCID = b.Get([]byte(key))
			value := b.Get([]byte(key))
			if value != nil {
				currNormTxsWithSCID = make([]byte, len(value))
				copy(currNormTxsWithSCID, value)
			}
		}
		return
	})

	err = bbs.DB.Update(func(tx *bbolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		if currNormTxsWithSCID == nil {
			normTxsWithSCID = append(normTxsWithSCID, normTxWithSCID)
		} else {
			// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
			_ = json.Unmarshal(currNormTxsWithSCID, &normTxsWithSCID)

			for _, v := range normTxsWithSCID {
				if v.Txid == normTxWithSCID.Txid {
					// Return nil if already exists in array.
					// Clause for this is in event we pop backwards in time and already have this data stored.
					// TODO: What if interaction happened on false-chain and pop to retain correct chain. Bad data may be stored here still, as it isn't removed. Need fix for this in future.
					return
				}
			}

			normTxsWithSCID = append(normTxsWithSCID, normTxWithSCID)
		}
		newNormTxsWithSCID, err = json.Marshal(normTxsWithSCID)
		if err != nil {
			return fmt.Errorf("[BBolt] could not marshal normTxsWithSCID info: %v", err)
		}

		err = b.Put([]byte(key), newNormTxsWithSCID)
		changes = true
		return
	})

	return
}

// Stores all scinvoke details of a given scid
func (bbs *BboltStore) StoreInvokeDetails(scid string, signer string, entrypoint string, topoheight int64, invokedetails *structures.SCTXParse) (changes bool, err error) {
	confBytes, err := json.Marshal(invokedetails)
	if err != nil {
		return changes, fmt.Errorf("[StoreInvokeDetails] could not marshal invokedetails info: %v", err)
	}

	bName := scid

	txidLen := len(invokedetails.Txid)
	key := signer + ":" + invokedetails.Txid[0:3] + invokedetails.Txid[txidLen-3:txidLen] + ":" + strconv.FormatInt(topoheight, 10) + ":" + entrypoint

	err = bbs.DB.Update(func(tx *bbolt.Tx) (err error) {
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
func (bbs *BboltStore) GetAllSCIDInvokeDetails(scid string) (invokedetails []*structures.SCTXParse) {
	bName := scid

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for _, v := c.First(); err == nil; _, v = c.Next() {
				if v != nil {
					var currdetails *structures.SCTXParse
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
func (bbs *BboltStore) GetAllSCIDInvokeDetailsByEntrypoint(scid string, entrypoint string) (invokedetails []*structures.SCTXParse) {
	bName := scid

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for _, v := c.First(); err == nil; _, v = c.Next() {
				if v != nil {
					var currdetails *structures.SCTXParse
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
func (bbs *BboltStore) GetAllSCIDInvokeDetailsBySigner(scid string, signerPart string) (invokedetails []*structures.SCTXParse) {
	bName := scid

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for _, v := c.First(); err == nil; _, v = c.Next() {
				if v != nil {
					var currdetails *structures.SCTXParse
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

// Stores simple getinfo polling from the daemon
func (bbs *BboltStore) StoreGetInfoDetails(getinfo *rpc.GetInfo_Result) (changes bool, err error) {
	confBytes, err := json.Marshal(getinfo)
	if err != nil {
		return changes, fmt.Errorf("[StoreGetInfoDetails] could not marshal getinfo info: %v", err)
	}

	bName := "getinfo"

	key := "getinfo"

	err = bbs.DB.Update(func(tx *bbolt.Tx) (err error) {
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

// Returns simple getinfo polling from the daemon
func (bbs *BboltStore) GetGetInfoDetails() (getinfo *rpc.GetInfo_Result) {
	var v []byte
	bName := "getinfo"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := "getinfo"
			v = b.Get([]byte(key))
		}

		return
	})

	if v != nil {
		_ = json.Unmarshal(v, &getinfo)
		return
	}

	return
}

// Stores SC variables at a given topoheight (called on any new scdeploy or scinvoke actions)
func (bbs *BboltStore) StoreSCIDVariableDetails(scid string, variables []*structures.SCIDVariable, topoheight int64) (changes bool, err error) {
	confBytes, err := json.Marshal(variables)
	if err != nil {
		return changes, fmt.Errorf("[StoreSCIDVariableDetails] could not marshal getinfo info: %v", err)
	}

	bName := scid + "vars"

	key := strconv.FormatInt(topoheight, 10)

	err = bbs.DB.Update(func(tx *bbolt.Tx) (err error) {
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

// Gets SC variables at a given topoheight
func (bbs *BboltStore) GetSCIDVariableDetailsAtTopoheight(scid string, topoheight int64) (hVars []*structures.SCIDVariable) {
	results := make(map[int64][]*structures.SCIDVariable)
	var heights []int64

	bName := scid + "vars"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for k, v := c.First(); err == nil; k, v = c.Next() {
				if k != nil && v != nil {
					topoheight, _ := strconv.ParseInt(string(k), 10, 64)
					heights = append(heights, topoheight)
					var variables []*structures.SCIDVariable
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
							fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' does not match string, uint64 or float64.\n", cval)
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
							fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' does not match string, uint64 or float64.\n", cval)
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
							fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' does not match string, uint64 or float64.\n", cval)
						} else {
							vs2k[ckey] = cval
						}
					}
				default:
					if ckey != nil {
						fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Key '%v' does not match string, uint64 or float64.\n", ckey)
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
			co := &structures.SCIDVariable{}

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
					fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' or Key '%v' does not match string, uint64 or float64.\n", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", uint64(ckey)))
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
					fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' or Key '%v' does not match string, uint64 or float64.\n", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", ckey))
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
					fmt.Printf("[GetSCIDVariableDetailsAtTopoheight] Value '%v' or Key '%v' does not match string, uint64 or float64.\n", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", ckey))
					continue
				}
			}

			hVars = append(hVars, co)
		}
	}

	return
}

// Gets SC variables at all topoheights
func (bbs *BboltStore) GetAllSCIDVariableDetails(scid string) (hVars []*structures.SCIDVariable) {
	results := make(map[int64][]*structures.SCIDVariable)
	var heights []int64

	bName := scid + "vars"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for k, v := c.First(); err == nil; k, v = c.Next() {
				if k != nil && v != nil {
					topoheight, _ := strconv.ParseInt(string(k), 10, 64)
					heights = append(heights, topoheight)
					var variables []*structures.SCIDVariable
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
							fmt.Printf("[GetAllSCIDVariableDetails] Value '%v' does not match string, uint64 or float64.\n", cval)
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
							fmt.Printf("[GetAllSCIDVariableDetails] Value '%v' does not match string, uint64 or float64.\n", cval)
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
							fmt.Printf("[GetAllSCIDVariableDetails] Value '%v' does not match string, uint64 or float64.\n", cval)
						} else {
							vs2k[ckey] = cval
						}
					}
				default:
					if ckey != nil {
						fmt.Printf("[GetAllSCIDVariableDetails] Key '%v' does not match string, uint64 or float64.\n", ckey)
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
			co := &structures.SCIDVariable{}

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
					fmt.Printf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' is does not match string, uint64 or float64.\n", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", uint64(ckey)))
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
					fmt.Printf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' does not match string, uint64 or float64.\n", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", ckey))
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
					fmt.Printf("[GetAllSCIDVariableDetails] Value '%v' or Key '%v' does not match string, uint64 or float64.\n", fmt.Sprintf("%v", cval), fmt.Sprintf("%v", ckey))
					continue
				}
			}

			hVars = append(hVars, co)
		}
	}

	return
}

// Gets SC variable keys at given topoheight who's value equates to a given interface{} (string/uint64)
func (bbs *BboltStore) GetSCIDKeysByValue(scid string, val interface{}, height int64, rmax bool) (keysstring []string, keysuint64 []uint64) {
	scidInteractionHeights := bbs.GetSCIDInteractionHeight(scid)

	interactionHeight := bbs.GetInteractionIndex(height, scidInteractionHeights, rmax)

	// TODO: If there's no interaction height, do we go get scvars against daemon and store? Or do we just ignore and return nil
	variables := bbs.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

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
func (bbs *BboltStore) GetSCIDValuesByKey(scid string, key interface{}, height int64, rmax bool) (valuesstring []string, valuesuint64 []uint64) {
	scidInteractionHeights := bbs.GetSCIDInteractionHeight(scid)

	interactionHeight := bbs.GetInteractionIndex(height, scidInteractionHeights, rmax)

	// TODO: If there's no interaction height, do we go get scvars against daemon and store? Or do we just ignore and return nil
	variables := bbs.GetSCIDVariableDetailsAtTopoheight(scid, interactionHeight)

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
func (bbs *BboltStore) StoreSCIDInteractionHeight(scid string, height int64) (changes bool, err error) {
	var currSCIDInteractionHeight []byte
	var interactionHeight []int64
	var newInteractionHeight []byte
	bName := scid + "heights"
	key := scid

	err = bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			value := b.Get([]byte(key))
			if value != nil {
				currSCIDInteractionHeight = make([]byte, len(value))
				copy(currSCIDInteractionHeight, value)
			}
		}
		return
	})

	err = bbs.DB.Update(func(tx *bbolt.Tx) (err error) {
		b, err := tx.CreateBucketIfNotExists([]byte(bName))
		if err != nil {
			return fmt.Errorf("bucket: %s", err)
		}

		if currSCIDInteractionHeight == nil {
			interactionHeight = append(interactionHeight, height)
		} else {
			// Retrieve value and conovert to SCIDInteractionHeight, so that you can manipulate and update db
			// fmt.Println(string(currSCIDInteractionHeight), &interactionHeight, interactionHeight)
			_ = json.Unmarshal(currSCIDInteractionHeight, &interactionHeight)

			if slices.Contains(interactionHeight, height) {

				// Return nil if already exists in array.

				// Clause for this is in event we pop backwards in time and already have this data stored.
				// TODO: What if interaction happened on false-chain and pop to retain correct chain. Bad data may be stored here still, as it isn't removed. Need fix for this in future.
				return
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

	return
}

// Gets SC interaction height and detail by a given SCID
func (bbs *BboltStore) GetSCIDInteractionHeight(scid string) (scidinteractions []int64) {
	bName := scid + "heights"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
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
}

func (bbs *BboltStore) GetInteractionIndex(topoheight int64, heights []int64, rmax bool) (height int64) {
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

// Stores any SCIDs that were attempted to be deployed but not correct - log scid/fees burnt attempting it.
func (bbs *BboltStore) StoreInvalidSCIDDeploys(scid string, fee uint64) (changes bool, err error) {
	var currSCIDInteractionHeight []byte

	currInvalidSCIDs := make(map[string]uint64)
	var newInvalidSCIDs []byte

	bName := "invalidscids"
	key := "invalid"

	err = bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			currSCIDInteractionHeight = b.Get([]byte(key))
		}
		return
	})

	err = bbs.DB.Update(func(tx *bbolt.Tx) (err error) {
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
func (bbs *BboltStore) GetInvalidSCIDDeploys() map[string]uint64 {
	invalidSCIDs := make(map[string]uint64)

	bName := "invalidscids"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
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

// Stores the miniblocks within a given blid
func (bbs *BboltStore) StoreMiniblockDetailsByHash(blid string, mbldetails []*structures.MBLInfo) (changes bool, err error) {

	confBytes, err := json.Marshal(mbldetails)
	if err != nil {
		return changes, fmt.Errorf("[StoreMiniblockDetailsByHash] could not marshal getinfo info: %v", err)
	}

	bName := "miniblocks"

	key := blid

	err = bbs.DB.Update(func(tx *bbolt.Tx) (err error) {
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

// Returns all miniblock details for synced chain
func (bbs *BboltStore) GetAllMiniblockDetails() map[string][]*structures.MBLInfo {
	mbldetails := make(map[string][]*structures.MBLInfo)

	bName := "miniblocks"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {

			c := b.Cursor()

			for k, v := c.First(); err == nil; k, v = c.Next() {
				if k != nil && v != nil {
					var currdetails []*structures.MBLInfo
					_ = json.Unmarshal(v, &currdetails)
					mbldetails[string(k)] = currdetails
				} else {
					break
				}
			}
		}

		return
	})

	return mbldetails
}

// Returns the miniblocks within a given blid if previously stored
func (bbs *BboltStore) GetMiniblockDetailsByHash(blid string) (miniblocks []*structures.MBLInfo) {
	bName := "miniblocks"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			key := blid
			v := b.Get([]byte(key))

			if v != nil {
				_ = json.Unmarshal(v, &miniblocks)
			}
		}
		return
	})

	return
}

// Stores counts of miniblock finders by address
func (bbs *BboltStore) StoreMiniblockCountByAddress(currCount int64, addr string) (changes bool, err error) {

	confBytes, err := json.Marshal(currCount)
	if err != nil {
		return changes, fmt.Errorf("[StoreMiniblockCountByAddress] could not marshal getinfo info: %v", err)
	}

	bName := "blockcount"

	key := addr

	err = bbs.DB.Update(func(tx *bbolt.Tx) (err error) {
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
func (bbs *BboltStore) GetMiniblockCountByAddress(addr string) (miniblocks int64) {
	bName := "blockcount"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
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
func (bbs *BboltStore) StoreIntegrators(integrator string) (changes bool, err error) {
	bName := "integrators"
	key := "integrators"

	var currIntegrators []byte
	newIntegratorsStag := make(map[string]int64)
	var newIntegrators []byte

	err = bbs.DB.View(func(tx *bbolt.Tx) (err error) {
		b := tx.Bucket([]byte(bName))
		if b != nil {
			currIntegrators = b.Get([]byte(key))
		}
		return
	})

	err = bbs.DB.Update(func(tx *bbolt.Tx) (err error) {
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
func (bbs *BboltStore) GetIntegrators() (integrators map[string]int64) {
	bName := "integrators"
	key := "integrators"

	bbs.DB.View(func(tx *bbolt.Tx) (err error) {
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
