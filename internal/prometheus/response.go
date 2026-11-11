package prometheus

import (
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
)

type queryResponse struct {
	Status   string   `json:"status"`
	Warnings []string `json:"warnings"`
	Data     struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Value  []json.RawMessage   `json:"value"`
			Values [][]json.RawMessage `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

func decodeSignal(body []byte, metric metricDefinition, start, end time.Time, resource finding.ResourceRef) (correlation.CorrelationSignal, error) {
	var result queryResponse
	if err := json.Unmarshal(body, &result); err != nil || result.Status != "success" ||
		result.Data.Result == nil || len(result.Data.Result) > 1 {
		return correlation.CorrelationSignal{}, errQuery
	}
	wantType := "matrix"
	if metric.instant {
		wantType = "vector"
	}
	if result.Data.ResultType != wantType {
		return correlation.CorrelationSignal{}, errQuery
	}

	var queryErr error
	if len(result.Warnings) > 0 {
		queryErr = errQuery
	}
	if len(result.Data.Result) == 0 {
		return correlation.CorrelationSignal{}, queryErr
	}

	series := result.Data.Result[0]
	points := series.Values
	maxSamples := int(end.Sub(start)/rangeStep) + 1
	if metric.instant {
		points = [][]json.RawMessage{series.Value}
		maxSamples = 1
	}
	if len(points) == 0 || len(points) > maxSamples {
		return correlation.CorrelationSignal{}, errQuery
	}

	var first, last time.Time
	var count int
	var peak float64
	for _, point := range points {
		stamp, value, ok := decodeSample(point)
		if !ok || stamp.Before(start) || stamp.After(end) {
			return correlation.CorrelationSignal{}, errQuery
		}
		if math.IsNaN(value) || math.IsInf(value, 0) {
			continue
		}
		if count == 0 || stamp.Before(first) {
			first = stamp
		}
		if count == 0 || stamp.After(last) {
			last = stamp
		}
		if count == 0 || value > peak {
			peak = value
		}
		count++
	}
	if count == 0 {
		return correlation.CorrelationSignal{}, queryErr
	}

	return correlation.CorrelationSignal{
		ID:        "prometheus/" + metric.name,
		Source:    correlation.SourcePrometheus,
		Type:      correlation.SignalTypeMetric,
		Resource:  &resource,
		FirstSeen: first.UTC().Format(time.RFC3339Nano),
		LastSeen:  last.UTC().Format(time.RFC3339Nano),
		Count:     count,
		Name:      metric.name,
		Value:     &peak,
		Unit:      metric.unit,
	}, queryErr
}

func decodeSample(point []json.RawMessage) (time.Time, float64, bool) {
	if len(point) != 2 {
		return time.Time{}, 0, false
	}
	var timestamp *float64
	var value string
	if err := json.Unmarshal(point[0], &timestamp); err != nil || timestamp == nil ||
		json.Unmarshal(point[1], &value) != nil {
		return time.Time{}, 0, false
	}
	number, err := strconv.ParseFloat(value, 64)
	seconds, fraction := math.Modf(*timestamp)
	return time.Unix(int64(seconds), int64(math.Round(fraction*float64(time.Second)))), number, err == nil
}
