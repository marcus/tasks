package recur

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// The calendar half of the recurrence grammar: schedules that name days of the
// week, days of the month, or a date in the year, rather than an interval.
//
//	w:mon,wed      every Monday and Wednesday
//	2w:sat         every second Saturday
//	m:15           the 15th of every month
//	m:last,2tue    the last day and the second Tuesday
//	y:07-04        July 4th
//	y:11:3thu      the third Thursday of November
//
// A calendar schedule already advances to the next occurrence after today, so
// the interval prefixes `.+` and `++` are refused on one; only `+` (advance ONE
// occurrence, ignoring today) is meaningful.
var (
	calendarShape = regexp.MustCompile(`\A(?:\.\+|\+\+|\+)?\d*[wmy]:`)
	calendar      = regexp.MustCompile(`\A(\.\+|\+\+|\+)?(\d+)?([wmy]):(.+)\z`)
	monthDay      = regexp.MustCompile(`\A\d{1,2}\z`)
	ordinalDay    = regexp.MustCompile(`\A(\d+|last)([a-z]+)\z`)
	yearlyDate    = regexp.MustCompile(`\A(\d{1,2})-(\d{1,2})\z`)
	yearlyOrdinal = regexp.MustCompile(`\A(\d{1,2}):(\d+|last)([a-z]+)\z`)
)

var days = []string{"mon", "tue", "wed", "thu", "fri", "sat", "sun"}

var dayIndex = func() map[string]int {
	index := map[string]int{}
	for position, day := range days {
		index[day] = position
	}
	return index
}()

var weekdaySet = []string{"mon", "tue", "wed", "thu", "fri"}
var weekendSet = []string{"sat", "sun"}

var dayFull = []string{"Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"}
var dayShort = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

var dayAliases = func() map[string]string {
	full := []string{"monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday"}
	table := map[string]string{}
	for position, name := range full {
		abbreviation := days[position]
		for _, key := range []string{name, name + "s", abbreviation, abbreviation + "s"} {
			table[key] = abbreviation
		}
	}
	table["tues"] = "tue"
	table["thur"] = "thu"
	table["thurs"] = "thu"
	table["weds"] = "wed"
	return table
}()

var monthFull = []string{"January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December"}

// lastOrdinal is the sentinel for the `last` spelling of an ordinal, which is a
// position counted from the end of the month rather than the start.
const lastOrdinal = -1

// scheduleKind names which calendar axis a schedule counts on.
type scheduleKind int

const (
	weekly scheduleKind = iota
	monthly
	yearly
)

// monthSpec is one monthly rule: a day of the month, the last day, or an
// ordinal weekday.
type monthSpec struct {
	day      int    // 1..31, or 0 when this is not a day-of-month rule
	last     bool   // the last day of the month
	ordinal  int    // 1..5, or lastOrdinal
	weekday  string // the abbreviation, when this is an ordinal-weekday rule
	ordinalY bool   // whether the ordinal fields are in use
}

// schedule is a parsed calendar schedule.
type schedule struct {
	prefix   string
	interval int
	kind     scheduleKind
	days     []string
	specs    []monthSpec
	month    int
	day      int
	ordinal  *monthSpec
}

// Calendar reports whether a value is a stored calendar schedule in its exact
// canonical spelling. That round trip is the whole test: a schedule that means
// something but is spelled differently is not a STORED value, and accepting it
// would let two spellings of one schedule sit in the file.
func Calendar(value string) bool {
	_, ok := parseSchedule(rubyStrip(value))
	return ok
}

func parseSchedule(value string) (schedule, bool) {
	if !calendarShape.MatchString(value) {
		return schedule{}, false
	}
	parsed, err := canonicalCalendar(value)
	if err != nil {
		return schedule{}, false
	}
	if canonicalString(parsed) != value {
		return schedule{}, false
	}
	return parsed, true
}

func canonicalCalendar(value string) (schedule, error) {
	match := calendar.FindStringSubmatch(value)
	if match == nil {
		return schedule{}, fmt.Errorf("unrecognized schedule")
	}
	prefix := match[1]
	if prefix == ".+" || prefix == "++" {
		return schedule{}, fmt.Errorf("%q is an interval prefix", prefix)
	}
	interval := 1
	if match[2] != "" {
		parsed, err := strconv.Atoi(match[2])
		if err != nil {
			return schedule{}, err
		}
		interval = parsed
	}
	if interval < 1 {
		return schedule{}, fmt.Errorf("recurrence interval must be at least 1")
	}
	body := match[4]
	switch match[3] {
	case "w":
		return canonicalWeekly(prefix, interval, body)
	case "m":
		return canonicalMonthly(prefix, interval, body)
	default:
		return canonicalYearly(prefix, interval, body)
	}
}

