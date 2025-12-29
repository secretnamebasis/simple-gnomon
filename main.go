package main

import (
	"fmt"
	"os"
	"slices"

	"github.com/secretnamebasis/simple-gnomon/app"
	"github.com/secretnamebasis/simple-gnomon/cmd"
)

var exit chan struct{}

func main() {

	if slices.Contains(os.Args, "--help") || slices.Contains(os.Args, "-h") {
		cmd.Start_gnomon_indexer()
		fmt.Println(`  --gui                          Enable GUI`)
		return
	}

	fmt.Println("Clear is better than clever. \n- Robert Pike")
	if slices.Contains(os.Args, "--gui") {
		fmt.Println("GUI ENABLED")
		// remove flag from args
		i := slices.Index(os.Args, "--gui")
		j := i + 1
		os.Args = slices.Delete(os.Args, i, j)

		app.RenderGUI()
		// block to prevent gnomon_indexer go routine closure
		<-exit
	} else {
		// start the indexer
		if err := cmd.Start_gnomon_indexer(); err != nil {
			fmt.Println(err)
			return
		}
	}
}
