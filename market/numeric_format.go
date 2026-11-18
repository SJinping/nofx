package market

import (
	"strconv"
	"strings"
)

// FormatPrice preserves the available float64 price precision without scientific
// notation. Only prompt/display text uses it; exchange tick rounding is separate.
func FormatPrice(value float64) string {
	if value == 0 {
		return "0"
	}
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// FormatIndicator keeps eight significant digits, rather than fixed decimal
// places, so small EMA/MACD/ATR values retain their magnitude and sign.
func FormatIndicator(value float64) string {
	if value == 0 {
		return "0"
	}
	return strconv.FormatFloat(value, 'g', 8, 64)
}

func formatPriceSlice(values []float64) string {
	return formatNumericSlice(values, FormatPrice)
}

func formatIndicatorSlice(values []float64) string {
	return formatNumericSlice(values, FormatIndicator)
}

func formatNumericSlice(values []float64, format func(float64) string) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = format(value)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
