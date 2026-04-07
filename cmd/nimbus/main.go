package main

import (
	"flag"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/wagnerbm/nimbusv2/internal/trace"
	"github.com/wagnerbm/nimbusv2/internal/ui"
)

func main() {
	traceEnabled := flag.Bool("trace", false, "Enable trace logging of TUI actions")
	traceFile := flag.String("trace-file", "nimbus-trace.log", "Path to trace output file")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: nimbus [flags]\n\nFlags:\n")
		flag.PrintDefaults()
	}
	flag.Parse()

	var tracePath string
	if *traceEnabled {
		tracePath = *traceFile
	}

	logger, err := trace.New(tracePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer logger.Close()

	logger.Log("nimbus starting")

	model := ui.NewModel(logger)
	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	logger.Log("nimbus exiting")
}
