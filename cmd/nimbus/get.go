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
	"time"

	"github.com/bwagner5/nimbus/pkg/instances"
	"github.com/bwagner5/nimbus/pkg/pretty"
	"github.com/bwagner5/nimbus/pkg/utils/tagutils"
	"github.com/bwagner5/nimbus/pkg/vm"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
)

type GetOptions struct {
	Name string `table:"Name"`
}

type GetUI struct {
	Name         string `table:"Name"`
	Status       string `table:"Status"`
	IAMRole      string `table:"Role"`
	Age          string `table:"Age"`
	Arch         string `table:"Arch"`
	InstanceType string `table:"Instance-Type"`
	Zone         string `table:"Zone"`
	CapacityType string `table:"Capacity-Type"`
	InstanceID   string `table:"ID"`
}

var (
	getOptions = GetOptions{}
	cmdGet     = &cobra.Command{
		Use:   "get ",
		Short: "get",
		Long:  `get`,
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return get(cmd.Context(), getOptions, globalOpts)
		},
	}
)

func init() {
	rootCmd.AddCommand(cmdLaunch)
	cmdLaunch.Flags().StringVar(&launchOptions.Name, "name", "", "Name of the VM")
}

func get(ctx context.Context, getOptions GetOptions, globalOpts GlobalOptions) error {
	awsCfg, err := AWSConfig(ctx, globalOpts)
	if err != nil {
		return err
	}

	instanceList, err := vm.New(awsCfg).List(ctx, globalOpts.Namespace, getOptions.Name)
	if err != nil {
		return err
	}

	instancesUI := lo.Map(instanceList, func(instance instances.Instance, _ int) GetUI {
		return instanceToGetUI(instance)
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

func instanceToGetUI(instance instances.Instance) GetUI {
	return GetUI{
		Name:         tagutils.EC2TagsToMap(instance.Tags)["Name"],
		Status:       string(instance.State.Name),
		IAMRole:      lo.FromPtr(instance.IamInstanceProfile.Id),
		Age:          time.Since(lo.FromPtr(instance.LaunchTime)).String(),
		Arch:         string(instance.Architecture),
		InstanceType: string(instance.InstanceType),
		Zone:         lo.FromPtr(instance.Placement.AvailabilityZone),
		CapacityType: string(instance.InstanceLifecycle),
		InstanceID:   lo.FromPtr(instance.InstanceId),
	}
}
