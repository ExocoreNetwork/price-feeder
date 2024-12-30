package debugger

import fetchertypes "github.com/ExocoreNetwork/price-feeder/fetcher/types"

func (p *PriceMsg) GetPriceInfo() fetchertypes.PriceInfo {
	return fetchertypes.PriceInfo{
		Price:     p.Price,
		Decimal:   p.Decimal,
		RoundID:   p.DetId,
		Timestamp: p.Timestamp,
	}
}
