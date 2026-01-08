package cmd

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/deroproject/derohe/rpc"
	"github.com/secretnamebasis/simple-gnomon/connections"
)

var info rpc.GetInfo_Result

// height processing point
func height_handling(ctx context.Context) {

	// simple-daemon
	for {
		select {
		case <-ctx.Done():
			return
		default:

			if !RUNNING {
				return
			}
			if achieved_current_height != 0 {
				time.Sleep(time.Second * time.Duration(info.Target))
			}

			if ending_height > 0 {
				now = min(ending_height, now)
			}
			// in case db needs to re-parse from a desired height
			if starting_height < now && starting_height > 0 && achieved_current_height == 0 {
				lowest_height = starting_height
			}

			result := now - lowest_height

			if result != 0 {
				wg := sync.WaitGroup{}
				height_processing := make(chan int64, result)
				for range parallel_blocks {
					wg.Add(1)
					go work_on_heights(ctx, height_processing, &wg)
				}
				fmt.Println("lowest", lowest_height)
				for height := lowest_height; height < now; height++ {
					if !RUNNING {
						return
					}
					TOPO = height

					height_processing <- height
				}
				close(height_processing)
				wg.Wait()
			}

			if achieved_current_height == 0 {
				fmt.Println("current height acheived, proceeding to passively index")
			}
			// height achieved
			height, _ := connections.Get_TopoHeight()
			achieved_current_height = height

			lowest_height = min(now, achieved_current_height)
		}
	}
}

func work_on_heights(ctx context.Context, height_processing chan int64, wg *sync.WaitGroup) {
	defer wg.Done()

	for height := range height_processing {

		if !RUNNING {
			return
		}

		// a simple backup strategy
		if achieved_current_height > 0 && !established_backup &&
			// if the current height is greater than a day of blocks...
			find_lowest_height(backup_database, now) {

			waitForAllQueues()

			backup(height)
		}

		measurements := []int{
			int(connections.DOWNLOADS.Load()),
			len(block_processing),
			len(transaction_processing),
			len(scid_processing),
			len(scid_db_queue),
		}

		if store_minis {
			measurements = append(measurements, len(mini_queue), len(mini_db_queue))
		}
		var m int
		for _, each := range measurements {
			m = max(m, each)
		}

		if m > 0 {
			time.Sleep(time.Millisecond * time.Duration(m))
		}
		//
		handle_height_task(height)
	}
}

func handle_height_task(height int64) {
	IN_PROGRESS.Swap(height)
	result, _ := connections.GetBlockInfo(rpc.GetBlock_Params{Height: uint64(height)})
	block_processing <- &processingStruct{Height: height, Result: result}
}
