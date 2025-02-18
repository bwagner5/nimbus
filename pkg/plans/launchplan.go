package plans

import (
	"github.com/bwagner5/nimbus/pkg/providers/amis"
	"github.com/bwagner5/nimbus/pkg/providers/igws"
	"github.com/bwagner5/nimbus/pkg/providers/instances"
	"github.com/bwagner5/nimbus/pkg/providers/instancetypes"
	"github.com/bwagner5/nimbus/pkg/providers/launchtemplates"
	"github.com/bwagner5/nimbus/pkg/providers/routetables"
	"github.com/bwagner5/nimbus/pkg/providers/securitygroups"
	"github.com/bwagner5/nimbus/pkg/providers/subnets"
	"github.com/bwagner5/nimbus/pkg/providers/vpcs"
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
	VPC             vpcs.VPC
	Subnets         []subnets.Subnet
	RouteTables     []routetables.RouteTable
	InternetGateway igws.InternetGateway
	SecurityGroups  []securitygroups.SecurityGroup
	AMIs            []amis.AMI
	InstanceTypes   []instancetypes.InstanceType
	Instances       []instances.Instance
	LaunchTemplate  launchtemplates.LaunchTemplate
}
