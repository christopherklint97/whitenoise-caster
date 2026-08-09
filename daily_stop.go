package main

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

type playbackStopper interface {
	Stop() error
}

// nextDailyStop returns the next occurrence of hour:minute in location.
// It intentionally uses a named IANA location so daylight-saving changes do
// not shift the scheduled local wall-clock time.
func nextDailyStop(now time.Time, hour, minute int, location *time.Location) time.Time {
	localNow := now.In(location)
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), hour, minute, 0, 0, location)
	if !next.After(localNow) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

func runDailyStop(ctx context.Context, stopper playbackStopper, logger *slog.Logger, hour, minute int, location *time.Location) {
	for {
		next := nextDailyStop(time.Now(), hour, minute, location)
		logger.Info("daily stop scheduled", "at", next.Format(time.RFC3339), "timezone", location)

		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return
		case <-timer.C:
			logger.Info("daily stop firing", "scheduled_at", next.Format(time.RFC3339), "timezone", location)
			if err := stopper.Stop(); err != nil {
				logger.Error("daily stop failed", "error", err)
			} else {
				logger.Info("daily stop completed")
			}
		}
	}
}

func parseDailyStop(timeOfDay, timezone string) (hour, minute int, location *time.Location, err error) {
	parsed, err := time.Parse("15:04", timeOfDay)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("time must be HH:MM: %w", err)
	}
	location, err = time.LoadLocation(timezone)
	if err != nil {
		return 0, 0, nil, fmt.Errorf("loading timezone %q: %w", timezone, err)
	}
	return parsed.Hour(), parsed.Minute(), location, nil
}
