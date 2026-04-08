package instances

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/aws/aws-sdk-go-v2/service/lightsail"
	lstypes "github.com/aws/aws-sdk-go-v2/service/lightsail/types"
	tea "charm.land/bubbletea/v2"
	"github.com/wagnerbm/nimbusv2/internal/aws"
	"github.com/wagnerbm/nimbusv2/internal/ui/utils"
)

var sparkBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// MetricSeries holds sorted data points for one metric.
type MetricSeries struct {
	Label  string
	Color  string
	Points []float64 // values sorted by time
	Unit   string
}

// MetricsData holds all fetched metrics for an instance.
type MetricsData struct {
	CPU          MetricSeries
	BurstPct     MetricSeries
	NetIn        MetricSeries
	NetOut       MetricSeries
	StatusFailed MetricSeries
	Range        time.Duration
}

// MetricsMsg carries fetched metrics.
type MetricsMsg struct {
	Data *MetricsData
	Err  error
}

// MetricRange options
type MetricRange struct {
	Label    string
	Duration time.Duration
	Period   int32 // seconds
}

var MetricRanges = []MetricRange{
	{"1w", 7 * 24 * time.Hour, 3600},
	{"2w", 14 * 24 * time.Hour, 7200},
	{"4w", 28 * 24 * time.Hour, 14400},
}

// FetchMetrics fetches all instance metrics for the given range.
func FetchMetrics(ctx context.Context, client *aws.Client, name, region string, mr MetricRange) tea.Cmd {
	return func() tea.Msg {
		svc := lightsail.NewFromConfig(client.WithRegion(region).Config())
		end := time.Now()
		start := end.Add(-mr.Duration)

		fetch := func(metric string, stat lstypes.MetricStatistic, unit lstypes.MetricUnit) ([]float64, error) {
			out, err := svc.GetInstanceMetricData(ctx, &lightsail.GetInstanceMetricDataInput{
				InstanceName: &name,
				MetricName:   lstypes.InstanceMetricName(metric),
				Period:       &mr.Period,
				StartTime:    &start,
				EndTime:      &end,
				Unit:         unit,
				Statistics:   []lstypes.MetricStatistic{stat},
			})
			if err != nil {
				return nil, err
			}
			// Sort by timestamp
			sort.Slice(out.MetricData, func(i, j int) bool {
				if out.MetricData[i].Timestamp == nil {
					return true
				}
				if out.MetricData[j].Timestamp == nil {
					return false
				}
				return out.MetricData[i].Timestamp.Before(*out.MetricData[j].Timestamp)
			})
			vals := make([]float64, len(out.MetricData))
			for i, dp := range out.MetricData {
				switch stat {
				case lstypes.MetricStatisticAverage:
					if dp.Average != nil {
						vals[i] = *dp.Average
					}
				case lstypes.MetricStatisticMaximum:
					if dp.Maximum != nil {
						vals[i] = *dp.Maximum
					}
				case lstypes.MetricStatisticSum:
					if dp.Sum != nil {
						vals[i] = *dp.Sum
					}
				}
			}
			return vals, nil
		}

		cpu, err := fetch("CPUUtilization", lstypes.MetricStatisticAverage, lstypes.MetricUnitPercent)
		if err != nil {
			return MetricsMsg{Err: err}
		}
		burst, _ := fetch("BurstCapacityPercentage", lstypes.MetricStatisticAverage, lstypes.MetricUnitPercent)
		netIn, _ := fetch("NetworkIn", lstypes.MetricStatisticSum, lstypes.MetricUnitBytes)
		netOut, _ := fetch("NetworkOut", lstypes.MetricStatisticSum, lstypes.MetricUnitBytes)
		status, _ := fetch("StatusCheckFailed", lstypes.MetricStatisticSum, lstypes.MetricUnitCount)

		// Convert network bytes to MB
		for i := range netIn {
			netIn[i] = netIn[i] / (1024 * 1024)
		}
		for i := range netOut {
			netOut[i] = netOut[i] / (1024 * 1024)
		}

		return MetricsMsg{Data: &MetricsData{
			CPU:          MetricSeries{Label: "CPU %", Color: "212", Points: cpu, Unit: "%"},
			BurstPct:     MetricSeries{Label: "Burst %", Color: "39", Points: burst, Unit: "%"},
			NetIn:        MetricSeries{Label: "Net In", Color: "42", Points: netIn, Unit: "MB"},
			NetOut:       MetricSeries{Label: "Net Out", Color: "214", Points: netOut, Unit: "MB"},
			StatusFailed: MetricSeries{Label: "Failures", Color: "196", Points: status, Unit: ""},
			Range:        mr.Duration,
		}}
	}
}

