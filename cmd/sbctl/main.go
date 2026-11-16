package main

import (
	"os"
	"sbctl/internal/sbctl"
)

func main() { os.Exit(sbctl.ControllerMain(os.Args[1:])) }
