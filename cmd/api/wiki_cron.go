package main

import (
	"errors"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type wikiCron struct {
	fields   [5]uint64
	location *time.Location
}

func parseWikiCron(expression, zone string) (wikiCron, error) {
	var schedule wikiCron
	fields := strings.Fields(expression)
	location, err := time.LoadLocation(zone)
	if err != nil || zone == "" || zone == "Local" || len(fields) != 5 {
		return schedule, errors.New("需要有效的五字段 Cron 和 IANA 时区")
	}
	schedule.location = location
	bounds := [5][2]int{{0, 59}, {0, 23}, {1, 31}, {1, 12}, {0, 6}}
	for i, field := range fields {
		for _, component := range strings.Split(field, ",") {
			rangeText, stepText, hasStep := strings.Cut(component, "/")
			step := 1
			if hasStep {
				step, err = strconv.Atoi(stepText)
				if err != nil || step < 1 || step > 60 {
					return schedule, errors.New("Cron 步长无效")
				}
			}
			low, high := bounds[i][0], bounds[i][1]
			if rangeText != "*" {
				start, end, ranged := strings.Cut(rangeText, "-")
				low, err = strconv.Atoi(start)
				if err != nil {
					return schedule, errors.New("Cron 范围无效")
				}
				if ranged {
					high, err = strconv.Atoi(end)
					if err != nil {
						return schedule, errors.New("Cron 范围无效")
					}
				} else if !hasStep {
					high = low
				}
			}
			if low < bounds[i][0] || high > bounds[i][1] || low > high {
				return schedule, errors.New("Cron 字段超出范围")
			}
			for value := low; value <= high; value += step {
				schedule.fields[i] |= 1 << value
			}
		}
	}
	return schedule, nil
}

func (c wikiCron) next(after time.Time) time.Time {
	for candidate, limit := after.Truncate(time.Minute).Add(time.Minute), after.AddDate(4, 1, 0); candidate.Before(limit); {
		local := candidate.In(c.location)
		dom := c.fields[2]&(1<<local.Day()) != 0
		dow := c.fields[4]&(1<<local.Weekday()) != 0
		dayMatches := dom && dow
		if c.fields[2] != (uint64(1)<<32)-2 && c.fields[4] != 127 {
			dayMatches = dom || dow
		}
		if dayMatches && c.fields[3]&(1<<local.Month()) != 0 {
			if c.fields[0]&(1<<local.Minute()) != 0 && c.fields[1]&(1<<local.Hour()) != 0 {
				return candidate
			}
			candidate = candidate.Add(time.Minute)
		} else {
			candidate = time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, c.location)
		}
	}
	return time.Time{}
}
