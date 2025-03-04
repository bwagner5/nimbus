package tui

import (
	"context"
	"fmt"

	"github.com/bwagner5/nimbus/pkg/logging"
	"github.com/bwagner5/nimbus/pkg/tui/list"
	"github.com/bwagner5/nimbus/pkg/vm"
	tea "github.com/charmbracelet/bubbletea"
)

func Launch(ctx context.Context, vmClient vm.AWSVM, namespace, name string, verbose bool) error {
	// can't log to the terminal, so log to a file
	if verbose {
		f, err := tea.LogToFile("debug.log", "debug")
		if err != nil {
			return err
		}
		defer f.Close()
		ctx = logging.ToContext(ctx, logging.DefaultFileLogger(verbose, f))
	} else {
		ctx = logging.ToContext(ctx, logging.NoOpLogger())
	}
	p := tea.NewProgram(list.NewList(ctx, vmClient, namespace, name), tea.WithContext(ctx), tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Alas, there's been an error: %v", err)
		return err
	}
	return nil
}
