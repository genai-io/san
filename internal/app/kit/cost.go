package kit

import (
	"fmt"

	"github.com/genai-io/san/internal/llm"
)

func FormatMoney(m llm.Money) string {
	switch m.Currency {
	case llm.CurrencyCNY:
		return formatCurrencyAmount("¥", m.Amount)
	case llm.CurrencyUSD:
		return formatCurrencyAmount("$", m.Amount)
	default:
		if m.Amount == 0 {
			return "0"
		}
		return fmt.Sprintf("%.3f %s", m.Amount, m.Currency)
	}
}

func formatCurrencyAmount(symbol string, amount float64) string {
	switch {
	case amount <= 0:
		return symbol + "0"
	case amount < 0.0001:
		return fmt.Sprintf("%s%.6f", symbol, amount)
	case amount < 0.01:
		return fmt.Sprintf("%s%.4f", symbol, amount)
	default:
		return fmt.Sprintf("%s%.3f", symbol, amount)
	}
}
