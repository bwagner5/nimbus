package routetables

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/bwagner5/nimbus/pkg/providers/igws"
	"github.com/bwagner5/nimbus/pkg/providers/natgws"
	"github.com/bwagner5/nimbus/pkg/providers/subnets"
	"github.com/bwagner5/nimbus/pkg/selectors"
	"github.com/bwagner5/nimbus/pkg/utils/tagutils"
	"github.com/samber/lo"
)

// Watcher discovers route tables based on selectors
type Watcher struct {
	routeTableAPI SDKRouteTablesOps
}

// SDKRouteTablesOps is an interface that combines the necessary EC2 SDK client interfaces
// AWS SDK for Go v2 does not provide a single interface that combines all the necessary methods
type SDKRouteTablesOps interface {
	ec2.DescribeRouteTablesAPIClient
	CreateRouteTable(context.Context, *ec2.CreateRouteTableInput, ...func(*ec2.Options)) (*ec2.CreateRouteTableOutput, error)
	DeleteRouteTable(context.Context, *ec2.DeleteRouteTableInput, ...func(*ec2.Options)) (*ec2.DeleteRouteTableOutput, error)
	AssociateRouteTable(context.Context, *ec2.AssociateRouteTableInput, ...func(*ec2.Options)) (*ec2.AssociateRouteTableOutput, error)
	DisassociateRouteTable(context.Context, *ec2.DisassociateRouteTableInput, ...func(*ec2.Options)) (*ec2.DisassociateRouteTableOutput, error)
	CreateRoute(context.Context, *ec2.CreateRouteInput, ...func(*ec2.Options)) (*ec2.CreateRouteOutput, error)
	DeleteRoute(context.Context, *ec2.DeleteRouteInput, ...func(*ec2.Options)) (*ec2.DeleteRouteOutput, error)
}

// Selector is a struct that represents a routeTable selector
type Selector struct {
	Tags  map[string]string
	ID    string
	VPCID string
}

// RouteTable represent an AWS RouteTable
// This is not the AWS SDK RouteTable type, but a wrapper around it so that we can add additional data
type RouteTable struct {
	ec2types.RouteTable
}

// ParseSelectors parses a string of selectors into a slice of Selector structs
func ParseSelectors(selectorStr string) ([]Selector, error) {
	selectors, err := selectors.ParseSelectorsTokens(selectorStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse routeTable selectors: %w", err)
	}
	routeTableSelectors := make([]Selector, 0, len(selectors))
	for _, selector := range selectors {
		routeTableSelector := Selector{
			Tags: selector.Tags,
		}
		for k, v := range selector.KeyVals {
			switch k {
			case "id":
				routeTableSelector.ID = v
			default:
				return nil, fmt.Errorf("invalid routeTable selector key: %s", k)
			}
		}
		routeTableSelectors = append(routeTableSelectors, routeTableSelector)
	}
	return routeTableSelectors, nil
}

// NewWatcher creates a new RouteTable Watcher
func NewWatcher(routeTableAPI SDKRouteTablesOps) Watcher {
	return Watcher{
		routeTableAPI: routeTableAPI,
	}
}

// Resolve returns a list of route tables that match the provided selectors
// Multiple calls to EC2 may be sent to resolve the selectors
func (w Watcher) Resolve(ctx context.Context, selectors []Selector) ([]RouteTable, error) {
	var routeTables []RouteTable
	for _, filters := range filterSets(selectors) {
		pager := ec2.NewDescribeRouteTablesPaginator(w.routeTableAPI, &ec2.DescribeRouteTablesInput{
			Filters: filters,
		})
		for pager.HasMorePages() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to describe route tables: %w", err)
			}

			routeTables = append(routeTables, lo.Map(page.RouteTables, func(sdkRouteTable ec2types.RouteTable, _ int) RouteTable {
				return RouteTable{sdkRouteTable}
			})...)
		}
	}
	return routeTables, nil
}

