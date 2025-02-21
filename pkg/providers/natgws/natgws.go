package natgws

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/providers/subnets"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/bwagner5/nimbus/pkg/utils/tagutils"
	"github.com/samber/lo"
)

// Watcher discovers NAT Gateways based on selectors
type Watcher struct {
	ec2API SDKIGWOps
}

// SDKIGWOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKIGWOps interface {
	ec2.DescribeNatGatewaysAPIClient
	CreateNatGateway(context.Context, *ec2.CreateNatGatewayInput, ...func(*ec2.Options)) (*ec2.CreateNatGatewayOutput, error)
	AllocateAddress(context.Context, *ec2.AllocateAddressInput, ...func(*ec2.Options)) (*ec2.AllocateAddressOutput, error)
}

// Selector is a struct that represents a NAT Gateway selector
type Selector struct {
	Tags  map[string]string
	ID    string
	VPCID string
}

// NATGateway represent an AWS NAT Gateway
// This is not the AWS SDK NatGateway type, but a wrapper around it so that we can add additional data
type NATGateway struct {
	ec2types.NatGateway
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse NAT Gateway selectors: %w", err)
	}
	internetGatewaySelectors := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		internetGatewaySelector := Selector{
			Tags: selector.Tags,
		}
		for k, v := range selector.KeyVals {
			switch k {
			case "id":
				internetGatewaySelector.ID = v
			default:
				return nil, fmt.Errorf("invalid NAT Gateway selector key: %s", k)
			}
		}
		internetGatewaySelectors = append(internetGatewaySelectors, internetGatewaySelector)
	}
	return internetGatewaySelectors, nil
}

// NewWatcher creates a new InternetGateway Watcher
func NewWatcher(ec2API SDKIGWOps) Watcher {
	return Watcher{
		ec2API: ec2API,
	}
}

// Resolve returns a list of NAT Gateways that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]NATGateway, error) {
	var natgws []NATGateway
	for _, filters := range filterSets(selectors) {
		pager := ec2.NewDescribeNatGatewaysPaginator(w.ec2API, &ec2.DescribeNatGatewaysInput{
			Filter: filters,
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to describe Internet Gateways: %w", err)
			}

			natgws = append(natgws, lo.Map(page.NatGateways, func(sdkNATGateway ec2types.NatGateway, _ int) NATGateway {
				return NATGateway{sdkNATGateway}
			})...)
		}
	}
	return natgws, nil
}

func (w Watcher) Create(ctx context.Context, namespace, name string, subnetsList []subnets.Subnet) (*NATGateway, error) {
	privateSubnets := lo.Filter(subnetsList, func(subnet subnets.Subnet, _ int) bool { return !*subnet.MapPublicIpOnLaunch })
	// do not create a NATGW if there are no private subnets
	if len(privateSubnets) == 0 {
		return nil, nil
	}
	publicSubnets := lo.Filter(subnetsList, func(subnet subnets.Subnet, _ int) bool { return *subnet.MapPublicIpOnLaunch })
	eipOut, err := w.ec2API.AllocateAddress(ctx, &ec2.AllocateAddressInput{
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeElasticIp,
				Tags:         tagutils.EC2NamespacedTags(namespace, name),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	natGWOut, err := w.ec2API.CreateNatGateway(ctx, &ec2.CreateNatGatewayInput{
		AllocationId: eipOut.AllocationId,
		SubnetId:     publicSubnets[0].SubnetId,
		TagSpecifications: []types.TagSpecification{
			{
				ResourceType: types.ResourceTypeNatgateway,
				Tags:         tagutils.EC2NamespacedTags(namespace, name),
			},
		},
	})
	if err != nil {
		return nil, err
	}
	waiter := ec2.NewNatGatewayAvailableWaiter(w.ec2API)
	if err := waiter.Wait(ctx, &ec2.DescribeNatGatewaysInput{NatGatewayIds: []string{*natGWOut.NatGateway.NatGatewayId}}, 5*time.Minute); err != nil {
		return &NATGateway{*natGWOut.NatGateway}, err
	}
	return &NATGateway{*natGWOut.NatGateway}, nil
}

// filterSets converts a slice of selectors into a slice of filters for use with the AWS SDK
// Each filter is executed as a separate list call.
// Terms within a Selector are AND'd and between Selectors are OR'd
func filterSets(selectorList []Selector) [][]ec2types.Filter {
	var filterResult [][]ec2types.Filter
	for _, term := range selectorList {
		filters := []ec2types.Filter{}
		if term.ID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("nat-gateway-id"),
				Values: []string{term.ID},
			})
		}
		if term.VPCID != "" {
			filters = append(filters, ec2types.Filter{
				Name:   aws.String("vpc-id"),
				Values: []string{term.VPCID},
			})
		}
		filters = append(filters, selectors.TagsToEC2Filters(term.Tags)...)
		filterResult = append(filterResult, filters)
	}
	return filterResult
}
