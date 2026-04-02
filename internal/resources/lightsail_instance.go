package resources

import (
	"context"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

type LightsailInstance struct {
	id, name, state, region, ip, blueprint string
}

func (i LightsailInstance) ID() string     { return i.id }
func (i LightsailInstance) Name() string   { return i.name }
func (i LightsailInstance) Status() string { return i.state }
func (i LightsailInstance) Region() string { return i.region }
func (i LightsailInstance) Columns() []string {
	return []string{"NAME", "STATE", "IP", "BLUEPRINT", "REGION"}
}
func (i LightsailInstance) Values() []string {
	return []string{i.name, i.state, i.ip, i.blueprint, i.region}
}

type LightsailProvider struct {
	client *aws.Client
}

func NewLightsailProvider(client *aws.Client) *LightsailProvider {
	return &LightsailProvider{client: client}
}

func (p *LightsailProvider) Kind() string { return "lightsail/instances" }

func (p *LightsailProvider) Headers() []string {
	return []string{"NAME", "STATE", "IP", "BLUEPRINT", "REGION"}
}

func (p *LightsailProvider) Fetch(ctx context.Context, region string) ([]Resource, error) {
	svc := lightsail.NewFromConfig(p.client.WithRegion(region).Config())
	var res []Resource
	var pageToken *string
	for {
		out, err := svc.GetInstances(ctx, &lightsail.GetInstancesInput{PageToken: pageToken})
		if err != nil {
			return res, err
		}
		for _, inst := range out.Instances {
			ip := ""
			if inst.PublicIpAddress != nil {
				ip = *inst.PublicIpAddress
			}
			res = append(res, LightsailInstance{
				id:        *inst.Arn,
				name:      *inst.Name,
				state:     *inst.State.Name,
				region:    region,
				ip:        ip,
				blueprint: *inst.BlueprintId,
			})
		}
		if out.NextPageToken == nil {
			break
		}
		pageToken = out.NextPageToken
	}
	return res, nil
}
