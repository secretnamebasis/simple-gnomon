package pkg

import (
	"context"
	"log"

	"github.com/secretnamebasis/simple-gnomon/db"
	structures "github.com/secretnamebasis/simple-gnomon/structs"
)

var mini_db_queue = make(chan miniStructure, 100_000)

func mini_db_writer(ctx context.Context) {
	all_details := database.GetAllMiniblockDetails()
	miner_map := map[string]int64{}
	for _, each := range all_details {
		for _, mini := range each {
			miner_map[mini.Miner]++
		}
	}
	work := func(staged miniStructure) {
		if _, ok := all_details[staged.Hash]; ok {
			return
		}
		database.StoreMiniblockDetailsByHash(staged.Hash, staged.Minis)
		for _, mini := range staged.Minis {
			miner_map[mini.Miner]++
			database.StoreMiniblockCountByAddress(miner_map[mini.Miner], mini.Miner)
		}
	}
	for {
		select {
		case <-ctx.Done():
			for areQueuesEmpty() {
				for staged := range mini_db_queue {
					work(staged)
				}
			}
			return
		case staged := <-mini_db_queue:
			work(staged)
		}
	}
}

var scid_db_queue = make(chan *structures.SCIDToIndexStage, 100_000)
var staged_for_writing = make(chan structures.SCIDToIndexStage, 100_000)

func scid_db_writer(ctx context.Context) {
	work := func(staged *structures.SCIDToIndexStage) {

		if staged.Method == "install" {
			staged_for_writing <- *staged
		}
		// store scid by tag
		if err := database.AddSCIDToIndex(*staged); err != nil {
			log.Fatal("indexer error:", err, staged.Scid, staged.Height)
			return
		}

		if achieved_current_height > 0 { // once the indexer has reached the top...
			// do incremental backups
			if err := backup_database.AddSCIDToIndex(*staged); err != nil {
				log.Fatal("indexer error:", err, staged.Scid, staged.Height)
				return
			}
			storeHeight(backup_database, int64(staged.Height))
		}

		// store counts
		database.StoreTxCount(holding_queue.registration.Load(), "registration")
		database.StoreTxCount(holding_queue.burn.Load(), "burn")
		database.StoreTxCount(holding_queue.normal.Load(), "normal")

		// store height
		storeHeight(database, int64(staged.Height))
	}
	for {
		select {
		case <-ctx.Done():
			for areQueuesEmpty() {
				for staged := range scid_db_queue {
					work(staged)
				}
			}
			return
		case staged := <-scid_db_queue:
			work(staged)
		}
	}
}

func storeHeight(d *db.BboltStore, height int64) error {
	if ok, err := d.StoreLastIndexHeight(height); !ok && err != nil {
		return err
	}
	return nil
}
