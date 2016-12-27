package main

import (
	"github.com/thisisfineio/gox/goxlib"
	"fmt"
	"os"
)

func main() {
	if err := goxlib.CrossCompile(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
