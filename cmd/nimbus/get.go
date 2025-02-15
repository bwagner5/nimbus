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
	"time"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/pretty"
	"github.com/bwagner5/nimbus/pkg/providers/instances"
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
		Args:  cobra.MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return get(cmd.Context(), getOptions, globalOpts)
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

	instanceList, err := vm.New(awsCfg).List(ctx, globalOpts.Namespace, getOptions.Name)
	if err != nil {
		return err
	}

	instancesUI := lo.FilterMap(instanceList, func(instance instances.Instance, _ int) (GetUI, bool) {
		if instance.State.Name == ec2types.InstanceStateNameTerminated {
			return GetUI{}, false
		}
		return instanceToGetUI(instance), true
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

// TODO: may want to add a type to instance that abstracts some of this basic info so that the TUI is similar
// It would be nice to not need two types between the sdk, cli, and tui
func instanceToGetUI(instance instances.Instance) GetUI {
	instanceProfileID := ""
	if instance.IamInstanceProfile != nil {
		instanceProfileID = strings.Split(*instance.IamInstanceProfile.Arn, "/")[1]
	}
	return GetUI{
		Name:         tagutils.EC2TagsToMap(instance.Tags)["Name"],
		Status:       string(instance.State.Name),
		IAMRole:      instanceProfileID,
		Age:          time.Since(lo.FromPtr(instance.LaunchTime)).Truncate(time.Second).String(),
		Arch:         string(instance.Architecture),
		InstanceType: string(instance.InstanceType),
		Zone:         lo.FromPtr(instance.Placement.AvailabilityZone),
		CapacityType: string(instance.InstanceLifecycle),
		InstanceID:   lo.FromPtr(instance.InstanceId),
	}
}