// Create creates a public and/or a private subnet based on the subnets, Internet Gateway, and NAT Gateway passed in.
// If subnetsList contains a subnet with MapPublicIpOnLaunch set to true, then Create will create 1 public route table
// If subnetsList does NOT contain a subnet with MapPublicIpOnLaunch set to true, then Create will create 1 private route table
// At most, 2 route tables will be created if subnetsList contains a subnet with MapPublicIpOnLaunch set to true and another set to false.
//
// Public Route Table is the first return and Private Route Table is the second return.
func (w Watcher) Create(ctx context.Context, namespace, name string, subnetsList []subnets.Subnet, igw *igws.InternetGateway, natgw *natgws.NATGateway) (*RouteTable, *RouteTable, error) {
	privateSubnets := lo.Filter(subnetsList, func(subnet subnets.Subnet, _ int) bool { return !*subnet.MapPublicIpOnLaunch })
	publicSubnets := lo.Filter(subnetsList, func(subnet subnets.Subnet, _ int) bool { return *subnet.MapPublicIpOnLaunch })
	if len(subnetsList) == 0 {
		return nil, nil, fmt.Errorf("no subnets received")
	}
	// PUBLIC SUBNET RESOURCES
	var publicRouteTable *RouteTable
	publicRawTags := tagutils.NamespacedTags(namespace, name)
	publicRawTags["Name"] = fmt.Sprintf("%s-PUBLIC", publicRawTags["Name"])
	publicTags := tagutils.MapToEC2Tags(publicRawTags)
	var publicRouteTableOut *ec2.CreateRouteTableOutput
	for i, publicSubnet := range publicSubnets {
		if i == 0 {
			var err error
			publicRouteTableOut, err = w.routeTableAPI.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{
				VpcId: publicSubnet.VpcId,
				TagSpecifications: []types.TagSpecification{
					{
						ResourceType: types.ResourceTypeRouteTable,
						Tags:         publicTags,
					},
				},
			})
			if err != nil {
				return nil, nil, err
			}
			publicRouteTable = &RouteTable{*publicRouteTableOut.RouteTable}
			if igw != nil {
				if _, err := w.routeTableAPI.CreateRoute(ctx, &ec2.CreateRouteInput{
					RouteTableId:         publicRouteTable.RouteTableId,
					DestinationCidrBlock: aws.String("0.0.0.0/0"),
					GatewayId:            igw.InternetGatewayId,
				}); err != nil {
					return nil, nil, err
				}
			}
		}
		if _, err := w.routeTableAPI.AssociateRouteTable(ctx, &ec2.AssociateRouteTableInput{
			RouteTableId: publicRouteTableOut.RouteTable.RouteTableId,
			SubnetId:     publicSubnet.SubnetId,
		}); err != nil {
			return nil, nil, err
		}
	}

	// PRIVATE SUBNET RESOURCES
	var privateRouteTable *RouteTable
	privateRawTags := tagutils.NamespacedTags(namespace, name)
	privateRawTags["Name"] = fmt.Sprintf("%s-PRIVATE", privateRawTags["Name"])
	privateTags := tagutils.MapToEC2Tags(privateRawTags)
	var privateRouteTableOut *ec2.CreateRouteTableOutput
	for i, privateSubnet := range privateSubnets {
		if i == 0 {
			var err error
			privateRouteTableOut, err = w.routeTableAPI.CreateRouteTable(ctx, &ec2.CreateRouteTableInput{
				VpcId: privateSubnet.VpcId,
				TagSpecifications: []types.TagSpecification{
					{
						ResourceType: types.ResourceTypeRouteTable,
						Tags:         privateTags,
					},
				},
			})
			if err != nil {
				return nil, nil, err
			}
			privateRouteTable = &RouteTable{*privateRouteTableOut.RouteTable}
			if natgw != nil {
				if _, err := w.routeTableAPI.CreateRoute(ctx, &ec2.CreateRouteInput{
					RouteTableId:         privateRouteTable.RouteTableId,
					DestinationCidrBlock: aws.String("0.0.0.0/0"),
					GatewayId:            natgw.NatGatewayId,
				}); err != nil {
					return nil, nil, err
				}
			}
		}
		if _, err := w.routeTableAPI.AssociateRouteTable(ctx, &ec2.AssociateRouteTableInput{
			RouteTableId: privateRouteTableOut.RouteTable.RouteTableId,
			SubnetId:     privateSubnet.SubnetId,
		}); err != nil {
			return nil, nil, err
		}
	}
	return publicRouteTable, privateRouteTable, nil
}

func (w Watcher) Delete(ctx context.Context, routeTable RouteTable) error {
	for _, route := range routeTable.Routes {
		if route.GatewayId != nil && strings.HasPrefix(*route.GatewayId, "igw-") {
			if _, err := w.routeTableAPI.DeleteRoute(ctx, &ec2.DeleteRouteInput{
				RouteTableId:         routeTable.RouteTableId,
				DestinationCidrBlock: route.DestinationCidrBlock,
			}); err != nil {
				return err
			}
		}
	}
	for _, association := range routeTable.Associations {
		if _, err := w.routeTableAPI.DisassociateRouteTable(ctx, &ec2.DisassociateRouteTableInput{AssociationId: association.RouteTableAssociationId}); err != nil {
			return err
		}
	}
	if _, err := w.routeTableAPI.DeleteRouteTable(ctx, &ec2.DeleteRouteTableInput{RouteTableId: routeTable.RouteTableId}); err != nil {
		return err
	}
	return nil
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
				Name:   aws.String("route-table-id"),
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
