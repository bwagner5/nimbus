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
	"log/slog"
	"os"

	"github.com/bwagner5/nimbus/pkg/logging"
	"github.com/bwagner5/nimbus/pkg/plans"
	"github.com/bwagner5/nimbus/pkg/pretty"
	"github.com/bwagner5/nimbus/pkg/providers/amis"
	"github.com/bwagner5/nimbus/pkg/providers/instancetypes"
	"github.com/bwagner5/nimbus/pkg/providers/securitygroups"
	"github.com/bwagner5/nimbus/pkg/providers/subnets"
	"github.com/bwagner5/nimbus/pkg/vm"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
)

type LaunchOptions struct {
	DryRun                bool
	Name                  string `table:"Name"`
	CapacityType          string `table:"Capacity Type"`
	InstanceTypeSelector  string `table:"Instance Type Selector"`
	SubnetSelector        string `table:"Subnet Selector"`
	AMISelector           string `table:"OS Image Selector"`
	IAMRole               string `table:"IAM Role"`
	SecurityGroupSelector string `table:"Security Group Selector"`
	UserData              string
}

var (
	launchOptions = LaunchOptions{}
	cmdLaunch     = &cobra.Command{
		Use:   "launch ",
		Short: "launch",
		Long:  `launch`,
		Args:  cobra.MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
				Level: lo.Ternary(globalOpts.Verbose, slog.LevelDebug, slog.LevelInfo),
			}))
			return launch(logging.ToContext(cmd.Context(), logger), launchOptions, globalOpts)
		},
	}
)

func init() {
	rootCmd.AddCommand(cmdLaunch)
	cmdLaunch.Flags().BoolVarP(&launchOptions.DryRun, "dry-run", "d", false, "Will NOT launch anything, only print the launch plan")
	cmdLaunch.Flags().StringVar(&launchOptions.Name, "name", "", "Name of the VM")
	cmdLaunch.Flags().StringVar(&launchOptions.CapacityType, "capacity-type", "", "Spot or On-Demand")
	cmdLaunch.Flags().StringVar(&launchOptions.InstanceTypeSelector, "instance-types", "", "Instance Type Criteria e.g. --instance-types 'vcpus:2-6,arch:arm64,local-storage:100GiB-'")
	cmdLaunch.Flags().StringVar(&launchOptions.IAMRole, "iam-role", "", "IAM Role")
	cmdLaunch.Flags().StringVar(&launchOptions.UserData, "user-data", "", "User Data or a file containing User Data. e.g --user-data file://userdata.sh")
	cmdLaunch.Flags().StringVar(&launchOptions.AMISelector, "amis", "", "AMI selector to dynamically find eligible OS Images. Selectors are AND'd together. e.g. --amis 'tag:Name=fancyOS,tag:Environment=dev' OR --amis 'id:ami-0123456'")
	cmdLaunch.Flags().StringVar(&launchOptions.SubnetSelector, "subnets", "", "Subnet selector to dynamically find eligible subnets. Selectors are AND'd together. e.g. --subnets 'tag:Name=public,tag:Environment=dev' OR --subnets 'id:subnet-0123456'")
	cmdLaunch.Flags().StringVar(&launchOptions.SecurityGroupSelector, "security-groups", "", "Security Group selector to dynamically find eligible security groups. Selectors are AND'd together. e.g. --security-groups 'tag:Name=public,tag:Environment=dev' OR --security-groups 'id:sg-0123456'")
}

func launch(ctx context.Context, launchOptions LaunchOptions, globalOpts GlobalOptions) error {
	awsCfg, err := AWSConfig(ctx, globalOpts)
	if err != nil {
		return err
	}

	subnetSelectors, err := subnets.ParseSelectors(launchOptions.SubnetSelector)
	if err != nil {
		return err
	}
	amiSelectors, err := amis.ParseSelectors(launchOptions.AMISelector)
	if err != nil {
		return err
	}
	securityGroupSelectors, err := securitygroups.ParseSelectors(launchOptions.SecurityGroupSelector)
	if err != nil {
		return err
	}
	instanceTypeSelectors, err := instancetypes.ParseSelectors(launchOptions.InstanceTypeSelector)
	if err != nil {
		return err
	}
	launchPlanInput := plans.LaunchPlan{
		Metadata: plans.LaunchMetadata{
			Namespace: globalOpts.Namespace,
			Name:      launchOptions.Name,
		},
		Spec: plans.LaunchSpec{
			CapacityType:           launchOptions.CapacityType,
			IAMRole:                launchOptions.IAMRole,
			InstanceTypeSelectors:  instanceTypeSelectors,
			SubnetSelectors:        subnetSelectors,
			AMISelectors:           amiSelectors,
			SecurityGroupSelectors: securityGroupSelectors,
			UserData:               launchOptions.UserData,
		},
	}

	launchPlan, err := vm.New(awsCfg).Launch(ctx, launchOptions.DryRun, launchPlanInput)
	if err != nil {
		if globalOpts.Verbose {
			fmt.Println(pretty.EncodeYAML(launchPlan))
		}
		return err
	}

	if globalOpts.Verbose {
		fmt.Println(pretty.EncodeYAML(launchPlan))
	}

	fmt.Printf("Launched %s/%s\n", globalOpts.Namespace, launchOptions.Name)

	return nil
}
