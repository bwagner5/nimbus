/*
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/bwagner5/nimbus/pkg/logging"
	"github.com/bwagner5/nimbus/pkg/pretty"
	"github.com/bwagner5/nimbus/pkg/vm"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
)

type DeleteOptions struct {
	Name  string
	All   bool
	Force bool
}

type DeleteUI struct {
	Name      string
	Namespace string
}

var (
	deleteOptions = DeleteOptions{}
	cmdDelete     = &cobra.Command{
		Use:   "delete ",
		Short: "delete",
		Long:  `delete`,
		Args:  cobra.MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: lo.Ternary(globalOpts.Verbose, slog.LevelDebug, slog.LevelInfo),
			}))
			return delete(logging.ToContext(cmd.Context(), logger), deleteOptions, globalOpts)
		},
	}
)

func init() {
	rootCmd.AddCommand(cmdDelete)
	cmdDelete.Flags().StringVar(&deleteOptions.Name, "name", "", "Name of the VM")
	cmdDelete.Flags().BoolVar(&deleteOptions.All, "all", false, "Delete everything in the namespace")
	cmdDelete.Flags().BoolVar(&deleteOptions.Force, "force", false, "Don't ask, just do it!")
}

func delete(ctx context.Context, deleteOptions DeleteOptions, globalOpts GlobalOptions) error {
	awsCfg, err := AWSConfig(ctx, globalOpts)
	if err != nil {
		return err
	}

	vmClient := vm.New(awsCfg)

	deletionPlan, err := vmClient.DeletionPlan(ctx, globalOpts.Namespace, deleteOptions.Name)
	if err != nil {
		return err
	}

	if !deleteOptions.Force {
		fmt.Println(pretty.EncodeYAML(deletionPlan))
		fmt.Printf("Proceed with deletion? ")
		reader := bufio.NewReader(os.Stdin)
		userInput, err := reader.ReadString('\n')
		if err != nil {
			return err
		}
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(userInput)), "y") {
			fmt.Println("Aborting deletion...")
			return nil
		}
	}

	deletionPlan, err = vmClient.Delete(ctx, deletionPlan)
	if err != nil {
		return err
	}

	return nil
}
