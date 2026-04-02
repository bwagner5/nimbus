package resources

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

type LightsailLoadBalancer struct {
	id, name, state, region, dns string
	instances                    int
}

func (l LightsailLoadBalancer) ID() string       { return l.id }
func (l LightsailLoadBalancer) Name() string     { return l.name }
func (l LightsailLoadBalancer) Status() string   { return l.state }
func (l LightsailLoadBalancer) Region() string   { return l.region }
func (l LightsailLoadBalancer) Columns() []string { return []string{"NAME", "STATE", "DNS", "INSTANCES", "REGION"} }
func (l LightsailLoadBalancer) Values() []string {
	return []string{l.name, l.state, l.dns, fmt.Sprintf("%d", l.instances), l.region}
}

type LightsailLoadBalancerProvider struct {
	client *aws.Client
}

func NewLightsailLoadBalancerProvider(client *aws.Client) *LightsailLoadBalancerProvider {
	return &LightsailLoadBalancerProvider{client: client}
}

func (p *LightsailLoadBalancerProvider) Kind() string { return "lightsail/loadbalancers" }

func (p *LightsailLoadBalancerProvider) Headers() []string {
	return []string{"NAME", "STATE", "DNS", "INSTANCES", "REGION"}
}

func (p *LightsailLoadBalancerProvider) Fetch(ctx context.Context, region string) ([]Resource, error) {
	svc := lightsail.NewFromConfig(p.client.WithRegion(region).Config())
	var res []Resource
	var pageToken *string
	for {
		out, err := svc.GetLoadBalancers(ctx, &lightsail.GetLoadBalancersInput{PageToken: pageToken})
		if err != nil {
			return res, err
		}
		for _, lb := range out.LoadBalancers {
			res = append(res, LightsailLoadBalancer{
				id:        *lb.Arn,
				name:      *lb.Name,
				state:     string(lb.State),
				region:    region,
				dns:       *lb.DnsName,
				instances: len(lb.InstanceHealthSummary),
			})
		}
		if out.NextPageToken == nil {
			break
		}
		pageToken = out.NextPageToken
	}
	return res, nil
}
