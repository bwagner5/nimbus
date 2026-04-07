package instances

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/resources"
)

const (
	RefreshNormal   = 30 * time.Second
	RefreshFast     = 5 * time.Second
	RefreshCooldown = 2 * time.Second
)

// RefreshTickMsg triggers a periodic refresh.
type RefreshTickMsg struct{ Gen int }

// ResourcesMsg carries fetched resources, optionally scoped to a region.
type ResourcesMsg struct {
	Resources []resources.Resource
	Region    string
}

// RegionsMsg carries fetched region names.
type RegionsMsg []string

// ErrMsg wraps an error as a tea.Msg.
type ErrMsg struct{ Err error }

func (e ErrMsg) Error() string { return e.Err.Error() }

// PartialMsg carries streaming results from a multi-region fetch.
type PartialMsg struct {
	Resources []resources.Resource
	Done      bool
	Total     int
	Completed int
	Next      *StreamRegionsCmd
}

// StreamRegionsCmd fetches resources from regions one at a time.
type StreamRegionsCmd struct {
	Regions  []string
	Provider resources.Provider
	Idx      int
	Ctx      context.Context
}

// Fetch fetches the next region's resources.
func (s StreamRegionsCmd) Fetch() tea.Msg {
	if s.Ctx.Err() != nil {
		return PartialMsg{Done: true, Total: len(s.Regions), Completed: s.Idx}
	}
	if s.Idx >= len(s.Regions) {
		return PartialMsg{Done: true, Total: len(s.Regions), Completed: len(s.Regions)}
	}
	res, _ := s.Provider.Fetch(s.Ctx, s.Regions[s.Idx])
	next := &StreamRegionsCmd{Regions: s.Regions, Provider: s.Provider, Idx: s.Idx + 1, Ctx: s.Ctx}
	return PartialMsg{Resources: res, Done: false, Total: len(s.Regions), Completed: s.Idx + 1, Next: next}
}

// ScheduleRefresh returns a tick command for the next refresh.
func ScheduleRefresh(gen int, pollRegion string) tea.Cmd {
	d := RefreshNormal
	if pollRegion != "" {
		d = RefreshFast
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return RefreshTickMsg{Gen: gen} })
}

// FetchRegions fetches sorted region names.
func FetchRegions(ctx context.Context, client *aws.Client) tea.Cmd {
	return func() tea.Msg {
		regions, err := client.FetchRegionsSorted(ctx)
		if err != nil {
			return ErrMsg{Err: err}
		}
		return RegionsMsg(append([]string{"global"}, regions...))
	}
}

// FetchResources fetches resources for the given provider/region. Uses streaming for "global".
func FetchResources(ctx context.Context, client *aws.Client, provider resources.Provider, region string) tea.Cmd {
	if region == "global" {
		return func() tea.Msg {
			regions, err := client.FetchRegionsSorted(ctx)
			if err != nil {
				return ErrMsg{Err: err}
			}
			return StreamRegionsCmd{Regions: regions, Provider: provider, Idx: 0, Ctx: ctx}
		}
	}
	return func() tea.Msg {
		res, err := provider.Fetch(ctx, region)
		if err != nil {
			return ErrMsg{Err: err}
		}
		return ResourcesMsg{Resources: res}
	}
}

// FetchRegionOnly fetches resources for a single region (used for scoped polling).
func FetchRegionOnly(ctx context.Context, provider resources.Provider, region string) tea.Cmd {
	return func() tea.Msg {
		res, _ := provider.Fetch(ctx, region)
		return ResourcesMsg{Resources: res, Region: region}
	}
}

// CheckPollSettled returns "" if all resources in pollRegion are in a terminal state.
func CheckPollSettled(res []resources.Resource, pollRegion string) string {
	if pollRegion == "" {
		return ""
	}
	for _, r := range res {
		if r.Region() != pollRegion {
			continue
		}
		switch r.Status() {
		case "running", "stopped", "terminated", "available":
		default:
			return pollRegion
		}
	}
	return ""
}

// FormatProgress returns a progress string for streaming fetches.
func FormatProgress(completed, total int) string {
	return fmt.Sprintf("%d/%d regions", completed, total)
}
