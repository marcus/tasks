package recur

import (
	"fmt"
	"math/big"
)

// CivilDate is an unzoned Ruby Date-compatible civil date. Its year is
// arbitrary precision because Ruby Date accepts interval projections beyond
// time.Time's range. Ruby Date's default Date::ITALY start means dates before
// 1582-10-15 use the Julian calendar, with the ten reform days omitted.
type CivilDate struct {
	Year  *big.Int
	Month int
	Day   int
}

func NewCivilDate(year int64, month, day int) CivilDate {
	return CivilDate{Year: big.NewInt(year), Month: month, Day: day}
}

func (d CivilDate) String() string {
	return fmt.Sprintf("%04s-%02d-%02d", d.Year.String(), d.Month, d.Day)
}

func (d CivilDate) Before(other CivilDate) bool { return d.Compare(other) < 0 }

func (d CivilDate) Equal(other CivilDate) bool { return d.Compare(other) == 0 }

func (d CivilDate) Compare(other CivilDate) int {
	if cmp := d.Year.Cmp(other.Year); cmp != 0 {
		return cmp
	}
	if d.Month != other.Month {
		if d.Month < other.Month {
			return -1
		}
		return 1
	}
	if d.Day < other.Day {
		return -1
	}
	if d.Day > other.Day {
		return 1
	}
	return 0
}

func (d CivilDate) addDays(days *big.Int) CivilDate {
	serial := daysFromCivil(d)
	serial.Add(serial, days)
	return civilFromDays(serial)
}

func (d CivilDate) addMonths(months *big.Int) CivilDate {
	index := new(big.Int).Mul(d.Year, big.NewInt(12))
	index.Add(index, big.NewInt(int64(d.Month-1)))
	index.Add(index, months)
	year, remainder := new(big.Int), new(big.Int)
	year.QuoRem(index, big.NewInt(12), remainder)
	if remainder.Sign() < 0 {
		remainder.Add(remainder, big.NewInt(12))
		year.Sub(year, big.NewInt(1))
	}
	month := int(remainder.Int64()) + 1
	day := d.Day
	if max := daysInMonth(year, month); day > max {
		day = max
	}
	return normalizeReformGap(CivilDate{Year: year, Month: month, Day: day})
}

func daysInMonth(year *big.Int, month int) int {
	switch month {
	case 2:
		if leapYear(year) {
			return 29
		}
		return 28
	case 4, 6, 9, 11:
		return 30
	default:
		return 31
	}
}

func leapYear(year *big.Int) bool {
	if year.Cmp(big.NewInt(1582)) < 0 {
		return mod(year, 4) == 0
	}
	return mod(year, 4) == 0 && (mod(year, 100) != 0 || mod(year, 400) == 0)
}

func mod(value *big.Int, divisor int64) int64 {
	return new(big.Int).Mod(value, big.NewInt(divisor)).Int64()
}

var italyReform = CivilDate{Year: big.NewInt(1582), Month: 10, Day: 15}

// daysFromCivil and civilFromDays use Ruby Date's default Date::ITALY
// calendar: Julian before 1582-10-15, Gregorian from that date onward. Day
// zero is 1970-01-01, which makes addition exact without constructing a
// time.Time.
func daysFromCivil(d CivilDate) *big.Int {
	if d.Before(italyReform) {
		return julianDaysFromCivil(d)
	}
	return gregorianDaysFromCivil(d)
}

func gregorianDaysFromCivil(d CivilDate) *big.Int {
	year := new(big.Int).Set(d.Year)
	if d.Month <= 2 {
		year.Sub(year, big.NewInt(1))
	}
	era, yoe := floorDiv(year, 400)
	month := d.Month
	if month > 2 {
		month -= 3
	} else {
		month += 9
	}
	doy := (153*month+2)/5 + d.Day - 1
	doe := new(big.Int).Mul(yoe, big.NewInt(365))
	doe.Add(doe, new(big.Int).Quo(yoe, big.NewInt(4)))
	doe.Sub(doe, new(big.Int).Quo(yoe, big.NewInt(100)))
	doe.Add(doe, big.NewInt(int64(doy)))
	result := new(big.Int).Mul(era, big.NewInt(146097))
	result.Add(result, doe)
	return result.Sub(result, big.NewInt(719468))
}

func civilFromDays(days *big.Int) CivilDate {
	if days.Cmp(gregorianDaysFromCivil(italyReform)) < 0 {
		return julianCivilFromDays(days)
	}
	return gregorianCivilFromDays(days)
}

