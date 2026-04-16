package pkg

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	network "github.com/deroproject/derohe/globals"
	"github.com/secretnamebasis/simple-gnomon/db"
	"github.com/secretnamebasis/simple-gnomon/globals"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

// BACKEND & BACKUPS
func set_up_backend() error {

	// if there is a search filter...
	if search_filter != "" {
		search := search_filter

		action := func(i int, terms []string) {
			indices = append(indices, structures.SearchFilter{
				Name:  "Filter " + strconv.Itoa(i),
				Terms: terms,
			})
		}

		callback := func(i int, filter string) {
			if strings.Contains(filter, ";") {
				terms := strings.Split(filter, ";")
				action(i, terms)
			} else {
				action(i, []string{filter})
			}
		}

		switch {
		case strings.Contains(search, ";;;"):
			for i, filter := range strings.Split(search, ";;;") {
				callback(i, filter)
			}
		case !strings.Contains(search, ";;;"):
			callback(0, search)
		}

	}

	if search_filter == "" && len(indices) == 0 {
		cfg := filepath.Join("config", "search.json")
		if _, err := os.Stat(cfg); err != nil {
			// for now, these are the collections we are looking for
			// title, search terms
			indices = []structures.SearchFilter{
				{Name: "g45", Terms: []string{"G45-NFT", "G45-AT", "G45-C", "G45-FAT", "G45-NAME", "T345"}},
				{Name: "nfa", Terms: []string{"ART-NFA-MS1"}},
				{Name: "tela", Terms: []string{"docVersion", "telaVersion"}},
			}

			if err := os.Mkdir(filepath.Dir(cfg), 0700); err != nil {
				if errors.Is(err, os.ErrExist) {
					fmt.Println(err)
				} else {
					return err
				}
			}

			b, err := json.MarshalIndent(indices, "", "\t")
			if err != nil {
				return err
			}

			if err := os.WriteFile(cfg, b, 0600); err != nil {
				return err
			}

		} else {

			fi, err := os.OpenFile(cfg, os.O_RDONLY, 0600)
			if err != nil {
				return err
			}

			b, err := io.ReadAll(fi)
			if err != nil {
				return err
			}

			if err := json.Unmarshal(b, &indices); err != nil {
				return err
			}
		}
	}

	fmt.Println("searches indices:", len(indices))

	type excluded struct {
		Name   string
		SCID   string
		Reason string
	}

	var excludes []excluded

	// if exclusions are provided...
	if exclusions != "" {
		exclude := exclusions

		callback := func(i int, scid string) {
			excludes = append(excludes, excluded{
				Name:   "Exclusion " + strconv.Itoa(i),
				SCID:   scid,
				Reason: "exclusion flag",
			})
		}

		switch {
		case strings.Contains(exclude, ";;;"):
			for i, filter := range strings.Split(exclude, ";;;") {
				callback(i, filter)
			}
		case !strings.Contains(exclude, ";;;"):
			callback(0, exclude)
		}
	}

	// otherwise if there is no flag
	if exclusions == "" && len(excludes) == 0 {
		exclude := filepath.Join("config", "exclude.json")
		if _, err := os.Stat(exclude); err != nil {
			// for now, these are the collections we are looking for
			// title, search terms
			excludes = []excluded{
				{Name: "NAMESERVICE", SCID: globals.NAMESERVICE, Reason: "Hardcoded Contract"},
				{Name: "Gnomon Smart Contract", SCID: globals.MAINNET_GNOMON_SCID, Reason: "Large Contract"},
			}

			if err := os.Mkdir(filepath.Dir(exclude), 0700); err != nil {

				if errors.Is(err, os.ErrExist) {
					fmt.Println(err)
				} else {
					return err
				}
			}

			b, err := json.MarshalIndent(excludes, "", "\t")
			if err != nil {
				return err
			}

			if err := os.WriteFile(exclude, b, 0600); err != nil {
				return err
			}
		} else {
			fi, err := os.OpenFile(exclude, os.O_RDONLY, 0600)
			if err != nil {
				return err
			}

			b, err := io.ReadAll(fi)
			if err != nil {
				return err
			}

			if err := json.Unmarshal(b, &excludes); err != nil {
				return err
			}
		}
	}
	fmt.Println("exclusions", len(excludes))

	// DB SETUP
	db_name := fmt.Sprintf("%s.db", "GNOMON")
	db_backup_name := db_name + ".bak"
	wd := network.GetDataDirectory()
	db_path := filepath.Join(wd, "gnomondb")
	var b *db.BboltStore
	var bb *db.BboltStore

	var err error

	b, err = db.NewBBoltDB(db_path, db_name)
	if err != nil {
		return err
	}

	bb, err = db.NewBBoltDB(db_path, db_backup_name)
	if err != nil {
		return err
	}
	time.Sleep(time.Second * 1) // we need a second okay...

	for _, exclude := range excludes {
		b.Exclusions = append(b.Exclusions, exclude.SCID)
		bb.Exclusions = append(bb.Exclusions, exclude.SCID)
	}

	// initialize each indexer
	database = b

	backup_database = bb

	return nil
}

// this will serve as the backup action
func backup() {
	mu := sync.Mutex{}

	// full backup
	mu.Lock()
	database.BackUpDatabases(silent)
	mu.Unlock()

	established_backup = true
}
