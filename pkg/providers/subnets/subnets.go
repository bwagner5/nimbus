package subnets

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/providers/vpcs"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/bwagner5/nimbus/pkg/utils/tagutils"
	"github.com/samber/lo"
)

const (
	subnetTypePublic  = "PUBLIC"
	subnetTypePrivate = "PRIVATE"
)

// Watcher discovers subnets based on selectors
type Watcher struct {
	subnetAPI SDKSubnetsOps
}

// SDKSubnetsOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKSubnetsOps interface {
	ec2.DescribeSubnetsAPIClient
	CreateSubnet(context.Context, *ec2.CreateSubnetInput, ...func(*ec2.Options)) (*ec2.CreateSubnetOutput, error)
	DeleteSubnet(context.Context, *ec2.DeleteSubnetInput, ...func(*ec2.Options)) (*ec2.DeleteSubnetOutput, error)
	ModifySubnetAttribute(context.Context, *ec2.ModifySubnetAttributeInput, ...func(*ec2.Options)) (*ec2.ModifySubnetAttributeOutput, error)
}

// Selector is a struct that represents a subnet selector
type Selector struct {
	Tags  map[string]string
	ID    string
	VPCID string
}

// Subnet represent an AWS Subnet
// This is not the AWS SDK Subnet type, but a wrapper around it so that we can add additional data
type Subnet struct {
	ec2types.Subnet
}

// SubnetSpec is used to specify parameters for creating a subnet
type SubnetSpec struct {
	AZ     string
	CIDR   string
	Public bool
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse subnet selectors: %w", err)
	}
	subnetSelectors := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		subnetSelector := Selector{
			Tags: selector.Tags,
		}
		for k, v := range selector.KeyVals {
			switch k {
			case "id":
				subnetSelector.ID = v
			default:
				return nil, fmt.Errorf("invalid subnet selector key: %s", k)
			}
		}
		subnetSelectors = append(subnetSelectors, subnetSelector)
	}
	return subnetSelectors, nil
}

// NewWatcher creates a new Subnet Watcher
func NewWatcher(subnetAPI SDKSubnetsOps) Watcher {
	return Watcher{
		subnetAPI: subnetAPI,
	}
}

// Resolve returns a list of subnets that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]Subnet, error) {
	var subnets []Subnet
	for _, filters := range filterSets(selectors) {
		pager := ec2.NewDescribeSubnetsPaginator(w.subnetAPI, &ec2.DescribeSubnetsInput{
			Filters: filters,
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to describe subnets: %w", err)
			}

			subnets = append(subnets, lo.Map(page.Subnets, func(sdkSubnet ec2types.Subnet, _ int) Subnet {
				return Subnet{sdkSubnet}
			})...)
		}
	}
	return subnets, nil
}

func (w Watcher) Create(ctx context.Context, namespace, name string, vpc *vpcs.VPC, subnetSpecs []SubnetSpec) ([]Subnet, error) {
	var subnetOutputs []*ec2.CreateSubnetOutput
	// Create subnets
	for _, subnet := range subnetSpecs {
		subnetType := lo.Ternary(subnet.Public, subnetTypePublic, subnetTypePrivate)
		subnetOutput, err := w.subnetAPI.CreateSubnet(ctx, &ec2.CreateSubnetInput{
			VpcId:            vpc.VpcId,
			AvailabilityZone: &subnet.AZ,
			CidrBlock:        &subnet.CIDR,
			TagSpecifications: []types.TagSpecification{{
				ResourceType: types.ResourceTypeSubnet,
				Tags:         tagutils.EC2NamespacedTags(namespace, name),
			}},
		})
		if err != nil {
			return nil, err
		}
		if subnetType == subnetTypePublic {
			subnetOutput.Subnet.MapPublicIpOnLaunch = aws.Bool(true)
		}
		subnetOutputs = append(subnetOutputs, subnetOutput)
	}
	// Modify any subnet attributes that we can't set on creation
	for _, subnet := range subnetOutputs {
		subnetOpts, ok := lo.Find(subnetSpecs, func(subnetSpec SubnetSpec) bool { return subnetSpec.CIDR == *subnet.Subnet.CidrBlock })
		if !ok {
			return nil, fmt.Errorf("unable to find SubnetSpec for subnet %s - %s", *subnet.Subnet.AvailabilityZone, *subnet.Subnet.CidrBlock)
		}
		// Can only modify 1 subnet attribute at a time
		if subnetOpts.Public {
			if _, err := w.subnetAPI.ModifySubnetAttribute(ctx, &ec2.ModifySubnetAttributeInput{
				SubnetId:            subnet.Subnet.SubnetId,
				MapPublicIpOnLaunch: &types.AttributeBooleanValue{Value: aws.Bool(true)},
			}); err != nil {
				return nil, err
			}
		}
	}
	return lo.Map(subnetOutputs, func(out *ec2.CreateSubnetOutput, _ int) Subnet { return Subnet{Subnet: *out.Subnet} }), nil
}

func (w Watcher) Delete(ctx context.Context, subnetID string) error {
	_, err := w.subnetAPI.DeleteSubnet(ctx, &ec2.DeleteSubnetInput{
		SubnetId: &subnetID,
	})
	return err
}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
func filterSets(selectors []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	idFilter := ec2types.Filter{Name: aws.String("subnet-id")}
	for _, term := range selectors {
		switch {
		case term.ID != "":
			idFilter.Values = append(idFilter.Values, term.ID)
		case term.VPCID != "":
			filterResult = append(filterResult, []ec2types.Filter{{
				Name:   aws.String("vpc-id"),
				Values: []string{term.VPCID},
			}})
		default:
			var filters []ec2types.Filter
			for k, v := range term.Tags {
				if v == "*" || v == "" {
					filters = append(filters, ec2types.Filter{
						Name:   aws.String("tag-key"),
						Values: []string{k},
					})
				} else {
					filters = append(filters, ec2types.Filter{
						Name:   aws.String(fmt.Sprintf("tag:%s", k)),
						Values: []string{v},
					})
				}
			}
			filterResult = append(filterResult, filters)
		}
	}
	if len(idFilter.Values) > 0 {
		filterResult = append(filterResult, []ec2types.Filter{idFilter})
	}
	return filterResult
}
