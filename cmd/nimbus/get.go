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

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/logging"
	"github.com/bwagner5/nimbus/pkg/pretty"
	"github.com/bwagner5/nimbus/pkg/providers/instances"
	"github.com/bwagner5/nimbus/pkg/tui"
	"github.com/bwagner5/nimbus/pkg/vm"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
)

type GetOptions struct {
	Name string `table:"Name"`
}

var (
	getOptions = GetOptions{}
	cmdGet     = &cobra.Command{
		Use:   "get ",
		Short: "get",
		Long:  `get`,
		Args:  cobra.MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := logging.ToContext(cmd.Context(), logging.DefaultLogger(globalOpts.Verbose))
			return get(ctx, getOptions, globalOpts)
		},
	}
)

func init() {
	rootCmd.AddCommand(cmdGet)
	cmdGet.Flags().StringVar(&getOptions.Name, "name", "", "Name of the VM")
}

func get(ctx context.Context, getOptions GetOptions, globalOpts GlobalOptions) error {
	awsCfg, err := AWSConfig(ctx, globalOpts)
	if err != nil {
		return err
	}

	vmClient := vm.New(awsCfg)

	if globalOpts.Output == OutputInteractive {
		return tui.Launch(ctx, vmClient, "get", globalOpts.Namespace, getOptions.Name, globalOpts.Verbose)
	}

	instanceList, err := vmClient.List(ctx, globalOpts.Namespace, getOptions.Name)
	if err != nil {
		return err
	}

	instancesUI := lo.FilterMap(instanceList, func(instance instances.Instance, _ int) (instances.PrettyInstance, bool) {
		if instance.State.Name == ec2types.InstanceStateNameTerminated {
			return instances.PrettyInstance{}, false
		}
		return instance.Prettify(), true
	})

	switch globalOpts.Output {
	case OutputJSON:
		fmt.Println(pretty.EncodeJSON(instancesUI))
	case OutputYAML:
		fmt.Println(pretty.EncodeYAML(instancesUI))
	case OutputTableShort:
		fmt.Println(pretty.Table(instancesUI, false))
	case OutputTableWide:
		fmt.Println(pretty.Table(instancesUI, true))
	}
	return nil
}
