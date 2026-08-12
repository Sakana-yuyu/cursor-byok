package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"cursor/internal/cursorcapabilities"
)

func main() {
	root := flag.String("root", "", "Cursor installation root")
	flag.Parse()
	report, err := cursorcapabilities.Scan(*root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	if err := encoder.Encode(report); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
