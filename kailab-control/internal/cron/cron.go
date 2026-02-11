// Package cron provides minimal cron expression parsing for GitHub Actions schedule triggers.
// Supports standard 5-field cron: minute hour day-of-month month day-of-week.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule represents a parsed cron expression.
type Schedule struct {
	Minutes    []bool // 0-59
	Hours      []bool // 0-23
	DaysOfMonth []bool // 1-31 (index 0 unused)
	Months     []bool // 1-12 (index 0 unused)
	DaysOfWeek []bool // 0-6 (Sunday=0)
}

// Parse parses a cron expression string (5 fields: minute hour dom month dow).
func Parse(expr string) (*Schedule, error) {
	fields := strings.Fields(strings.TrimSpace(expr))
	if len(fields) != 5 {
		return nil, fmt.Errorf("cron: expected 5 fields, got %d", len(fields))
	}

	s := &Schedule{
		Minutes:     make([]bool, 60),
		Hours:       make([]bool, 24),
		DaysOfMonth: make([]bool, 32), // 1-31
		Months:      make([]bool, 13), // 1-12
		DaysOfWeek:  make([]bool, 7),  // 0-6
	}

	if err := parseField(fields[0], s.Minutes, 0, 59); err != nil {
		return nil, fmt.Errorf("cron minute: %w", err)
	}
	if err := parseField(fields[1], s.Hours, 0, 23); err != nil {
		return nil, fmt.Errorf("cron hour: %w", err)
	}
	if err := parseField(fields[2], s.DaysOfMonth, 1, 31); err != nil {
		return nil, fmt.Errorf("cron day-of-month: %w", err)
	}
	if err := parseField(fields[3], s.Months, 1, 12); err != nil {
		return nil, fmt.Errorf("cron month: %w", err)
	}
	if err := parseField(fields[4], s.DaysOfWeek, 0, 6); err != nil {
		return nil, fmt.Errorf("cron day-of-week: %w", err)
	}

	return s, nil
}

// Match returns true if the given time matches this schedule.
func (s *Schedule) Match(t time.Time) bool {
	return s.Minutes[t.Minute()] &&
		s.Hours[t.Hour()] &&
		s.DaysOfMonth[t.Day()] &&
		s.Months[int(t.Month())] &&
		s.DaysOfWeek[int(t.Weekday())]
}

// Next returns the next time after t that matches this schedule.
// Searches up to 1 year ahead. Returns zero time if not found.
func (s *Schedule) Next(after time.Time) time.Time {
	// Start from the next minute
	t := after.Truncate(time.Minute).Add(time.Minute)
	limit := after.Add(366 * 24 * time.Hour)

	for t.Before(limit) {
		if s.Match(t) {
			return t
		}
		t = t.Add(time.Minute)
	}
	return time.Time{}
}

// parseField parses a single cron field and sets the matching bits in the bool slice.
func parseField(field string, bits []bool, min, max int) error {
	// Handle comma-separated lists
	for _, part := range strings.Split(field, ",") {
		if err := parsePart(part, bits, min, max); err != nil {
			return err
		}
	}
	return nil
}

// parsePart parses a single part of a cron field (value, range, step, or wildcard).
func parsePart(part string, bits []bool, min, max int) error {
	// Handle step: */5 or 1-30/5
	step := 1
	if idx := strings.Index(part, "/"); idx >= 0 {
		var err error
		step, err = strconv.Atoi(part[idx+1:])
		if err != nil || step <= 0 {
			return fmt.Errorf("invalid step: %s", part)
		}
		part = part[:idx]
	}

	// Handle wildcard
	if part == "*" {
		for i := min; i <= max; i += step {
			bits[i] = true
		}
		return nil
	}

	// Handle range: 1-5
	if idx := strings.Index(part, "-"); idx >= 0 {
		lo, err := strconv.Atoi(part[:idx])
		if err != nil {
			return fmt.Errorf("invalid range start: %s", part)
		}
		hi, err := strconv.Atoi(part[idx+1:])
		if err != nil {
			return fmt.Errorf("invalid range end: %s", part)
		}
		if lo < min || hi > max || lo > hi {
			return fmt.Errorf("range out of bounds: %d-%d (valid: %d-%d)", lo, hi, min, max)
		}
		for i := lo; i <= hi; i += step {
			bits[i] = true
		}
		return nil
	}

	// Single value
	val, err := strconv.Atoi(part)
	if err != nil {
		return fmt.Errorf("invalid value: %s", part)
	}
	if val < min || val > max {
		return fmt.Errorf("value %d out of bounds (%d-%d)", val, min, max)
	}
	bits[val] = true
	return nil
}
