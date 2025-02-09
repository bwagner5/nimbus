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

	"github.com/bwagner5/vm/pkg/vm"
	"github.com/spf13/cobra"
)

type LaunchOptions struct {
	DryRun         bool
	Name           string   `table:"Name"`
	CapacityType   string   `table:"Capacity Type"`
	InstanceType   string   `table:"Instance Type"`
	SubnetSelector []string `table:"Subnet Selector"`
	AMISelector    []string `table:"OS Image Selector"`
	IAMRole        string   `table:"IAM Role"`
	UserData       string
}

var (
	launchOptions = LaunchOptions{}
	cmdLaunch     = &cobra.Command{
		Use:   "launch ",
		Short: "launch",
		Long:  `launch`,
		Args:  cobra.MinimumNArgs(0),
		RunE: func(cmd *cobra.Command, _ []string) error {
			awsCfg, err := AWSConfig(cmd.Context(), globalOpts)
			if err != nil {
				return err
			}

			launchPlan, err := vm.New(awsCfg).Launch(cmd.Context(), launchOptions)
			if err != nil {
				return err
			}
			return nil
		},
	}
)

func init() {
	rootCmd.AddCommand(cmdLaunch)
	cmdLaunch.Flags().BoolVarP(&launchOptions.DryRun, "dry-run", "d", false, "Will NOT launch anything, only print the launch plan")
	cmdLaunch.Flags().StringVar(&launchOptions.Name, "name", "", "Name of the VM")
	cmdLaunch.Flags().StringVar(&launchOptions.CapacityType, "capacity-type", "", "Spot or On-Demand")
	cmdLaunch.Flags().StringVar(&launchOptions.InstanceType, "instance-type", "", "Instance Type")
	cmdLaunch.Flags().StringVar(&launchOptions.IAMRole, "iam-role", "", "IAM Role")
	cmdLaunch.Flags().StringVar(&launchOptions.UserData, "user-data", "", "User Data or a file containing User Data. e.g --user-data file://userdata.sh")
	cmdLaunch.Flags().StringSliceVar(&launchOptions.AMISelector, "amis", []string{}, "AMI selector to dynamically find eligible OS Images. Selectors are AND'd together. e.g. --amis 'tag:Name=fancyOS,tag:Environment=dev' OR --amis 'id:ami-0123456'")
	cmdLaunch.Flags().StringSliceVar(&launchOptions.SubnetSelector, "subnets", []string{}, "Subnet selector to dynamically find eligible subnets. Selectors are AND'd together. e.g. --subnets 'tag:Name=public,tag:Environment=dev' OR --subnets 'id:subnet-0123456'")
}

func launch(ctx context.Context, launchOptions LaunchOptions) error {

	return nil
}
