package recur

import (
	"fmt"
	"math/big"
)

// CivilDate is an unzoned proleptic-Gregorian date. Its year is arbitrary
// precision because Ruby Date accepts interval projections beyond time.Time's
// range.
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
	return CivilDate{Year: year, Month: month, Day: day}
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
	return mod(year, 4) == 0 && (mod(year, 100) != 0 || mod(year, 400) == 0)
}

func mod(value *big.Int, divisor int64) int64 {
	return new(big.Int).Mod(value, big.NewInt(divisor)).Int64()
}

// daysFromCivil and civilFromDays are the arbitrary-precision form of the
// proleptic-Gregorian conversion used by time libraries. Day zero is
// 1970-01-01, which makes addition exact without constructing a time.Time.
func daysFromCivil(d CivilDate) *big.Int {
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

func floorDiv(value *big.Int, divisor int64) (*big.Int, *big.Int) {
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(value, big.NewInt(divisor), remainder)
	if remainder.Sign() < 0 {
		remainder.Add(remainder, big.NewInt(divisor))
		quotient.Sub(quotient, big.NewInt(1))
	}
	return quotient, remainder
}
