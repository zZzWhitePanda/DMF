package humanize

import (
	"math/rand"
	"sort"
	"time"
)

// BreakKind distinguishes a short humanizing pause from a larger "fatigued,
// needs real rest" pause.
type BreakKind int

const (
	// BreakShort is a 5-15 minute scattered pause inside a chunk.
	BreakShort BreakKind = iota
	// BreakBig is the fatigue-reset pause that ends a chunk. Duration scales
	// with total session runtime (longer sessions → longer sleeps).
	BreakBig
)

// String returns "short" or "big" for logging / event payloads.
func (k BreakKind) String() string {
	if k == BreakBig {
		return "big"
	}
	return "short"
}

// scheduledShort is one planned short break inside the current chunk.
type scheduledShort struct {
	FireAt   time.Time
	Duration time.Duration
}

// chunk is a run-period between big breaks, with scheduled short breaks inside.
type chunk struct {
	Start   time.Time
	End     time.Time        // when the big break fires
	Shorts  []scheduledShort // random times within the chunk
	NextIdx int              // index of the next short break to fire
}

// FatigueScheduler plans run/break cycles based on simulated player fatigue.
//
// Mental model: a "fatigue bar" fills as the grinder runs. Short breaks (5-15m,
// scattered randomly) scrub a little off the bar but keep the overall climb.
// When the bar hits its (randomized) cap, a big break fires — duration scales
// with how long the session has been running, so long-haul runs eventually
// take sleep-sized pauses. The bigger the last big break, the longer the next
// chunk of grind-time can run before the next big break — exactly like a
// rested player can play longer than one who just took a nap.
//
// Implementation-wise, we don't track a fatigue number directly. We just
// pre-plan each chunk at its start: pick a chunk duration (scaled from the
// last big break), pick N short breaks, scatter them randomly across the
// chunk. When the chunk ends, fire the big break (duration from runtime
// tier), then plan the next chunk off the new last-big-break value.
type FatigueScheduler struct {
	rng          *rand.Rand
	sessionStart time.Time

	chunk      *chunk
	lastBigDur time.Duration // 0 before any big break has fired
	chunkCount int           // 0 before the first chunk, incremented in planChunk — used to apply the first-chunk "no breaks in the first 1.5h" floor
}

// NewFatigueScheduler constructs a scheduler. sessionStart is the moment the
// run began — used to tier big-break durations (4h/8h/24h/48h/72h+).
func NewFatigueScheduler(sessionStart time.Time) *FatigueScheduler {
	return &FatigueScheduler{
		rng:          rand.New(rand.NewSource(time.Now().UnixNano())),
		sessionStart: sessionStart,
	}
}

// Next returns the time until the next break should fire, how long that
// break will last, and whether it's a short or big break. Callers wait
// `fireIn` duration then invoke the break, calling MarkFired after. If a
// chunk is uninitialized or exhausted, Next transparently plans the next
// chunk first.
func (s *FatigueScheduler) Next(now time.Time) (fireIn, breakDur time.Duration, kind BreakKind) {
	if s.chunk == nil || now.After(s.chunk.End) {
		s.planChunk(now)
	}
	// Any un-fired shorts in this chunk?
	if s.chunk.NextIdx < len(s.chunk.Shorts) {
		p := s.chunk.Shorts[s.chunk.NextIdx]
		fireIn = p.FireAt.Sub(now)
		if fireIn < 0 {
			fireIn = 0
		}
		return fireIn, p.Duration, BreakShort
	}
	// Shorts exhausted — big break is next, scheduled for chunk end.
	fireIn = s.chunk.End.Sub(now)
	if fireIn < 0 {
		fireIn = 0
	}
	return fireIn, s.nextBigDuration(now), BreakBig
}

// MarkFired advances the scheduler after a break actually ran. Shorts
// increment the chunk's cursor; a big break resets the chunk (so the next
// Next() call plans a fresh one) and records its duration for the next
// chunk-length calculation.
func (s *FatigueScheduler) MarkFired(kind BreakKind, dur time.Duration) {
	if kind == BreakShort {
		if s.chunk != nil {
			s.chunk.NextIdx++
		}
		return
	}
	s.lastBigDur = dur
	s.chunk = nil
}

// planChunk builds the next chunk: picks a length (scaled from last big
// break), decides how many short breaks to insert, and distributes them
// randomly within the chunk with a minimum separation.
func (s *FatigueScheduler) planChunk(now time.Time) {
	chunkLen := s.chunkLength()
	end := now.Add(chunkLen)

	// Short breaks per chunk: roughly 1 per 50 min with ±1 jitter. Chunks
	// under 30 min skip shorts entirely (not enough room).
	chunkMins := chunkLen.Minutes()
	count := 0
	if chunkMins >= 30 {
		count = int(chunkMins / 50)
		if count < 1 {
			count = 1
		}
		count += s.rng.Intn(2) // 0 or 1 extra
	}

	// Distribute within [chunkStart + headBuffer, chunkEnd - 5m] so
	// breaks don't bunch at the boundaries. First chunk uses a 90-min
	// head buffer so no short break can fire inside the first 1.5
	// hours of the session (LO's rule). Subsequent chunks use the
	// standard 10-min buffer.
	//
	// If the chunk is too short to fit the buffers plus any slack, skip
	// shorts entirely — fine for a short first chunk at the 90-min floor
	// (no shorts fit; big break fires at 90 min).
	const (
		tailBuffer = 5 * time.Minute
		minSlack   = 10 * time.Minute
	)
	headBuffer := 10 * time.Minute
	if s.chunkCount == 0 {
		headBuffer = 90 * time.Minute
	}
	if chunkLen < headBuffer+tailBuffer+minSlack {
		count = 0
	}

	shorts := make([]scheduledShort, 0, count)
	if count > 0 {
		windowStart := now.Add(headBuffer)
		windowEnd := end.Add(-tailBuffer)
		windowDur := windowEnd.Sub(windowStart)

		offsets := make([]time.Duration, count)
		for i := range offsets {
			offsets[i] = time.Duration(s.rng.Int63n(int64(windowDur)))
		}
		sort.Slice(offsets, func(i, j int) bool { return offsets[i] < offsets[j] })

		// Enforce ≥15 min between consecutive shorts so two don't fire
		// back-to-back when the RNG clusters them.
		const minSep = 15 * time.Minute
		for i := 1; i < len(offsets); i++ {
			if offsets[i]-offsets[i-1] < minSep {
				offsets[i] = offsets[i-1] + minSep
			}
		}

		for _, off := range offsets {
			fireAt := windowStart.Add(off)
			if fireAt.After(windowEnd) {
				break // ran out of room after separation enforcement
			}
			shorts = append(shorts, scheduledShort{
				FireAt:   fireAt,
				Duration: s.shortBreakDuration(),
			})
		}
	}

	s.chunk = &chunk{
		Start:  now,
		End:    end,
		Shorts: shorts,
	}
	s.chunkCount++
}