func canonicalWeekly(prefix string, interval int, body string) (schedule, error) {
	parts := strings.Split(body, ",")
	selected := []string{}
	for _, part := range parts {
		if part == "" {
			return schedule{}, fmt.Errorf("weekly schedules need at least one day")
		}
		day, known := dayAliases[part]
		if !known {
			return schedule{}, fmt.Errorf("unknown day of week: %q", part)
		}
		selected = append(selected, day)
	}
	return schedule{prefix: prefix, interval: interval, kind: weekly, days: uniqueSortedDays(selected)}, nil
}

func uniqueSortedDays(selected []string) []string {
	seen := map[string]bool{}
	unique := []string{}
	for _, day := range selected {
		if !seen[day] {
			seen[day] = true
			unique = append(unique, day)
		}
	}
	sort.SliceStable(unique, func(left, right int) bool {
		return dayIndex[unique[left]] < dayIndex[unique[right]]
	})
	return unique
}

func canonicalMonthly(prefix string, interval int, body string) (schedule, error) {
	parts := strings.Split(body, ",")
	specs := []monthSpec{}
	for _, part := range parts {
		switch {
		case part == "":
			return schedule{}, fmt.Errorf("monthly schedules need at least one rule")
		case monthDay.MatchString(part):
			day, _ := strconv.Atoi(part)
			if day < 1 || day > 31 {
				return schedule{}, fmt.Errorf("day of month must be 1–31: %q", part)
			}
			specs = append(specs, monthSpec{day: day})
		case part == "last":
			specs = append(specs, monthSpec{last: true})
		default:
			match := ordinalDay.FindStringSubmatch(part)
			if match == nil {
				return schedule{}, fmt.Errorf("unrecognized monthly rule: %q", part)
			}
			weekday, known := dayAliases[match[2]]
			if !known {
				return schedule{}, fmt.Errorf("unknown day of week: %q", match[2])
			}
			ordinal := lastOrdinal
			if match[1] != "last" {
				parsed, err := strconv.Atoi(match[1])
				if err != nil || parsed < 1 || parsed > 5 {
					return schedule{}, fmt.Errorf("ordinal weekdays run from 1 to 5 or \"last\": %q", part)
				}
				ordinal = parsed
			}
			specs = append(specs, monthSpec{ordinal: ordinal, weekday: weekday, ordinalY: true})
		}
	}
	return schedule{prefix: prefix, interval: interval, kind: monthly, specs: uniqueSortedSpecs(specs)}, nil
}

// specKey is the total order monthly rules canonicalize into: plain days
// first in numeric order, then the last-day rule, then ordinal weekdays.
func specKey(spec monthSpec) [3]int {
	switch {
	case spec.ordinalY:
		ordinal := spec.ordinal
		if ordinal == lastOrdinal {
			ordinal = 6
		}
		return [3]int{2, ordinal, dayIndex[spec.weekday]}
	case spec.last:
		return [3]int{1, 0, 0}
	default:
		return [3]int{0, spec.day, 0}
	}
}

func uniqueSortedSpecs(specs []monthSpec) []monthSpec {
	sort.SliceStable(specs, func(left, right int) bool {
		return specKey(specs[left]) != specKey(specs[right]) &&
			lessKey(specKey(specs[left]), specKey(specs[right]))
	})
	seen := map[[3]int]bool{}
	unique := []monthSpec{}
	for _, spec := range specs {
		key := specKey(spec)
		if seen[key] {
			continue
		}
		seen[key] = true
		unique = append(unique, spec)
	}
	return unique
}

func lessKey(left, right [3]int) bool {
	for index := range left {
		if left[index] != right[index] {
			return left[index] < right[index]
		}
	}
	return false
}

func canonicalYearly(prefix string, interval int, body string) (schedule, error) {
	if match := yearlyDate.FindStringSubmatch(body); match != nil {
		month, _ := strconv.Atoi(match[1])
		day, _ := strconv.Atoi(match[2])
		if month < 1 || month > 12 || !validDate(2024, month, day) {
			return schedule{}, fmt.Errorf("invalid yearly date: %q", body)
		}
		return schedule{prefix: prefix, interval: interval, kind: yearly, month: month, day: day}, nil
	}
	if match := yearlyOrdinal.FindStringSubmatch(body); match != nil {
		month, _ := strconv.Atoi(match[1])
		if month < 1 || month > 12 {
			return schedule{}, fmt.Errorf("invalid month: %q", match[1])
		}
		weekday, known := dayAliases[match[3]]
		if !known {
			return schedule{}, fmt.Errorf("unknown day of week: %q", match[3])
		}
		ordinal := lastOrdinal
		if match[2] != "last" {
			parsed, err := strconv.Atoi(match[2])
			if err != nil || parsed < 1 || parsed > 5 {
				return schedule{}, fmt.Errorf("ordinal weekdays run from 1 to 5 or \"last\": %q", body)
			}
			ordinal = parsed
		}
		return schedule{prefix: prefix, interval: interval, kind: yearly, month: month,
			ordinal: &monthSpec{ordinal: ordinal, weekday: weekday, ordinalY: true}}, nil
	}
	return schedule{}, fmt.Errorf("unrecognized yearly rule: %q", body)
}

