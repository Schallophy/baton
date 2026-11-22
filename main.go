// Command baton collects the local session stores of coding agents and turns
// the one you were in the middle of into a handoff transcript for the next one.
package main

import (
	"os"

	"github.com/Schallophy/baton/internal/app"
)

func main() {
	os.Exit(app.Run())
}
