package main

import (
	"os"

	"github.com/josephbolus/agentfactory/internal/factorycli"
)

func main() {
	os.Exit(factorycli.Run(factorycli.Options{
		Arguments: os.Args[1:],
		Stdout:    os.Stdout,
		Stderr:    os.Stderr,
	}))
}