// chunkLength picks how long the next grind-period runs before the big
// break. First chunk (no prior big break): 90-180 min — the 90-minute
// floor enforces LO's "no breaks in the first 1.5 hours" rule since
// the chunk's big break fires at its end. After that: scaled off the
// last big break — a 30 min nap earns ~90 min of grind; a 1h break
// earns ~2.5h; a 3h sleep earns ~7+ hours.
func (s *FatigueScheduler) chunkLength() time.Duration {
	if s.chunkCount == 0 {
		return time.Duration(90+s.rng.Intn(90)) * time.Minute
	}
	if s.lastBigDur == 0 {
		// Shouldn't happen (chunkCount>0 but lastBigDur still 0 means we
		// never fired a big break on the previous chunk — treat as
		// first-chunk-ish for safety).
		return time.Duration(90+s.rng.Intn(90)) * time.Minute
	}
	// Multiplier 2.0-3.0: cover the "a 30m break lets me grind ~90m, an
	// hour break lets me grind 2-3h" feel LO described.
	mult := 2.0 + s.rng.Float64()
	chunkDur := time.Duration(float64(s.lastBigDur) * mult)
	// Clamp so a weirdly short big break can't produce <45m chunks (which
	// would skip shorts entirely) and a sleep-sized big break can't produce
	// absurd 10h+ non-stop grinds.
	if chunkDur < 45*time.Minute {
		chunkDur = 45 * time.Minute
	}
	if chunkDur > 6*time.Hour {
		chunkDur = 6 * time.Hour
	}
	return chunkDur
}

// shortBreakDuration returns a random 5-15 min duration at second
// resolution, deliberately avoiding round-minute values so pauses look
// like "11m 42s" rather than "15m 00s" — real people don't take tidy
// quarter-hour breaks.
func (s *FatigueScheduler) shortBreakDuration() time.Duration {
	const (
		minSecs = 5 * 60
		maxSecs = 15 * 60
	)
	secs := minSecs + s.rng.Intn(maxSecs-minSecs)
	if secs%60 == 0 {
		// Nudge off the round minute by 1-58 seconds.
		secs += 1 + s.rng.Intn(58)
	}
	return time.Duration(secs) * time.Second
}

// nextBigDuration tiers the big break by total session runtime. Longer
// sessions → longer breaks, so a 48-hour run doesn't take the same 45-min
// pauses it took in hour three. Each tier picks a random second-resolution
// duration inside its range (avoiding round minutes).
//
// Tiering:
//
//	< 4h  : 30-60 min  (early session, light fatigue)
//	4-8h  : 45-90 min  (mid-session)
//	8-24h : 1-2 hours  (first "nap")
//	24-48h: 1.5-3 h    (sleep-ish)
//	48-72h: 2-4 h      (longer sleeps)
//	72h+  : 3-6 h      (full sleep cycles — the "I live here" tier)
func (s *FatigueScheduler) nextBigDuration(now time.Time) time.Duration {
	runtime := now.Sub(s.sessionStart)
	var minSecs, maxSecs int
	switch {
	case runtime < 4*time.Hour:
		minSecs, maxSecs = 30*60, 60*60
	case runtime < 8*time.Hour:
		minSecs, maxSecs = 45*60, 90*60
	case runtime < 24*time.Hour:
		minSecs, maxSecs = 60*60, 120*60
	case runtime < 48*time.Hour:
		minSecs, maxSecs = 90*60, 180*60
	case runtime < 72*time.Hour:
		minSecs, maxSecs = 120*60, 240*60
	default:
		minSecs, maxSecs = 180*60, 360*60
	}
	secs := minSecs + s.rng.Intn(maxSecs-minSecs)
	if secs%60 == 0 {
		secs += 1 + s.rng.Intn(58)
	}
	return time.Duration(secs) * time.Second
}

// FormatDuration renders a duration as "1h 23m 45s" (or "23m 45s" if under
// an hour) — the human-readable form used in break events + logs.
func FormatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return formatHMS(int(h), int(m), int(s))
	}
	return formatMS(int(m), int(s))
}

func formatHMS(h, m, s int) string {
	return itoa(h) + "h " + itoa(m) + "m " + itoa(s) + "s"
}

func formatMS(m, s int) string {
	return itoa(m) + "m " + itoa(s) + "s"
}

// itoa is a tiny internal int→string helper to avoid pulling strconv/fmt
// into this file for a two-value formatter.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	buf := [12]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
