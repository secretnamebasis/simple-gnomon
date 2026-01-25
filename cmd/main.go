package main

import (
	"fmt"

	"github.com/secretnamebasis/simple-gnomon/pkg"
)

func main() {

	// start the indexer
	if err := pkg.Start_gnomon_indexer(); err != nil {
		fmt.Println(err)
		return
	}
	// block to prevent gnomon go routine closure
	select {}
}
