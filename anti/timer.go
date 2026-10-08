package main

// The timer, started from a line in any note:
//
//	timer             a stopwatch
//	timer 3.5         a countdown of three and a half minutes; timer 3:30 too
//	timer 9am         a countdown to a time of day; timer 21:15 too
//	timer 5: Laundry  a countdown with a title
//	timer 25 5        a pomodoro, 25 minutes of work and 5 of rest
//	timer pomo        the usual pomodoro, 25 and 5
//	timer p           pause, or resume
//	timer r           start the timer again
//	timer s           stop it; timer 0 too
//
// There is one timer, the window's: it shows in the label whatever note is
// up, and says when a countdown or a stretch of a pomodoro is over.

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type timerKind int

const (
	stopwatch timerKind = iota + 1
	countdown
	pomodoro
)

type timer struct {
	kind  timerKind
	title string
	// for a countdown, how long; for a pomodoro, the stretches
	length, work, rest time.Duration
	resting            bool
	// begun is when the running stretch began, less what ran before a
	// pause; paused is when it was paused
	begun, paused time.Time
}

var (
	clockRE    = regexp.MustCompile(`^(\d{1,2})(?::(\d{2}))?\s*(am|pm)$`)
	hhmmRE     = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)
	durationRE = regexp.MustCompile(`^\d+(\.\d+)?$`)
)

// timerCommand does a timer line to t (nil for none running) at now: the
// timer then, nil for none; an error for a line it cannot read.
func timerCommand(t *timer, line string, now time.Time) (*timer, error) {
	rest := strings.TrimSpace(line[len("timer"):])
	title := ""
	// the title is after the first colon not followed by a digit
	for i := 0; i < len(rest); i++ {
		if rest[i] == ':' && (i+1 == len(rest) || rest[i+1] < '0' || rest[i+1] > '9') {
			rest, title = strings.TrimSpace(rest[:i]), strings.TrimSpace(rest[i+1:])
			break
		}
	}
	arg := strings.ToLower(rest)
	switch arg {
	case "":
		return &timer{kind: stopwatch, title: title, begun: now}, nil
	case "p", "pause", "resume":
		if t == nil {
			return nil, nil
		}
		u := *t
		if u.paused.IsZero() {
			u.paused = now
		} else {
			u.begun = u.begun.Add(now.Sub(u.paused))
			u.paused = time.Time{}
		}
		return &u, nil
	case "r", "restart":
		if t == nil {
			return nil, nil
		}
		u := *t
		u.begun, u.paused, u.resting = now, time.Time{}, false
		return &u, nil
	case "s", "stop", "0":
		return nil, nil
	case "pomo", "pomodoro":
		return &timer{kind: pomodoro, title: title, work: 25 * time.Minute, rest: 5 * time.Minute, begun: now}, nil
	}
	if f := strings.Fields(arg); len(f) == 2 && durationRE.MatchString(f[0]) && durationRE.MatchString(f[1]) {
		return &timer{kind: pomodoro, title: title, work: minutes(f[0]), rest: minutes(f[1]), begun: now}, nil
	}
	if m := clockRE.FindStringSubmatch(arg); m != nil {
		h, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		if h > 12 || mm > 59 {
			return nil, fmt.Errorf("timer %s: not a time of day", rest)
		}
		h %= 12
		if m[3] == "pm" {
			h += 12
		}
		return until(now, h, mm, title), nil
	}
	if m := hhmmRE.FindStringSubmatch(arg); m != nil {
		a, _ := strconv.Atoi(m[1])
		b, _ := strconv.Atoi(m[2])
		if b > 59 {
			return nil, fmt.Errorf("timer %s: not a time", rest)
		}
		// 3:30 is a duration, 21:15 a time of day
		if a >= 13 && a <= 23 {
			return until(now, a, b, title), nil
		}
		d := time.Duration(a)*time.Minute + time.Duration(b)*time.Second
		return &timer{kind: countdown, title: title, length: d, begun: now}, nil
	}
	if durationRE.MatchString(arg) {
		return &timer{kind: countdown, title: title, length: minutes(arg), begun: now}, nil
	}
	return nil, fmt.Errorf("timer %s: a number of minutes, m:ss, a time of day, two numbers for a pomodoro, or p, r, s", rest)
}

func minutes(s string) time.Duration {
	f, _ := strconv.ParseFloat(s, 64)
	return time.Duration(f * float64(time.Minute))
}

// until is a countdown to the next h:mm.
func until(now time.Time, h, m int, title string) *timer {
	at := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if !at.After(now) {
		at = at.AddDate(0, 0, 1)
	}
	return &timer{kind: countdown, title: title, length: at.Sub(now), begun: now}
}

// elapsed is how long the running stretch has run.
func (t *timer) elapsed(now time.Time) time.Duration {
	if !t.paused.IsZero() {
		now = t.paused
	}
	return now.Sub(t.begun)
}

// tick moves t on to now: a pomodoro on to its next stretch when one is
// over. It says what is over, if anything; a countdown over is done.
func (t *timer) tick(now time.Time) (over string, done bool) {
	switch t.kind {
	case countdown:
		if t.elapsed(now) >= t.length {
			return t.named("time is up"), true
		}
	case pomodoro:
		stretch := t.work
		if t.resting {
			stretch = t.rest
		}
		if t.elapsed(now) >= stretch {
			t.begun = t.begun.Add(stretch)
			t.resting = !t.resting
			if t.resting {
				return t.named("work is over: rest"), false
			}
			return t.named("rest is over: work"), false
		}
	}
	return "", false
}

func (t *timer) named(s string) string {
	if t.title != "" {
		return t.title + ": " + s
	}
	return s
}

// show is the timer as the label shows it.
func (t *timer) show(now time.Time) string {
	var d time.Duration
	word := "timer"
	switch t.kind {
	case stopwatch:
		d = t.elapsed(now)
	case countdown:
		d = t.length - t.elapsed(now)
	case pomodoro:
		word = "work"
		stretch := t.work
		if t.resting {
			word, stretch = "rest", t.rest
		}
		d = stretch - t.elapsed(now)
	}
	if d < 0 {
		d = 0
	}
	s := word + " " + clock(d)
	if t.title != "" {
		s = t.title + " " + s
	}
	if !t.paused.IsZero() {
		s += " (paused)"
	}
	return s
}

// clock writes a duration as m:ss, or h:mm:ss.
func clock(d time.Duration) string {
	sec := int(d.Round(time.Second) / time.Second)
	if sec >= 3600 {
		return fmt.Sprintf("%d:%02d:%02d", sec/3600, sec%3600/60, sec%60)
	}
	return fmt.Sprintf("%d:%02d", sec/60, sec%60)
}
