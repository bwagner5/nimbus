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
	_ "embed"
	"fmt"
	"os"

	"dario.cat/mergo"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/bwagner5/nimbus/pkg/tui"
	"github.com/bwagner5/nimbus/pkg/vm"
	"github.com/samber/lo"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

const (
	OutputYAML        = "yaml"
	OutputJSON        = "json"
	OutputTableShort  = "short"
	OutputTableWide   = "wide"
	OutputInteractive = "interactive"
)

var (
	version = ""
)

type GlobalOptions struct {
	Namespace  string
	Verbose    bool
	Version    bool
	Output     string
	ConfigFile string
	Region     string
	Profile    string
}

type RootOptions struct {
	Attribution bool
}

var (
	globalOpts = GlobalOptions{}
	rootOpts   = RootOptions{}
	rootCmd    = &cobra.Command{
		Use:     "vm",
		Version: version,
		RunE: func(cmd *cobra.Command, args []string) error {
			return root(cmd.Context(), globalOpts)
		},
	}
)

//go:generate cp -r ../ATTRIBUTION.md ./
//go:embed ATTRIBUTION.md
var attribution string

func main() {
	rootCmd.Flags().BoolVar(&rootOpts.Attribution, "attribution", false, "show attributions")
	rootCmd.PersistentFlags().BoolVar(&globalOpts.Verbose, "verbose", false, "Verbose output")
	rootCmd.PersistentFlags().BoolVar(&globalOpts.Version, "version", false, "version")
	rootCmd.PersistentFlags().StringVarP(&globalOpts.Output, "output", "o", OutputTableShort,
		fmt.Sprintf("Output mode: %v", []string{OutputTableShort, OutputTableWide, OutputYAML, OutputJSON, OutputInteractive}))
	rootCmd.PersistentFlags().StringVarP(&globalOpts.ConfigFile, "file", "f", "", "YAML Config File")

	rootCmd.PersistentFlags().StringVarP(&globalOpts.Namespace, "namespace", "n", "", "Logical grouping of resources. All resources are tagged with the namespace.")
	rootCmd.PersistentFlags().StringVarP(&globalOpts.Region, "region", "r", "", "AWS Region")
	rootCmd.PersistentFlags().StringVarP(&globalOpts.Profile, "profile", "p", "", "AWS CLI Profile")

	rootCmd.AddCommand(&cobra.Command{Use: "completion", Hidden: true})
	cobra.EnableCommandSorting = false

	lo.Must0(rootCmd.Execute())
}

func root(ctx context.Context, globalOpts GlobalOptions) error {
	if rootOpts.Attribution {
		fmt.Println(attribution)
		return nil
	}
	awsCfg, err := AWSConfig(ctx, globalOpts)
	if err != nil {
		return err
	}

	vmClient := vm.New(awsCfg)

	if globalOpts.Output == OutputInteractive {
		return tui.Launch(ctx, vmClient, "get", globalOpts.Namespace, getOptions.Name, globalOpts.Verbose)
	}
	return nil
}

func ParseConfig[T any](globalOpts GlobalOptions, opts T) (T, error) {
	if globalOpts.ConfigFile == "" {
		return opts, nil
	}
	configBytes, err := os.ReadFile(globalOpts.ConfigFile)
	if err != nil {
		return opts, err
	}
	var parsedCreateOpts T
	if err := yaml.Unmarshal(configBytes, &parsedCreateOpts); err != nil {
		return opts, err
	}
	if err := mergo.Merge(&opts, parsedCreateOpts, mergo.WithOverride); err != nil {
		return opts, err
	}
	return opts, nil
}

func AWSConfig(ctx context.Context, globalOptions GlobalOptions) (*aws.Config, error) {
	var options []func(*config.LoadOptions) error
	if globalOptions.Region != "" {
		options = append(options, config.WithRegion(globalOptions.Region))
	}
	if globalOptions.Profile != "" {
		options = append(options, config.WithSharedConfigProfile(globalOptions.Profile))
	}
	cfg, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	return &cfg, nil
}
