package cmd

import oracletypes "github.com/ExocoreNetwork/exocore/x/oracle/types"

// Define the types for the feeder
type feederParams struct {
	startRoundID uint64
	startBlock   uint64
	endBlock     uint64
	interval     uint64
	decimal      int32
	tokenIDStr   string
	feederID     int64
	tokenName    string
}

// Define the types for the event
type eventRes struct {
	height       uint64
	txHeight     uint64
	gas          int64
	price        string
	decimal      int
	params       *feederParams
	priceUpdated bool
}

// Define the types for the feederInfo
type feederInfo struct {
	params      *feederParams
	latestPrice string
	updateCh    chan eventRes
}

func (f *feederParams) update(p oracletypes.Params) (updated bool) {
	tokenFeeder := p.TokenFeeders[f.feederID]
	if tokenFeeder.StartBaseBlock != f.startBlock {
		f.startBlock = tokenFeeder.StartBaseBlock
		updated = true
	}
	if tokenFeeder.EndBlock != f.endBlock {
		f.endBlock = tokenFeeder.EndBlock
		updated = true
	}
	if tokenFeeder.Interval != f.interval {
		f.interval = tokenFeeder.Interval
		updated = true
	}
	if p.Tokens[tokenFeeder.TokenID].Decimal != f.decimal {
		f.decimal = p.Tokens[tokenFeeder.TokenID].Decimal
		updated = true
	}
	return
}
