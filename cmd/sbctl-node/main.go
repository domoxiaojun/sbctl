package main

import (
	"os"
	"sbctl/internal/sbctl"
)

func main() { os.Exit(sbctl.NodeMain(os.Args[1:])) }
