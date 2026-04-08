package resources

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	"github.com/wagnerbm/nimbusv2/internal/aws"
)

type LightsailInstance struct {
	id, name, state, region, ip, blueprint string
	createdAt                              time.Time
}

func (i LightsailInstance) ID() string     { return i.id }
func (i LightsailInstance) Name() string   { return i.name }
func (i LightsailInstance) Status() string { return i.state }
func (i LightsailInstance) Region() string { return i.region }
func (i LightsailInstance) Columns() []string {
	return []string{"NAME", "STATE", "IP", "BLUEPRINT", "UPTIME", "REGION"}
}
func (i LightsailInstance) Values() []string {
	return []string{i.name, i.state, i.ip, i.blueprint, formatUptime(i.createdAt), i.region}
}

func formatUptime(created time.Time) string {
	if created.IsZero() {
		return "-"
	}
	d := time.Since(created)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 30*24*time.Hour:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	case d < 365*24*time.Hour:
		return fmt.Sprintf("%dmo", int(d.Hours()/(24*30)))
	default:
		return fmt.Sprintf("%dy", int(d.Hours()/(24*365)))
	}
}

type LightsailProvider struct {
	client *aws.Client
}

func NewLightsailProvider(client *aws.Client) *LightsailProvider {
	return &LightsailProvider{client: client}
}

func (p *LightsailProvider) Kind() string { return "lightsail/instances" }

func (p *LightsailProvider) Headers() []string {
	return []string{"NAME", "STATE", "IP", "BLUEPRINT", "UPTIME", "REGION"}
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
				createdAt: func() time.Time { if inst.CreatedAt != nil { return *inst.CreatedAt }; return time.Time{} }(),
			})
		}
		if out.NextPageToken == nil {
			break
		}
		pageToken = out.NextPageToken
	}
	return res, nil
}
