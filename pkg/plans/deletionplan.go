package plans

import (
	"github.com/bwagner5/nimbus/pkg/providers/instances"
	"github.com/bwagner5/nimbus/pkg/providers/launchtemplates"
	"github.com/bwagner5/nimbus/pkg/providers/securitygroups"
	"github.com/bwagner5/nimbus/pkg/providers/subnets"
	"github.com/bwagner5/nimbus/pkg/providers/vpcs"
)

type DeletionPlan struct {
	Metadata DeletionMetadata
	Spec     DeletionSpec
	Status   DeletionStatus
}

type DeletionMetadata struct {
	Namespace string
	Name      string
}

type DeletionSpec struct {
	VPCs            []vpcs.VPC
	Subnets         []subnets.Subnet
	SecurityGroups  []securitygroups.SecurityGroup
	LaunchTemplates []launchtemplates.LaunchTemplate
	Instances       []instances.Instance
}

type DeletionStatus struct {
	// Deletion status maps a resource-id to a bool representing that the resource has been deleted.
	VPCs            map[string]bool
	Subnets         map[string]bool
	SecurityGroups  map[string]bool
	Instances       map[string]bool
	LaunchTemplates map[string]bool
}
