package launchplan

import (
	"github.com/bwagner5/nimbus/pkg/providers/amis"
	"github.com/bwagner5/nimbus/pkg/providers/instances"
	"github.com/bwagner5/nimbus/pkg/providers/instancetypes"
	"github.com/bwagner5/nimbus/pkg/providers/launchtemplates"
	"github.com/bwagner5/nimbus/pkg/providers/securitygroups"
	"github.com/bwagner5/nimbus/pkg/providers/subnets"
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
	InstanceTypeSelectors  []instancetypes.Selector
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
	InstanceTypes  []instancetypes.InstanceType
	Instances      []instances.Instance
	LaunchTemplate launchtemplates.LaunchTemplate
}
