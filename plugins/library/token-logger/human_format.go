// Copyright 2026 Ayesh Almeida
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package tokenlogger

import (
	"crypto/sha256"
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

const maxHumanDimensionRunes = 32

type decimalUnit struct {
	value  uint64
	suffix string
}

var humanTokenUnits = []decimalUnit{
	{value: 1_000, suffix: "K"},
	{value: 1_000_000, suffix: "M"},
	{value: 1_000_000_000, suffix: "B"},
	{value: 1_000_000_000_000, suffix: "T"},
}

// formatHumanTokens uses decimal units, one fractional digit at most, and
// half-up rounding. A rounded 1000 of one unit is promoted to the next unit.
func formatHumanTokens(value int64) string {
	negative, magnitude := signedMagnitude(value)
	if magnitude < 1_000 {
		return strconv.FormatInt(value, 10)
	}
	unitIndex := 0
	for unitIndex+1 < len(humanTokenUnits) && magnitude >= humanTokenUnits[unitIndex+1].value {
		unitIndex++
	}
	for {
		unit := humanTokenUnits[unitIndex]
		whole := magnitude / unit.value
		remainder := magnitude % unit.value
		tenths := whole*10 + (remainder*10+unit.value/2)/unit.value
		if tenths >= 10_000 && unitIndex+1 < len(humanTokenUnits) {
			unitIndex++
			continue
		}
		sign := ""
		if negative {
			sign = "-"
		}
		if tenths%10 == 0 {
			return fmt.Sprintf("%s%d%s", sign, tenths/10, unit.suffix)
		}
		return fmt.Sprintf("%s%d.%d%s", sign, tenths/10, tenths%10, unit.suffix)
	}
}

func signedMagnitude(value int64) (bool, uint64) {
	if value >= 0 {
		return false, uint64(value)
	}
	return true, uint64(-(value + 1)) + 1
}

func formatExactCount(value int64) string {
	raw := strconv.FormatInt(value, 10)
	sign := ""
	digits := raw
	if strings.HasPrefix(raw, "-") {
		sign = "-"
		digits = strings.TrimPrefix(raw, "-")
	}
	first := len(digits) % 3
	if first == 0 {
		first = 3
	}
	var formatted strings.Builder
	formatted.WriteString(sign)
	formatted.WriteString(digits[:first])
	for index := first; index < len(digits); index += 3 {
		formatted.WriteByte(',')
		formatted.WriteString(digits[index : index+3])
	}
	return formatted.String()
}

func formatHumanDimension(value string) string {
	if value == "" {
		return "(none)"
	}
	safe := escapeHumanText(value)
	runes := []rune(safe)
	if len(runes) <= maxHumanDimensionRunes {
		return safe
	}
	digest := sha256.Sum256([]byte(value))
	suffix := fmt.Sprintf("…#%x", digest[:3])
	suffixRunes := []rune(suffix)
	prefixLength := maxHumanDimensionRunes - len(suffixRunes)
	return string(runes[:prefixLength]) + suffix
}

func escapeHumanText(value string) string {
	var escaped strings.Builder
	for _, character := range value {
		switch character {
		case '\t':
			escaped.WriteString(`\t`)
		case '\n':
			escaped.WriteString(`\n`)
		case '\r':
			escaped.WriteString(`\r`)
		case '\x1b':
			escaped.WriteString(`\x1B`)
		default:
			if unicode.IsControl(character) || unicode.In(character, unicode.Cf) {
				if character <= 0xffff {
					fmt.Fprintf(&escaped, `\u%04X`, character)
				} else {
					fmt.Fprintf(&escaped, `\U%08X`, character)
				}
				continue
			}
			escaped.WriteRune(character)
		}
	}
	return escaped.String()
}

func humanCostDisplay(measures UsageMeasures) (string, string) {
	unknown := measures.UnknownCostRequests
	known := measures.RequestCount - unknown
	if known < 0 {
		known = 0
	}

	if measures.CalculatedCurrency == "mixed" {
		status := "mixed currencies"
		if unknown > 0 {
			status += "; " + formatExactCount(unknown) + " pricing unavailable"
		}
		return "not comparable", status
	}

	cost := formatCostMicros(measures.CalculatedCostMicros)
	if measures.CalculatedCurrency != "" {
		cost += " " + measures.CalculatedCurrency
	}
	if unknown == 0 {
		return cost, "calculated"
	}
	if known == 0 && measures.CalculatedCostMicros == 0 && measures.CalculatedCurrency == "" {
		return "unknown", "pricing unavailable (" + formatExactCount(unknown) + ")"
	}
	return cost, "partial; " + formatExactCount(unknown) + " pricing unavailable"
}

func formatCostMicros(value int64) string {
	negative, magnitude := signedMagnitude(value)
	whole := magnitude / 1_000_000
	fraction := magnitude % 1_000_000
	sign := ""
	if negative {
		sign = "-"
	}
	if fraction == 0 {
		return fmt.Sprintf("%s%d", sign, whole)
	}
	fractionText := strings.TrimRight(fmt.Sprintf("%06d", fraction), "0")
	return fmt.Sprintf("%s%d.%s", sign, whole, fractionText)
}
