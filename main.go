package main

import (
	"fmt"

	"github.com/secretnamebasis/simple-gnomon/cmd"
)

func main() {

	fmt.Println("Clear is better than clever. \n- Robert Pike")

	// start the indexer
	if err := cmd.Start_gnomon_indexer(); err != nil {
		fmt.Println(err)
		return
	}
	// block to prevent gnomon go routine closure
	select {}
}