// RenderSparkline renders a multi-row sparkline graph for one or more series.
// graphHeight controls how many rows tall each graph is.
func RenderSparkline(series []MetricSeries, width, graphHeight int) string {
	if width < 10 {
		width = 10
	}
	if graphHeight < 1 {
		graphHeight = 1
	}
	graphW := width - 2

	var b strings.Builder

	for si, s := range series {
		if len(s.Points) == 0 {
			continue
		}

		resampled := resample(s.Points, graphW)

		mn, mx := resampled[0], resampled[0]
		for _, v := range resampled {
			if v < mn {
				mn = v
			}
			if v > mx {
				mx = v
			}
		}

		current := resampled[len(resampled)-1]
		labelStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(s.Color)).Bold(true)
		dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
		graphStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(s.Color))

		valStr := formatMetricVal(current, s.Unit)
		maxStr := formatMetricVal(mx, s.Unit)
		b.WriteString("  " + labelStyle.Render(s.Label) + " " + labelStyle.Render(valStr) + dimStyle.Render("  max:"+maxStr) + "\n")

		// Multi-row sparkline: each row represents a band of the value range
		totalLevels := graphHeight * len(sparkBlocks)
		for row := graphHeight - 1; row >= 0; row-- {
			var line strings.Builder
			line.WriteString("  ")
			for _, v := range resampled {
				level := 0
				if mx > mn {
					level = int(math.Round((v - mn) / (mx - mn) * float64(totalLevels-1)))
				}
				if level < 0 {
					level = 0
				}
				if level >= totalLevels {
					level = totalLevels - 1
				}
				// Which block char for this row?
				rowBase := row * len(sparkBlocks)
				if level >= rowBase+len(sparkBlocks) {
					line.WriteRune(sparkBlocks[len(sparkBlocks)-1]) // full block
				} else if level >= rowBase {
					line.WriteRune(sparkBlocks[level-rowBase])
				} else {
					line.WriteRune(' ')
				}
			}
			b.WriteString(graphStyle.Render(line.String()) + "\n")
		}

		if si < len(series)-1 {
			b.WriteString("\n")
		}
	}

	return b.String()
}

// RenderMetrics renders all metric graphs.
func RenderMetrics(data *MetricsData, loading bool, width int) string {
	if data == nil {
		return utils.HelpStyle.Render("  " + string(sparkBlocks[1]) + " Loading metrics...")
	}

	var b strings.Builder
	section := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("39"))
	loadingInd := ""
	if loading {
		loadingInd = utils.HelpStyle.Render(" ⟳")
	}

	b.WriteString(section.Render("  ─── CPU & Burst ") + loadingInd + "\n")
	b.WriteString(RenderSparkline([]MetricSeries{data.CPU, data.BurstPct}, width, 4))
	b.WriteString("\n")

	b.WriteString(section.Render("  ─── Network I/O (MB) ") + loadingInd + "\n")
	b.WriteString(RenderSparkline([]MetricSeries{data.NetIn, data.NetOut}, width, 4))
	b.WriteString("\n")

	if hasNonZero(data.StatusFailed.Points) {
		b.WriteString(section.Render("  ─── Status Check Failures ") + "\n")
		b.WriteString(RenderSparkline([]MetricSeries{data.StatusFailed}, width, 3))
		b.WriteString("\n")
	}

	return b.String()
}

func resample(data []float64, targetLen int) []float64 {
	if len(data) <= targetLen {
		return data
	}
	result := make([]float64, targetLen)
	ratio := float64(len(data)) / float64(targetLen)
	for i := range result {
		start := int(float64(i) * ratio)
		end := int(float64(i+1) * ratio)
		if end > len(data) {
			end = len(data)
		}
		if start >= end {
			start = end - 1
		}
		sum := 0.0
		for j := start; j < end; j++ {
			sum += data[j]
		}
		result[i] = sum / float64(end-start)
	}
	return result
}

func formatMetricVal(v float64, unit string) string {
	if unit == "%" {
		return fmt.Sprintf("%.1f%%", v)
	}
	if unit == "MB" {
		if v >= 1024 {
			return fmt.Sprintf("%.1fGB", v/1024)
		}
		return fmt.Sprintf("%.1fMB", v)
	}
	if v == 0 {
		return "0"
	}
	return fmt.Sprintf("%.1f", v)
}

func hasNonZero(pts []float64) bool {
	for _, v := range pts {
		if v > 0 {
			return true
		}
	}
	return false
}
