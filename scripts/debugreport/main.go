package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	"cursor/internal/appdata"
	"cursor/internal/debugreport"
)

func main() {
	flags := flag.NewFlagSet("debugreport", flag.ExitOnError)
	historyRoot := flags.String("history-root", appdata.HistoryRootPath(), "history directory to read")
	conversationID := flags.String("conversation", "", "conversation ID")
	requestID := flags.String("request", "", "request ID")
	if err := flags.Parse(os.Args[1:]); err != nil {
		exitf(err.Error())
	}
	if strings.TrimSpace(*conversationID) == "" || strings.TrimSpace(*requestID) == "" {
		exitf("-conversation and -request are required")
	}
	report, err := debugreport.LoadRequestReport(*historyRoot, *conversationID, *requestID)
	if err != nil {
		exitf(err.Error())
	}
	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		exitf(err.Error())
	}
	fmt.Println(string(encoded))
}

func exitf(message string) {
	fmt.Fprintf(os.Stderr, "debugreport failed: %s\n", strings.TrimSpace(message))
	os.Exit(1)
}
