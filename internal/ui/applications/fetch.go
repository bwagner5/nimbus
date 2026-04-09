package applications

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	lstypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	tea "charm.land/bubbletea/v2"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/resources"
)

const tagPrefix = "nimbus:app:"

// Environment represents an app environment parsed from instance tags.
type Environment struct {
	Name    string
	Targets []TargetInstance
}

// TargetInstance is a Lightsail instance associated with an app/env.
type TargetInstance struct {
	Name   string
	State  string
	IP     string
	Region string
}

// AppDetail holds full application detail.
type AppDetail struct {
	Name         string
	Bucket       string
	Region       string
	State        string
	Environments []Environment
}

// AppDetailMsg carries fetched app detail.
type AppDetailMsg struct {
	Detail *AppDetail
	Err    error
}

// CreateAppMsg is returned when app creation completes.
type CreateAppMsg struct {
	Err  error
	Name string
}

// AccountIDMsg carries the AWS account ID.
type AccountIDMsg struct {
	AccountID string
	Err       error
}

// FetchAccountID gets the AWS account ID via STS.
func FetchAccountID(ctx context.Context, client *aws.Client) tea.Cmd {
	return func() tea.Msg {
		svc := sts.NewFromConfig(client.Config())
		out, err := svc.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		if err != nil {
			return AccountIDMsg{Err: err}
		}
		return AccountIDMsg{AccountID: *out.Account}
	}
}

// FetchAppDetail fetches full app detail: bucket info + instance tags for targets.
func FetchAppDetail(ctx context.Context, client *aws.Client, appName, bucketName, region string) tea.Cmd {
	return func() tea.Msg {
		svc := lightsail.NewFromConfig(client.WithRegion(region).Config())

		// Get bucket info
		buckOut, err := svc.GetBuckets(ctx, &lightsail.GetBucketsInput{BucketName: &bucketName})
		if err != nil {
			return AppDetailMsg{Err: err}
		}
		state := "active"
		if len(buckOut.Buckets) > 0 && buckOut.Buckets[0].State != nil && buckOut.Buckets[0].State.Code != nil {
			state = *buckOut.Buckets[0].State.Code
		}

		// Scan all instances across this region for matching tags
		envMap := map[string]*Environment{}
		var pageToken *string
		for {
			instOut, err := svc.GetInstances(ctx, &lightsail.GetInstancesInput{PageToken: pageToken})
			if err != nil {
				break
			}
			for _, inst := range instOut.Instances {
				for _, tag := range inst.Tags {
					if tag.Key == nil || !strings.HasPrefix(*tag.Key, tagPrefix+appName+":") {
						continue
					}
					// Tag format: nimbus:app:<appname>:<envname>
					envName := strings.TrimPrefix(*tag.Key, tagPrefix+appName+":")
					if envName == "" {
						continue
					}
					env, ok := envMap[envName]
					if !ok {
						env = &Environment{Name: envName}
						envMap[envName] = env
					}
					ip := ""
					if inst.PublicIpAddress != nil {
						ip = *inst.PublicIpAddress
					}
					instState := ""
					if inst.State != nil && inst.State.Name != nil {
						instState = *inst.State.Name
					}
					env.Targets = append(env.Targets, TargetInstance{
						Name:   *inst.Name,
						State:  instState,
						IP:     ip,
						Region: region,
					})
				}
			}
			if instOut.NextPageToken == nil {
				break
			}
			pageToken = instOut.NextPageToken
		}

		var envs []Environment
		for _, e := range envMap {
			envs = append(envs, *e)
		}

		return AppDetailMsg{Detail: &AppDetail{
			Name:         appName,
			Bucket:       bucketName,
			Region:       region,
			State:        state,
			Environments: envs,
		}}
	}
}

// CreateApp creates a Lightsail bucket for the application.
func CreateApp(ctx context.Context, client *aws.Client, accountID, appName, region string) tea.Cmd {
	return func() tea.Msg {
		svc := lightsail.NewFromConfig(client.WithRegion(region).Config())
		bucketName := fmt.Sprintf("%s%s-%s", resources.AppBucketPrefix, accountID, appName)
		_, err := svc.CreateBucket(ctx, &lightsail.CreateBucketInput{
			BucketName: &bucketName,
			BundleId:   strPtr("small_1_0"),
		})
		if err != nil {
			return CreateAppMsg{Err: err, Name: appName}
		}
		return CreateAppMsg{Name: appName}
	}
}

// AddTarget tags an instance as a deployment target for an app/env.
func AddTarget(ctx context.Context, client *aws.Client, instanceName, appName, envName, region string) tea.Cmd {
	return func() tea.Msg {
		svc := lightsail.NewFromConfig(client.WithRegion(region).Config())
		tagKey := fmt.Sprintf("%s%s:%s", tagPrefix, appName, envName)
		tagVal := "true"
		_, err := svc.TagResource(ctx, &lightsail.TagResourceInput{
			ResourceName: &instanceName,
			Tags:         []lstypes.Tag{{Key: &tagKey, Value: &tagVal}},
		})
		if err != nil {
			return CreateAppMsg{Err: err, Name: appName}
		}
		return CreateAppMsg{Name: appName}
	}
}

func strPtr(s string) *string { return &s }
