package vpcs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/bwagner5/nimbus/pkg/utils/tagutils"
	"github.com/bwagner5/vpcctl/pkg/vpc"
	"github.com/samber/lo"
)

// Watcher discovers vpcs based on selectors
type Watcher struct {
	vpcAPI SDKVPCsOps
	vpcctl vpc.Client
}

// SDKVPCsOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKVPCsOps interface {
	ec2.DescribeVpcsAPIClient
	CreateVpc(context.Context, *ec2.CreateVpcInput, ...func(*ec2.Options)) (*ec2.CreateVpcOutput, error)
	DeleteVpc(context.Context, *ec2.DeleteVpcInput, ...func(*ec2.Options)) (*ec2.DeleteVpcOutput, error)
	DescribeAvailabilityZones(context.Context, *ec2.DescribeAvailabilityZonesInput, ...func(*ec2.Options)) (*ec2.DescribeAvailabilityZonesOutput, error)
}

// Selector is a struct that represents a vpc selector
type Selector struct {
	Tags map[string]string
	ID   string
}

// VPC represent an AWS VPC
// This is not the AWS SDK VPC type, but a wrapper around it so that we can add additional data
type VPC struct {
	ec2types.Vpc
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse vpc selectors: %w", err)
	}
	vpcSelectors := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		vpcSelector := Selector{
			Tags: selector.Tags,
		}
		for k, v := range selector.KeyVals {
			switch k {
			case "id":
				vpcSelector.ID = v
			default:
				return nil, fmt.Errorf("invalid vpc selector key: %s", k)
			}
		}
		vpcSelectors = append(vpcSelectors, vpcSelector)
	}
	return vpcSelectors, nil
}

// NewWatcher creates a new VPC Watcher
func NewWatcher(awsCfg aws.Config, vpcAPI SDKVPCsOps) Watcher {
	return Watcher{
		vpcAPI: vpcAPI,
		vpcctl: *vpc.New(awsCfg),
	}
}

// Resolve returns a list of vpcs that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]VPC, error) {
	var vpcs []VPC
	for i, filters := range filterSets(selectors) {
		pager := ec2.NewDescribeVpcsPaginator(w.vpcAPI, &ec2.DescribeVpcsInput{
			Filters: filters,
			VpcIds:  lo.Ternary(selectors[i].ID == "", nil, []string{selectors[i].ID}),
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to describe vpcs: %w", err)
			}

			vpcs = append(vpcs, lo.Map(page.Vpcs, func(sdkVPC ec2types.Vpc, _ int) VPC {
				return VPC{sdkVPC}
			})...)
		}
	}
	return vpcs, nil
}

func (w Watcher) Create(ctx context.Context, namespace string, name string, cidr string) (*VPC, error) {
	vpcOut, err := w.vpcAPI.CreateVpc(ctx, &ec2.CreateVpcInput{
		CidrBlock: aws.String(cidr),
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeVpc,
				Tags:         tagutils.EC2NamespacedTags(namespace, name),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	return &VPC{Vpc: *vpcOut.Vpc}, nil
}

func (w Watcher) Delete(ctx context.Context, vpcID string) error {
	_, err := w.vpcAPI.DeleteVpc(ctx, &ec2.DeleteVpcInput{
		VpcId: &vpcID,
	})
	return err
}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
// Each filter is executed as a separate list call.
// Terms within a Selector are AND'd and between Selectors are OR'd
func filterSets(selectorList []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	for _, term := range selectorList {
		filters := []ec2types.Filter{}
		filters = append(filters, selectors.TagsToEC2Filters(term.Tags)...)
		filterResult = append(filterResult, filters)
	}
	return filterResult
}