func gregorianCivilFromDays(days *big.Int) CivilDate {
	z := new(big.Int).Add(days, big.NewInt(719468))
	era, doe := floorDiv(z, 146097)
	yoe := new(big.Int).Set(doe)
	yoe.Sub(yoe, new(big.Int).Quo(doe, big.NewInt(1460)))
	yoe.Add(yoe, new(big.Int).Quo(doe, big.NewInt(36524)))
	yoe.Sub(yoe, new(big.Int).Quo(doe, big.NewInt(146096)))
	yoe.Quo(yoe, big.NewInt(365))
	year := new(big.Int).Mul(era, big.NewInt(400))
	year.Add(year, yoe)
	doy := new(big.Int).Mul(yoe, big.NewInt(365))
	doy.Add(doy, new(big.Int).Quo(yoe, big.NewInt(4)))
	doy.Sub(doy, new(big.Int).Quo(yoe, big.NewInt(100)))
	doy.Sub(doe, doy)
	mp := new(big.Int).Mul(doy, big.NewInt(5))
	mp.Add(mp, big.NewInt(2))
	mp.Quo(mp, big.NewInt(153))
	day := new(big.Int).Mul(mp, big.NewInt(153))
	day.Add(day, big.NewInt(2))
	day.Quo(day, big.NewInt(5))
	day.Sub(doy, day)
	day.Add(day, big.NewInt(1))
	month := int(mp.Int64())
	if month < 10 {
		month += 3
	} else {
		month -= 9
	}
	if month <= 2 {
		year.Add(year, big.NewInt(1))
	}
	return CivilDate{Year: year, Month: month, Day: int(day.Int64())}
}

func julianDaysFromCivil(d CivilDate) *big.Int {
	year := new(big.Int).Set(d.Year)
	if d.Month <= 2 {
		year.Sub(year, big.NewInt(1))
	}
	month := d.Month
	if month > 2 {
		month -= 3
	} else {
		month += 9
	}
	doy := (153*month+2)/5 + d.Day - 1
	result := new(big.Int).Mul(year, big.NewInt(365))
	quarters, _ := floorDiv(year, 4)
	result.Add(result, quarters)
	result.Add(result, big.NewInt(int64(doy+1721118-2440588)))
	return result
}

func julianCivilFromDays(days *big.Int) CivilDate {
	// This is the inverse Julian-day conversion, with the Unix epoch offset
	// removed and arbitrary-precision quotients retained throughout.
	z := new(big.Int).Add(days, big.NewInt(2440588+32082))
	fourZ := new(big.Int).Mul(z, big.NewInt(4))
	fourZ.Add(fourZ, big.NewInt(3))
	d, _ := floorDiv(fourZ, 1461)
	e := new(big.Int).Mul(d, big.NewInt(1461))
	e, _ = floorDiv(e, 4)
	e.Sub(z, e)
	fiveE := new(big.Int).Mul(e, big.NewInt(5))
	fiveE.Add(fiveE, big.NewInt(2))
	m, _ := floorDiv(fiveE, 153)
	dayTerm := new(big.Int).Mul(m, big.NewInt(153))
	dayTerm.Add(dayTerm, big.NewInt(2))
	dayTerm.Quo(dayTerm, big.NewInt(5))
	day := new(big.Int).Sub(e, dayTerm)
	day.Add(day, big.NewInt(1))
	month := int(m.Int64()) + 3
	year := new(big.Int).Sub(d, big.NewInt(4800))
	if month > 12 {
		month -= 12
		year.Add(year, big.NewInt(1))
	}
	return CivilDate{Year: year, Month: month, Day: int(day.Int64())}
}

func normalizeReformGap(d CivilDate) CivilDate {
	if d.Year.Cmp(big.NewInt(1582)) == 0 && d.Month == 10 && d.Day >= 5 && d.Day <= 14 {
		return CivilDate{Year: big.NewInt(1582), Month: 10, Day: 4}
	}
	return d
}

func floorDiv(value *big.Int, divisor int64) (*big.Int, *big.Int) {
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(value, big.NewInt(divisor), remainder)
	if remainder.Sign() < 0 {
		remainder.Add(remainder, big.NewInt(divisor))
		quotient.Sub(quotient, big.NewInt(1))
	}
	return quotient, remainder
}