func validDate(year, month, day int) bool {
	if month < 1 || month > 12 || day < 1 {
		return false
	}
	candidate := time.Date(year, time.Month(month), day, 0, 0, 0, 0, time.UTC)
	return candidate.Year() == year && int(candidate.Month()) == month && candidate.Day() == day
}

func canonicalString(parsed schedule) string {
	count := ""
	if parsed.interval != 1 {
		count = strconv.Itoa(parsed.interval)
	}
	switch parsed.kind {
	case weekly:
		return parsed.prefix + count + "w:" + strings.Join(parsed.days, ",")
	case monthly:
		rules := make([]string, len(parsed.specs))
		for index, spec := range parsed.specs {
			rules[index] = specString(spec)
		}
		return parsed.prefix + count + "m:" + strings.Join(rules, ",")
	default:
		body := fmt.Sprintf("%02d-%02d", parsed.month, parsed.day)
		if parsed.ordinal != nil {
			body = fmt.Sprintf("%02d:%s", parsed.month, specString(*parsed.ordinal))
		}
		return parsed.prefix + count + "y:" + body
	}
}

func specString(spec monthSpec) string {
	switch {
	case spec.ordinalY:
		ordinal := strconv.Itoa(spec.ordinal)
		if spec.ordinal == lastOrdinal {
			ordinal = "last"
		}
		return ordinal + spec.weekday
	case spec.last:
		return "last"
	default:
		return strconv.Itoa(spec.day)
	}
}

// humanizeCalendar is the gloss `list` and `show` print beside a recurring
// task: "every Mon and Wed", "monthly on the 15th", "yearly on July 4".
func humanizeCalendar(parsed schedule) string {
	var body string
	switch parsed.kind {
	case weekly:
		body = humanizeWeekly(parsed)
	case monthly:
		body = humanizeMonthly(parsed)
	default:
		body = humanizeYearly(parsed)
	}
	if parsed.prefix == "+" {
		return body + " (one hop)"
	}
	return body
}

func humanizeWeekly(parsed schedule) string {
	every := parsed.interval == 1
	var label string
	switch {
	case equalDays(parsed.days, weekdaySet):
		label = "weekday"
		if !every {
			label = "weekdays"
		}
	case equalDays(parsed.days, weekendSet):
		label = "weekend"
		if !every {
			label = "weekends"
		}
	case len(parsed.days) == 1:
		label = dayFull[dayIndex[parsed.days[0]]]
	default:
		short := make([]string, len(parsed.days))
		for index, day := range parsed.days {
			short[index] = dayShort[dayIndex[day]]
		}
		label = strings.Join(short, ", ")
	}
	if every {
		return "every " + label
	}
	return fmt.Sprintf("every %d weeks on %s", parsed.interval, label)
}

func humanizeMonthly(parsed schedule) string {
	lead := "monthly on"
	if parsed.interval != 1 {
		lead = fmt.Sprintf("every %d months on", parsed.interval)
	}
	words := make([]string, len(parsed.specs))
	for index, spec := range parsed.specs {
		words[index] = humanizeSpec(spec)
	}
	return lead + " the " + joinWords(words)
}

func humanizeSpec(spec monthSpec) string {
	switch {
	case spec.ordinalY:
		return ordinalWord(spec.ordinal) + " " + dayFull[dayIndex[spec.weekday]]
	case spec.last:
		return "last day"
	default:
		return ordinalWord(spec.day)
	}
}

func humanizeYearly(parsed schedule) string {
	lead := "yearly on"
	if parsed.interval != 1 {
		lead = fmt.Sprintf("every %d years on", parsed.interval)
	}
	if parsed.ordinal != nil {
		return fmt.Sprintf("%s the %s %s of %s", lead,
			ordinalWord(parsed.ordinal.ordinal), dayFull[dayIndex[parsed.ordinal.weekday]],
			monthFull[parsed.month-1])
	}
	return fmt.Sprintf("%s %s %d", lead, monthFull[parsed.month-1], parsed.day)
}

func joinWords(words []string) string {
	if len(words) <= 1 {
		if len(words) == 0 {
			return ""
		}
		return words[0]
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

func ordinalWord(number int) string {
	if number == lastOrdinal {
		return "last"
	}
	suffix := "th"
	if number%100 < 11 || number%100 > 13 {
		switch number % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(number) + suffix
}

func equalDays(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
