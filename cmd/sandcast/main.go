// Command sandcast turns a project directory into a sandbox image.
package main

import (
	"os"

	"github.com/rhuss/sandcast/internal/cli"
)

func main() {
	os.Exit(cli.Execute(os.Args[1:]))
}
