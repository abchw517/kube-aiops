package observability

import (
	"errors"
	"regexp"
	"time"

	"github.com/abchw517/kube-aiops/internal/correlation"
	"github.com/abchw517/kube-aiops/internal/finding"
)

var objectName = regexp.MustCompile(`^[a-z0-9]([a-z0-9.-]*[a-z0-9])?$`)

func ValidateScope(scope correlation.CorrelationScope) error {
	resource := scope.Resource
	if scope.Cluster != finding.LocalCluster || len(resource.Name) > 253 || !objectName.MatchString(resource.Name) {
		return errors.New("invalid observability resource scope")
	}
	if resource.Kind == "Node" {
		if scope.Namespace != "" || resource.Namespace != "" {
			return errors.New("invalid observability node scope")
		}
	} else if scope.Namespace != resource.Namespace || len(scope.Namespace) > 63 || !objectName.MatchString(scope.Namespace) {
		return errors.New("invalid observability namespace scope")
	}
	return nil
}

func QueryBounds(query correlation.Query) (time.Time, time.Time, time.Duration, error) {
	start, startErr := time.Parse(time.RFC3339, query.Window.Start)
	end, endErr := time.Parse(time.RFC3339, query.Window.End)
	window := end.Sub(start)
	if startErr != nil || endErr != nil || window <= 0 || window > correlation.MaxWindow ||
		query.Budget.WindowSeconds != int(window/time.Second) ||
		query.Budget.MaxSignalsPerSource < 1 || query.Budget.MaxSignalsPerSource > correlation.MaxSignalsPerSource ||
		query.Budget.PerSourceTimeoutMillis < 1 || query.Budget.PerSourceTimeoutMillis > int(correlation.MaxPerSourceTimeout/time.Millisecond) {
		return time.Time{}, time.Time{}, 0, errors.New("invalid observability query budget")
	}
	// Leave the correlation service time to retain evidence from queries that already finished.
	timeout := time.Duration(query.Budget.PerSourceTimeoutMillis) * time.Millisecond * 9 / 10
	return start, end, timeout, nil
}
