package vm

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/bwagner5/vm/pkg/amis"
	"github.com/bwagner5/vm/pkg/securitygroups"
	"github.com/bwagner5/vm/pkg/subnets"
)

type VMI interface {
	Launch(context.Context, LaunchPlan)
	Describe(context.Context)
	Terminate(context.Context)
}

type LaunchPlan struct {
	Metadata LaunchMetadata
	Spec     LaunchSpec
	Status   LaunchStatus
}

type LaunchMetadata struct {
	Namespace string
	Name      string
}

type LaunchSpec struct {
	CapacityType            string
	InstanceType            string
	SubnetSelectors         []subnets.Selector
	SecurityGroupsSelectors []securitygroups.Selector
	AMISelectors            []amis.Selector
	IAMRole                 string
	UserData                string
}

type LaunchStatus struct {
	Subnets        []subnets.Subnet
	SecurityGroups []securitygroups.SecurityGroup
	AMIs           []amis.AMI
}

type AWSVM struct {
	awsCfg *aws.Config
}

func New(awsCfg *aws.Config) AWSVM {
	return AWSVM{
		awsCfg: awsCfg,
	}
}

func (v AWSVM) Launch(ctx context.Context, dryRun bool, launchPlan LaunchPlan) (LaunchPlan, error) {
	return launchPlan, nil
}
