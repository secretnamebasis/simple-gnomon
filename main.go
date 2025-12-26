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

	if slices.Contains(os.Args, "-help") {
		cmd.Start_gnomon_indexer()
		fmt.Println(`  -no-gui                          Disable GUI`)
		return
	}

	fmt.Println("Clear is better than clever. \n- Robert Pike")
	if !slices.Contains(os.Args, "-no-gui") {

		fmt.Println("GUI ENABLED")
		app.RenderGUI()

	} else {

		fmt.Println("GUI DISABLED")

		// remove flag from args
		i := slices.Index(os.Args, "-no-gui")
		j := i + 1
		os.Args = slices.Delete(os.Args, i, j)

		// start the indexer
		if err := cmd.Start_gnomon_indexer(); err != nil {
			fmt.Println(err)
			return
		}

		// block to prevent gnomon_indexer go routine closure
		<-exit
	}
}
