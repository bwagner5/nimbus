package launchplan

import (
	"github.com/bwagner5/vm/pkg/amis"
	"github.com/bwagner5/vm/pkg/securitygroups"
	"github.com/bwagner5/vm/pkg/subnets"
)

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
	CapacityType           string
	InstanceType           string
	SubnetSelectors        []subnets.Selector
	SecurityGroupSelectors []securitygroups.Selector
	AMISelectors           []amis.Selector
	IAMRole                string
	UserData               string
}

type LaunchStatus struct {
	Subnets        []subnets.Subnet
	SecurityGroups []securitygroups.SecurityGroup
	AMIs           []amis.AMI
}
