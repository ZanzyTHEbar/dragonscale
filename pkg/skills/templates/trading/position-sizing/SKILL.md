---
name: position-sizing
description: "Position sizing strategies — how much capital to allocate per trade"
tags: trading, sizing, money-management
domain: finance
links: risk-management
---

# Position Sizing

## Methods

### Fixed Fractional

Risk a fixed percentage of account equity per trade.

```
Position Size = (Account * Risk%) / (Entry - Stop)
```

Example: $100K account, 1% risk, $50 entry, $48 stop:
Position = ($100K * 0.01) / ($50 - $48) = 500 shares

### Kelly Criterion

Optimal fraction based on win rate and payoff ratio:

```
Kelly% = W - (1-W)/R
```

Where W = win rate, R = avg win / avg loss. **Use half-Kelly in practice** to account for estimation error.

### Volatility-Based (ATR)

Scale position size inversely with volatility:

```
Position Size = (Account * Risk%) / (N * ATR)
```

Where N is a multiplier (typically 1.5-2.0).

## Rules

- Never exceed 5% of account in a single position
- Scale into winners, not losers
- Reduce size during drawdowns; increase as equity grows

See [[risk-management]] for the broader risk framework.
