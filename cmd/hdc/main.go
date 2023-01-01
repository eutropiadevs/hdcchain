package main

import (
	"os"

	"github.com/cosmos/cosmos-sdk/server"
	servercmd "github.com/cosmos/cosmos-sdk/server/cmd"

	hdc "github.com/eutropiadevs/hdc/app"
)

func main() {
	hdc.SetAccountAddressPrefixes()

	root, _ := NewRootCmd()
	if err := servercmd.Execute(root, hdc.DefaultNodeHome); err != nil {
		switch e := err.(type) {
		case server.ErrorCode:
			os.Exit(e.Code)
		default:
			os.Exit(1)
		}
	}
}
