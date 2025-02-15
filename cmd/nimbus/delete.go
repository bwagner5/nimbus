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
	"context"
	"fmt"
	"strings"

	"github.com/bwagner5/nimbus/pkg/vm"
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
			return delete(cmd.Context(), deleteOptions, globalOpts)
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
	resourcesDeleted, err := vm.New(awsCfg).Delete(ctx, globalOpts.Namespace, deleteOptions.Name)
	if err != nil {
		return err
	}
	if len(resourcesDeleted) == 0 {
		fmt.Println("Nothing to delete")
	} else {
		fmt.Printf("Deleted %s\n", strings.Join(resourcesDeleted, ", "))
	}
	return nil
}
